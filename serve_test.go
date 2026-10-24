package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The test binary runs main when asked, so attach can start serve from it.
func TestMain(m *testing.M) {
	if mode := os.Getenv("CAST_HELPER_TEST_MAIN"); mode != "" {
		if len(os.Args) < 2 || os.Args[1] != "serve" { // attach starts serve
			os.Args = []string{os.Args[0], mode}
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTwoClientsShareOneServer(t *testing.T) {
	dir := t.TempDir()
	env := append(os.Environ(), "CAST_HELPER_TEST_MAIN=attach", "XDG_RUNTIME_DIR="+dir, "XDG_CACHE_HOME="+dir)
	type attached struct {
		cmd    *exec.Cmd
		in     *os.File
		events *bufio.Scanner
	}
	attach := func() attached {
		cmd := exec.Command(os.Args[0])
		cmd.Env = env
		r, w, _ := os.Pipe()
		cmd.Stdin = r
		out, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		return attached{cmd, w, bufio.NewScanner(out)}
	}
	next := func(a attached) map[string]any {
		if !a.events.Scan() {
			t.Fatal("no event")
		}
		var ev map[string]any
		json.Unmarshal(a.events.Bytes(), &ev)
		return ev
	}

	a, b := attach(), attach()
	for _, c := range []attached{a, b} {
		if ev := next(c); ev["event"] != "state" {
			t.Fatalf("first event %v, want the state snapshot", ev)
		}
	}
	// A request's reply goes to the client that asked.
	a.in.WriteString(`{"cmd":"check"}` + "\n")
	if ev := next(a); ev["event"] != "check" || ev["protocol"] != float64(protocolVersion) {
		t.Fatalf("check reply %v", ev)
	}

	a.in.Close()
	b.in.Close()
	for _, c := range []attached{a, b} {
		if err := c.cmd.Wait(); err != nil {
			t.Errorf("attach after stdin closed: %v", err)
		}
	}
	sock := filepath.Join(dir, "cast-helper.sock")
	if _, err := os.Stat(sock); err != nil {
		t.Fatal("the server should still be up right after the last client left")
	}
	for range 100 {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			return
		}
	}
	t.Error("the server didn't exit when idle")
}
