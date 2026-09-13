package ccuds

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortTempDir returns a directory under /tmp so unix socket paths stay
// under macOS's 104-byte sun_path limit (t.TempDir is far too long).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pdxp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestBuildFrame_Golden(t *testing.T) {
	w := Wrapper{From: "uds:/tmp/cc-socks/1.sock", FromName: "a", FromMode: "bypass", Text: "hi"}
	line, err := BuildFrame("m1", "/tmp/cc-socks/1.sock", w)
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	want := `{"msgV":1,"msg_id":"m1","type":"user","priority":"next","from":"uds:/tmp/cc-socks/1.sock","message":{"role":"user","content":"<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"a\" from-mode=\"bypass\">\nhi\n</cross-session-message>"}}` + "\n"
	if string(line) != want {
		t.Fatalf("BuildFrame =\n%s\nwant\n%s", line, want)
	}
}

// TestBuildFrame_FieldSetExact: the harness drops frames it does not
// recognise, so the key set must be exactly what 2.1.270 accepts.
func TestBuildFrame_FieldSetExact(t *testing.T) {
	line, err := BuildFrame("m1", "/s.sock", Wrapper{From: "uds:/s.sock", FromName: "n", FromMode: "bypass", Text: "t"})
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}
	if !strings.HasSuffix(string(line), "\n") || strings.Count(string(line), "\n") != 1 {
		t.Fatalf("frame must be exactly one line with a trailing newline: %q", line)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(line, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantKeys := []string{"from", "message", "msgV", "msg_id", "priority", "type"}
	if len(top) != len(wantKeys) {
		t.Fatalf("top-level keys = %v, want %v", keysOf(top), wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := top[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(top["message"], &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if len(msg) != 2 || msg["role"] == nil || msg["content"] == nil {
		t.Fatalf("message keys = %v, want [content role]", keysOf(msg))
	}
	if string(top["msgV"]) != "1" || string(top["type"]) != `"user"` || string(top["priority"]) != `"next"` || string(msg["role"]) != `"user"` {
		t.Fatalf("constant fields wrong: %s", line)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestParseFrame_StringContent(t *testing.T) {
	line := []byte(`{"msgV":1,"msg_id":"abc","type":"user","priority":"next","from":"uds:/tmp/cc-socks/9.sock","message":{"role":"user","content":"<cross-session-message from=\"uds:/tmp/cc-socks/9.sock\" from-name=\"x\" from-mode=\"bypass\" hop-chain=\"0a\">\nreply\n</cross-session-message>"},"extra":true}` + "\n")
	f, err := ParseFrame(line)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if f.MsgV != 1 || f.MsgID != "abc" || f.Type != "user" || f.Priority != "next" || f.From != "uds:/tmp/cc-socks/9.sock" {
		t.Fatalf("header fields wrong: %+v", f)
	}
	if f.Message.Role != "user" {
		t.Fatalf("Role = %q", f.Message.Role)
	}
	w, ok := Parse(f.Message.Content)
	if !ok || w.Text != "reply" || w.HopChain != "0a" {
		t.Fatalf("content did not parse as wrapper: ok=%v %+v", ok, w)
	}
}

func TestParseFrame_BlockArrayContent(t *testing.T) {
	line := []byte(`{"msgV":1,"msg_id":"abc","type":"user","priority":"next","message":{"role":"user","content":[{"type":"text","text":"part one\n"},{"type":"image","source":{}},{"type":"text","text":"part two"}]}}`)
	f, err := ParseFrame(line)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if f.Message.Content != "part one\npart two" {
		t.Fatalf("Content = %q", f.Message.Content)
	}
	if f.From != "" {
		t.Fatalf("From = %q, want empty", f.From)
	}
}

func TestParseFrame_Errors(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"not json":         "hello\n",
		"content number":   `{"msgV":1,"msg_id":"a","type":"user","priority":"next","message":{"role":"user","content":42}}`,
		"content object":   `{"msgV":1,"msg_id":"a","type":"user","priority":"next","message":{"role":"user","content":{"text":"x"}}}`,
		"block not object": `{"msgV":1,"msg_id":"a","type":"user","priority":"next","message":{"role":"user","content":["plain"]}}`,
		"array json":       `[1,2]`,
	}
	for name, in := range cases {
		if f, err := ParseFrame([]byte(in)); err == nil {
			t.Errorf("%s: ParseFrame err = nil, got %+v", name, f)
		}
	}
}

func TestFromSocket(t *testing.T) {
	if p, ok := FromSocket("uds:/tmp/cc-socks/1.sock"); !ok || p != "/tmp/cc-socks/1.sock" {
		t.Fatalf("FromSocket(uds:) = %q, %v", p, ok)
	}
	for _, in := range []string{"", "/tmp/cc-socks/1.sock", "UDS:/x", "uds:", "tcp:127.0.0.1:1"} {
		if p, ok := FromSocket(in); ok {
			t.Errorf("FromSocket(%q) ok = true (%q)", in, p)
		}
	}
}

// listenAt binds a unix listener in a short temp dir and returns it with
// its path. The listener is closed at test end.
func listenAt(t *testing.T) (net.Listener, string) {
	t.Helper()
	dir := shortTempDir(t)
	path := filepath.Join(dir, "l.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln, path
}

// acceptOne accepts one conn on ln and hands it to handle in a goroutine.
// The conn is closed at test end so a handler that "never closes" still
// does not leak past the test.
func acceptOne(t *testing.T, ln net.Listener, handle func(c net.Conn)) {
	t.Helper()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { c.Close() })
		handle(c)
	}()
}

func TestWriteFrame_PeerReadsAllAndCloses(t *testing.T) {
	ln, path := listenAt(t)
	got := make(chan []byte, 1)
	acceptOne(t, ln, func(c net.Conn) {
		b, _ := io.ReadAll(c)
		c.Close()
		got <- b
	})
	line := []byte("{\"x\":1}\n")
	if err := WriteFrame(context.Background(), path, line, 2*time.Second); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	select {
	case b := <-got:
		if string(b) != string(line) {
			t.Fatalf("peer read %q, want %q", b, line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer never finished reading")
	}
}

func TestWriteFrame_PeerNeverReads_WriteIncomplete(t *testing.T) {
	ln, path := listenAt(t)
	acceptOne(t, ln, func(c net.Conn) { /* hold the conn, never read */ })
	line := make([]byte, 512*1024)
	for i := range line {
		line[i] = 'a'
	}
	line[len(line)-1] = '\n'
	const timeout = 300 * time.Millisecond
	start := time.Now()
	err := WriteFrame(context.Background(), path, line, timeout)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrWriteIncomplete) {
		t.Fatalf("err = %v, want ErrWriteIncomplete", err)
	}
	if elapsed > timeout+100*time.Millisecond {
		t.Fatalf("returned after %v, want within %v", elapsed, timeout+100*time.Millisecond)
	}
}

func TestWriteFrame_PeerReadsButNeverCloses_PostWriteTimeout(t *testing.T) {
	ln, path := listenAt(t)
	drained := make(chan struct{})
	acceptOne(t, ln, func(c net.Conn) {
		io.Copy(io.Discard, c) // returns at our half-close
		close(drained)
		// never close c
	})
	const timeout = 300 * time.Millisecond
	start := time.Now()
	err := WriteFrame(context.Background(), path, []byte("{}\n"), timeout)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrPostWriteTimeout) {
		t.Fatalf("err = %v, want ErrPostWriteTimeout", err)
	}
	if elapsed > timeout+100*time.Millisecond {
		t.Fatalf("returned after %v", elapsed)
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("peer never saw EOF from our half-close")
	}
}

func TestWriteFrame_NoListener_DialError(t *testing.T) {
	dir := shortTempDir(t)
	err := WriteFrame(context.Background(), filepath.Join(dir, "nobody.sock"), []byte("{}\n"), time.Second)
	if err == nil {
		t.Fatal("err = nil, want dial error")
	}
	if errors.Is(err, ErrWriteIncomplete) || errors.Is(err, ErrPostWriteTimeout) {
		t.Fatalf("dial error must not be classified as a write error: %v", err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("err = %T %v, want *net.OpError", err, err)
	}
}

func TestWriteFrame_ContextCancelDuringEOFWait(t *testing.T) {
	ln, path := listenAt(t)
	acceptOne(t, ln, func(c net.Conn) {
		io.Copy(io.Discard, c)
		// never close c
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := WriteFrame(ctx, path, []byte("{}\n"), 10*time.Second)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > time.Second {
		t.Fatalf("returned after %v, want promptly after cancel", elapsed)
	}
}
