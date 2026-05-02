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

// ----- Task D — Predicate B (recent delivery readable) -------------------

func brokerRecordWithStateDir(stateDir string) BrokerRecord {
	r := brokerRecord()
	r.StateDir = stateDir
	return r
}

// TestEvalPredicateB_CompletedJobWithinWindow_FilePresent — completed job
// within window AND jobs/<id>.json exists with a result/rendered field
// → B=true.
func TestEvalPredicateB_CompletedJobWithinWindow_FilePresent(t *testing.T) {
	now := time.Now()
	completed := now.Add(-5 * time.Minute)
	jobs := []StateJobLite{{
		ID: "j1", Status: "completed", UpdatedAt: completed, CompletedAt: timePtr(completed),
	}}
	fs := newMemFS()
	fs.writeFile("/state/jobs/j1.json", `{"result":"ok"}`)
	rec := brokerRecordWithStateDir("/state")
	ok, _ := EvalPredicateB(rec, jobs, fs, DefaultRecentResultWindow)
	if !ok {
		t.Errorf("expected B=true (completed in window with file)")
	}
}

// TestEvalPredicateB_CompletedJobWithinWindow_FileMissing — completed in
// window but no jobs/<id>.json on disk → B=false.
func TestEvalPredicateB_CompletedJobWithinWindow_FileMissing(t *testing.T) {
	now := time.Now()
	completed := now.Add(-5 * time.Minute)
	jobs := []StateJobLite{{
		ID: "j1", Status: "completed", UpdatedAt: completed, CompletedAt: timePtr(completed),
	}}
	fs := newMemFS()
	rec := brokerRecordWithStateDir("/state")
	ok, _ := EvalPredicateB(rec, jobs, fs, DefaultRecentResultWindow)
	if ok {
		t.Errorf("expected B=false (file missing)")
	}
}

// TestEvalPredicateB_CompletedJobBeyondWindow — completed long ago → B=false.
func TestEvalPredicateB_CompletedJobBeyondWindow(t *testing.T) {
	now := time.Now()
	completed := now.Add(-2 * time.Hour)
	jobs := []StateJobLite{{
		ID: "j1", Status: "completed", UpdatedAt: completed, CompletedAt: timePtr(completed),
	}}
	fs := newMemFS()
	fs.writeFile("/state/jobs/j1.json", `{"result":"ok"}`)
	rec := brokerRecordWithStateDir("/state")
	ok, _ := EvalPredicateB(rec, jobs, fs, DefaultRecentResultWindow)
	if ok {
		t.Errorf("expected B=false (beyond window)")
	}
}

// TestEvalPredicateB_FailedJobWithinWindow — only completed status counts,
// failed/cancelled do not → B=false.
func TestEvalPredicateB_FailedJobWithinWindow(t *testing.T) {
	now := time.Now()
	when := now.Add(-1 * time.Minute)
	jobs := []StateJobLite{{
		ID: "j1", Status: "failed", UpdatedAt: when, CompletedAt: timePtr(when),
	}}
	fs := newMemFS()
	fs.writeFile("/state/jobs/j1.json", `{"error":"boom"}`)
	rec := brokerRecordWithStateDir("/state")
	ok, _ := EvalPredicateB(rec, jobs, fs, DefaultRecentResultWindow)
	if ok {
		t.Errorf("expected B=false (failed status doesn't count)")
	}
}

// TestEvalPredicateB_NoJobs — empty jobs slice → B=false.
func TestEvalPredicateB_NoJobs(t *testing.T) {
	fs := newMemFS()
	rec := brokerRecordWithStateDir("/state")
	ok, _ := EvalPredicateB(rec, nil, fs, DefaultRecentResultWindow)
	if ok {
		t.Errorf("expected B=false (no jobs)")
	}
}

// TestEvalPredicateB_MalformedJobFile — file exists but JSON parse fails →
// B=false.
func TestEvalPredicateB_MalformedJobFile(t *testing.T) {
	now := time.Now()
	completed := now.Add(-5 * time.Minute)
	jobs := []StateJobLite{{
		ID: "j1", Status: "completed", UpdatedAt: completed, CompletedAt: timePtr(completed),
	}}
	fs := newMemFS()
	fs.writeFile("/state/jobs/j1.json", `{not-json`)
	rec := brokerRecordWithStateDir("/state")
	ok, _ := EvalPredicateB(rec, jobs, fs, DefaultRecentResultWindow)
	if ok {
		t.Errorf("expected B=false (malformed file)")
	}
}

// ----- Task E — Predicate C (live ownership) -----------------------------

// fakeRegistry is a minimal LaunchRegistryReader for tests; full impl is
// task I.
type fakeRegistry struct {
	entries map[string]LaunchEntry
}

func (r *fakeRegistry) Lookup(brokerKey string) (*LaunchEntry, bool) {
	e, ok := r.entries[brokerKey]
	if !ok {
		return nil, false
	}
	return &e, true
}

func (r *fakeRegistry) Empty() bool { return len(r.entries) == 0 }

// fakePaneLister returns a fixed set of live pane IDs.
type fakePaneLister struct {
	live map[string]bool
	err  error
}

func (l *fakePaneLister) IsAlive(pane string) (bool, error) {
	if l.err != nil {
		return false, l.err
	}
	return l.live[pane], nil
}

// memFSWithDirs lets us add an existing directory entry to memFS so cwd
// stat succeeds.
func memFSWithCwd(cwd string) *memFS {
	fs := newMemFS()
	fs.dirs[cwd] = true
	return fs
}

// TestEvalPredicateC_CwdExists_PaneAlive — cwd exists + registry pane alive
// → C=true.
func TestEvalPredicateC_CwdExists_PaneAlive(t *testing.T) {
	rec := brokerRecord()
	rec.Cwd = "/tmp/work"
	fs := memFSWithCwd("/tmp/work")
	reg := &fakeRegistry{entries: map[string]LaunchEntry{
		rec.Key: {BrokerKey: rec.Key, TmuxPane: "%42"},
	}}
	panes := &fakePaneLister{live: map[string]bool{"%42": true}}
	ok, _ := EvalPredicateC(rec, fs, reg, panes)
	if !ok {
		t.Errorf("expected C=true (cwd exists + pane alive)")
	}
}

// TestEvalPredicateC_CwdExists_PaneGone — cwd exists but pane closed
// → C=false.
func TestEvalPredicateC_CwdExists_PaneGone(t *testing.T) {
	rec := brokerRecord()
	rec.Cwd = "/tmp/work"
	fs := memFSWithCwd("/tmp/work")
	reg := &fakeRegistry{entries: map[string]LaunchEntry{
		rec.Key: {BrokerKey: rec.Key, TmuxPane: "%42"},
	}}
	panes := &fakePaneLister{live: map[string]bool{}} // pane gone
	ok, _ := EvalPredicateC(rec, fs, reg, panes)
	if ok {
		t.Errorf("expected C=false (pane gone)")
	}
}

// TestEvalPredicateC_CwdENOENT — cwd definitively missing → C=false.
func TestEvalPredicateC_CwdENOENT(t *testing.T) {
	rec := brokerRecord()
	rec.Cwd = "/missing/path"
	rec.CwdExists = false
	rec.Anomalies = []Anomaly{{Code: AnomalyCwdMissing}}
	fs := newMemFS()
	reg := &fakeRegistry{entries: map[string]LaunchEntry{
		rec.Key: {BrokerKey: rec.Key, TmuxPane: "%42"},
	}}
	panes := &fakePaneLister{live: map[string]bool{"%42": true}}
	ok, _ := EvalPredicateC(rec, fs, reg, panes)
	if ok {
		t.Errorf("expected C=false (cwd ENOENT)")
	}
}

// TestEvalPredicateC_CwdTransientError — transient stat error (not ENOENT)
// → fall through (do not gate on cwd); pane evidence still wins.
func TestEvalPredicateC_CwdTransientError(t *testing.T) {
	rec := brokerRecord()
	rec.Cwd = "/tmp/work"
	rec.CwdExists = false // stat failed transiently
	rec.Anomalies = []Anomaly{{Code: AnomalyCwdTransientStatError}}
	fs := newMemFS() // path absent in memFS — but the anomaly tells us it's transient
	reg := &fakeRegistry{entries: map[string]LaunchEntry{
		rec.Key: {BrokerKey: rec.Key, TmuxPane: "%42"},
	}}
	panes := &fakePaneLister{live: map[string]bool{"%42": true}}
	ok, _ := EvalPredicateC(rec, fs, reg, panes)
	if !ok {
		t.Errorf("expected C=true (transient cwd error + pane alive)")
	}
}

// TestEvalPredicateC_NoRegistryEntry — cwd exists but registry has no entry
// → C=false (no positive ownership evidence).
func TestEvalPredicateC_NoRegistryEntry(t *testing.T) {
	rec := brokerRecord()
	rec.Cwd = "/tmp/work"
	fs := memFSWithCwd("/tmp/work")
	reg := &fakeRegistry{entries: map[string]LaunchEntry{}}
	panes := &fakePaneLister{live: map[string]bool{}}
	ok, _ := EvalPredicateC(rec, fs, reg, panes)
	if ok {
		t.Errorf("expected C=false (no registry entry)")
	}
}

// TestEvalPredicateC_RegistryEntry_NoPane — registry entry exists but its
// pane is gone → C=false.
func TestEvalPredicateC_RegistryEntry_NoPane(t *testing.T) {
	rec := brokerRecord()
	rec.Cwd = "/tmp/work"
	fs := memFSWithCwd("/tmp/work")
	reg := &fakeRegistry{entries: map[string]LaunchEntry{
		rec.Key: {BrokerKey: rec.Key, TmuxPane: "%99"},
	}}
	panes := &fakePaneLister{live: map[string]bool{"%42": true}} // %99 not live
	ok, _ := EvalPredicateC(rec, fs, reg, panes)
	if ok {
		t.Errorf("expected C=false (pane closed)")
	}
}

// ----- Task F — EvalDecision composer (two-layer responsibility) ---------

// decisionOptsAllFalse constructs a populated DecisionOpts where every
// predicate-leg fails by default. Tests override individual fields to flip
// one leg true.
func decisionOptsAllFalse(_ bool) DecisionOpts {
	return DecisionOpts{
		FS:             newMemFS(),
		Lister:         NewFakeProcessLister(nil),
		Dialer:         &fakeDialer{dialErr: errors.New("dial err")},
		Registry:       &fakeRegistry{entries: map[string]LaunchEntry{}},
		Panes:          &fakePaneLister{},
		IdleTimeout:    30 * time.Minute,
		ResultWindow:   DefaultRecentResultWindow,
		StaleThreshold: DefaultStaleRunningThreshold,
	}
}

func brokerWithLastJobAge(d time.Duration) BrokerRecord {
	rec := brokerRecord()
	t := time.Now().Add(-d)
	rec.LastJobUpdatedAt = &t
	return rec
}

// TestEvalDecision_ATrue_NoKill — predicate A true → baseline Kill=false.
func TestEvalDecision_ATrue_NoKill(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour) // idle expired
	opts := decisionOptsAllFalse(false)
	// Force RPC to return active thread → A true.
	opts.Dialer = &fakeDialer{respondWith: `{"threads":[{"id":"t1","status":"active"}]}`}
	// Registry has matching entry so foreign would NOT fire.
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	opts.Panes = &fakePaneLister{live: map[string]bool{"%1": true}}
	res := EvalDecision(context.Background(), rec, opts)
	if res.Kill {
		t.Errorf("expected Kill=false (A true), got %+v", res)
	}
	if !res.Predicates.A {
		t.Errorf("expected A=true, got %+v", res.Predicates)
	}
}

// TestEvalDecision_BTrue_NoKill — predicate B true → baseline Kill=false.
func TestEvalDecision_BTrue_NoKill(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour)
	rec.StateDir = "/state"
	opts := decisionOptsAllFalse(false)
	// Plant a recent completed job + readable result file.
	now := time.Now()
	body := `{"jobs":[{"id":"j1","status":"completed","updatedAt":"` + now.Add(-5*time.Minute).Format(time.RFC3339) + `","completedAt":"` + now.Add(-5*time.Minute).Format(time.RFC3339) + `","pid":null}]}`
	fs := newMemFS()
	fs.writeFile("/state/state.json", body)
	fs.writeFile("/state/jobs/j1.json", `{"result":"ok"}`)
	opts.FS = fs
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	opts.Panes = &fakePaneLister{live: map[string]bool{}}
	res := EvalDecision(context.Background(), rec, opts)
	if res.Kill {
		t.Errorf("expected Kill=false (B true), got %+v", res)
	}
	if !res.Predicates.B {
		t.Errorf("expected B=true, got %+v", res.Predicates)
	}
}

// TestEvalDecision_CTrue_NoKill — predicate C true → baseline Kill=false.
func TestEvalDecision_CTrue_NoKill(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour)
	rec.Cwd = "/tmp/work"
	rec.CwdExists = true
	opts := decisionOptsAllFalse(false)
	opts.FS = memFSWithCwd("/tmp/work")
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	opts.Panes = &fakePaneLister{live: map[string]bool{"%1": true}}
	res := EvalDecision(context.Background(), rec, opts)
	if res.Kill {
		t.Errorf("expected Kill=false (C true), got %+v", res)
	}
	if !res.Predicates.C {
		t.Errorf("expected C=true, got %+v", res.Predicates)
	}
}

// TestEvalDecision_AllFalse_IdleExpired_BaselineKill — registry has matching
// entry; baseline Kill=true, Reason=idle_timeout, AnomaliesAdded empty.
func TestEvalDecision_AllFalse_IdleExpired_BaselineKill(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour) // idle expired
	rec.Cwd = "/tmp/missing"
	rec.CwdExists = false
	rec.Anomalies = []Anomaly{{Code: AnomalyCwdMissing}}
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	res := EvalDecision(context.Background(), rec, opts)
	if !res.Kill {
		t.Errorf("expected Kill=true (all false + idle), got %+v", res)
	}
	if res.Reason != "idle_timeout" {
		t.Errorf("expected Reason=idle_timeout, got %q", res.Reason)
	}
	if len(res.AnomaliesAdded) != 0 {
		t.Errorf("expected no AnomaliesAdded, got %+v", res.AnomaliesAdded)
	}
}

// TestEvalDecision_AllFalse_IdleNotExpired_NoKill — baseline Kill=false.
func TestEvalDecision_AllFalse_IdleNotExpired_NoKill(t *testing.T) {
	rec := brokerWithLastJobAge(5 * time.Minute) // not expired
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	res := EvalDecision(context.Background(), rec, opts)
	if res.Kill {
		t.Errorf("expected Kill=false (idle not expired), got %+v", res)
	}
}

// TestEvalDecision_Foreign_AllFalse_IdleNotExpired_NoKill_PlusForeignAnomaly
// — foreign + not idle → baseline Kill=false (idle protects),
// Reason=foreign_quarantine, AnomaliesAdded=[AnomalyForeignOwner].
func TestEvalDecision_Foreign_AllFalse_IdleNotExpired_NoKill_PlusForeignAnomaly(t *testing.T) {
	rec := brokerWithLastJobAge(5 * time.Minute)
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{}} // empty
	res := EvalDecision(context.Background(), rec, opts)
	if res.Kill {
		t.Errorf("expected Kill=false (idle not expired protects), got %+v", res)
	}
	if res.Reason != "foreign_quarantine" {
		t.Errorf("expected Reason=foreign_quarantine, got %q", res.Reason)
	}
	if len(res.AnomaliesAdded) != 1 || res.AnomaliesAdded[0] != AnomalyForeignOwner {
		t.Errorf("expected AnomaliesAdded=[foreign_owner], got %+v", res.AnomaliesAdded)
	}
}

// TestEvalDecision_Foreign_AllFalse_IdleExpired_BaselineKillTrue_PlusForeignAnomaly
// — round-3 regression: foreign + idle + ¬A∧¬B∧¬C → baseline Kill=true,
// Reason=foreign_quarantine, AnomaliesAdded=[AnomalyForeignOwner].
//
// The decision layer reports both: kill IS baseline-valid, AND broker is
// foreign — sweep handler decides whether to honour it.
func TestEvalDecision_Foreign_AllFalse_IdleExpired_BaselineKillTrue_PlusForeignAnomaly(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour) // idle expired
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{}} // empty
	res := EvalDecision(context.Background(), rec, opts)
	if !res.Kill {
		t.Fatalf("expected Kill=true (baseline kill valid for foreign + idle), got %+v", res)
	}
	if res.Reason != "foreign_quarantine" {
		t.Errorf("expected Reason=foreign_quarantine, got %q", res.Reason)
	}
	if len(res.AnomaliesAdded) != 1 || res.AnomaliesAdded[0] != AnomalyForeignOwner {
		t.Errorf("expected AnomaliesAdded=[foreign_owner], got %+v", res.AnomaliesAdded)
	}
}

// TestEvalDecision_Foreign_PredicateATrue_NoKill_PlusForeignAnomaly —
// round-3 regression: foreign + predicate A true → baseline Kill=false
// (alive protection wins over both idle and foreign).
//
// Operator-explicit override on this broker MUST NOT kill it (the sweep
// handler test in task Q enforces).
func TestEvalDecision_Foreign_PredicateATrue_NoKill_PlusForeignAnomaly(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour)
	opts := decisionOptsAllFalse(false)
	opts.Dialer = &fakeDialer{respondWith: `{"threads":[{"id":"t1","status":"active"}]}`}
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{}}
	res := EvalDecision(context.Background(), rec, opts)
	if res.Kill {
		t.Errorf("expected Kill=false (alive protection wins), got %+v", res)
	}
	if res.Reason != "foreign_quarantine" {
		t.Errorf("expected Reason=foreign_quarantine, got %q", res.Reason)
	}
	if !res.Predicates.A {
		t.Errorf("expected A=true, got %+v", res.Predicates)
	}
}

// TestEvalDecision_RegistryMissing_AllForeign_BaselinePerBroker — registry
// Empty()==true; every broker gets AnomaliesAdded=[AnomalyForeignOwner] +
// Reason=foreign_quarantine; baseline Kill follows per-broker.
func TestEvalDecision_RegistryMissing_AllForeign_BaselinePerBroker(t *testing.T) {
	idleRec := brokerWithLastJobAge(2 * time.Hour)
	freshRec := brokerWithLastJobAge(5 * time.Minute)
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{}}

	idleRes := EvalDecision(context.Background(), idleRec, opts)
	if !idleRes.Kill {
		t.Errorf("idle broker baseline kill should be true: %+v", idleRes)
	}
	if idleRes.Reason != "foreign_quarantine" {
		t.Errorf("idle broker reason %q want foreign_quarantine", idleRes.Reason)
	}

	freshRes := EvalDecision(context.Background(), freshRec, opts)
	if freshRes.Kill {
		t.Errorf("fresh broker baseline kill should be false: %+v", freshRes)
	}
	if freshRes.Reason != "foreign_quarantine" {
		t.Errorf("fresh broker reason %q want foreign_quarantine", freshRes.Reason)
	}
}

// TestEvalDecision_RegistryNil_AllForeign_BaselinePerBroker — defensive:
// opts.Registry == nil treated identically to Empty().
func TestEvalDecision_RegistryNil_AllForeign_BaselinePerBroker(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour)
	opts := decisionOptsAllFalse(false)
	opts.Registry = nil
	res := EvalDecision(context.Background(), rec, opts)
	if !res.Kill {
		t.Errorf("expected Kill=true (idle expired baseline), got %+v", res)
	}
	if res.Reason != "foreign_quarantine" {
		t.Errorf("expected Reason=foreign_quarantine, got %q", res.Reason)
	}
	if len(res.AnomaliesAdded) != 1 {
		t.Errorf("expected one anomaly added, got %+v", res.AnomaliesAdded)
	}
}

// TestEvalDecision_RegistryHasOtherKey_ThisIsForeign — registry populated
// but lookup miss for this brokerKey → foreign_quarantine.
func TestEvalDecision_RegistryHasOtherKey_ThisIsForeign(t *testing.T) {
	rec := brokerWithLastJobAge(2 * time.Hour)
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{
		"someotherkey": {BrokerKey: "someotherkey", TmuxPane: "%99"},
	}}
	res := EvalDecision(context.Background(), rec, opts)
	if !res.Kill {
		t.Errorf("expected Kill=true (idle expired), got %+v", res)
	}
	if res.Reason != "foreign_quarantine" {
		t.Errorf("expected Reason=foreign_quarantine, got %q", res.Reason)
	}
}

// TestEvalDecision_NilLastJobUpdatedAt_IdleInfinite_BaselineKill — nil
// LastJobUpdatedAt → idle=∞ → idle considered expired, baseline kill true
// (uses populated registry so reason stays idle_timeout).
func TestEvalDecision_NilLastJobUpdatedAt_IdleInfinite_BaselineKill(t *testing.T) {
	rec := brokerRecord() // no LastJobUpdatedAt
	rec.LastJobUpdatedAt = nil
	opts := decisionOptsAllFalse(false)
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	res := EvalDecision(context.Background(), rec, opts)
	if !res.Kill {
		t.Errorf("expected Kill=true (nil idle treated as expired), got %+v", res)
	}
	if res.Reason != "idle_timeout" {
		t.Errorf("expected Reason=idle_timeout, got %q", res.Reason)
	}
}
