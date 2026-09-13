package ccuds

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeProcStart never forks ps.
func fakeProcStart(pid int) (string, error) { return testProcStart, nil }

// peerOpts builds options confined to dir: registry files and the socket
// both land under it, never under ~/.claude/sessions or /tmp/cc-socks.
func peerOpts(dir string, pid int, name string) VirtualPeerOptions {
	return VirtualPeerOptions{
		PID:         pid,
		SockDir:     filepath.Join(dir, "socks"),
		RegistryDir: filepath.Join(dir, "reg"),
		Name:        name,
		Cwd:         "/Users/wake",
		Version:     "2.1.270",
		PidDomain:   "darwin",
		ProcStart:   fakeProcStart,
	}
}

func startPeer(t *testing.T, dir string, pid int, name string) *VirtualPeer {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "reg"), 0o700); err != nil {
		t.Fatal(err)
	}
	v, err := StartVirtualPeer(peerOpts(dir, pid, name))
	if err != nil {
		t.Fatalf("StartVirtualPeer(%d): %v", pid, err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

// closeWithin runs v.Close in a goroutine and fails if it has not returned
// within d.
func closeWithin(t *testing.T, v *VirtualPeer, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- v.Close() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Close did not return within %v", d)
		return nil
	}
}

func TestVirtualPeer_TwoPeersBindDistinctSockets(t *testing.T) {
	dir := shortTempDir(t)
	a := startPeer(t, dir, 1001, "a")
	b := startPeer(t, dir, 1002, "b")
	if a.SockPath() == b.SockPath() {
		t.Fatalf("same socket path %q", a.SockPath())
	}
	if filepath.Base(a.SockPath()) != "1001.sock" || filepath.Base(b.SockPath()) != "1002.sock" {
		t.Fatalf("socket names %q %q", a.SockPath(), b.SockPath())
	}
	for _, v := range []*VirtualPeer{a, b} {
		info, err := os.Lstat(v.SockPath())
		if err != nil {
			t.Fatalf("socket missing: %v", err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			t.Fatalf("%s is not a socket", v.SockPath())
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
		}
		c, err := net.DialTimeout("unix", v.SockPath(), time.Second)
		if err != nil {
			t.Fatalf("dial %s: %v", v.SockPath(), err)
		}
		c.Close()
		if len(v.Files()) != 2 {
			t.Fatalf("Files() = %v, want json + key", v.Files())
		}
	}
	sockDirInfo, err := os.Stat(filepath.Join(dir, "socks"))
	if err != nil || sockDirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("sock dir: %v mode %o, want created 0700", err, sockDirInfo.Mode().Perm())
	}
	// Registry files sit where a real Claude Code would look, with the
	// peer's own socket as messagingSocketPath.
	feats, ok := ReadPeerFeatures(filepath.Join(dir, "reg"), 1001)
	if !ok || len(feats) == 0 {
		t.Fatalf("registry for 1001 not readable: %v %v", feats, ok)
	}
	if got := RegistryProcStart(a.Files()[0]); got != testProcStart {
		t.Fatalf("registry procStart = %q", got)
	}
}

func TestVirtualPeer_FrameWrittenAppearsVerbatim(t *testing.T) {
	dir := shortTempDir(t)
	v := startPeer(t, dir, 2001, "target")
	w := Wrapper{From: "uds:/tmp/cc-socks/7.sock", FromName: "sender", FromMode: "bypass", Text: "hello <peer> & \"friends\""}
	line, err := BuildFrame("11111111-2222-4333-8444-555555555555", "/tmp/cc-socks/7.sock", w)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(context.Background(), v.SockPath(), line, 2*time.Second); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	select {
	case got, ok := <-v.Frames():
		if !ok {
			t.Fatal("Frames closed")
		}
		if got != strings.TrimSuffix(string(line), "\n") {
			t.Fatalf("frame =\n%q\nwant\n%q", got, line)
		}
		f, err := ParseFrame([]byte(got))
		if err != nil {
			t.Fatal(err)
		}
		pw, ok := Parse(f.Message.Content)
		if !ok || pw != w {
			t.Fatalf("round trip: %+v", pw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame delivered")
	}
}

func TestVirtualPeer_MultipleLinesOneConnAndEmptyLinesSkipped(t *testing.T) {
	dir := shortTempDir(t)
	v := startPeer(t, dir, 2002, "target")
	c, err := net.Dial("unix", v.SockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("one\n\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"one", "two"} {
		select {
		case got := <-v.Frames():
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("frame %q never arrived", want)
		}
	}
}

func TestVirtualPeer_ConnWithoutNewlineDoesNotBlockClose(t *testing.T) {
	dir := shortTempDir(t)
	v := startPeer(t, dir, 2003, "target")
	c, err := net.Dial("unix", v.SockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(`{"msgV":1,"partial":`)); err != nil {
		t.Fatal(err)
	}
	// Let the accept loop pick the conn up before Close runs.
	time.Sleep(50 * time.Millisecond)
	if err := closeWithin(t, v, time.Second); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The peer closed our conn: a read must not hang and must not succeed.
	c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	if n, err := c.Read(buf); err == nil {
		t.Fatalf("read after Close returned %d bytes, want error", n)
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("conn still open after Close (read timed out)")
	}
}

func TestVirtualPeer_ConsumerNeverReadsDoesNotBlockClose(t *testing.T) {
	dir := shortTempDir(t)
	v := startPeer(t, dir, 2004, "target")
	c, err := net.Dial("unix", v.SockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Far more lines than any internal buffer so the reader goroutine is
	// parked in its send when Close runs.
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("line\n")
	}
	if _, err := c.Write([]byte(sb.String())); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := closeWithin(t, v, time.Second); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestVirtualPeer_CloseRemovesEverythingAndIsIdempotent(t *testing.T) {
	dir := shortTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "reg"), 0o700); err != nil {
		t.Fatal(err)
	}
	v, err := StartVirtualPeer(peerOpts(dir, 2005, "target"))
	if err != nil {
		t.Fatal(err)
	}
	sock := v.SockPath()
	files := v.Files()
	if err := v.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket still present: %v", err)
	}
	for _, f := range files {
		if _, err := os.Lstat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still present: %v", f, err)
		}
	}
	select {
	case _, ok := <-v.Frames():
		if ok {
			t.Fatal("Frames delivered a value after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Frames not closed after Close")
	}
	if err := v.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	if _, err := net.DialTimeout("unix", sock, 500*time.Millisecond); err == nil {
		t.Fatal("socket still accepting after Close")
	}
}

func TestVirtualPeer_StaleSocketReplaced(t *testing.T) {
	dir := shortTempDir(t)
	sockDir := filepath.Join(dir, "socks")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(sockDir, "2006.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // leaves an orphan socket file: dialing it is refused
	if _, err := os.Lstat(stale); err != nil {
		t.Fatalf("stale socket should remain for the test: %v", err)
	}
	v := startPeer(t, dir, 2006, "target")
	c, err := net.DialTimeout("unix", v.SockPath(), time.Second)
	if err != nil {
		t.Fatalf("new peer not reachable after replacing stale socket: %v", err)
	}
	c.Close()
}

func TestVirtualPeer_LiveForeignListenerIsError(t *testing.T) {
	dir := shortTempDir(t)
	sockDir := filepath.Join(dir, "socks")
	regDir := filepath.Join(dir, "reg")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(regDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filenameFor(sockDir, 2007)
	foreign, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	v, err := StartVirtualPeer(peerOpts(dir, 2007, "target"))
	if err == nil {
		v.Close()
		t.Fatal("StartVirtualPeer succeeded over a live foreign listener")
	}
	// The foreign listener and its socket are untouched.
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("foreign socket was removed: %v", err)
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("foreign listener no longer reachable: %v", err)
	}
	c.Close()
	// No registry files were left behind.
	entries, err := os.ReadDir(regDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("registry dir not clean after failed start: %v", entries)
	}
}

func TestVirtualPeer_RegistryFailureUndoesSocket(t *testing.T) {
	dir := shortTempDir(t)
	regDir := filepath.Join(dir, "reg")
	if err := os.MkdirAll(regDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pre-occupy <pid>.json so WriteRegistry is refused.
	if err := os.WriteFile(filepath.Join(regDir, "2008.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := StartVirtualPeer(peerOpts(dir, 2008, "target"))
	if err == nil {
		v.Close()
		t.Fatal("StartVirtualPeer succeeded with an occupied registry slot")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	if _, err := os.Lstat(filenameFor(filepath.Join(dir, "socks"), 2008)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind after failed start: %v", err)
	}
}

func filenameFor(sockDir string, pid int) string {
	return filepath.Join(sockDir, strconv.Itoa(pid)+".sock")
}
