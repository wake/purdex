package proxyhelper_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/peers/proxyhelper"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
)

const sessionA = "aaaaaaaa-1111-4111-8111-111111111111"

func testConfig(reg, socks string) proxyhelper.Config {
	return proxyhelper.Config{
		Name:        "air/foo",
		RegistryDir: reg,
		SockDir:     socks,
		Version:     "2.1.270",
		Cwd:         "/Users/wake",
		SessionID:   sessionA,
	}
}

// tempDirs is proxyhelpertest.TempDirs in this file's (registry, socks)
// argument order.
func tempDirs(t *testing.T) (reg, socks string) {
	t.Helper()
	socks, reg = proxyhelpertest.TempDirs(t)
	return reg, socks
}

var (
	exists      = proxyhelpertest.Exists
	writeToSock = proxyhelpertest.WriteToSock
)

func recvFrame(t *testing.T, h proxyhelper.Handle, within time.Duration) string {
	t.Helper()
	select {
	case f, ok := <-h.Frames():
		if !ok {
			t.Fatalf("Frames closed before a frame arrived")
		}
		return f
	case <-time.After(within):
		t.Fatalf("no frame within %v", within)
		return ""
	}
}

// assertFramesClosed fails unless Frames closes within d.
func assertFramesClosed(t *testing.T, h proxyhelper.Handle, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case _, ok := <-h.Frames():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("Frames not closed within %v", d)
		}
	}
}

// assertNoGoroutineGrowth polls for at most 1 s until the goroutine count
// is back at or below before.
func assertNoGoroutineGrowth(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutines: %d before, %d after 1 s", before, runtime.NumGoroutine())
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func stopWithin(t *testing.T, h proxyhelper.Handle, grace, within time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- h.Stop(grace) }()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("Stop(%v) did not return within %v", grace, within)
		return nil
	}
}

func TestSpawn_HappyPath(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if h.PID() < 900000 {
		t.Errorf("pid = %d, want ≥ 900000", h.PID())
	}
	if want := filepath.Join(socks, strconv.Itoa(h.PID())+".sock"); h.Sock() != want {
		t.Errorf("sock = %q, want %q", h.Sock(), want)
	}
	if len(h.Files()) != 2 {
		t.Fatalf("files = %v, want 2", h.Files())
	}
	for _, p := range append([]string{h.Sock()}, h.Files()...) {
		if !exists(p) {
			t.Errorf("%s missing after ready", p)
		}
	}
	if f.Spawns() != 1 {
		t.Errorf("Spawns = %d, want 1", f.Spawns())
	}

	// Both lines on ONE connection: the peer reads each accepted connection
	// on its own goroutine, so only same-connection order is a contract.
	c, err := net.Dial("unix", h.Sock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write([]byte(`{"type":"user","msg_id":"1"}` + "\n" + `{"type":"user","msg_id":"2"}` + "\n")); err != nil {
		t.Fatalf("write frames: %v", err)
	}
	c.Close()
	if got := recvFrame(t, h, 2*time.Second); got != `{"type":"user","msg_id":"1"}` {
		t.Errorf("frame 1 = %q", got)
	}
	if got := recvFrame(t, h, 2*time.Second); got != `{"type":"user","msg_id":"2"}` {
		t.Errorf("frame 2 = %q", got)
	}

	if err := stopWithin(t, h, 2*time.Second, 3*time.Second); err != nil {
		t.Errorf("Stop: %v", err)
	}
	assertFramesClosed(t, h, time.Second)
	for _, p := range append([]string{h.Sock()}, h.Files()...) {
		if exists(p) {
			t.Errorf("%s left behind after Stop", p)
		}
	}
	if f.Stops() != 1 || f.Signals() != 0 {
		t.Errorf("Stops = %d, Signals = %d; want 1, 0 (graceful stop)", f.Stops(), f.Signals())
	}
}

func TestSpawn_BrokenIsErrNotReadyWithoutLeaks(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{Variant: proxyhelpertest.Broken})
	before := runtime.NumGoroutine()
	start := time.Now()
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 200*time.Millisecond)
	elapsed := time.Since(start)
	if h != nil || !errors.Is(err, proxyhelper.ErrNotReady) {
		t.Fatalf("Spawn = %v, %v; want nil, ErrNotReady", h, err)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("Spawn took %v, want ≤ readyTimeout+100ms", elapsed)
	}
	if f.Stops() != 1 {
		t.Errorf("Wait not observed: Stops = %d", f.Stops())
	}
	if f.Signals() == 0 {
		t.Errorf("a stalled helper must be killed: Signals = 0")
	}
	assertNoGoroutineGrowth(t, before)
}

func TestSpawn_RefusingIsErrNotReady(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{Variant: proxyhelpertest.Refusing})
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if h != nil || !errors.Is(err, proxyhelper.ErrNotReady) {
		t.Fatalf("Spawn = %v, %v; want nil, ErrNotReady", h, err)
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("error %q should carry the helper's reason", err)
	}
	if f.Stops() != 1 {
		t.Errorf("Wait not observed: Stops = %d", f.Stops())
	}
}

func TestSpawn_BrokenRegisteredLeftoversRemoved(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{Variant: proxyhelpertest.BrokenRegistered})
	pid := proxyhelpertest.PeekPID()
	// A foreign key file for the same pid but another process: untouched.
	foreignKey := filepath.Join(reg, strconv.Itoa(pid)+".ffff.key")
	if err := os.WriteFile(foreignKey, []byte(`{"peerToken":"x","procStart":"Mon Jan  1 00:00:00 2001","pidDomain":"darwin"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 200*time.Millisecond)
	if h != nil || !errors.Is(err, proxyhelper.ErrNotReady) {
		t.Fatalf("Spawn = %v, %v; want nil, ErrNotReady", h, err)
	}
	if got := f.LastPID(); got != pid {
		t.Fatalf("fake used pid %d, PeekPID said %d", got, pid)
	}
	jsonPath := filepath.Join(reg, strconv.Itoa(pid)+".json")
	if exists(jsonPath) {
		t.Errorf("%s left behind (sessionId matched — must be removed)", jsonPath)
	}
	keys, _ := filepath.Glob(filepath.Join(reg, strconv.Itoa(pid)+".*.key"))
	if len(keys) != 1 || keys[0] != foreignKey {
		t.Errorf("key files after cleanup = %v, want only the foreign %s", keys, foreignKey)
	}
	if sock := filepath.Join(socks, strconv.Itoa(pid)+".sock"); exists(sock) {
		t.Errorf("%s left behind (nobody listens — must be removed)", sock)
	}
}

func TestSpawn_BrokenForeignJSONUntouched(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{Variant: proxyhelpertest.Broken})
	pid := proxyhelpertest.PeekPID()
	// Another session already owns <pid>.json (pid reuse): a different
	// sessionId proves it is not ours.
	foreignJSON := filepath.Join(reg, strconv.Itoa(pid)+".json")
	body := `{"pid":` + strconv.Itoa(pid) + `,"sessionId":"bbbbbbbb-2222-4222-8222-222222222222","procStart":"Mon Jan  1 00:00:00 2001"}`
	if err := os.WriteFile(foreignJSON, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	foreignKey := filepath.Join(reg, strconv.Itoa(pid)+".eeee.key")
	if err := os.WriteFile(foreignKey, []byte(`{"peerToken":"x","procStart":"Mon Jan  1 00:00:00 2001","pidDomain":"darwin"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 200*time.Millisecond)
	if h != nil || !errors.Is(err, proxyhelper.ErrNotReady) {
		t.Fatalf("Spawn = %v, %v; want nil, ErrNotReady", h, err)
	}
	if got := f.LastPID(); got != pid {
		t.Fatalf("fake used pid %d, PeekPID said %d", got, pid)
	}
	if !exists(foreignJSON) {
		t.Errorf("foreign %s was removed", foreignJSON)
	}
	if !exists(foreignKey) {
		t.Errorf("foreign %s was removed", foreignKey)
	}
}

func TestHandle_StopIsIdempotentAndConcurrencySafe(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = h.Stop(time.Second)
		}()
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("Stop #%d: %v", i, e)
		}
	}
	if err := h.Stop(time.Second); err != nil {
		t.Errorf("late Stop: %v", err)
	}
	if f.Stops() != 1 {
		t.Errorf("Stops = %d, want 1 (Wait once)", f.Stops())
	}
	assertFramesClosed(t, h, time.Second)
}

func TestHandle_StopWhileFloodedAndUnread(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	before := runtime.NumGoroutine()
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Flood 10 000 frames while nobody reads Frames. The writer blocks once
	// every buffer is full and is released when the helper closes the
	// socket, so it runs on its own goroutine and ignores errors.
	c, err := net.Dial("unix", h.Sock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	floodDone := make(chan struct{})
	go func() {
		defer close(floodDone)
		defer c.Close()
		line := []byte(`{"type":"user","message":{"role":"user","content":"flood"}}` + "\n")
		for i := 0; i < 10000; i++ {
			if _, err := c.Write(line); err != nil {
				return
			}
		}
	}()
	// Give the pipeline time to wedge on the unread Frames channel.
	time.Sleep(100 * time.Millisecond)

	grace := 500 * time.Millisecond
	_ = stopWithin(t, h, grace, grace+time.Second)
	assertFramesClosed(t, h, time.Second)
	select {
	case <-floodDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("flood writer still blocked after Stop")
	}
	assertNoGoroutineGrowth(t, before)
}

func TestSpawn_CancelledCallerContextDoesNotKillReadyHelper(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	procCtx := context.Background()
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	h, err := proxyhelper.Spawn(procCtx, f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer h.Stop(time.Second)
	// The caller bounded only its own wait; cancelling it must not reach
	// the process.
	cancelCaller()
	<-callerCtx.Done()
	time.Sleep(50 * time.Millisecond)

	writeToSock(t, h.Sock(), `{"type":"user","after":"cancel"}`)
	if got := recvFrame(t, h, 2*time.Second); got != `{"type":"user","after":"cancel"}` {
		t.Errorf("frame = %q", got)
	}
	if f.Signals() != 0 {
		t.Errorf("helper was signalled %d times", f.Signals())
	}
}

func TestHandle_HelperExitOnItsOwnClosesFramesAndStopReturns(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	f.ExitOnItsOwn()
	assertFramesClosed(t, h, 2*time.Second)
	_ = stopWithin(t, h, time.Second, 2*time.Second)
}

func TestSpawn_HoldStopBlocksStopUntilReleased(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	hold := make(chan struct{})
	f.HoldStop(hold)
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Stop(50 * time.Millisecond) }()
	select {
	case err := <-done:
		t.Fatalf("Stop returned %v while held", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(hold)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop did not return after the hold was released")
	}
}

func TestSpawn_BarrierBlocksReadyUntilRelease(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{Variant: proxyhelpertest.Barrier})
	type res struct {
		h   proxyhelper.Handle
		err error
	}
	ch := make(chan res, 1)
	go func() {
		h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 5*time.Second)
		ch <- res{h, err}
	}()
	select {
	case r := <-ch:
		t.Fatalf("Spawn returned (%v, %v) before Release", r.h, r.err)
	case <-time.After(200 * time.Millisecond):
	}
	f.Release()
	var r res
	select {
	case r = <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("Spawn did not return after Release")
	}
	if r.err != nil {
		t.Fatalf("Spawn after Release: %v", r.err)
	}
	defer r.h.Stop(time.Second)
	writeToSock(t, r.h.Sock(), `{"type":"user"}`)
	if got := recvFrame(t, r.h, 2*time.Second); got != `{"type":"user"}` {
		t.Errorf("frame = %q", got)
	}
}

// writeScript writes an executable shell script standing in for the pdx
// binary; ExecStarter runs it as `<script> peer-proxy`.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pdx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecStarter_RealProcessHappyPath(t *testing.T) {
	exe := writeScript(t, `
[ "$1" = "peer-proxy" ] || { echo "bad argv: $*" >&2; exit 2; }
[ -z "$PDX_LEAKED" ] || { echo "env leaked" >&2; exit 2; }
read line
echo "config: $line" >&2
echo '{"ready":true,"pid":'$$',"sock":"/tmp/none.sock","files":["/tmp/a.json","/tmp/a.key"]}'
echo '{"frame":"hello"}'
echo 'garbage line'
cat >/dev/null
`)
	t.Setenv("PDX_LEAKED", "1")
	var stderr safeBuffer
	start := proxyhelper.ExecStarter(exe, &stderr)
	h, err := proxyhelper.Spawn(context.Background(), start, proxyhelper.Config{Name: "x", SessionID: sessionA}, 5*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v (stderr: %s)", err, stderr.String())
	}
	if h.Sock() != "/tmp/none.sock" || len(h.Files()) != 2 {
		t.Errorf("Handle = sock %q files %v", h.Sock(), h.Files())
	}
	if got := recvFrame(t, h, 2*time.Second); got != "hello" {
		t.Errorf("frame = %q", got)
	}
	if err := stopWithin(t, h, 2*time.Second, 4*time.Second); err != nil {
		t.Errorf("Stop: %v", err)
	}
	assertFramesClosed(t, h, time.Second)
	if err := syscall.Kill(h.PID(), 0); err == nil {
		t.Errorf("pid %d still alive after Stop", h.PID())
	}
	prefix := "peer-proxy[" + strconv.Itoa(h.PID()) + "]: config: "
	if !strings.Contains(stderr.String(), prefix+`{"name":"x"`) {
		t.Errorf("stderr = %q, want a %q-prefixed config echo", stderr.String(), prefix)
	}
}

func TestExecStarter_AbortKillsProcess(t *testing.T) {
	// cat echoes the config line back as the "ready" line: a decode error.
	exe := writeScript(t, "exec cat\n")
	var stderr safeBuffer
	h, err := proxyhelper.Spawn(context.Background(), proxyhelper.ExecStarter(exe, &stderr), proxyhelper.Config{Name: "x", SessionID: sessionA, RegistryDir: t.TempDir(), SockDir: t.TempDir()}, 5*time.Second)
	if h != nil || !errors.Is(err, proxyhelper.ErrNotReady) {
		t.Fatalf("Spawn = %v, %v; want nil, ErrNotReady", h, err)
	}
}

func TestExecStarter_StalledProcessIsKilled(t *testing.T) {
	// exec keeps it a single process: SIGKILL leaves no orphan behind.
	exe := writeScript(t, "read line\nexec sleep 30\n")
	var stderr safeBuffer
	start := time.Now()
	h, err := proxyhelper.Spawn(context.Background(), proxyhelper.ExecStarter(exe, &stderr), proxyhelper.Config{Name: "x", SessionID: sessionA, RegistryDir: t.TempDir(), SockDir: t.TempDir()}, 300*time.Millisecond)
	if h != nil || !errors.Is(err, proxyhelper.ErrNotReady) {
		t.Fatalf("Spawn = %v, %v; want nil, ErrNotReady", h, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Spawn took %v; the stalled process must be SIGKILLed, not waited for", elapsed)
	}
}

func TestHandle_OverlongStdoutLineSurfacesErrTooLong(t *testing.T) {
	// After the ready line the process emits a 5 MiB line with no newline:
	// the pump cannot frame it, closes Frames and reports why.
	exe := writeScript(t, `
read line
echo '{"ready":true,"pid":'$$',"sock":"/tmp/none.sock","files":[]}'
exec head -c 5242880 /dev/zero
`)
	var stderr safeBuffer
	var logged safeBuffer
	origLogf := proxyhelper.Logf
	proxyhelper.Logf = func(format string, args ...any) { fmt.Fprintf(&logged, format+"\n", args...) }
	t.Cleanup(func() { proxyhelper.Logf = origLogf })

	h, err := proxyhelper.Spawn(context.Background(), proxyhelper.ExecStarter(exe, &stderr), proxyhelper.Config{Name: "x", SessionID: sessionA, RegistryDir: t.TempDir(), SockDir: t.TempDir()}, 5*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	assertFramesClosed(t, h, 5*time.Second)
	if !errors.Is(h.Err(), bufio.ErrTooLong) {
		t.Errorf("Err() = %v, want bufio.ErrTooLong", h.Err())
	}
	if !strings.Contains(logged.String(), "stdout pump ended") || !strings.Contains(logged.String(), "too long") {
		t.Errorf("Logf did not receive the pump error: %q", logged.String())
	}
	// The process is stuck writing into a pipe nobody drains: Stop must
	// still return after grace + SIGKILL.
	_ = stopWithin(t, h, 200*time.Millisecond, 3*time.Second)
}

func TestHandle_ErrIsNilAfterCleanStop(t *testing.T) {
	reg, socks := tempDirs(t)
	f := proxyhelpertest.New(proxyhelpertest.Options{})
	h, err := proxyhelper.Spawn(context.Background(), f.Starter(), testConfig(reg, socks), 2*time.Second)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := stopWithin(t, h, time.Second, 2*time.Second); err != nil {
		t.Errorf("Stop: %v", err)
	}
	assertFramesClosed(t, h, time.Second)
	if h.Err() != nil {
		t.Errorf("Err() after a clean Stop = %v, want nil", h.Err())
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
