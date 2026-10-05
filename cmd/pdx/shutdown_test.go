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
	closeHook func()        // runs at the start of CloseModules, before closeGate
	stops     atomic.Int32

	restartAccepted bool         // what BeginShutdown reports (a restart 202 sent before the sequence)
	beginCalls      atomic.Int32 // not in the step recorder: tests compare exact step lists
}

func (t *fakeTarget) BeginShutdown() bool {
	t.beginCalls.Add(1)
	return t.restartAccepted
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
	if t.closeHook != nil {
		t.closeHook()
	}
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
	lateErr     error // if set, Serve returns it (not ErrServerClosed) once released
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
	if s.lateErr != nil {
		return s.lateErr
	}
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
	rec     *recorder
	target  *fakeTarget
	srv     *fakeServer
	sig     chan os.Signal
	restart chan struct{}
	logs    []string
	logMu   sync.Mutex

	exitMu    sync.Mutex
	exitCalls []int // records exit() calls instead of ever calling real os.Exit

	cancelHook func() // if non-nil, runs inside cancel() after recording
}

func newHarness() *harness {
	rec := &recorder{}
	return &harness{
		rec:     rec,
		target:  &fakeTarget{rec: rec},
		srv:     newFakeServer(rec),
		sig:     make(chan os.Signal, 1),
		restart: make(chan struct{}, 1),
	}
}

func (h *harness) cancel() {
	h.rec.add("cancel", nil)
	if h.cancelHook != nil {
		h.cancelHook()
	}
}

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
	return serveAndWait(h.srv, nil, h.sig, h.restart, h.cancel, h.target, budget, h.logf, h.exit)
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

// recvOrFail receives from ch, failing fast instead of hanging to the go
// test timeout if a deadlock keeps the value from ever arriving.
func recvOrFail[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// waitForLog polls the harness log until a line containing substr appears.
func waitForLog(t *testing.T, h *harness, substr string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		for _, l := range h.logged() {
			if strings.Contains(l, substr) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("log never contained %q; logs = %v", substr, h.logged())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// waitForExit polls until the injected exit func has been called with code.
func waitForExit(t *testing.T, h *harness, code int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		for _, c := range h.exited() {
			if c == code {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("exit(%d) never called; exits = %v", code, h.exited())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

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
// against a lone first signal being misread as a second one: sig already
// has a buffered value when serveAndWait starts. If the sig branch wins
// the initial select it is the trigger; if the serveErr branch wins, the
// buffered value is still the FIRST signal — the Serve-first watcher must
// log it with the send-again hint, never call exit(130) (a hard exit that
// skips CloseModules) for what is really a single keypress. Regardless of
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

// TestServeAndWait_SignalDuringServeTriggeredSequenceDoesNotExitEarly:
// when the sequence was triggered by Serve returning (no signal yet), a
// signal arriving while StopModules is still blocked is the FIRST signal,
// not a second impatient one. It must not call exit — it is logged with a
// "send again" hint and the sequence completes normally once StopModules
// unblocks. The signal is sent only after StopModules has been observed
// to start, via the started gate, so the test is deterministic.
func TestServeAndWait_SignalDuringServeTriggeredSequenceDoesNotExitEarly(t *testing.T) {
	h := newHarness()
	bindLost := errors.New("bind lost")
	h.srv.serveErr = bindLost // Serve fails first — no signal yet.

	started := make(chan struct{})
	release := make(chan struct{})
	h.target.stopHook = func(context.Context) {
		close(started)
		<-release
	}

	done := h.runAsync(testBudget)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("StopModules never started")
	}

	h.sig <- syscall.SIGTERM // first signal, arriving mid-sequence

	// Give the (would-be) watcher a chance to misfire before releasing
	// StopModules, so a regression would be caught here rather than
	// masked by the release below.
	time.Sleep(50 * time.Millisecond)
	if exits := h.exited(); len(exits) != 0 {
		t.Fatalf("exit was called %v before StopModules unblocked; sequence was triggered by Serve, not a signal", exits)
	}

	close(release)

	select {
	case err := <-done:
		if !errors.Is(err, bindLost) {
			t.Fatalf("serveAndWait returned %v, want %v", err, bindLost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveAndWait did not return after StopModules unblocked")
	}

	if exits := h.exited(); len(exits) != 0 {
		t.Fatalf("exit calls = %v, want none", exits)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	hinted := false
	for _, l := range h.logged() {
		if strings.Contains(l, "during shutdown") && strings.Contains(l, "send again to exit immediately") {
			hinted = true
		}
	}
	if !hinted {
		t.Fatalf("first signal during a Serve-triggered shutdown was not logged with the send-again hint; logs = %q", h.logged())
	}
}

// TestServeAndWait_TwoSignalsDuringServeTriggeredSequenceExitImmediately:
// the Serve-first path must still honour a forced exit. The first signal
// during the sequence only logs (above); the SECOND calls exit(130)
// without waiting for the stuck StopModules.
func TestServeAndWait_TwoSignalsDuringServeTriggeredSequenceExitImmediately(t *testing.T) {
	h := newHarness()
	bindLost := errors.New("bind lost")
	h.srv.serveErr = bindLost // Serve fails first — no signal yet.

	started := make(chan struct{})
	release := make(chan struct{})
	h.target.stopHook = func(context.Context) {
		close(started)
		<-release
	}

	done := h.runAsync(testBudget)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("StopModules never started")
	}

	h.sig <- syscall.SIGINT // first signal during the sequence: log only
	waitHint := time.After(2 * time.Second)
	for {
		hinted := false
		for _, l := range h.logged() {
			if strings.Contains(l, "send again to exit immediately") {
				hinted = true
			}
		}
		if hinted {
			break
		}
		select {
		case <-waitHint:
			t.Fatalf("first signal was not logged with the send-again hint; logs = %q", h.logged())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if exits := h.exited(); len(exits) != 0 {
		t.Fatalf("exit called %v after the FIRST signal", exits)
	}

	h.sig <- syscall.SIGINT // second signal: exit now

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
		if !errors.Is(err, bindLost) {
			t.Fatalf("serveAndWait returned %v, want %v", err, bindLost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveAndWait did not return after StopModules unblocked")
	}
}

// TestServeAndWait_BufferedSignalThenSecondDuringSequenceExits is the
// codex R2 case as stated: Serve fails immediately AND a signal is already
// buffered in sig before the initial select. Whichever branch wins the
// race, that buffered signal is the user's FIRST Ctrl-C (either it
// triggered the sequence, or — Serve-first — the watcher logs it with the
// send-again hint); the SECOND one, sent while StopModules is blocked,
// must call exit(130). The outcome is the same on both branches, so the
// assertion is deterministic even though the branch taken isn't. (In
// practice the sig branch wins here: the Serve goroutine has not run by
// the time the select is entered. The Serve-first side of the rule is
// pinned deterministically by the next test.)
func TestServeAndWait_BufferedSignalThenSecondDuringSequenceExits(t *testing.T) {
	h := newHarness()
	bindLost := errors.New("bind lost")
	h.srv.serveErr = bindLost // Serve fails immediately ...
	h.sig <- syscall.SIGINT   // ... and a signal is already buffered.

	started := make(chan struct{})
	release := make(chan struct{})
	h.target.stopHook = func(context.Context) {
		close(started)
		<-release
	}

	done := h.runAsync(testBudget)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("StopModules never started")
	}

	// Exactly one signal so far: no forced exit yet, on either branch.
	time.Sleep(20 * time.Millisecond)
	if exits := h.exited(); len(exits) != 0 {
		t.Fatalf("exit called %v after only the buffered (first) signal", exits)
	}

	h.sig <- syscall.SIGINT // second signal while StopModules is blocked

	waitFor := time.After(2 * time.Second)
	for len(h.exited()) == 0 {
		select {
		case <-waitFor:
			t.Fatalf("exit was not called after the second signal; logs = %q", h.logged())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if exits := h.exited(); !equalInts(exits, []int{130}) {
		t.Fatalf("exit calls = %v, want [130]", exits)
	}

	close(release)

	select {
	case err := <-done:
		if !errors.Is(err, bindLost) {
			t.Fatalf("serveAndWait returned %v, want %v", err, bindLost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveAndWait did not return after StopModules unblocked")
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

// TestServeAndWait_ServeFirstBufferedSignalIsFirstThenSecondExits pins the
// Serve-first side of the rule deterministically: Serve fails with sig
// empty (so the serveErr branch wins the initial select — nothing else is
// ready), and the harness's cancel hook, which serveAndWait calls right
// after arming the watcher and before StopModules, buffers one signal. The
// watcher must treat whatever is in sig — buffered or new — as the FIRST
// signal: it is logged with the send-again hint and exit is not called.
// The SECOND signal, sent while StopModules is still blocked, calls
// exit(130); the sequence then completes once StopModules unblocks.
func TestServeAndWait_ServeFirstBufferedSignalIsFirstThenSecondExits(t *testing.T) {
	h := newHarness()
	bindLost := errors.New("bind lost")
	h.srv.serveErr = bindLost
	h.cancelHook = func() { h.sig <- syscall.SIGTERM } // buffered before StopModules runs

	started := make(chan struct{})
	release := make(chan struct{})
	h.target.stopHook = func(context.Context) {
		close(started)
		<-release
	}

	done := h.runAsync(testBudget)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("StopModules never started")
	}

	// The buffered signal is the first: hint logged, no exit.
	waitHint := time.After(2 * time.Second)
	for {
		hinted := false
		for _, l := range h.logged() {
			if strings.Contains(l, "during shutdown") && strings.Contains(l, "send again to exit immediately") {
				hinted = true
			}
		}
		if hinted {
			break
		}
		select {
		case <-waitHint:
			t.Fatalf("buffered first signal was not logged with the send-again hint; logs = %q", h.logged())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if exits := h.exited(); len(exits) != 0 {
		t.Fatalf("exit called %v after only the buffered (first) signal", exits)
	}

	h.sig <- syscall.SIGTERM // second signal while StopModules is blocked

	waitFor := time.After(2 * time.Second)
	for len(h.exited()) == 0 {
		select {
		case <-waitFor:
			t.Fatalf("exit was not called after the second signal; logs = %q", h.logged())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if exits := h.exited(); !equalInts(exits, []int{130}) {
		t.Fatalf("exit calls = %v, want [130]", exits)
	}

	close(release)

	select {
	case err := <-done:
		if !errors.Is(err, bindLost) {
			t.Fatalf("serveAndWait returned %v, want %v", err, bindLost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveAndWait did not return after StopModules unblocked")
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
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
	go func() { done <- serveAndWait(srv, ln, sig, nil, cancel, target, budget, t.Logf, noExit) }()

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

func TestServeAndWait_RestartRunsSequenceAndReturnsErrRestart(t *testing.T) {
	h := newHarness()
	h.restart <- struct{}{}
	err := h.run(testBudget)
	if !errors.Is(err, errRestart) {
		t.Fatalf("serveAndWait returned %v, want errRestart", err)
	}
	want := []string{"cancel", "StopModules", "Shutdown", "CloseModules"}
	if got := h.rec.names(); !equalSteps(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	if len(h.exited()) != 0 {
		t.Fatalf("exit called: %v", h.exited())
	}
}

func TestServeAndWait_SignalDuringRestartCancelsRestart(t *testing.T) {
	h := newHarness()
	unblock := make(chan struct{})
	entered := make(chan struct{})
	h.target.stopHook = func(context.Context) { close(entered); <-unblock }
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	recvOrFail(t, entered, "StopModules/CloseModules to start")
	h.sig <- syscall.SIGTERM // `pdx stop` while the restart's shutdown runs
	waitForLog(t, h, "exiting instead of restarting")
	close(unblock)
	if err := recvOrFail(t, out, "serveAndWait to return"); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil (a stop, not a restart)", err)
	}
	if h.rec.count("CloseModules") != 1 {
		t.Fatalf("first signal must not skip CloseModules: steps = %v", h.rec.names())
	}
	if len(h.exited()) != 0 {
		t.Fatalf("first signal must not force exit: %v", h.exited())
	}
}

func TestServeAndWait_TwoSignalsDuringRestartExitImmediately(t *testing.T) {
	h := newHarness()
	unblock := make(chan struct{})
	entered := make(chan struct{})
	h.target.stopHook = func(context.Context) { close(entered); <-unblock }
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	recvOrFail(t, entered, "StopModules/CloseModules to start")
	h.sig <- syscall.SIGTERM
	waitForLog(t, h, "exiting instead of restarting")
	h.sig <- syscall.SIGTERM
	waitForExit(t, h, 130)
	close(unblock)
	if err := recvOrFail(t, out, "serveAndWait to return"); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil (restart cancelled by the first signal)", err)
	}
}

// A signal at the TAIL of the sequence (during CloseModules) must still
// cancel the restart. This pins that behaviour only; it does not prove the
// watcher join (<-watcherDone before reading restartCancelled), which is
// correct by construction — the log line happens-after the Store, so the
// test passes with or without the join.
func TestServeAndWait_SignalDuringCloseModulesCancelsRestart(t *testing.T) {
	h := newHarness()
	unblock := make(chan struct{})
	entered := make(chan struct{})
	h.target.closeHook = func() { close(entered); <-unblock }
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	recvOrFail(t, entered, "StopModules/CloseModules to start")
	h.sig <- syscall.SIGTERM
	waitForLog(t, h, "exiting instead of restarting")
	close(unblock)
	if err := recvOrFail(t, out, "serveAndWait to return"); err != nil {
		t.Fatalf("serveAndWait returned %v, want nil", err)
	}
	if h.rec.count("CloseModules") != 1 {
		t.Fatalf("CloseModules not recorded once: steps = %v", h.rec.names())
	}
}

// A real Serve error that surfaces during a restart is logged, not lost
// behind errRestart (F4).
func TestServeAndWait_RestartLogsRealServeError(t *testing.T) {
	h := newHarness()
	h.srv.lateErr = errors.New("accept boom")
	h.restart <- struct{}{}
	out := h.runAsync(testBudget)
	err := recvOrFail(t, out, "serveAndWait to return")
	if !errors.Is(err, errRestart) {
		t.Fatalf("serveAndWait returned %v, want errRestart", err)
	}
	waitForLog(t, h, "server error during restart: accept boom")
}

// A restart the endpoint accepted (202 sent) just before a Serve failure won
// the trigger select is honoured, not lost behind the Serve error (G1).
func TestServeAndWait_ServeErrorWithAcceptedRestartStillRestarts(t *testing.T) {
	h := newHarness()
	h.target.restartAccepted = true
	h.srv.serveErr = errors.New("accept boom")
	out := h.runAsync(testBudget)
	err := recvOrFail(t, out, "serveAndWait to return")
	if !errors.Is(err, errRestart) {
		t.Fatalf("serveAndWait returned %v, want errRestart", err)
	}
	waitForLog(t, h, "restart accepted before shutdown began")
	waitForLog(t, h, "server error during restart: accept boom")
}

// A signal still wins over an accepted restart (D4).
func TestServeAndWait_SignalWithAcceptedRestartStops(t *testing.T) {
	h := newHarness()
	h.target.restartAccepted = true
	h.sig <- syscall.SIGTERM
	err := h.run(testBudget)
	if err != nil {
		t.Fatalf("serveAndWait returned %v, want nil (stop)", err)
	}
}

func TestServeAndWait_CallsBeginShutdownOnce(t *testing.T) {
	for name, trigger := range map[string]func(h *harness){
		"signal":  func(h *harness) { h.sig <- syscall.SIGTERM },
		"serve":   func(h *harness) { h.srv.serveErr = errors.New("boom") },
		"restart": func(h *harness) { h.restart <- struct{}{} },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			trigger(h)
			h.run(testBudget)
			if n := h.target.beginCalls.Load(); n != 1 {
				t.Fatalf("BeginShutdown called %d times, want 1", n)
			}
		})
	}
}

func TestServeAndWait_RestartCarriesCleanupErrors(t *testing.T) {
	h := newHarness()
	h.target.stopErr = errors.New("nex: timeout")
	h.restart <- struct{}{}
	err := h.run(testBudget)
	var rr *restartRequested
	if !errors.Is(err, errRestart) || !errors.As(err, &rr) {
		t.Fatalf("err = %v, want a restartRequested", err)
	}
	if len(rr.warnings) != 1 || rr.warnings[0] != "stop modules: nex: timeout" {
		t.Fatalf("warnings = %v", rr.warnings)
	}
	waitForLog(t, h, "restart: continuing despite 1 cleanup error(s)")
}

func TestServeAndWait_CleanRestartHasNoWarnings(t *testing.T) {
	h := newHarness()
	h.restart <- struct{}{}
	var rr *restartRequested
	if err := h.run(testBudget); !errors.As(err, &rr) || len(rr.warnings) != 0 {
		t.Fatalf("err = %v, warnings = %v", err, rr)
	}
}
