package codexbroker

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestVerifyIdentity_Match — fakeLister returns matching pid+lstart+cmdline
// → ok=true.
func TestVerifyIdentity_Match(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{
		Key:    "k1",
		PID:    4321,
		Lstart: lstart,
	}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart,
		Cmdline: "node /opt/codex/dist/app-server-broker.mjs serve --cwd /tmp/x",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if !ok {
		t.Errorf("expected ok=true on full match")
	}
}

// TestVerifyIdentity_LstartMismatch — pid same, lstart differs by >1s →
// ok=false.
func TestVerifyIdentity_LstartMismatch(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart.Add(5 * time.Second), // 5s drift
		Cmdline: "node /opt/codex/dist/app-server-broker.mjs",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on lstart drift")
	}
}

// TestVerifyIdentity_PidGone — fakeLister returns no matching pid → ok=false.
func TestVerifyIdentity_PidGone(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: time.Now()}
	lister := NewFakeProcessLister([]RawProcess{{PID: 9999, Cmdline: "other"}})
	ok, _ := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on pid gone")
	}
}

// TestVerifyIdentity_CmdlineMismatch — pid+lstart match but cmdline isn't
// a broker → ok=false (PID-reuse defence).
func TestVerifyIdentity_CmdlineMismatch(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart,
		Cmdline: "/usr/bin/zsh -i",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on cmdline mismatch")
	}
}

// TestVerifyIdentity_LstartZeroVsNonZero — PR review R3 finding A regression:
// time.Time.Sub between time.Time{} (year 1) and time.Now() overflows to
// math.MinInt64 nanoseconds, which negates to itself, so the abs-then-tolerance
// trick silently passes through as "match". The explicit zero-equality guard
// closes that loophole.
func TestVerifyIdentity_LstartZeroVsNonZero(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: time.Now()}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  time.Time{}, // zero
		Cmdline: "node app-server-broker.mjs",
	}})
	ok, detail := VerifyIdentity(rec, lister)
	if ok {
		t.Errorf("expected ok=false on zero vs non-zero Lstart, got match")
	}
	if detail != "lstart-zero-mismatch" {
		t.Errorf("expected detail=lstart-zero-mismatch, got %q", detail)
	}
}

// TestVerifyIdentity_LstartBothZero — both zero is treated as match (does
// happen on platforms or fixtures where Lstart is intentionally unset).
func TestVerifyIdentity_LstartBothZero(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321} // zero Lstart
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Cmdline: "node app-server-broker.mjs",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if !ok {
		t.Errorf("expected ok=true when both Lstart are zero")
	}
}

// TestVerifyIdentity_LstartTolerance_1s — lstart drift within ±1s is
// accepted (round-trip via ps formatting can shift seconds).
func TestVerifyIdentity_LstartTolerance_1s(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  lstart.Add(900 * time.Millisecond),
		Cmdline: "node app-server-broker.mjs",
	}})
	ok, _ := VerifyIdentity(rec, lister)
	if !ok {
		t.Errorf("expected ok=true within 1s tolerance")
	}
}

// Smoke for KillSequence struct compile.
func TestKillSequence_StructCompiles(t *testing.T) {
	ks := &KillSequence{
		Rec: BrokerRecord{Key: "k1"},
	}
	if ks.Rec.Key != "k1" {
		t.Errorf("rec lost")
	}
	// KillResult zero-value should be usable.
	var res KillResult
	_ = res.GracefulOk
}

// -- Test doubles for kill-sequence steps --------------------------------

// killDialer is a one-shot dialer for kill-sequence graceful tests. Unlike
// the predicate-A dialer, the broker's shutdown handler does not return a
// useful body; the only signals we care about are "did Dial succeed" and
// "did the process exit before the budget".
type killDialer struct {
	dialErr     error
	hangForever bool
}

type killConn struct {
	hang     bool
	deadline time.Time
}

func (c *killConn) Read(b []byte) (int, error) {
	if c.hang {
		if c.deadline.IsZero() {
			c.deadline = time.Now().Add(10 * time.Second)
		}
		until := time.Until(c.deadline)
		if until > 0 {
			time.Sleep(until)
		}
		return 0, &net.OpError{Op: "read", Err: errors.New("i/o timeout")}
	}
	// Pretend the broker accepted the request and emitted a 200 OK.
	resp := "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"
	n := copy(b, resp)
	return n, nil
}

func (c *killConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *killConn) Close() error                       { return nil }
func (c *killConn) LocalAddr() net.Addr                { return &net.UnixAddr{} }
func (c *killConn) RemoteAddr() net.Addr               { return &net.UnixAddr{} }
func (c *killConn) SetDeadline(t time.Time) error      { c.deadline = t; return nil }
func (c *killConn) SetReadDeadline(t time.Time) error  { c.deadline = t; return nil }
func (c *killConn) SetWriteDeadline(t time.Time) error { return nil }

func (d *killDialer) Dial(network, address string) (net.Conn, error) {
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	return &killConn{hang: d.hangForever}, nil
}

// dynamicLister is a ProcessLister whose row set is mutated between calls
// (used to simulate a broker exiting after SIGTERM).
type dynamicLister struct {
	mu   sync.Mutex
	rows []RawProcess
}

func newDynamicLister(rows []RawProcess) *dynamicLister {
	cp := make([]RawProcess, len(rows))
	copy(cp, rows)
	return &dynamicLister{rows: cp}
}

func (l *dynamicLister) List(_ context.Context) ([]RawProcess, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]RawProcess, len(l.rows))
	copy(out, l.rows)
	return out, nil
}

func (l *dynamicLister) clearPID(pid int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.rows[:0]
	for _, row := range l.rows {
		if row.PID != pid {
			out = append(out, row)
		}
	}
	l.rows = out
}

func (l *dynamicLister) clearAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = nil
}

// capturingSignaller captures every Kill call so tests can assert order /
// arguments. Optionally clears a dynamic lister to simulate the kernel
// reaping the process, with an optional delay.
type capturingSignaller struct {
	mu     sync.Mutex
	calls  []signalCall
	clears *dynamicLister
	delay  time.Duration
	err    error
}

type signalCall struct {
	pid int
	sig syscall.Signal
}

func (s *capturingSignaller) Kill(pid int, sig syscall.Signal) error {
	s.mu.Lock()
	s.calls = append(s.calls, signalCall{pid: pid, sig: sig})
	s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.clears != nil {
		// Allow test to model "process took N ms to actually die" by sleeping
		// before clearing the row from the lister.
		if s.delay > 0 {
			time.AfterFunc(s.delay, func() {
				if pid < 0 {
					s.clears.clearPID(-pid)
				} else {
					s.clears.clearPID(pid)
				}
			})
		} else {
			if pid < 0 {
				s.clears.clearPID(-pid)
			} else {
				s.clears.clearPID(pid)
			}
		}
	}
	return nil
}

func (s *capturingSignaller) snapshot() []signalCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]signalCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// -- Task L: Step 2 graceful RPC shutdown --------------------------------

// TestStepGraceful_Success — fake dialer accepts the connection and the
// process disappears from the lister within budget → graceful=true.
func TestStepGraceful_Success(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{
		Key:      "k1",
		PID:      4321,
		Lstart:   lstart,
		Endpoint: "unix:/tmp/cxc-x/broker.sock",
	}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Lstart: lstart,
		Cmdline: "node app-server-broker.mjs",
	}})
	dialer := &killDialer{}
	ks := &KillSequence{
		Rec:             rec,
		Lister:          lister,
		Dialer:          dialer,
		GracefulTimeout: 500 * time.Millisecond,
	}
	// Drop the row immediately to simulate broker exit.
	go func() {
		time.Sleep(20 * time.Millisecond)
		lister.clearAll()
	}()
	ok := ks.stepGraceful(context.Background())
	if !ok {
		t.Errorf("expected stepGraceful=true once process gone")
	}
}

// TestStepGraceful_RPCTimeout — fake dialer hangs beyond budget → false,
// no panic.
func TestStepGraceful_RPCTimeout(t *testing.T) {
	rec := BrokerRecord{
		Key:      "k1",
		PID:      4321,
		Lstart:   time.Now(),
		Endpoint: "unix:/tmp/cxc-x/broker.sock",
	}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Cmdline: "node app-server-broker.mjs",
	}})
	dialer := &killDialer{hangForever: true}
	ks := &KillSequence{
		Rec:             rec,
		Lister:          lister,
		Dialer:          dialer,
		GracefulTimeout: 80 * time.Millisecond,
	}
	start := time.Now()
	ok := ks.stepGraceful(context.Background())
	if ok {
		t.Errorf("expected stepGraceful=false on hang")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("stepGraceful did not respect budget: %v", elapsed)
	}
}

// TestStepGraceful_EndpointMissing — empty endpoint → false immediately,
// no dial attempt.
func TestStepGraceful_EndpointMissing(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, Endpoint: ""}
	dialer := &killDialer{dialErr: errors.New("should not be called")}
	ks := &KillSequence{
		Rec:             rec,
		Dialer:          dialer,
		Lister:          NewFakeProcessLister(nil),
		GracefulTimeout: 200 * time.Millisecond,
	}
	if ok := ks.stepGraceful(context.Background()); ok {
		t.Errorf("expected stepGraceful=false on empty endpoint")
	}
}

// TestStepGraceful_DialerNil — defensive: a nil dialer returns false rather
// than panicking.
func TestStepGraceful_DialerNil(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, Endpoint: "unix:/tmp/x/sock"}
	ks := &KillSequence{Rec: rec, Lister: NewFakeProcessLister(nil)}
	if ok := ks.stepGraceful(context.Background()); ok {
		t.Errorf("expected stepGraceful=false with nil dialer")
	}
}

// -- Task M: Steps 3+4 SIGTERM + SIGKILL ---------------------------------

// fakePgidLooker pretends getpgid() returns the configured value; tests can
// override the production lookup via KillSequence.PgidLookup so the kill
// path can be exercised without spawning a real process group.
func fakePgidLooker(pgid int, err error) func(int) (int, error) {
	return func(_ int) (int, error) { return pgid, err }
}

// TestStepSIGTERM_ProcessExitsGracefully — fake signaller records SIGTERM;
// after the kill, the lister returns no row for the pid → step returns true.
func TestStepSIGTERM_ProcessExitsGracefully(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart,
	}})
	sig := &capturingSignaller{clears: lister}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(4321, nil),
		TermTimeout: 500 * time.Millisecond,
	}
	ok, err := ks.stepSIGTERM(context.Background())
	if err != nil {
		t.Fatalf("unexpected stepSIGTERM err: %v", err)
	}
	if !ok {
		t.Fatalf("expected stepSIGTERM=true after process exits")
	}
	calls := sig.snapshot()
	if len(calls) != 1 || calls[0].sig != syscall.SIGTERM || calls[0].pid != -4321 {
		t.Errorf("expected one SIGTERM to -4321, got %+v", calls)
	}
}

// TestStepSIGTERM_Timeout_ProcStillAlive — signaller does not clear the row;
// stepSIGTERM should time out → false.
func TestStepSIGTERM_Timeout_ProcStillAlive(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart,
	}})
	sig := &capturingSignaller{} // no clears → process stays alive
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(4321, nil),
		TermTimeout: 80 * time.Millisecond,
	}
	if ok, _ := ks.stepSIGTERM(context.Background()); ok {
		t.Errorf("expected stepSIGTERM=false on timeout")
	}
}

// TestStepSIGTERM_PgidLeOne_Refused — getpgid returns 1 → stepSIGTERM
// refuses to signal and returns false. Defends against init / kernel oddities.
func TestStepSIGTERM_PgidLeOne_Refused(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart,
	}})
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(1, nil),
		TermTimeout: 100 * time.Millisecond,
	}
	if ok, _ := ks.stepSIGTERM(context.Background()); ok {
		t.Errorf("expected stepSIGTERM=false on pgid=1")
	}
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on pgid=1, got %+v", got)
	}
}

// TestStepSIGTERM_PgidLookupError_Refused — getpgid returns an error → no
// signal sent. (Kernel weirdness defence.)
func TestStepSIGTERM_PgidLookupError_Refused(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      newDynamicLister([]RawProcess{{PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart}}),
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(0, errors.New("getpgid ESRCH")),
		TermTimeout: 100 * time.Millisecond,
	}
	if ok, _ := ks.stepSIGTERM(context.Background()); ok {
		t.Errorf("expected stepSIGTERM=false on getpgid error")
	}
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on getpgid error, got %+v", got)
	}
}

// TestStepSIGKILL_KillsStubbornProcess — signaller clears the row; lister
// returns empty → stepSIGKILL true.
func TestStepSIGKILL_KillsStubbornProcess(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart,
	}})
	sig := &capturingSignaller{clears: lister}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(4321, nil),
		KillTimeout: 500 * time.Millisecond,
	}
	if ok, err := ks.stepSIGKILL(context.Background()); !ok {
		t.Errorf("expected stepSIGKILL=true after process gone, err=%v", err)
	}
	calls := sig.snapshot()
	if len(calls) != 1 || calls[0].sig != syscall.SIGKILL || calls[0].pid != -4321 {
		t.Errorf("expected one SIGKILL to -4321, got %+v", calls)
	}
}

// TestStepSIGKILL_2sTimeout_Partial — lister never returns empty → false.
// Caller (Run wiring in task P) records KillOk=false on the result.
func TestStepSIGKILL_2sTimeout_Partial(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart,
	}})
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(4321, nil),
		KillTimeout: 80 * time.Millisecond,
	}
	if ok, _ := ks.stepSIGKILL(context.Background()); ok {
		t.Errorf("expected stepSIGKILL=false on timeout")
	}
}

// TestStepSIGKILL_PgidLeOne_Refused — pgid=1 → stepSIGKILL refuses.
func TestStepSIGKILL_PgidLeOne_Refused(t *testing.T) {
	lstart := time.Now()
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      newDynamicLister([]RawProcess{{PID: 4321, Cmdline: "node app-server-broker.mjs", Lstart: lstart}}),
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(1, nil),
		KillTimeout: 100 * time.Millisecond,
	}
	if ok, _ := ks.stepSIGKILL(context.Background()); ok {
		t.Errorf("expected stepSIGKILL=false on pgid=1")
	}
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on pgid=1, got %+v", got)
	}
}

// -- Task N: Step 5 verify family gone -----------------------------------

// errLister is a ProcessLister that always returns an error.
type errLister struct{ err error }

func (l errLister) List(_ context.Context) ([]RawProcess, error) {
	return nil, l.err
}

// TestStepVerifyGone_AllGone — lister returns no rows for our pid or any
// child sharing our lstart → step returns true.
func TestStepVerifyGone_AllGone(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	// Lister returns an unrelated process whose lstart is different.
	lister := newDynamicLister([]RawProcess{{
		PID: 9999, Lstart: lstart.Add(1 * time.Hour),
		Cmdline: "/usr/bin/zsh -i",
	}})
	ks := &KillSequence{Rec: rec, Lister: lister}
	if ok := ks.stepVerifyGone(context.Background()); !ok {
		t.Errorf("expected stepVerifyGone=true with no family rows")
	}
}

// TestStepVerifyGone_StrayChildRemains — lister returns a child whose lstart
// matches the original family → false.
func TestStepVerifyGone_StrayChildRemains(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		// Child of the broker — same lstart, different pid.
		PID: 4322, PPID: 4321, Lstart: lstart,
		Cmdline: "node app-server-broker.mjs --task-worker",
	}})
	ks := &KillSequence{Rec: rec, Lister: lister}
	if ok := ks.stepVerifyGone(context.Background()); ok {
		t.Errorf("expected stepVerifyGone=false with stray child")
	}
}

// TestStepVerifyGone_OurPidStillThere — the original pid is still present →
// false (most direct evidence the kill failed).
func TestStepVerifyGone_OurPidStillThere(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Lstart: lstart, Cmdline: "node app-server-broker.mjs",
	}})
	ks := &KillSequence{Rec: rec, Lister: lister}
	if ok := ks.stepVerifyGone(context.Background()); ok {
		t.Errorf("expected stepVerifyGone=false when our pid is still listed")
	}
}

// TestStepVerifyGone_ListerError — best-effort: a lister error returns true
// rather than blocking cleanup. Spec §5.4 lines 458-459.
func TestStepVerifyGone_ListerError(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: time.Now()}
	ks := &KillSequence{Rec: rec, Lister: errLister{err: errors.New("ps unavailable")}}
	if ok := ks.stepVerifyGone(context.Background()); !ok {
		t.Errorf("expected stepVerifyGone=true on lister error (best-effort)")
	}
}

// -- Task O: Step 6 cleanup with socket verification ---------------------

// fakeSocketVerifier returns a fixed (held, err) regardless of input.
type fakeSocketVerifier struct {
	held bool
	err  error
}

func (f *fakeSocketVerifier) AnyPidHoldsSocket(_ string, _ time.Duration) (bool, error) {
	return f.held, f.err
}

// TestStepCleanup_SocketNotHeld_Removed — verifier reports no holders →
// SocketDir gets RemoveAll'd.
func TestStepCleanup_SocketNotHeld_Removed(t *testing.T) {
	dir := t.TempDir()
	// Create a fake socket-dir layout.
	sockDir := dir + "/cxc-test"
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sockDir+"/broker.sock", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := BrokerRecord{Key: "k1", PID: 4321, SocketDir: sockDir}
	ks := &KillSequence{Rec: rec}
	verifier := &fakeSocketVerifier{held: false}
	ok := ks.stepCleanup(context.Background(), verifier)
	if !ok {
		t.Errorf("expected stepCleanup=true when socket not held")
	}
	if pathExistsKT(sockDir) {
		t.Errorf("expected SocketDir removed, still exists at %s", sockDir)
	}
}

// TestStepCleanup_SocketHeld_Deferred — verifier reports held → SocketDir
// is NOT removed; step returns false to signal defer.
func TestStepCleanup_SocketHeld_Deferred(t *testing.T) {
	dir := t.TempDir()
	sockDir := dir + "/cxc-test"
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sockDir+"/broker.sock", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := BrokerRecord{Key: "k1", PID: 4321, SocketDir: sockDir}
	ks := &KillSequence{Rec: rec}
	verifier := &fakeSocketVerifier{held: true}
	ok := ks.stepCleanup(context.Background(), verifier)
	if ok {
		t.Errorf("expected stepCleanup=false when socket held")
	}
	if !pathExistsKT(sockDir) {
		t.Errorf("expected SocketDir preserved when held; missing at %s", sockDir)
	}
}

// TestStepCleanup_NoSocketDir_SkipsVerify — empty SocketDir → step returns
// true immediately, no verifier call.
func TestStepCleanup_NoSocketDir_SkipsVerify(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321, SocketDir: ""}
	ks := &KillSequence{Rec: rec}
	verifier := &fakeSocketVerifier{held: true} // would defer if called
	ok := ks.stepCleanup(context.Background(), verifier)
	if !ok {
		t.Errorf("expected stepCleanup=true when SocketDir empty")
	}
}

// -- Task P: wire KillSequence.Run ---------------------------------------

// TestKillSequence_HappyPath_AllSteps — full Steps 0-6 with all fakes.
// Graceful succeeds, no SIGTERM/SIGKILL needed but they may run depending
// on lister timing — the contract under test is: Run returns a populated
// KillResult, the audit file exists with both preimage and postscript,
// and the SocketDir is removed.
func TestKillSequence_HappyPath_AllSteps(t *testing.T) {
	dir := t.TempDir()
	auditDir := dir + "/audit"
	sockDir := dir + "/cxc-x"
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sockDir+"/broker.sock", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{
		Key:       "happy",
		PID:       4321,
		Lstart:    lstart,
		Endpoint:  "unix:" + sockDir + "/broker.sock",
		SocketDir: sockDir,
		StateDir:  dir + "/state",
		Cwd:       "/tmp/x",
	}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Lstart: lstart,
		Cmdline: "node /opt/codex/dist/app-server-broker.mjs serve --cwd /tmp/x",
	}})
	dialer := &killDialer{}
	sig := &capturingSignaller{clears: lister}
	verifier := &fakeSocketVerifier{held: false}

	ks := &KillSequence{
		Rec:             rec,
		Lister:          lister,
		Dialer:          dialer,
		Signaller:       sig,
		FS:              auditFS{},
		AuditDir:        auditDir,
		PgidLookup:      fakePgidLooker(4321, nil),
		GracefulTimeout: 100 * time.Millisecond,
		TermTimeout:     100 * time.Millisecond,
		KillTimeout:     100 * time.Millisecond,
	}
	// Drop the row 30ms in so graceful succeeds.
	go func() {
		time.Sleep(30 * time.Millisecond)
		lister.clearAll()
	}()
	res, err := ks.Run(context.Background(), DecisionResult{Reason: "idle_timeout", Kill: true}, verifier)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.CleanedUp {
		t.Errorf("expected CleanedUp=true")
	}
	if pathExistsKT(sockDir) {
		t.Errorf("expected SocketDir removed; still exists at %s", sockDir)
	}
	// Audit file should exist with the orphan- prefix.
	entries, _ := os.ReadDir(auditDir)
	if len(entries) == 0 {
		t.Fatalf("expected audit file, got none in %s", auditDir)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "orphan-happy-") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected orphan-happy-*.json in audit dir; got %v", entries)
	}
}

// TestKillSequence_StepLatencyMs_Populated — verify the three latency
// slots are populated after a Run that exercises graceful + SIGTERM (we
// drop the row only after a SIGTERM).
func TestKillSequence_StepLatencyMs_Populated(t *testing.T) {
	dir := t.TempDir()
	auditDir := dir + "/audit"
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{
		Key:    "lat",
		PID:    4321,
		Lstart: lstart,
		// no Endpoint → graceful skips quickly
	}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Lstart: lstart,
		Cmdline: "node app-server-broker.mjs",
	}})
	sig := &capturingSignaller{clears: lister, delay: 30 * time.Millisecond}
	ks := &KillSequence{
		Rec:             rec,
		Lister:          lister,
		Signaller:       sig,
		FS:              auditFS{},
		AuditDir:        auditDir,
		PgidLookup:      fakePgidLooker(4321, nil),
		GracefulTimeout: 30 * time.Millisecond,
		TermTimeout:     500 * time.Millisecond,
		KillTimeout:     500 * time.Millisecond,
	}
	res, err := ks.Run(context.Background(), DecisionResult{Reason: "idle_timeout", Kill: true}, &fakeSocketVerifier{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Step 2 (graceful) ran but produced no exit (no endpoint) — latency
	// non-negative.
	if res.StepLatencyMs[0] < 0 {
		t.Errorf("graceful latency negative: %d", res.StepLatencyMs[0])
	}
	// SIGTERM ran with a 30ms delay before the row cleared → polling the
	// 20ms ticker at least once gives a positive latency.
	if res.StepLatencyMs[1] <= 0 {
		t.Errorf("expected positive SIGTERM latency, got %d", res.StepLatencyMs[1])
	}
}

// TestKillSequence_E2Abort_IdentityMismatch — Step 0 mismatch → Run returns
// non-nil error, no signals sent, no audit preimage written.
func TestKillSequence_E2Abort_IdentityMismatch(t *testing.T) {
	dir := t.TempDir()
	auditDir := dir + "/audit"
	rec := BrokerRecord{
		Key:    "e2",
		PID:    4321,
		Lstart: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	// Lister returns a different lstart → identity mismatch.
	lister := NewFakeProcessLister([]RawProcess{{
		PID: 4321, Lstart: time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC),
		Cmdline: "node app-server-broker.mjs",
	}})
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:        rec,
		Lister:     lister,
		Signaller:  sig,
		FS:         auditFS{},
		AuditDir:   auditDir,
		PgidLookup: fakePgidLooker(4321, nil),
	}
	_, err := ks.Run(context.Background(), DecisionResult{Reason: "idle_timeout", Kill: true}, &fakeSocketVerifier{})
	if err == nil {
		t.Fatalf("expected error on identity mismatch")
	}
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on identity mismatch, got %+v", got)
	}
	// No audit file should have been written either.
	entries, _ := os.ReadDir(auditDir)
	if len(entries) > 0 {
		t.Errorf("expected no audit files on identity mismatch; got %v", entries)
	}
}

// TestKillSequence_AppendPostscript_OnSuccess — after a successful Run,
// the audit dump JSON contains both the preimage Reason and the kill
// postscript.
func TestKillSequence_AppendPostscript_OnSuccess(t *testing.T) {
	dir := t.TempDir()
	auditDir := dir + "/audit"
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{
		Key:    "post",
		PID:    4321,
		Lstart: lstart,
	}
	lister := newDynamicLister([]RawProcess{{
		PID: 4321, Lstart: lstart,
		Cmdline: "node app-server-broker.mjs",
	}})
	sig := &capturingSignaller{clears: lister}
	ks := &KillSequence{
		Rec:             rec,
		Lister:          lister,
		Signaller:       sig,
		FS:              auditFS{},
		AuditDir:        auditDir,
		PgidLookup:      fakePgidLooker(4321, nil),
		GracefulTimeout: 30 * time.Millisecond,
		TermTimeout:     500 * time.Millisecond,
		KillTimeout:     500 * time.Millisecond,
	}
	res, err := ks.Run(context.Background(), DecisionResult{Reason: "idle_timeout", Kill: true}, &fakeSocketVerifier{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TermOk {
		t.Errorf("expected TermOk=true (process cleared on SIGTERM)")
	}
	entries, _ := os.ReadDir(auditDir)
	if len(entries) == 0 {
		t.Fatalf("expected audit file, got none")
	}
	body, err := os.ReadFile(auditDir + "/" + entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	// Postscript should have killSequence.termOk=true marshalled.
	if !strings.Contains(string(body), `"termOk": true`) {
		t.Errorf("postscript did not include termOk=true; body=%s", string(body))
	}
}

// -- PR review finding A: re-verify identity at every signal step --------

// driftLister is a ProcessLister whose return values can be mutated mid-run
// to simulate identity drift between Step 0 and Step 3 / Step 4 (PID-reuse
// race within the kill sequence). It also implements clearPID so the
// signaller fakes can model "process actually died" without exposing the
// inner dynamicLister.
type driftLister struct {
	mu         sync.Mutex
	rows       []RawProcess
	swapAfter  int                            // call # at which to swap to swapRows
	swapRows   []RawProcess
	calls      int
	transition func(prev []RawProcess) []RawProcess
}

func (l *driftLister) List(_ context.Context) ([]RawProcess, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	// swap once after the configured call number (e.g. swapAfter=1 means
	// the first List() returns rows; the second + onward returns swapRows).
	if l.calls > l.swapAfter && (l.transition != nil || l.swapRows != nil) {
		if l.transition != nil {
			l.rows = l.transition(l.rows)
			l.transition = nil
		} else if l.swapRows != nil {
			l.rows = l.swapRows
			l.swapRows = nil
		}
	}
	out := make([]RawProcess, len(l.rows))
	copy(out, l.rows)
	return out, nil
}

func (l *driftLister) clearPID(pid int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.rows[:0]
	for _, row := range l.rows {
		if row.PID != pid {
			out = append(out, row)
		}
	}
	l.rows = out
}

// TestStepSIGTERM_ReVerifiesIdentity_AbortsOnDrift — finding A regression:
// stepSIGTERM must re-fetch identity *immediately* before syscall.Kill so
// a PID-reuse during the audit/graceful window does not result in signalling
// an unrelated process group.
//
// Setup: lister returns the same PID with a DIFFERENT lstart at the moment
// stepSIGTERM is called (i.e., the original broker exited and the kernel
// handed the same PID to a fresh unrelated process between Step 0 and
// here).
//
// Expectation: stepSIGTERM aborts before sending SIGTERM and returns
// (false, identityMismatchErr). Zero signals are recorded.
func TestStepSIGTERM_ReVerifiesIdentity_AbortsOnDrift(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	driftedLstart := lstart.Add(1 * time.Hour)
	lister := NewFakeProcessLister([]RawProcess{{
		PID: 4321, Lstart: driftedLstart, Cmdline: "/usr/bin/zsh -i",
	}})
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(4321, nil),
		TermTimeout: 100 * time.Millisecond,
	}
	ok, err := ks.stepSIGTERM(context.Background())
	if ok {
		t.Errorf("expected stepSIGTERM=false on identity drift")
	}
	if !IsIdentityMismatch(err) {
		t.Errorf("expected identityMismatchErr from stepSIGTERM, got %v", err)
	}
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on identity drift, got %+v", got)
	}
}

// TestStepSIGKILL_ReVerifiesIdentity_AbortsOnDrift — finding A regression:
// same as the SIGTERM test, applied to Step 4 SIGKILL. A drift between Step
// 3 and Step 4 (e.g., the stubborn process finally died and PID was reused
// during the SIGTERM-budget poll) must abort the SIGKILL too.
func TestStepSIGKILL_ReVerifiesIdentity_AbortsOnDrift(t *testing.T) {
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "k1", PID: 4321, Lstart: lstart}
	lister := &driftLister{
		rows: []RawProcess{{PID: 4321, Lstart: lstart.Add(2 * time.Hour), Cmdline: "/usr/bin/zsh"}},
	}
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:         rec,
		Lister:      lister,
		Signaller:   sig,
		PgidLookup:  fakePgidLooker(4321, nil),
		KillTimeout: 100 * time.Millisecond,
	}
	ok, err := ks.stepSIGKILL(context.Background())
	if ok {
		t.Errorf("expected stepSIGKILL=false on identity drift")
	}
	if !IsIdentityMismatch(err) {
		t.Errorf("expected identityMismatchErr from stepSIGKILL, got %v", err)
	}
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on identity drift, got %+v", got)
	}
}

// TestKillSequenceRun_DriftBetweenStep0AndStep3_AbortsAndReturnsMismatch —
// finding A end-to-end: Step 0 succeeds, then identity drifts before Step 3.
// Run must propagate the identity-mismatch error and NOT send any signal.
func TestKillSequenceRun_DriftBetweenStep0AndStep3_AbortsAndReturnsMismatch(t *testing.T) {
	dir := t.TempDir()
	auditDir := dir + "/audit"
	lstart := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	rec := BrokerRecord{Key: "drift", PID: 4321, Lstart: lstart}
	driftedLstart := lstart.Add(1 * time.Hour)
	// Step 0 (call 1) returns matching row; subsequent calls return drifted.
	lister := &driftLister{
		rows: []RawProcess{{PID: 4321, Lstart: lstart, Cmdline: "node app-server-broker.mjs"}},
		swapAfter: 1, // first call (Step 0) sees match; later calls see drift
		swapRows: []RawProcess{{PID: 4321, Lstart: driftedLstart, Cmdline: "/usr/bin/zsh"}},
	}
	sig := &capturingSignaller{}
	ks := &KillSequence{
		Rec:             rec,
		Lister:          lister,
		Signaller:       sig,
		FS:              auditFS{},
		AuditDir:        auditDir,
		PgidLookup:      fakePgidLooker(4321, nil),
		GracefulTimeout: 10 * time.Millisecond,
		TermTimeout:     50 * time.Millisecond,
		KillTimeout:     50 * time.Millisecond,
	}
	_, err := ks.Run(context.Background(), DecisionResult{Reason: "idle_timeout", Kill: true}, &fakeSocketVerifier{})
	if !IsIdentityMismatch(err) {
		t.Fatalf("expected identityMismatchErr from Run after drift, got %v", err)
	}
	// Zero signals must have reached the kernel.
	if got := sig.snapshot(); len(got) != 0 {
		t.Errorf("expected zero signals on Step 3 drift, got %+v", got)
	}
}

// -- Test helpers used across Tasks L–P ----------------------------------

// pathExistsKT reports whether a filesystem path is reachable. The KT suffix
// keeps it from clashing with similarly-named helpers elsewhere in the
// package's test suite.
func pathExistsKT(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// _ keeps the imports we need across the file alive (some tests below use
// strings/atomic; this no-op keeps the linter quiet if a future refactor
// drops the only consumer).
var _ = strings.Contains
var _ atomic.Int64
