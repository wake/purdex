package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// swapStdio points os.Stdin/os.Stdout at fresh pipes for the test and
// returns the ends the test drives: the stdin writer and a stdout reader.
func swapStdio(t *testing.T) (stdinW *os.File, stdout *bufio.Reader) {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() {
		os.Stdin, os.Stdout = origIn, origOut
		inW.Close()
		inR.Close()
		outW.Close()
		outR.Close()
	})
	return inW, bufio.NewReader(outR)
}

func readLineWithin(t *testing.T, r *bufio.Reader, d time.Duration) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() {
		line, _ := r.ReadString('\n')
		ch <- line
	}()
	select {
	case line := <-ch:
		return strings.TrimSuffix(line, "\n")
	case <-time.After(d):
		t.Fatalf("no stdout line within %v", d)
		return ""
	}
}

func TestRunPeerProxy_ReadyThenStdinEOFExitsZero(t *testing.T) {
	origPS := peerProxyProcStartFn
	t.Cleanup(func() { peerProxyProcStartFn = origPS })
	peerProxyProcStartFn = func(int) (string, error) { return "Sun Sep 13 18:57:56 2026", nil }

	root, err := os.MkdirTemp("/tmp", "pdxp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	reg := filepath.Join(root, "reg")
	if err := os.MkdirAll(reg, 0o700); err != nil {
		t.Fatal(err)
	}
	socks := filepath.Join(root, "socks")

	stdinW, stdout := swapStdio(t)
	code := make(chan int, 1)
	go func() { code <- runPeerProxy() }()

	cfg := `{"name":"air/foo","registry_dir":"` + reg + `","sock_dir":"` + socks + `","version":"2.1.270","session_id":"11111111-2222-4333-8444-555555555555","cwd":"/"}` + "\n"
	if _, err := stdinW.Write([]byte(cfg)); err != nil {
		t.Fatal(err)
	}
	line := readLineWithin(t, stdout, 3*time.Second)
	var ready struct {
		Ready bool     `json:"ready"`
		PID   int      `json:"pid"`
		Sock  string   `json:"sock"`
		Files []string `json:"files"`
		Error string   `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &ready); err != nil {
		t.Fatalf("ready line %q: %v", line, err)
	}
	if !ready.Ready {
		t.Fatalf("ready:false: %s", ready.Error)
	}
	if ready.PID != os.Getpid() {
		t.Errorf("pid = %d, want the helper's own pid %d", ready.PID, os.Getpid())
	}
	if !strings.HasPrefix(ready.Sock, socks) || len(ready.Files) != 2 {
		t.Errorf("sock %q files %v", ready.Sock, ready.Files)
	}

	stdinW.Close()
	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("exit code = %d, want 0", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("runPeerProxy did not return after stdin EOF")
	}
	if _, err := os.Lstat(ready.Sock); err == nil {
		t.Errorf("socket %s left behind", ready.Sock)
	}
}

func TestRunPeerProxy_BadConfigExitsOne(t *testing.T) {
	stdinW, stdout := swapStdio(t)
	code := make(chan int, 1)
	go func() { code <- runPeerProxy() }()
	if _, err := stdinW.Write([]byte("nope\n")); err != nil {
		t.Fatal(err)
	}
	line := readLineWithin(t, stdout, 3*time.Second)
	if !strings.Contains(line, `"ready":false`) {
		t.Errorf("stdout = %q, want a ready:false line", line)
	}
	select {
	case c := <-code:
		if c != 1 {
			t.Errorf("exit code = %d, want 1", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("runPeerProxy did not return")
	}
}

func TestMainDispatchesPeerProxy(t *testing.T) {
	// main() is a bare switch; the case must exist verbatim so `pdx
	// peer-proxy` reaches runPeerProxy without touching config or HTTP.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `case "peer-proxy":`) || !strings.Contains(string(src), "os.Exit(runPeerProxy())") {
		t.Errorf("main.go does not dispatch peer-proxy to runPeerProxy")
	}
}
