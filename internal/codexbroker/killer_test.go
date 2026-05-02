package codexbroker

import (
	"context"
	"errors"
	"net"
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

// -- Test helpers used across Tasks L–P ----------------------------------

// _ keeps the imports we need across the file alive (some tests below use
// strings/atomic; this no-op keeps the linter quiet if a future refactor
// drops the only consumer).
var _ = strings.Contains
var _ atomic.Int64
