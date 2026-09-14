// cmd/pdx/shutdown_test.go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// step is one recorded call in the shutdown sequence: its name, when it
// happened, and the ctx it received (nil for calls that take none).
type step struct {
	name string
	at   time.Time
	ctx  context.Context
}

// recorder collects steps from fakes running on different goroutines.
type recorder struct {
	mu    sync.Mutex
	steps []step
}

func (r *recorder) add(name string, ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step{name: name, at: time.Now(), ctx: ctx})
}

func (r *recorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.steps))
	for _, s := range r.steps {
		out = append(out, s.name)
	}
	return out
}

func (r *recorder) find(name string) (step, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.steps {
		if s.name == name {
			return s, true
		}
	}
	return step{}, false
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.steps {
		if s.name == name {
			n++
		}
	}
	return n
}

// fakeTarget is a shutdownTarget whose StopModules / CloseModules record
// themselves and can be made to fail, ignore ctx, or block.
type fakeTarget struct {
	rec       *recorder
	stopErr   error
	stopHook  func(ctx context.Context) // runs inside StopModules after recording
	closeErr  error
	closeGate chan struct{} // if non-nil, CloseModules blocks until it is closed
	stops     atomic.Int32
}

func (t *fakeTarget) StopModules(ctx context.Context) error {
	t.stops.Add(1)
	t.rec.add("StopModules", ctx)
	if t.stopHook != nil {
		t.stopHook(ctx)
	}
	return t.stopErr
}

func (t *fakeTarget) CloseModules() error {
	if t.closeGate != nil {
		<-t.closeGate
	}
	t.rec.add("CloseModules", nil)
	return t.closeErr
}

// fakeServer is a server whose Serve either fails immediately (serveErr)
// or blocks until Shutdown/Close, then returns http.ErrServerClosed — the
// same contract *http.Server honours.
type fakeServer struct {
	rec         *recorder
	serveErr    error
	shutdownErr error
	done        chan struct{}
	closeOnce   sync.Once
}

func newFakeServer(rec *recorder) *fakeServer {
	return &fakeServer{rec: rec, done: make(chan struct{})}
}

func (s *fakeServer) release() { s.closeOnce.Do(func() { close(s.done) }) }

func (s *fakeServer) Serve(net.Listener) error {
	if s.serveErr != nil {
		return s.serveErr
	}
	<-s.done
	return http.ErrServerClosed
}

func (s *fakeServer) Shutdown(ctx context.Context) error {
	s.rec.add("Shutdown", ctx)
	s.release()
	return s.shutdownErr
}

func (s *fakeServer) Close() error {
	s.rec.add("Close", nil)
	s.release()
	return nil
}

// harness wires a recorder, fake target, fake server, recording cancel
// and a log sink so each test only states what differs.
type harness struct {
	rec    *recorder
	target *fakeTarget
	srv    *fakeServer
	sig    chan os.Signal
	logs   []string
	logMu  sync.Mutex

	exitMu    sync.Mutex
	exitCalls []int // records exit() calls instead of ever calling real os.Exit
}

func newHarness() *harness {
	rec := &recorder{}
	return &harness{
		rec:    rec,
		target: &fakeTarget{rec: rec},
		srv:    newFakeServer(rec),
		sig:    make(chan os.Signal, 1),
	}
}

func (h *harness) cancel() { h.rec.add("cancel", nil) }

func (h *harness) logf(format string, args ...any) {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	h.logs = append(h.logs, fmt.Sprintf(format, args...))
}

func (h *harness) logged() []string {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	return append([]string(nil), h.logs...)
}

// exit is the harness's injected exit func: it records the code instead of
// ever calling the real os.Exit, which would kill the test binary.
func (h *harness) exit(code int) {
	h.exitMu.Lock()
	defer h.exitMu.Unlock()
	h.exitCalls = append(h.exitCalls, code)
}

func (h *harness) exited() []int {
	h.exitMu.Lock()
	defer h.exitMu.Unlock()
	return append([]int(nil), h.exitCalls...)
}

// run calls serveAndWait with the harness fakes and the given budget.
func (h *harness) run(budget time.Duration) error {
	return serveAndWait(h.srv, nil, h.sig, h.cancel, h.target, budget, h.logf, h.exit)
}

// runAsync calls run on a goroutine and returns a channel that yields
// the result once serveAndWait returns.
func (h *harness) runAsync(budget time.Duration) <-chan error {
	out := make(chan error, 1)
	go func() { out <- h.run(budget) }()
	return out
}

func equalSteps(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

const testBudget = time.Second

func TestServeAndWait_SignalRunsSequenceInOrder(t *testing.T) {
	h := newHarness()
	h.sig <- syscall.SIGTERM

	err := h.run(testBudget)
	if err != nil {
		t.Fatalf("serveAndWait returned %v, want nil (ErrServerClosed is swallowed)", err)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	if h.rec.count("Close") != 0 {
		t.Fatalf("Close must not be called when Shutdown succeeds; steps = %v", h.rec.names())
	}
}

func TestServeAndWait_ServeFailsFirstStillRunsSequence(t *testing.T) {
	h := newHarness()
	bindLost := errors.New("bind lost")
	h.srv.serveErr = bindLost
	// No signal is ever sent.

	err := h.run(testBudget)
	if !errors.Is(err, bindLost) {
		t.Fatalf("serveAndWait returned %v, want %v", err, bindLost)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

// TestServeAndWait_SignalAndServeErrorRaceRunsSequenceOnce also guards
// against a stale first signal being misread as a second one: sig already
// has a buffered value when serveAndWait starts, so when the serveErr
// branch wins the initial select instead, that value must be drained
// before the second-signal watcher is armed — otherwise the watcher would
// immediately "see" it and call exit(130), a hard exit that skips
// CloseModules, for what is really still the first signal. Regardless of
// which branch of the race actually wins (Go's select makes no promise
// here), exit must never be called: asserting exited() == nil is
// deterministic in outcome even though the branch taken isn't — see the
// fix-wave report for why forcing a specific branch isn't attempted.
func TestServeAndWait_SignalAndServeErrorRaceRunsSequenceOnce(t *testing.T) {
	h := newHarness()
	h.srv.serveErr = errors.New("bind lost")
	h.sig <- syscall.SIGINT // both triggers are ready before serveAndWait starts

	_ = h.run(testBudget)

	if n := h.target.stops.Load(); n != 1 {
		t.Fatalf("StopModules called %d times, want exactly 1", n)
	}
	if n := h.rec.count("cancel"); n != 1 {
		t.Fatalf("cancel called %d times, want exactly 1", n)
	}
	if n := h.rec.count("CloseModules"); n != 1 {
		t.Fatalf("CloseModules called %d times, want exactly 1", n)
	}
	if exits := h.exited(); len(exits) != 0 {
		t.Fatalf("exit calls = %v, want none — a stale first signal must not be misread as a second one", exits)
	}
}

func TestServeAndWait_ShutdownDeadlineForcesClose(t *testing.T) {
	h := newHarness()
	h.srv.shutdownErr = context.DeadlineExceeded
	h.sig <- syscall.SIGTERM

	if err := h.run(testBudget); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil", err)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "Close", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

func TestServeAndWait_StopModulesErrorIsLoggedAndSequenceContinues(t *testing.T) {
	h := newHarness()
	h.target.stopErr = errors.New("stream: still draining")
	h.sig <- syscall.SIGTERM

	if err := h.run(testBudget); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil", err)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	found := false
	for _, l := range h.logged() {
		if strings.Contains(l, "still draining") {
			found = true
		}
	}
	if !found {
		t.Fatalf("StopModules error was not logged; logs = %q", h.logged())
	}
}

func TestServeAndWait_StopModulesOverrunsBudget(t *testing.T) {
	const budget = 20 * time.Millisecond
	h := newHarness()
	// StopModules ignores ctx and overruns the budget: the same ctx
	// reaches Shutdown already expired, and the sequence still completes.
	h.target.stopHook = func(context.Context) { time.Sleep(budget + 50*time.Millisecond) }
	h.sig <- syscall.SIGTERM

	if err := h.run(budget); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil", err)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	sd, ok := h.rec.find("Shutdown")
	if !ok {
		t.Fatal("Shutdown not recorded")
	}
	if sd.ctx == nil || sd.ctx.Err() == nil {
		t.Fatalf("Shutdown must receive an already-expired ctx after StopModules overran the budget; ctx.Err() = %v", ctxErr(sd.ctx))
	}
}

func TestServeAndWait_SameCtxForStopModulesAndShutdown(t *testing.T) {
	h := newHarness()
	h.sig <- syscall.SIGTERM

	if err := h.run(testBudget); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil", err)
	}
	stop, ok := h.rec.find("StopModules")
	if !ok {
		t.Fatal("StopModules not recorded")
	}
	sd, ok := h.rec.find("Shutdown")
	if !ok {
		t.Fatal("Shutdown not recorded")
	}
	if stop.ctx == nil || stop.ctx != sd.ctx {
		t.Fatalf("StopModules and Shutdown must receive the same ctx value (N1 rule 1); got %p vs %p", stop.ctx, sd.ctx)
	}
	if _, hasDeadline := stop.ctx.Deadline(); !hasDeadline {
		t.Fatal("shutdown ctx must carry the budget deadline")
	}
}

func TestServeAndWait_WaitsForCloseModules(t *testing.T) {
	h := newHarness()
	gate := make(chan struct{})
	h.target.closeGate = gate
	h.sig <- syscall.SIGTERM

	done := h.runAsync(testBudget)

	// While CloseModules is blocked, serveAndWait must not have returned.
	select {
	case err := <-done:
		t.Fatalf("serveAndWait returned (%v) before CloseModules finished", err)
	case <-time.After(50 * time.Millisecond):
	}
	if h.rec.count("CloseModules") != 0 {
		t.Fatal("CloseModules recorded before the gate was released")
	}

	close(gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveAndWait returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveAndWait did not return after CloseModules was released")
	}
	if h.rec.count("CloseModules") != 1 {
		t.Fatalf("CloseModules recorded %d times, want 1", h.rec.count("CloseModules"))
	}
}

// TestServeAndWait_SecondSignalDuringBlockingStopModulesExitsImmediately is
// item 5's test: a second signal arriving while the shutdown sequence is
// stuck behind a blocking StopModules must not wait out the rest of the
// budget — it calls the injected exit(130) right away. exit is a harness
// fake, so this never calls the real os.Exit.
func TestServeAndWait_SecondSignalDuringBlockingStopModulesExitsImmediately(t *testing.T) {
	h := newHarness()
	started := make(chan struct{})
	release := make(chan struct{})
	h.target.stopHook = func(context.Context) {
		close(started)
		<-release
	}
	h.sig <- syscall.SIGTERM

	done := h.runAsync(testBudget)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("StopModules never started")
	}

	h.sig <- syscall.SIGTERM // second signal while StopModules is still blocked

	waitFor := time.After(2 * time.Second)
	for len(h.exited()) == 0 {
		select {
		case <-waitFor:
			t.Fatal("exit was not called after the second signal")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if exits := h.exited(); !equalInts(exits, []int{130}) {
		t.Fatalf("exit calls = %v, want [130]", exits)
	}

	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveAndWait returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveAndWait did not return after StopModules unblocked")
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// recordingHTTPServer wraps a real *http.Server so the I7 test can
// timestamp Shutdown/Close alongside the fake target's CloseModules.
type recordingHTTPServer struct {
	*http.Server
	rec *recorder

	// shutdownErr is set by Shutdown and read only after serveAndWait has
	// returned (the CloseModules step that precedes that return happens
	// strictly after Shutdown returns, so this plain field is race-free).
	shutdownErr error
}

func (s *recordingHTTPServer) Shutdown(ctx context.Context) error {
	s.rec.add("Shutdown", ctx)
	err := s.Server.Shutdown(ctx)
	s.shutdownErr = err
	s.rec.add("Shutdown.returned", nil)
	return err
}

func (s *recordingHTTPServer) Close() error {
	s.rec.add("Close", nil)
	err := s.Server.Close()
	s.rec.add("Close.returned", nil)
	return err
}

// I7: a streaming response that never finishes must not hold up
// shutdown past the budget — Shutdown times out, Close cuts the
// connection, and only then do modules close.
func TestServeAndWait_RealServerStreamingHandlerTimesOutThenCloses(t *testing.T) {
	const budget = 50 * time.Millisecond

	rec := &recorder{}
	flushed := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		flushed <- struct{}{}
		<-release
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &recordingHTTPServer{Server: &http.Server{Handler: handler}, rec: rec}
	target := &fakeTarget{rec: rec}
	sig := make(chan os.Signal, 1)
	cancel := func() { rec.add("cancel", nil) }

	done := make(chan error, 1)
	noExit := func(int) {}
	go func() { done <- serveAndWait(srv, ln, sig, cancel, target, budget, t.Logf, noExit) }()

	// Open a streaming request and wait until the handler has flushed
	// headers — from then on the connection is "active" for Shutdown.
	client := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(client.CloseIdleConnections)
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		resp, err := client.Get("http://" + ln.Addr().String() + "/stream")
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never flushed")
	}

	sig <- syscall.SIGTERM

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveAndWait returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveAndWait did not return; shutdown is stuck behind the streaming handler")
	}
	select {
	case <-reqDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client request never terminated after Close")
	}

	want := []string{"cancel", "StopModules", "Shutdown", "Shutdown.returned", "Close", "Close.returned", "CloseModules"}
	if got := rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	closeRet, _ := rec.find("Close.returned")
	closeMods, _ := rec.find("CloseModules")
	if !closeMods.at.After(closeRet.at) && !closeMods.at.Equal(closeRet.at) {
		t.Fatalf("CloseModules (%v) ran before Close returned (%v)", closeMods.at, closeRet.at)
	}
	// Clock-independent stand-in for "Shutdown timed out": assert the error
	// it returned rather than comparing elapsed time against budget (which
	// carries a theoretical microsecond flake window).
	if !errors.Is(srv.shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown(ctx) returned %v, want context.DeadlineExceeded", srv.shutdownErr)
	}
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
