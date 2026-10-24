package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// client is one connection: the stdio client, or one on the serve socket.
type client struct {
	mu       sync.Mutex
	w        io.Writer
	conn     net.Conn // nil for stdio
	closed   bool
	owner    owner // from its hello
	discover bool  // wants the device list; guarded by helper.cmu
}

func newClient(w io.Writer, conn net.Conn) *client { return &client{w: w, conn: conn} }

// send writes one event. A client that can't keep up is dropped rather than
// holding up the others. Sending to nil does nothing.
func (c *client) send(v any) {
	if c == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("encode event: %v", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if c.conn != nil {
		c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	}
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		log.Printf("client: %v", err)
		c.closed = true
		if c.conn != nil {
			c.conn.Close()
		}
	}
}

func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.conn != nil {
		c.conn.Close()
	}
}

// runtimeDir holds the socket and the lock; the cache folder stands in when
// there is no XDG_RUNTIME_DIR, since /tmp is shared with other users.
func runtimeDir() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir, nil
	}
	return cacheDir()
}

func socketPath() (string, error) {
	dir, err := runtimeDir()
	return filepath.Join(dir, "cast-helper.sock"), err
}

// runServe is the shared helper. A lock keeps it to one per user; a second
// one exits quietly and its caller connects to the first.
func runServe() error {
	dir, err := runtimeDir()
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "cast-helper.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	sock := filepath.Join(dir, "cast-helper.sock")
	os.Remove(sock) // left by one that crashed; the lock says none is running
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer os.Remove(sock)
	defer ln.Close()
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	// A client that has gone away must not end the server mid-write.
	signal.Ignore(syscall.SIGPIPE)

	h := newHelper()
	h.serving = true
	removeStaleRemuxes()
	log.Printf("serving on %s", sock)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go h.serveClient(newClient(conn, conn), conn)
		}
	}()
	h.checkIdle() // started by a client that never connected

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case <-sig:
	case <-h.idle:
		log.Print("idle, exiting")
	}
	h.shutdown()
	return nil
}

// checkIdle ends a serving helper a moment after the last client left with
// nothing casting, so a client reconnecting right away finds it still there.
func (h *helper) checkIdle() {
	if !h.serving {
		return
	}
	idle := func() bool {
		h.cmu.Lock()
		n := len(h.clients)
		h.cmu.Unlock()
		h.mu.Lock()
		defer h.mu.Unlock()
		return n == 0 && h.sess == nil
	}
	if !idle() {
		return
	}
	time.AfterFunc(5*time.Second, func() {
		if idle() {
			select {
			case h.idle <- struct{}{}:
			default:
			}
		}
	})
}

// runAttach connects stdin and stdout to the serving helper, starting it
// first if none is running. It exits when either side closes: 0 when stdin
// did, 1 when the helper went away.
func runAttach() error {
	sock, err := socketPath()
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		if err := startServer(); err != nil {
			return err
		}
		for range 100 {
			time.Sleep(50 * time.Millisecond)
			if conn, err = net.Dial("unix", sock); err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
	}
	defer conn.Close()
	stdinDone := make(chan struct{})
	go func() {
		io.Copy(conn, os.Stdin)
		conn.(*net.UnixConn).CloseWrite()
		close(stdinDone)
	}()
	io.Copy(os.Stdout, conn)
	select {
	case <-stdinDone:
		return nil
	default:
		return errors.New("the helper closed the connection")
	}
}

// startServer runs cast-helper serve in its own session, so it outlives the
// client that started it. It logs to the cache folder.
func startServer() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	logFile, err := os.Create(filepath.Join(dir, "serve.log"))
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
