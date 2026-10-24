// Command cast-helper casts the screen and media to Chromecast and AirPlay
// devices. Clients send one JSON command per line and get one JSON event per
// line; PROTOCOL.md lists them.
//
//	cast-helper          talks over stdin and stdout, exits when stdin closes
//	cast-helper serve    the shared helper, on a socket in $XDG_RUNTIME_DIR
//	cast-helper attach   connects stdin and stdout to it, starting it if needed
package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// device is one TV or speaker. One that speaks Chromecast and AirPlay is a
// single device: it keeps its Chromecast id and lists both protocols.
type device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Model       string   `json:"model"`
	Protocol    string   `json:"protocol"` // the main one: chromecast when there is one
	Protocols   []string `json:"protocols"`
	Host        string   `json:"-"`
	Port        int      `json:"-"` // Cast control port
	AirplayPort int      `json:"-"`
}

func (d device) has(protocol string) bool { return slices.Contains(d.Protocols, protocol) }

type command struct {
	Cmd      string  `json:"cmd"`
	On       bool    `json:"on"`
	Device   string  `json:"device"`
	Level    float64 `json:"level"`
	Path     string  `json:"path"`
	URL      string  `json:"url"`
	Port     int     `json:"port"` // file server port, 0 for any
	Pin      string  `json:"pin"`  // AirPlay pairing PIN or password
	Position float64 `json:"position"`
	Size     int     `json:"size"` // thumbnail width
	// hello
	Client string `json:"client"`
	Name   string `json:"name"`
	// play, and each queue item
	queueSource
	Items         []commandItem `json:"items"`
	sources       []queueSource // edit: Items with playlists expanded
	Index         int           `json:"index"`
	Interval      int           `json:"interval"`
	Loop          bool          `json:"loop"`
	Repeat        string        `json:"repeat"` // queue and repeat: "show", "item" or ""
	Times         int           `json:"times"`  // queue and repeat: 0 until stopped
	ShrinkPhotos  *bool         `json:"shrinkPhotos"`
	ConvertVideos *bool         `json:"convertVideos"`
	castOptions
}

type commandItem struct {
	Path, URL string
	queueSource
}

// source is the path or link of a play command or queue item.
func source(path, link string) (string, error) {
	if link != "" {
		link = strings.TrimSpace(link)
		if !isLink(link) {
			return "", fmt.Errorf("not an http(s) link: %q", link)
		}
		return link, nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("not an absolute path: %q", path)
	}
	return path, nil
}

// queueSources checks a queue's items and expands the playlist files among them.
func queueSources(items []commandItem) []queueSource {
	var srcs []queueSource
	add := func(path, link string, meta queueSource) {
		if src, err := source(path, link); err == nil {
			meta.Source = src
			srcs = append(srcs, meta)
		}
	}
	for _, it := range items {
		if it.URL == "" && isPlaylist(it.Path) {
			entries, _ := readPlaylist(it.Path)
			for _, e := range entries {
				add(e.Path, e.URL, queueSource{Title: e.Title})
			}
			continue
		}
		add(it.Path, it.URL, it.queueSource)
	}
	return srcs
}

func orTrue(b *bool) bool { return b == nil || *b }

// activeSession is a mirroring cast or a file playing on the TV.
type activeSession interface {
	deviceID() string
	status() map[string]any
	run() (code string, err error)
	Stop()
	Wait(time.Duration)
	SetVolume(level float64)
}

// pendingSession holds the slot while the device is looked up.
type pendingSession struct {
	h     *helper
	id    string
	since time.Time
}

func (p *pendingSession) deviceID() string { return p.id }
func (p *pendingSession) status() map[string]any {
	return map[string]any{"device": p.id, "phase": "starting", "since": p.since.Unix()}
}
func (p *pendingSession) run() (string, error) { return "", nil }
func (p *pendingSession) Wait(time.Duration)   {}
func (p *pendingSession) SetVolume(float64)    {}
func (p *pendingSession) Stop() {
	p.h.mu.Lock()
	if p.h.sess == p {
		p.h.sess = nil
	}
	p.h.mu.Unlock()
	p.h.emitState()
	p.h.checkIdle()
}

type helper struct {
	cmu     sync.Mutex
	clients map[*client]bool
	serving bool          // cast-helper serve: quits when idle
	idle    chan struct{} // nothing connected and nothing casting

	mu           sync.Mutex
	devices      map[string]device
	stopDiscover context.CancelFunc
	discoverers  int // the plugin's request plus a start waiting for its device
	sess         activeSession
	owner        owner // the client that started sess
	// The portal's restore tokens are single use: the newest one is kept for
	// moving a cast, which opens a new portal session.
	restoreToken string
}

// owner names the client that started a session, so every client can say
// where it plays from.
type owner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newHelper() *helper {
	return &helper{clients: map[*client]bool{}, devices: map[string]device{}, idle: make(chan struct{}, 1)}
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.SetPrefix("")
	var err error
	switch mode := strings.Join(os.Args[1:], " "); mode {
	case "":
		err = runStdio()
	case "serve":
		err = runServe()
	case "attach":
		err = runAttach()
	case "version", "--version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q; use serve, attach or no argument", mode)
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// runStdio serves one client on stdin and stdout and stops its cast when
// stdin closes.
func runStdio() error {
	h := newHelper()
	removeStaleRemuxes()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		h.serveClient(newClient(os.Stdout, nil), os.Stdin)
		close(done)
	}()
	select {
	case <-sig:
	case <-done:
	}
	h.shutdown()
	return nil
}

// serveClient reads one client's commands until it disconnects.
func (h *helper) serveClient(c *client, r io.Reader) {
	h.cmu.Lock()
	h.clients[c] = true
	h.cmu.Unlock()
	c.send(h.stateEvent())
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var cmd command
		if err := json.Unmarshal(sc.Bytes(), &cmd); err != nil {
			log.Printf("bad command %q: %v", sc.Text(), err)
			continue
		}
		debugf("command from %s: %s", cmp.Or(c.owner.ID, "client"), sc.Bytes())
		h.handle(cmd, c)
	}
	h.cmu.Lock()
	delete(h.clients, c)
	h.cmu.Unlock()
	h.setDiscovery(c, false)
	c.close()
	h.checkIdle()
}

// version is this release; the release workflow checks the tag against it.
const version = "0.1.0"

// protocolVersion goes up when commands or events are added. Changes are
// additive, so a client needs a helper with at least its own number.
const protocolVersion = 3

func (h *helper) handle(c command, from *client) {
	switch c.Cmd {
	case "hello":
		from.owner = owner{ID: c.Client, Name: c.Name}
	case "check":
		report := dependencyReport()
		report["version"] = version
		from.send(report)
	case "discover":
		h.setDiscovery(from, c.On)
	case "start":
		// Chromecast unless the device only speaks AirPlay, or the user chose
		// AirPlay for mirroring it.
		h.start(c.Device, from, func(d device) activeSession {
			if d.has("airplay") && (c.Protocol == "airplay" || !d.has("chromecast")) {
				return newAirplaySession(h, d, c.castOptions)
			}
			return newSession(h, d, c.castOptions)
		})
	case "pair":
		h.mu.Lock()
		a, _ := h.sess.(*airplaySession)
		h.mu.Unlock()
		if a != nil {
			a.pair(c.Pin)
		}
	case "play":
		src, err := source(c.Path, c.URL)
		if err != nil {
			from.send(errorEvent("unsupportedLink", err))
			return
		}
		opts := queueOptions{ShrinkPhotos: c.ShrinkPhotos != nil && *c.ShrinkPhotos, ConvertVideos: orTrue(c.ConvertVideos), Port: c.Port}
		item := c.queueSource
		item.Source = src
		h.start(c.Device, from, func(d device) activeSession { return newMediaSession(h, d, []queueSource{item}, 0, opts) })
	case "playlist":
		go h.expandPlaylist(c.Path, from)
	case "queue":
		srcs := queueSources(c.Items)
		if len(srcs) == 0 {
			from.send(errorEvent("unsupportedLink", errors.New("empty queue")))
			return
		}
		opts := queueOptions{Interval: c.Interval, Loop: c.Loop, Repeat: c.Repeat, Times: max(c.Times, 0), ShrinkPhotos: c.ShrinkPhotos != nil && *c.ShrinkPhotos,
			ConvertVideos: orTrue(c.ConvertVideos), Port: c.Port}
		h.start(c.Device, from, func(d device) activeSession { return newMediaSession(h, d, srcs, c.Index, opts) })
	case "move":
		go h.move(c.Device)
	case "thumbnail":
		go h.thumbnail(c.Path, c.Size, from)
	case "pause", "resume", "seek", "subtitles", "next", "previous", "jump", "slideshow", "edit", "repeat":
		if c.Cmd == "edit" {
			c.sources = queueSources(c.Items)
		}
		h.mu.Lock()
		m, _ := h.sess.(*mediaSession)
		h.mu.Unlock()
		if m != nil {
			m.control(c)
		}
	case "stop":
		h.mu.Lock()
		s := h.sess
		h.mu.Unlock()
		if s != nil {
			s.Stop()
		}
	case "volume":
		h.mu.Lock()
		s := h.sess
		h.mu.Unlock()
		if s != nil {
			s.SetVolume(c.Level)
		}
	default:
		log.Printf("unknown command %q", c.Cmd)
	}
}

// emit sends an event to every client.
func (h *helper) emit(v any) {
	h.cmu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.cmu.Unlock()
	for _, c := range clients {
		c.send(v)
	}
}

func (h *helper) emitError(code string, err error) { h.emit(errorEvent(code, err)) }

func errorEvent(code string, err error) map[string]any {
	log.Printf("error %s: %v", code, err)
	msg := map[string]any{"event": "error", "code": code}
	var m *missingError
	if errors.As(err, &m) {
		msg["feature"], msg["missing"] = m.feature, m.missing
	}
	if err != nil {
		msg["detail"] = err.Error()
	}
	return msg
}

// missingError says which packages a feature needs; the plugin names them.
type missingError struct {
	feature string
	missing []string
}

func (e *missingError) Error() string {
	return e.feature + " needs " + strings.Join(e.missing, ", ")
}

// need returns a missingError when a feature's packages aren't installed.
func need(feature string) error {
	if missing := missingFor(feature); len(missing) > 0 {
		return &missingError{feature, missing}
	}
	return nil
}

// emitState pushes the full state; clients keep no state of their own.
func (h *helper) emitState() { h.emit(h.stateEvent()) }

func (h *helper) stateEvent() map[string]any {
	h.mu.Lock()
	devs := make([]device, 0, len(h.devices))
	for _, d := range h.devices {
		devs = append(devs, d)
	}
	var sess any
	if h.sess != nil {
		st := h.sess.status()
		if h.owner.ID != "" {
			st["owner"] = h.owner
		}
		sess = st
	}
	h.mu.Unlock()
	slices.SortFunc(devs, func(a, b device) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return map[string]any{"event": "state", "devices": devs, "session": sess}
}

// setDiscovery browses while any client shows a device list.
func (h *helper) setDiscovery(from *client, on bool) {
	h.cmu.Lock()
	from.discover = on
	on = false
	for c := range h.clients {
		on = on || c.discover
	}
	h.cmu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if on == (h.discoverers&1 == 1) {
		return
	}
	if on {
		h.discoverers |= 1
	} else {
		h.discoverers &^= 1
	}
	h.updateDiscoveryLocked()
}

func (h *helper) updateDiscoveryLocked() {
	if h.discoverers != 0 && h.stopDiscover == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.stopDiscover = cancel
		go func() {
			err := discover(ctx, func(devs []device) {
				h.mu.Lock()
				h.devices = map[string]device{}
				for _, d := range devs {
					h.devices[d.ID] = d
				}
				h.mu.Unlock()
				h.emitState()
			})
			if err != nil && ctx.Err() == nil {
				if missing := need("discovery"); missing != nil {
					h.emitError("missingDependency", missing)
				} else {
					h.emitError("avahi", err)
				}
			}
		}()
	}
	if h.discoverers == 0 && h.stopDiscover != nil {
		h.stopDiscover()
		h.stopDiscover = nil
		// Keep the device being cast to; forget the rest.
		for id := range h.devices {
			if h.sess == nil || h.sess.deviceID() != id {
				delete(h.devices, id)
			}
		}
		go h.emitState()
	}
}

// lookup finds a device, browsing for a few seconds if it isn't known yet
// (the Control Center tile starts a cast without showing the list).
func (h *helper) lookup(id string) (device, bool) {
	h.mu.Lock()
	d, ok := h.devices[id]
	if ok {
		h.mu.Unlock()
		return d, true
	}
	h.discoverers |= 2
	h.updateDiscoveryLocked()
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.discoverers &^= 2
		h.updateDiscoveryLocked()
		h.mu.Unlock()
	}()
	for range 30 {
		time.Sleep(200 * time.Millisecond)
		h.mu.Lock()
		d, ok = h.devices[id]
		h.mu.Unlock()
		if ok {
			return d, true
		}
	}
	return device{}, false
}

// start casts to a device for a client; a moved cast keeps its owner and has
// no client.
func (h *helper) start(id string, from *client, create func(device) activeSession) {
	h.mu.Lock()
	by := h.owner
	if from != nil {
		by = from.owner
	}
	if h.sess != nil {
		busy := fmt.Errorf("already casting to %s", h.sess.deviceID())
		h.mu.Unlock()
		from.send(errorEvent("busy", busy))
		return
	}
	// Reserve the slot so a second start can't race this one.
	placeholder := &pendingSession{h: h, id: id, since: time.Now()}
	h.sess, h.owner = placeholder, by
	h.mu.Unlock()
	h.emitState()

	go func() {
		d, ok := h.lookup(id)
		h.mu.Lock()
		cancelled := h.sess != placeholder
		h.mu.Unlock()
		if cancelled {
			return
		}
		if !ok {
			h.endSession(placeholder, "deviceNotFound", fmt.Errorf("%s did not show up on the network", id))
			return
		}
		s := create(d)
		h.mu.Lock()
		if h.sess != placeholder { // stopped while looking up
			h.mu.Unlock()
			return
		}
		h.sess = s
		h.mu.Unlock()
		code, err := s.run()
		h.endSession(s, code, err)
	}()
}

func (h *helper) endSession(s activeSession, code string, err error) {
	h.mu.Lock()
	if h.sess == s {
		h.sess = nil
	}
	h.mu.Unlock()
	if code != "" {
		h.emitError(code, err)
	}
	h.emitState()
	h.checkIdle()
}

// move ends what is casting and starts the same on another device: a
// mirroring cast with the same options, playback at the same item and time.
func (h *helper) move(id string) {
	h.mu.Lock()
	s := h.sess
	token := h.restoreToken
	h.mu.Unlock()
	if s == nil || s.deviceID() == id {
		return
	}
	var create func(device) activeSession
	switch s := s.(type) {
	case *session:
		opts := s.opts
		if token != "" {
			opts.RestoreToken = token
		}
		create = func(d device) activeSession { return newSession(h, d, opts) }
	case *airplaySession:
		opts := s.opts
		create = func(d device) activeSession {
			if d.has("airplay") && !d.has("chromecast") {
				return newAirplaySession(h, d, opts)
			}
			return newSession(h, d, opts)
		}
	case *mediaSession:
		index, at := s.position()
		opts := s.opts
		opts.StartAt = at
		srcs := s.sources()
		create = func(d device) activeSession { return newMediaSession(h, d, srcs, index, opts) }
	default:
		return
	}
	s.Stop()
	s.Wait(5 * time.Second)
	for range 50 { // until run has handed the slot back
		h.mu.Lock()
		free := h.sess != s
		h.mu.Unlock()
		if free {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.start(id, nil, create)
}

func (h *helper) shutdown() {
	h.mu.Lock()
	s := h.sess
	if h.stopDiscover != nil {
		h.stopDiscover()
	}
	h.mu.Unlock()
	if s != nil {
		s.Stop()
		s.Wait(5 * time.Second)
	}
}

// removeStaleRemuxes deletes MP4 copies a crashed helper left in the cache.
// Only one helper runs at a time, so none of them is in use.
func removeStaleRemuxes() {
	cache, err := os.UserCacheDir()
	if err != nil {
		return
	}
	stale, _ := filepath.Glob(filepath.Join(cache, "dms", "cast", "[0-9]*-*"))
	// Helpers up to 0.0.2 named remuxes <pid>.mp4.
	old, _ := filepath.Glob(filepath.Join(cache, "dms", "cast", "[0-9]*.mp4*"))
	for _, f := range append(stale, old...) {
		os.Remove(f)
	}
}
