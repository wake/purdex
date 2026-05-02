package codexbroker

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeDialer drives the broker RPC seam in predicate-A tests. Its Dial may
// either return a fake net.Conn that responds with the canned threads JSON
// payload, or fail with the canned error. The connection is one-shot and
// honours read/write deadlines so timeouts are deterministic.
type fakeDialer struct {
	respondWith string // canned response body for thread/list
	dialErr     error
	hangForever bool // simulate RPC hanging past timeout
}

type fakeConn struct {
	read    *strings.Reader
	deadlne time.Time
	closed  bool
	hang    bool
}

func (c *fakeConn) Read(b []byte) (int, error) {
	if c.hang {
		// Block until deadline.
		if c.deadlne.IsZero() {
			c.deadlne = time.Now().Add(10 * time.Second)
		}
		until := time.Until(c.deadlne)
		if until > 0 {
			time.Sleep(until)
		}
		return 0, &net.OpError{Op: "read", Err: errors.New("i/o timeout")}
	}
	return c.read.Read(b)
}
func (c *fakeConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *fakeConn) Close() error                       { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr                { return &net.UnixAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr               { return &net.UnixAddr{} }
func (c *fakeConn) SetDeadline(t time.Time) error      { c.deadlne = t; return nil }
func (c *fakeConn) SetReadDeadline(t time.Time) error  { c.deadlne = t; return nil }
func (c *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

func (d *fakeDialer) Dial(network, address string) (net.Conn, error) {
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	if d.hangForever {
		return &fakeConn{hang: true}, nil
	}
	// Build a minimal HTTP/1.1 response with the canned JSON body so the
	// production code's HTTP parsing path is exercised end-to-end.
	body := d.respondWith
	resp := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
		itoa(len(body)) + "\r\n\r\n" + body
	return &fakeConn{read: strings.NewReader(resp)}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	s := string(buf[i:])
	if neg {
		s = "-" + s
	}
	return s
}

func brokerRecord() BrokerRecord {
	return BrokerRecord{
		Key:      "abcdef0123456789",
		PID:      4321,
		Cwd:      "/tmp/work",
		Endpoint: "unix:/tmp/cxc-XX/broker.sock",
		StateDir: "/state",
	}
}

// TestEvalPredicateA_RPCActiveThread — fake dialer returns one active thread
// → A=true.
func TestEvalPredicateA_RPCActiveThread(t *testing.T) {
	dialer := &fakeDialer{respondWith: `{"threads":[{"id":"t1","status":"active"}]}`}
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, nil, NewFakeProcessLister(nil), dialer)
	if !ok {
		t.Errorf("expected predicate A true on active thread")
	}
}

// TestEvalPredicateA_RPCUnreachable_StateRunning_NotStale — RPC dial fails;
// jobs[] has a running job whose pid is alive with a broker cmdline → A=true
// (conflict rule: RPC stall does not penalise broker, spec §5.1 lines 367-368).
func TestEvalPredicateA_RPCUnreachable_StateRunning_NotStale(t *testing.T) {
	dialer := &fakeDialer{dialErr: errors.New("connection refused")}
	now := time.Now()
	jobs := []StateJobLite{{
		ID: "j1", Status: "running", UpdatedAt: now.Add(-1 * time.Minute), Pid: intPtr(5555),
	}}
	lister := NewFakeProcessLister([]RawProcess{{
		PID: 5555, Cmdline: "node /opt/codex/dist/app-server-broker.mjs serve --cwd /tmp/work",
	}})
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, jobs, lister, dialer)
	if !ok {
		t.Errorf("expected predicate A true (RPC down + non-stale running job)")
	}
}

// TestEvalPredicateA_RPCUnreachable_StateRunning_Stale — RPC dial fails;
// jobs[] has a running job that is stale → A=false.
func TestEvalPredicateA_RPCUnreachable_StateRunning_Stale(t *testing.T) {
	dialer := &fakeDialer{dialErr: errors.New("connection refused")}
	now := time.Now()
	jobs := []StateJobLite{{
		ID: "j1", Status: "running", UpdatedAt: now.Add(-2 * time.Hour), Pid: nil,
	}}
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, jobs, NewFakeProcessLister(nil), dialer)
	if ok {
		t.Errorf("expected predicate A false (RPC down + stale running job)")
	}
}

// TestEvalPredicateA_RPCDown_StateJobsStale_ReturnsFalse — RPC dialer returns
// error; two running jobs both stale (one nil-pid past threshold, one
// non-nil-pid that lister reports dead) → A=false.
func TestEvalPredicateA_RPCDown_StateJobsStale_ReturnsFalse(t *testing.T) {
	dialer := &fakeDialer{dialErr: errors.New("dial unix: connection refused")}
	now := time.Now()
	jobs := []StateJobLite{
		{ID: "j1", Status: "running", UpdatedAt: now.Add(-3 * time.Hour), Pid: nil},
		{ID: "j2", Status: "running", UpdatedAt: now.Add(-30 * time.Minute), Pid: intPtr(6666)},
	}
	// Lister returns no rows → pid 6666 dead.
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, jobs, NewFakeProcessLister(nil), dialer)
	if ok {
		t.Errorf("expected predicate A false (both stale)")
	}
}

// TestEvalPredicateA_RPCUnreachable_StateQueued — RPC dial fails; queued jobs
// always count regardless of stale check → A=true.
func TestEvalPredicateA_RPCUnreachable_StateQueued(t *testing.T) {
	dialer := &fakeDialer{dialErr: errors.New("dial timeout")}
	jobs := []StateJobLite{{
		ID: "j1", Status: "queued", UpdatedAt: time.Now().Add(-1 * time.Minute),
	}}
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, jobs, NewFakeProcessLister(nil), dialer)
	if !ok {
		t.Errorf("expected predicate A true (queued job)")
	}
}

// TestEvalPredicateA_RPCOk_NoThreads_StateEmpty — RPC says no threads + jobs
// empty → A=false.
func TestEvalPredicateA_RPCOk_NoThreads_StateEmpty(t *testing.T) {
	dialer := &fakeDialer{respondWith: `{"threads":[]}`}
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, nil, NewFakeProcessLister(nil), dialer)
	if ok {
		t.Errorf("expected predicate A false (no RPC threads + no jobs)")
	}
}

// TestEvalPredicateA_RPCTimeout_StateEmpty — RPC dialer hangs; predicate must
// timeout cleanly and fall through to jobs (empty) → A=false. No panic.
func TestEvalPredicateA_RPCTimeout_StateEmpty(t *testing.T) {
	dialer := &fakeDialer{hangForever: true}
	rec := brokerRecord()
	// Use a tiny budget through the ctx so the test is fast.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ok, _ := EvalPredicateA(ctx, rec, nil, NewFakeProcessLister(nil), dialer)
	if ok {
		t.Errorf("expected predicate A false (RPC timeout + no jobs)")
	}
}

// TestEvalPredicateA_RPCDown_NilJobs — RPC dial fails AND jobs slice is nil
// (caller passed empty because ReadStateJobs failed) → A=false.
func TestEvalPredicateA_RPCDown_NilJobs(t *testing.T) {
	dialer := &fakeDialer{dialErr: errors.New("dial: ENOENT")}
	rec := brokerRecord()
	ok, _ := EvalPredicateA(context.Background(), rec, nil, NewFakeProcessLister(nil), dialer)
	if ok {
		t.Errorf("expected predicate A false (RPC down + nil jobs)")
	}
}

// Smoke: make sure syscall import survives in test file (used elsewhere).
var _ = syscall.SIGTERM
