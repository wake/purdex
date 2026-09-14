package proxyhelper

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/peers/ccuds"
)

const testProcStart = "Sun Sep 13 18:57:56 2026"

func fakeProcStart(int) (string, error) { return testProcStart, nil }

// tempDirs returns short registry and socket dirs under /tmp (Unix socket
// paths are length-limited) with the registry dir created.
func tempDirs(t *testing.T) (reg, socks string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pdxp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	reg = filepath.Join(root, "reg")
	if err := os.MkdirAll(reg, 0o700); err != nil {
		t.Fatal(err)
	}
	return reg, filepath.Join(root, "socks")
}

func testConfig(reg, socks string) Config {
	return Config{
		Name:         "air/foo",
		RegistryDir:  reg,
		SockDir:      socks,
		Version:      "2.1.270",
		Cwd:          "/Users/wake",
		SessionID:    "11111111-2222-4333-8444-555555555555",
		PeerFeatures: []string{"notify_idle"},
	}
}

// runner drives Run over pipes the way the daemon does.
type runner struct {
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	out     *bufio.Reader
	done    chan error
	cancel  context.CancelFunc
	sigs    chan os.Signal
	exited  bool // set once waitDone has consumed done
}

// startRun runs Run in a goroutine with a test-owned Signals channel and
// fakeProcStart; PID is passed through untouched (0 ⇒ Run's own default).
func startRun(t *testing.T, o Options) *runner {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{stdinW: stdinW, stdoutR: stdoutR, out: bufio.NewReader(stdoutR), done: make(chan error, 1), cancel: cancel}
	r.sigs = make(chan os.Signal, 1)
	o.Signals = r.sigs
	if o.ProcStart == nil {
		o.ProcStart = fakeProcStart
	}
	go func() {
		err := Run(ctx, stdinR, stdoutW, o)
		stdoutW.Close()
		r.done <- err
	}()
	t.Cleanup(func() {
		cancel()
		stdinW.Close()
		stdoutR.Close()
		if r.exited {
			return
		}
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Errorf("Run did not return after cleanup")
		}
	})
	return r
}

func (r *runner) writeConfig(t *testing.T, cfg Config) {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.stdinW.Write(append(b, '\n')); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func (r *runner) readLine(t *testing.T) string {
	t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := r.out.ReadString('\n')
		ch <- res{l, err}
	}()
	select {
	case x := <-ch:
		if x.err != nil {
			t.Fatalf("read stdout line: %v (got %q)", x.err, x.line)
		}
		return strings.TrimSuffix(x.line, "\n")
	case <-time.After(3 * time.Second):
		t.Fatalf("no stdout line within 3s")
		return ""
	}
}

type readyMsg struct {
	Ready bool     `json:"ready"`
	PID   int      `json:"pid"`
	Sock  string   `json:"sock"`
	Files []string `json:"files"`
	Error string   `json:"error"`
}

func (r *runner) ready(t *testing.T) readyMsg {
	t.Helper()
	line := r.readLine(t)
	var m readyMsg
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("ready line not JSON: %v (%q)", err, line)
	}
	return m
}

func (r *runner) mustReady(t *testing.T) readyMsg {
	t.Helper()
	m := r.ready(t)
	if !m.Ready {
		t.Fatalf("ready = false: %s", m.Error)
	}
	return m
}

func (r *runner) waitDone(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.exited = true
		return err
	case <-time.After(within):
		t.Fatalf("Run did not return within %v", within)
		return nil
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func assertGone(t *testing.T, m readyMsg) {
	t.Helper()
	if exists(m.Sock) {
		t.Errorf("socket %s still exists", m.Sock)
	}
	for _, f := range m.Files {
		if exists(f) {
			t.Errorf("registry file %s still exists", f)
		}
	}
}

func writeToSock(t *testing.T, sock string, line []byte) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial %s: %v", sock, err)
	}
	defer c.Close()
	if _, err := c.Write(line); err != nil {
		t.Fatalf("write to %s: %v", sock, err)
	}
}

func TestRun_ReadyLineThenStdinEOFCleansUp(t *testing.T) {
	reg, socks := tempDirs(t)
	r := startRun(t, Options{PID: 910001})
	r.writeConfig(t, testConfig(reg, socks))

	m := r.mustReady(t)
	if m.PID != 910001 {
		t.Errorf("pid = %d, want 910001", m.PID)
	}
	if want := filepath.Join(socks, "910001.sock"); m.Sock != want {
		t.Errorf("sock = %q, want %q", m.Sock, want)
	}
	if len(m.Files) != 2 {
		t.Fatalf("files = %v, want 2 entries", m.Files)
	}
	if want := filepath.Join(reg, "910001.json"); m.Files[0] != want {
		t.Errorf("files[0] = %q, want %q", m.Files[0], want)
	}
	if !strings.HasSuffix(m.Files[1], ".key") || !strings.HasPrefix(filepath.Base(m.Files[1]), "910001.") {
		t.Errorf("files[1] = %q, want <reg>/910001.<sha>.key", m.Files[1])
	}
	// Both files and the socket exist the instant the ready line is observed.
	for _, p := range append([]string{m.Sock}, m.Files...) {
		if !exists(p) {
			t.Errorf("%s does not exist at ready time", p)
		}
	}
	if got := ccuds.RegistryProcStart(m.Files[0]); got != testProcStart {
		t.Errorf("json procStart = %q, want %q", got, testProcStart)
	}

	r.stdinW.Close()
	if err := r.waitDone(t, time.Second); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	assertGone(t, m)
}

func TestRun_EchoesInboundFrameByteExact(t *testing.T) {
	reg, socks := tempDirs(t)
	r := startRun(t, Options{PID: 910003})
	r.writeConfig(t, testConfig(reg, socks))
	m := r.mustReady(t)

	frame, err := ccuds.BuildFrame("m1", "/tmp/x.sock", ccuds.Wrapper{From: "uds:/tmp/x.sock", FromName: "n", Text: "hi <b> & \"q\" \\ é"})
	if err != nil {
		t.Fatal(err)
	}
	writeToSock(t, m.Sock, frame)

	line := r.readLine(t)
	var fl struct {
		Frame *string `json:"frame"`
	}
	if err := json.Unmarshal([]byte(line), &fl); err != nil || fl.Frame == nil {
		t.Fatalf("frame line = %q (err %v)", line, err)
	}
	if want := strings.TrimSuffix(string(frame), "\n"); *fl.Frame != want {
		t.Errorf("frame = %q, want %q", *fl.Frame, want)
	}
}

func TestRun_SignalCleansUp(t *testing.T) {
	reg, socks := tempDirs(t)
	r := startRun(t, Options{PID: 910004})
	r.writeConfig(t, testConfig(reg, socks))
	m := r.mustReady(t)
	r.sigs <- syscall.SIGTERM
	if err := r.waitDone(t, time.Second); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	assertGone(t, m)
}

func TestRun_ContextCancelCleansUp(t *testing.T) {
	reg, socks := tempDirs(t)
	r := startRun(t, Options{PID: 910005})
	r.writeConfig(t, testConfig(reg, socks))
	m := r.mustReady(t)
	r.cancel()
	if err := r.waitDone(t, time.Second); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	assertGone(t, m)
}

func TestRun_StdoutClosedByReaderExitsAndCleansUp(t *testing.T) {
	reg, socks := tempDirs(t)
	r := startRun(t, Options{PID: 910006})
	r.writeConfig(t, testConfig(reg, socks))
	m := r.mustReady(t)
	// The daemon is gone: its read end is closed. The helper notices on its
	// next stdout write and treats it like stdin EOF.
	r.stdoutR.Close()
	writeToSock(t, m.Sock, []byte(`{"type":"user"}`+"\n"))
	if err := r.waitDone(t, time.Second); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	assertGone(t, m)
}

func TestRun_UnwritableRegistryDirRefuses(t *testing.T) {
	_, socks := tempDirs(t)
	r := startRun(t, Options{PID: 910007})
	r.writeConfig(t, testConfig(filepath.Join(socks, "missing-reg"), socks))
	m := r.ready(t)
	if m.Ready || m.Error == "" {
		t.Fatalf("ready = %+v, want ready:false with an error", m)
	}
	if err := r.waitDone(t, time.Second); err == nil {
		t.Errorf("Run returned nil, want the registry error")
	}
	if exists(filepath.Join(socks, "910007.sock")) {
		t.Errorf("socket left behind after a refused start")
	}
}

func TestRun_NonJSONConfigRefuses(t *testing.T) {
	r := startRun(t, Options{PID: 910008})
	if _, err := r.stdinW.Write([]byte("this is not json\n")); err != nil {
		t.Fatal(err)
	}
	m := r.ready(t)
	if m.Ready || m.Error == "" {
		t.Fatalf("ready = %+v, want ready:false with an error", m)
	}
	if err := r.waitDone(t, time.Second); err == nil {
		t.Errorf("Run returned nil, want a config error")
	}
}

func TestRun_StdinEOFBeforeConfigRefuses(t *testing.T) {
	r := startRun(t, Options{PID: 910009})
	r.stdinW.Close()
	m := r.ready(t)
	if m.Ready || m.Error == "" {
		t.Fatalf("ready = %+v, want ready:false with an error", m)
	}
	if err := r.waitDone(t, time.Second); err == nil {
		t.Errorf("Run returned nil, want a config error")
	}
}

func TestRun_ZeroPIDMeansOwnPID(t *testing.T) {
	reg, socks := tempDirs(t)
	r := startRun(t, Options{})
	r.writeConfig(t, testConfig(reg, socks))
	m := r.mustReady(t)
	if m.PID != os.Getpid() {
		t.Errorf("pid = %d, want os.Getpid() %d", m.PID, os.Getpid())
	}
	if base := filepath.Base(m.Sock); base != strconv.Itoa(os.Getpid())+".sock" {
		t.Errorf("sock = %q, want <dir>/%d.sock", m.Sock, os.Getpid())
	}
}
