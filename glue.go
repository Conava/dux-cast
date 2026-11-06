package main

import (
	"context"
	"log"
	"os"
	"slices"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
)

// discover reports the full device list on every change until ctx ends.
func discover(ctx context.Context, onChange func(devs []device, noCast []string)) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	d, err := StartDiscovery(conn)
	if err != nil {
		return err
	}
	defer d.Stop()
	found := map[string]Device{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-d.Errors:
			log.Printf("discovery: %v", err)
		case e := <-d.Events:
			if e.Added {
				found[e.Device.ID] = e.Device
			} else {
				delete(found, e.Device.ID)
			}
			onChange(mergeDevices(found), tvsWithoutCast(found))
		}
	}
}

// mergeDevices joins a device's Chromecast and AirPlay entries, which avahi
// reports separately, by address.
func mergeDevices(found map[string]Device) []device {
	byAddr := map[string]*device{}
	var order []string
	for _, f := range found {
		if f.Protocol == "androidtv" {
			continue
		}
		d := byAddr[f.Addr]
		if d == nil {
			d = &device{Host: f.Addr}
			byAddr[f.Addr] = d
			order = append(order, f.Addr)
		}
		d.Protocols = append(d.Protocols, f.Protocol)
		switch f.Protocol {
		case "chromecast":
			d.ID, d.Name, d.Model, d.Protocol, d.Port = f.ID, f.Name, f.Model, "chromecast", int(f.Port)
		case "airplay":
			d.AirplayPort = int(f.Port)
			if d.Protocol == "" {
				d.ID, d.Name, d.Model, d.Protocol = f.ID, f.Name, f.Model, "airplay"
			}
		}
	}
	list := make([]device, 0, len(order))
	for _, a := range order {
		d := byAddr[a]
		slices.Sort(d.Protocols)
		list = append(list, *d)
	}
	return list
}

// tvsWithoutCast names the Android TVs on the network that advertise neither
// Cast nor AirPlay, so they can't show up as devices.
func tvsWithoutCast(found map[string]Device) []string {
	casts := map[string]bool{}
	for _, f := range found {
		if f.Protocol != "androidtv" {
			casts[f.Addr] = true
		}
	}
	names := []string{}
	for _, f := range found {
		if f.Protocol == "androidtv" && !casts[f.Addr] {
			names = append(names, f.Name)
		}
	}
	slices.Sort(names)
	return names
}

// debug turns on per-message and per-request logging, for troubleshooting:
// the debug command, CAST_DEBUG=1 in the shell's environment, or a development
// build with -ldflags "-X main.debugBuild=1".
var debugBuild string

var debug atomic.Bool

func init() { debug.Store(os.Getenv("CAST_DEBUG") != "" || debugBuild != "") }

func debugf(format string, args ...any) {
	if debug.Load() {
		log.Printf(format, args...)
	}
}
