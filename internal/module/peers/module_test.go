package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
	"github.com/wake/purdex/internal/store"
)

// fixture76973 is the same real registry-file shape used by
// internal/peers/registry_test.go (mlab pid 76973), reused here so the
// module-level happy-path test exercises a realistic registry entry without
// duplicating the fixture's meaning.
const fixture76973 = `{"pid":76973,"sessionId":"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c","cwd":"/Users/wake/Workspace/wake/purdex","startedAt":1789314156000,"procStart":"Sun Sep 13 15:22:36 2026","version":"2.1.270","peerProtocol":1,"peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],"kind":"interactive","entrypoint":"cli","pidDomain":"darwin","tmux":"mt1:@10.%10","messagingSocketPath":"/tmp/cc-socks/76973.sock","name":"purdex-47","nameSource":"derived","nameSince":1789314156000,"updatedAt":1789314156100,"status":"busy","statusUpdatedAt":1789314156100}`

// fixture76973ProcStart is the instant fixture76973's procStart denotes (UTC).
var fixture76973ProcStart = time.Date(2026, 9, 13, 15, 22, 36, 0, time.UTC)

func writeRegistryFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

// allLiveLiveness returns a Liveness whose Stat/PidAlive always succeed and
// whose StartTime always matches startTime, so every well-formed registry
// entry is treated as live.
func allLiveLiveness(startTime time.Time) ipeers.Liveness {
	return ipeers.Liveness{
		Stat:      func(path string) error { return nil },
		PidAlive:  func(pid int) bool { return true },
		StartTime: func(pid int) (time.Time, error) { return startTime, nil },
	}
}

// newTestCore builds a minimal *core.Core carrying just the config fields
// the peers handler reads (HostID, Peers.Alias) under CfgMu.
func newTestCore(t *testing.T, hostID, alias string) *core.Core {
	t.Helper()
	return newTestCoreWithHosts(t, hostID, alias, nil)
}

// newTestCoreWithHosts is newTestCore plus configured peer hosts, for
// scope=all fan-out tests.
func newTestCoreWithHosts(t *testing.T, hostID, alias string, hosts []config.PeerHost) *core.Core {
	t.Helper()
	cfg := &config.Config{
		HostID: hostID,
		Peers:  config.PeersConfig{Alias: alias, Hosts: hosts},
	}
	return core.New(core.CoreDeps{
		Config:   cfg,
		Registry: core.NewServiceRegistry(),
	})
}

// newTestModule builds a *Module with the given collaborators wired
// directly (bypassing Init), for handler-level tests. Every other seam
// (helper manager over a fake starter, in-memory audit, limiters on the
// test clock, real frame writer, a post seam that fails the test unless
// the test overrides m.post) takes the fixture default — see newTestModuleWith for the
// knobs. client/fetch default to production values
// (newRemoteClient/fetchRemote); scope=all tests override m.fetch.
func newTestModule(t *testing.T, c *core.Core, sessions session.SessionProvider, owners agent.OwnerResolver, registryDir string, liveness ipeers.Liveness, clock *fakeClock, budget time.Duration) *Module {
	t.Helper()
	return newTestModuleWith(t, fixtureOpts{
		core:        c,
		sessions:    sessions,
		owners:      owners,
		registryDir: registryDir,
		liveness:    liveness,
		clock:       clock,
		budget:      budget,
	}).m
}

// fixtureOpts are newTestModuleWith's knobs. The zero value of every field
// not listed as required takes the fixture default.
type fixtureOpts struct {
	core     *core.Core              // required
	sessions session.SessionProvider // required
	owners   agent.OwnerResolver     // required
	// registryDir is the module's registry dir, passed through verbatim
	// ("" stays "" — some P1 tests rely on the read error). The helper
	// manager registers its helpers there too when it is non-empty, so a
	// helper's own registry entry is visible to localEnvelope; when it is
	// empty the manager uses the fixture's own short registry dir.
	registryDir string
	liveness    ipeers.Liveness
	clock       *fakeClock // nil ⇒ a clock stuck at Unix(0, 0)
	budget      time.Duration
	// variant selects the fake helper's behaviour (Normal by default).
	variant proxyhelpertest.Variant
	// readyTimeout bounds a helper spawn (2 s by default; a Broken variant
	// test shortens it so the spawn fails fast).
	readyTimeout time.Duration
	// sockWriteTimeout is the inbox write budget (2 s by default).
	sockWriteTimeout time.Duration
	// noSweep leaves the helper manager unswept (Acquire ⇒ ErrNotReady).
	noSweep bool
}

// moduleFixture is a Module plus every fake it was wired from.
type moduleFixture struct {
	m           *Module
	root        string // short /tmp dir: socket paths must stay short
	sockDir     string
	registryDir string // the manager's registry dir
	proxiesPath string
	fake        *proxyhelpertest.Fake
	audit       *fakeAudit
	titles      *store.PeerLabelStore
	logs        *logSink
	frames      chan frameEvent // every onFrame call the manager makes
	clock       *fakeClock
	postCalls   atomic.Int32
}

// newTestModuleWith builds a Module over fakes for every seam and returns
// it with the fakes. The helper manager is swept (unless opts.noSweep) so
// Acquire works; Module.Stop runs at cleanup, bounded.
func newTestModuleWith(t *testing.T, opts fixtureOpts) *moduleFixture {
	t.Helper()
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	f := &moduleFixture{
		root:        filepath.Dir(regDir),
		sockDir:     sockDir,
		registryDir: regDir,
		proxiesPath: filepath.Join(filepath.Dir(regDir), "proxies.json"),
		fake:        proxyhelpertest.New(proxyhelpertest.Options{Variant: opts.variant}),
		logs:        &logSink{},
		frames:      make(chan frameEvent, 64),
		clock:       opts.clock,
	}
	if f.clock == nil {
		f.clock = &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	}
	if opts.registryDir != "" {
		f.registryDir = opts.registryDir
	}
	if opts.readyTimeout <= 0 {
		opts.readyTimeout = 2 * time.Second
	}
	if opts.sockWriteTimeout <= 0 {
		opts.sockWriteTimeout = 2 * time.Second
	}

	meta, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatalf("open meta store: %v", err)
	}
	t.Cleanup(func() { meta.Close() })
	f.audit = &fakeAudit{real: meta.PeerMessages()}
	f.titles = meta.PeerLabels()

	// fakeClock is not goroutine-safe and the helper manager reads the
	// clock from its own goroutines: one mutex-guarded accessor feeds
	// every seam, so the P1 tests' "one entry per Now call" sequence
	// semantics are preserved and the race detector stays quiet.
	var clockMu sync.Mutex
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return f.clock.Now()
	}

	stopCtx, stopCancel := context.WithCancel(context.Background())
	m := &Module{
		core:             opts.core,
		sessions:         opts.sessions,
		owners:           opts.owners,
		registryDir:      opts.registryDir,
		liveness:         opts.liveness,
		budget:           opts.budget,
		now:              now,
		client:           newRemoteClient(),
		fetch:            fetchRemote,
		logf:             f.logs.logf,
		audit:            f.audit,
		titles:           f.titles,
		dedup:            newDedupSet(ipeers.DedupWindow, now),
		pairs:            newPairLimiter(ipeers.PairRateLimit, ipeers.PairRateWindow, now),
		hostLimit:        newHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, now),
		writeFrame:       ccuds.WriteFrame,
		sockWriteTimeout: opts.sockWriteTimeout,
		newMsgID:         uuid.NewString,
		deliverClient:    newDeliverClient(),
		post: func(context.Context, *http.Client, string, string, ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error) {
			f.postCalls.Add(1)
			t.Errorf("post seam called; a test that sends must override m.post")
			return ipeers.DeliverResponse{}, nil, errors.New("post seam called")
		},
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
		replySem:   make(chan struct{}, 8),
	}
	f.m = m
	m.helpers = newHelperManager(helperManagerConfig{
		Start:        f.fake.Starter(),
		ProxiesPath:  f.proxiesPath,
		RegistryDir:  f.registryDir,
		SockDir:      sockDir,
		Version:      ccuds.VerifiedCCVersion,
		Now:          now,
		ProcStart:    proxyhelpertest.ProcStart,
		PidAlive:     func(int) bool { return false },
		DialRefused:  func(string) bool { return true },
		Signal:       func(int, os.Signal) error { return nil },
		LiveEntries:  func() []ipeers.Entry { return nil },
		ReadyTimeout: opts.readyTimeout,
		TermGrace:    100 * time.Millisecond,
		OnFrame: func(h *helper, line string) {
			m.handleReplyFrame(h, line)
			f.frames <- frameEvent{h, line}
		},
		Log: f.logs.logf,
	})
	if !opts.noSweep {
		if err := m.helpers.Sweep(); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
	}
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { m.Stop(context.Background()); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("cleanup: Module.Stop did not return within 10 s")
		}
	})
	return f
}

// fakeAudit is the real in-memory PeerMessageStore with injectable
// failures for Insert and SetResult.
type fakeAudit struct {
	mu           sync.Mutex
	real         *store.PeerMessageStore
	insertErr    error
	setResultErr error
}

func (a *fakeAudit) fail(insert, setResult error) {
	a.mu.Lock()
	a.insertErr, a.setResultErr = insert, setResult
	a.mu.Unlock()
}

func (a *fakeAudit) Insert(p store.PeerMessage) (int64, error) {
	a.mu.Lock()
	err := a.insertErr
	a.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return a.real.Insert(p)
}

func (a *fakeAudit) SetResult(id int64, effectiveMode, result, errText string) error {
	a.mu.Lock()
	err := a.setResultErr
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return a.real.SetResult(id, effectiveMode, result, errText)
}

func (a *fakeAudit) Tail(n int) ([]store.PeerMessage, error) { return a.real.Tail(n) }

func doGetPeers(t *testing.T, m *Module, target string) *httptest.ResponseRecorder {
	t.Helper()
	return doGetPeersWithContext(t, m, target, context.Background())
}

// doGetPeersWithContext is doGetPeers but lets the caller supply the request
// context, so scope=all tests can inject a principal via
// middleware.WithPrincipal.
func doGetPeersWithContext(t *testing.T, m *Module, target string, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestHandlePeers_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "76973.json", fixture76973)

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "mt1code", Name: "mt1", Cwd: "/Users/wake/Workspace/wake/purdex", TmuxInstance: "inst1"},
		{Code: "aigora3code", Name: "aigora3", Cwd: "/work/aigora3", TmuxInstance: "inst1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"mt1code": {
			AgentType:  "cc",
			SessionID:  "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c",
			Cwd:        "/Users/wake/Workspace/wake/purdex",
			TmuxPaneID: "%10",
			LastSeenAt: 1789314156000,
			Status:     "busy",
		},
		// aigora3code deliberately absent: no owner.
	}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.OK {
		t.Fatalf("ok = false, want true; error=%q", got.Error)
	}
	if got.Partial {
		t.Fatalf("partial = true, want false")
	}
	if got.HostID != "mlab:abc123" {
		t.Errorf("host_id = %q, want %q", got.HostID, "mlab:abc123")
	}

	var mt1Rec, aigoraRec *ipeers.PeerRecord
	for i := range got.Peers {
		switch got.Peers[i].SessionCode {
		case "mt1code":
			mt1Rec = &got.Peers[i]
		case "aigora3code":
			aigoraRec = &got.Peers[i]
		}
	}
	if mt1Rec == nil {
		t.Fatalf("mt1 record not found in peers: %+v", got.Peers)
	}
	if !mt1Rec.Deliverable {
		t.Errorf("mt1 Deliverable = false, want true")
	}
	if mt1Rec.Agent == nil || mt1Rec.Agent.PeerName != "purdex-47" {
		t.Errorf("mt1 Agent = %+v, want PeerName purdex-47", mt1Rec.Agent)
	}

	if aigoraRec == nil {
		t.Fatalf("aigora3 record not found in peers: %+v", got.Peers)
	}
	if aigoraRec.Reason != "no_agent" {
		t.Errorf("aigora3 Reason = %q, want %q", aigoraRec.Reason, "no_agent")
	}
	if aigoraRec.Deliverable {
		t.Errorf("aigora3 Deliverable = true, want false")
	}

	if len(owners.calls) != 2 {
		t.Errorf("resolver calls = %v, want 2 calls", owners.calls)
	}
}

// TestHandlePeers_ResolverError_ReportedAsUnresolved pins Item 1 (#988): a
// session whose owner lookup fails (tmux read error, resolver timeout,
// cancelled context) must be reported the same way as one whose lookup never
// ran — agent:null, reason:"" — not as no_agent, and it must still mark the
// whole response partial:true even though the budget was never exceeded.
func TestHandlePeers_ResolverError_ReportedAsUnresolved(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "s1", Name: "s1", Cwd: "/a"},
	}}
	owners := &fakeOwners{
		owners: map[string]agent.PaneOwner{},
		errs:   map[string]error{"s1": errFakeProvider},
	}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.OK {
		t.Fatalf("ok = false, want true; error=%q", got.Error)
	}
	if !got.Partial {
		t.Errorf("partial = false, want true")
	}
	if len(got.Peers) != 1 {
		t.Fatalf("peers = %+v, want 1", got.Peers)
	}
	rec := got.Peers[0]
	if rec.Agent != nil {
		t.Errorf("Agent = %+v, want nil", rec.Agent)
	}
	if rec.Reason != "" {
		t.Errorf("Reason = %q, want empty", rec.Reason)
	}
	if rec.Deliverable {
		t.Errorf("Deliverable = true, want false")
	}
}

// TestHandlePeers_TmuxRestartedDuringInventory pins Item 2: mirroring
// handleSessionProvenance, the tmux server generation is sampled before
// ListSessions() and again after the owner loop. When both samples are
// non-empty and differ, the tmux server restarted mid-inventory and the
// whole response is refused (ok:false, partial:false, peers:[]) rather than
// risking session/owner data from two different tmux generations stitched
// into one answer.
func TestHandlePeers_TmuxRestartedDuringInventory(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{
		sessions:  []session.SessionInfo{{Code: "s1", Name: "s1"}},
		instances: []string{"6901:1", "6901:2"},
	}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"s1": {AgentType: "cc", SessionID: "sess-1"},
	}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got.OK {
		t.Errorf("ok = true, want false")
	}
	if got.Error != "tmux server restarted during inventory" {
		t.Errorf("error = %q, want %q", got.Error, "tmux server restarted during inventory")
	}
	if got.Partial {
		t.Errorf("partial = true, want false")
	}
	if len(got.Peers) != 0 {
		t.Errorf("peers = %+v, want empty", got.Peers)
	}
}

// #1293 §3.2: the session list is read under the inventory's own budget, so a
// hung tmux read cannot hold GET /api/peers past it — the answer is an
// ok:false envelope at (about) the budget, not a request that never returns.
func TestLocalEnvelope_SessionListBoundedByBudget(t *testing.T) {
	sessions := &fakeSessions{blockList: true}
	owners := &fakeOwners{}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	const budget = 100 * time.Millisecond
	m := newTestModule(t, c, sessions, owners, t.TempDir(), allLiveLiveness(fixture76973ProcStart), clock, budget)

	done := make(chan ipeers.Envelope, 1)
	start := time.Now()
	go func() { done <- m.localEnvelope(context.Background(), "mlab:abc123", "mlab") }()
	select {
	case env := <-done:
		if elapsed := time.Since(start); elapsed > budget+time.Second {
			t.Errorf("localEnvelope took %v, want about the %v budget", elapsed, budget)
		}
		if env.OK {
			t.Errorf("ok = true, want false for a list that hit the budget")
		}
		if !strings.Contains(env.Error, context.DeadlineExceeded.Error()) {
			t.Errorf("error = %q, want the list's deadline error", env.Error)
		}
		if sessions.listCalls.Load() != 1 {
			t.Errorf("list calls = %d, want 1", sessions.listCalls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("localEnvelope never returned: the session list is not bounded by the budget")
	}
}

// TestHandlePeers_TmuxInstanceUnknown_ProceedsNormally is the companion case:
// when either sample is "" (unknown), the mismatch check cannot fire — the
// handler proceeds exactly as before this change.
func TestHandlePeers_TmuxInstanceUnknown_ProceedsNormally(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{
		sessions:  []session.SessionInfo{{Code: "s1", Name: "s1"}},
		instances: []string{"", "x"},
	}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"s1": {AgentType: "cc", SessionID: "sess-1"},
	}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.OK {
		t.Fatalf("ok = false, want true; error=%q", got.Error)
	}
}

func TestHandlePeers_SoftBudget(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(1000, 0)

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "s1", Name: "s1", Cwd: "/a"},
		{Code: "s2", Name: "s2", Cwd: "/b"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"s1": {AgentType: "cc", SessionID: "sess-1", Status: "idle"},
		"s2": {AgentType: "cc", SessionID: "sess-2", Status: "idle"},
	}}
	// call1: handler start -> t0 (deadline = t0+2s)
	// call2: s1's expiry check -> t0+1s (before deadline -> resolved)
	// call3: s2's expiry check -> t0+3s (>= deadline -> expired)
	clock := &fakeClock{times: []time.Time{t0, t0.Add(1 * time.Second), t0.Add(3 * time.Second)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.OK {
		t.Fatalf("ok = false, want true; error=%q", got.Error)
	}
	if !got.Partial {
		t.Errorf("partial = false, want true")
	}

	if len(owners.calls) != 1 || owners.calls[0] != "s1" {
		t.Fatalf("resolver calls = %v, want exactly [s1]", owners.calls)
	}

	var s2Rec *ipeers.PeerRecord
	for i := range got.Peers {
		if got.Peers[i].SessionCode == "s2" {
			s2Rec = &got.Peers[i]
		}
	}
	if s2Rec == nil {
		t.Fatalf("s2 record not found: %+v", got.Peers)
	}
	if s2Rec.Agent != nil {
		t.Errorf("s2 Agent = %+v, want nil", s2Rec.Agent)
	}
	if s2Rec.Reason != "" {
		t.Errorf("s2 Reason = %q, want empty", s2Rec.Reason)
	}
}

func TestHandlePeers_BudgetConsumedBeforeAnySession(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(2000, 0)

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "s1", Name: "s1"},
		{Code: "s2", Name: "s2"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"s1": {AgentType: "cc", SessionID: "sess-1"},
		"s2": {AgentType: "cc", SessionID: "sess-2"},
	}}
	// call1: handler start -> t0 (deadline = t0+2s)
	// call2: s1 check -> t0+3s (expired)
	// call3: s2 check -> repeats t0+3s (expired) since sequence is exhausted
	clock := &fakeClock{times: []time.Time{t0, t0.Add(3 * time.Second)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.Partial {
		t.Errorf("partial = false, want true")
	}
	if len(owners.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none", owners.calls)
	}
}

func TestHandlePeers_BoundaryExactlyExpired(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(3000, 0)

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "s1", Name: "s1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"s1": {AgentType: "cc", SessionID: "sess-1"},
	}}
	// call1: handler start -> t0 (deadline = t0+2s)
	// call2: s1 check -> exactly t0+2s -> treated as expired (!Before ==
	// true when equal)
	clock := &fakeClock{times: []time.Time{t0, t0.Add(2 * time.Second)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.Partial {
		t.Errorf("partial = false, want true (boundary must count as expired)")
	}
	if len(owners.calls) != 0 {
		t.Fatalf("resolver calls = %v, want none (boundary must count as expired)", owners.calls)
	}
}

func TestHandlePeers_ProviderError(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{err: errFakeProvider}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"peers":[]`) {
		t.Fatalf("body does not contain literal \"peers\":[]; body=%s", rr.Body.String())
	}

	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got.OK {
		t.Errorf("ok = true, want false")
	}
	if got.Error == "" {
		t.Errorf("error = %q, want non-empty", got.Error)
	}
	if got.Peers == nil || len(got.Peers) != 0 {
		t.Errorf("peers = %+v, want empty non-nil slice", got.Peers)
	}
}

// A missing Claude Code registry dir is not an error: it is treated as an
// empty registry, so the handler still returns ok:true with every session
// resolved as best it can without any live registry entries (no_agent /
// not_cc, per whatever the fixture's owners yield).
func TestHandlePeers_RegistryDirMissing(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")
	sessions := &fakeSessions{sessions: []session.SessionInfo{{Code: "s1", Name: "s1"}}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, missingDir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.OK {
		t.Fatalf("ok = false, want true; error=%q", got.Error)
	}
	if len(got.Peers) != 1 {
		t.Fatalf("peers = %+v, want 1", got.Peers)
	}
	// s1 has no owner in this fixture's owners map, so it resolves to a
	// plain no_agent shell row — no live registry entries exist either way.
	if got.Peers[0].Reason != "no_agent" {
		t.Errorf("peers[0].Reason = %q, want no_agent", got.Peers[0].Reason)
	}
	if got.Peers[0].Deliverable {
		t.Errorf("peers[0].Deliverable = true, want false")
	}
}

// registryDir pointing at a regular file is a genuine listing error, unlike
// a missing dir, and still produces ok:false.
func TestHandlePeers_RegistryDirIsRegularFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	sessions := &fakeSessions{sessions: []session.SessionInfo{{Code: "s1", Name: "s1"}}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, filePath, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if got.OK {
		t.Errorf("ok = true, want false")
	}
	if len(got.Peers) != 0 {
		t.Errorf("peers = %+v, want empty", got.Peers)
	}
}

// TestHandlePeers_ScopeAllUnknownPrincipal_Forbidden pins the "no principal
// at all" case (e.g. PeerAuth not mounted, or a bug upstream): scope=all
// must still refuse rather than silently defaulting to a wide-open fan-out.
func TestHandlePeers_ScopeAllUnknownPrincipal_Forbidden(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers?scope=all")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandlePeers_ScopeAllHostPrincipal_Forbidden pins the defence-in-depth
// 403 for a host principal: HostRoutePolicy already refuses scope=all
// upstream in PeerAuth, but the handler must not trust that alone.
func TestHandlePeers_ScopeAllHostPrincipal_Forbidden(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{
		Kind: middleware.PrincipalHost, Alias: "peer-a", HostID: "peer-a:1",
	})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandlePeers_ScopeAll_UnknownScope_BadRequest pins the "any other
// scope" 400, unrelated to admin/host distinctions.
func TestHandlePeers_ScopeAll_UnknownScopeRejected(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=bogus", ctx)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandlePeers_ScopeAll_FanOut is the main fan-out case: an admin
// principal, two configured hosts (one healthy, one closed/unreachable),
// exercised against a real *http.Client (client/fetch left at their
// production defaults from newTestModule) via httptest servers. It pins
// three rows in config order — local, then host-a (healthy), then host-b
// (closed) — with local's peers coming from the usual fake session/owner
// wiring.
func TestHandlePeers_ScopeAll_FanOut(t *testing.T) {
	dir := t.TempDir()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-a" {
			t.Errorf("host-a request Authorization = %q, want Bearer tok-a", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID:  "host-a:111",
			OK:      true,
			Partial: false,
			Peers: []ipeers.PeerRecord{
				{SessionCode: "remote-1"},
			},
		})
	}))
	defer healthy.Close()

	closedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closedSrv.URL
	closedSrv.Close() // connection refused for any request against closedURL

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "local-1", Name: "local-1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: healthy.URL, Token: "tok-a", HostID: "host-a:111"},
		{Alias: "host-b", URL: closedURL, Token: "tok-b", HostID: "host-b:222"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 3 {
		t.Fatalf("hosts = %+v, want 3 rows", got.Hosts)
	}

	local := got.Hosts[0]
	if local.Alias != "mlab" || local.HostID != "mlab:abc123" {
		t.Errorf("local row = %+v, want alias=mlab host_id=mlab:abc123", local)
	}
	if !local.OK || len(local.Peers) != 1 || local.Peers[0].SessionCode != "local-1" {
		t.Errorf("local row = %+v, want ok=true with local-1", local)
	}

	hostA := got.Hosts[1]
	if hostA.Alias != "host-a" || hostA.HostID != "host-a:111" {
		t.Errorf("host-a row = %+v, want alias=host-a host_id=host-a:111", hostA)
	}
	if !hostA.OK {
		t.Errorf("host-a row = %+v, want ok=true", hostA)
	}
	if len(hostA.Peers) != 1 || hostA.Peers[0].SessionCode != "remote-1" {
		t.Errorf("host-a peers = %+v, want [remote-1]", hostA.Peers)
	}

	hostB := got.Hosts[2]
	if hostB.Alias != "host-b" || hostB.HostID != "host-b:222" {
		t.Errorf("host-b row = %+v, want alias=host-b host_id=host-b:222", hostB)
	}
	if hostB.OK {
		t.Errorf("host-b row = %+v, want ok=false (closed server)", hostB)
	}
	if hostB.Error == "" {
		t.Errorf("host-b row error = %q, want non-empty", hostB.Error)
	}
	if hostB.Peers == nil || len(hostB.Peers) != 0 {
		t.Errorf("host-b peers = %+v, want empty non-nil slice", hostB.Peers)
	}
}

// TestHandlePeers_ScopeAll_HostIDMismatch pins the host_id verification: a
// remote whose reported host_id differs from the configured (non-empty)
// HostID is reported as a failed row, not silently trusted.
func TestHandlePeers_ScopeAll_HostIDMismatch(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID:  "actually-different:999",
			OK:      true,
			Partial: false,
			Peers:   []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: "expected:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false", row)
	}
	wantErr := "host_id mismatch: got actually-different:999"
	if row.Error != wantErr {
		t.Errorf("row.Error = %q, want %q", row.Error, wantErr)
	}
	if row.HostID != "expected:111" {
		t.Errorf("row.HostID = %q, want configured value %q", row.HostID, "expected:111")
	}
}

// TestHandlePeers_ScopeAll_HostIDMismatchBounded pins Item 3's bounding of
// the remote-supplied host_id embedded in the "host_id mismatch: got <x>"
// row error: a 300-byte remote host_id must not appear in full.
func TestHandlePeers_ScopeAll_HostIDMismatchBounded(t *testing.T) {
	dir := t.TempDir()
	longHostID := strings.Repeat("y", 300)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID:  longHostID,
			OK:      true,
			Partial: false,
			Peers:   []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: "expected:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false", row)
	}
	if !strings.HasPrefix(row.Error, "host_id mismatch: got ") {
		t.Errorf("row.Error = %q, want prefix %q", row.Error, "host_id mismatch: got ")
	}
	if strings.Contains(row.Error, longHostID) {
		t.Errorf("row.Error contains the full 300-byte remote host_id unbounded: %q", row.Error)
	}
	if !strings.HasSuffix(row.Error, "…") {
		t.Errorf("row.Error = %q, want to end with an ellipsis", row.Error)
	}
	if len(row.Error) > 230 {
		t.Errorf("row.Error length = %d bytes, want bounded; error=%q", len(row.Error), row.Error)
	}
}

// TestHandlePeers_ScopeAll_UnpairedInvalidLearnedHostID_FailureRow pins
// Item 5: when the configured host entry has no HostID yet (unpaired),
// fetchHostResult falls back to the remote's own reported env.HostID —
// attacker-controlled — and must validate it with validHostID before
// trusting it, rather than merely bounding/truncating an otherwise-invalid
// value into an ok=true row. A 300-byte remote host_id fails validHostID
// (over the 128-byte limit), so the row must be a bounded failure instead.
func TestHandlePeers_ScopeAll_UnpairedInvalidLearnedHostID_FailureRow(t *testing.T) {
	dir := t.TempDir()
	longHostID := strings.Repeat("q", 300)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID:  longHostID,
			OK:      true,
			Partial: false,
			Peers:   []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	// HostID left empty: this host is unpaired/unverified, so
	// fetchHostResult must fall back to (and validate) the remote's own
	// reported value.
	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: ""},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false (invalid learned host_id)", row)
	}
	if row.Error != "peer returned an invalid host_id" {
		t.Errorf("row.Error = %q, want %q", row.Error, "peer returned an invalid host_id")
	}
	if row.Peers == nil || len(row.Peers) != 0 {
		t.Errorf("row.Peers = %+v, want empty non-nil slice", row.Peers)
	}
}

// TestHandlePeers_ScopeAll_RemoteErrorBounded pins Item 3's bounding of a
// remote peer's own reported Error text as it flows into a scope=all row:
// prefixed "peer: " and truncated, mirroring the 502 body verifyHost
// produces for the add/put paths.
func TestHandlePeers_ScopeAll_RemoteErrorBounded(t *testing.T) {
	dir := t.TempDir()
	longErr := strings.Repeat("z", 300)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID:  "host-a:111",
			OK:      false,
			Error:   longErr,
			Partial: false,
			Peers:   []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: "host-a:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false", row)
	}
	if !strings.HasPrefix(row.Error, "peer: ") {
		t.Errorf("row.Error = %q, want prefix %q", row.Error, "peer: ")
	}
	if !strings.HasSuffix(row.Error, "…") {
		t.Errorf("row.Error = %q, want to end with an ellipsis", row.Error)
	}
	if len(row.Error) > 210 {
		t.Errorf("row.Error length = %d bytes, want <= 210; error=%q", len(row.Error), row.Error)
	}
}

// TestHandlePeers_ScopeAll_PeerEchoesOurTokenIsRedacted (#1152): a peer
// that answers with OUR outbound token inside any of its free-text fields
// must not get that token into a row — the row is what the CLI prints, the
// verify route returns and the Peers page renders.
func TestHandlePeers_ScopeAll_PeerEchoesOurTokenIsRedacted(t *testing.T) {
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "host-a:111", OK: false, Error: "you sent " + tok + " to me",
			Alias: "self-" + tok, DaemonVersion: "v-" + tok,
			UnknownRegistryFiles: []string{"/tmp/" + tok},
			Peers: []ipeers.PeerRecord{{
				RowKind: "session", SessionCode: "s1", SessionName: "sess-" + tok, Title: "t-" + tok, Cwd: "/w/" + tok,
				Agent: &ipeers.AgentInfo{Type: "cc", PeerName: "pn-" + tok, Version: "1"},
			}},
		})
	}))
	defer srv.Close()
	hosts := []config.PeerHost{{Alias: "host-a", URL: srv.URL, Token: tok, HostID: "host-a:111"}}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, &fakeSessions{}, &fakeOwners{owners: map[string]agent.PaneOwner{}}, "", ipeers.DefaultLiveness(), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, 2*time.Second)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers?scope=all", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if s := rr.Body.String(); strings.Contains(s, tok) {
		t.Fatalf("outbound token echoed into the aggregate: %s", s)
	}
	if !strings.Contains(rr.Body.String(), "[redacted]") {
		t.Fatalf("expected a [redacted] marker; body=%s", rr.Body.String())
	}
}

// TestHandlePeers_ScopeAll_LearnedHostIDRedacted (#1152 fix round 1): an
// unpaired host's top-level HostID in a scope=all row falls back to the
// peer's own reported host_id, which can carry our outbound token and
// still pass validHostID's shape check. Mirrors
// TestHandleVerifyHost_LearnedHostIDRedacted through the scope=all path.
func TestHandlePeers_ScopeAll_LearnedHostIDRedacted(t *testing.T) {
	const tok = "pdxp_deadbeefdeadbeefdeadbeefdeadbeef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{HostID: tok, OK: true, Peers: []ipeers.PeerRecord{}})
	}))
	defer srv.Close()
	hosts := []config.PeerHost{{Alias: "host-a", URL: srv.URL, Token: tok, HostID: ""}}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, &fakeSessions{}, &fakeOwners{owners: map[string]agent.PaneOwner{}}, "", ipeers.DefaultLiveness(), &fakeClock{times: []time.Time{time.Unix(0, 0)}}, 2*time.Second)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers?scope=all", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), tok) {
		t.Fatalf("outbound token echoed into the aggregate host_id: %s", rr.Body.String())
	}
}

// TestHandlePeers_ScopeAll_RemoteNotOKWithoutText pins spec §4.1: a peer
// that answers ok=false with NO error text must still produce a row whose
// Error names the cause. Before this, the row copied env.Error verbatim and
// came back as {ok:false, error:""} — and the verify route (hosts_verify.go)
// reuses this function, so the page would have shown a red row with no
// reason. verifyHost already used this exact string for the add/put 502.
func TestHandlePeers_ScopeAll_RemoteNotOKWithoutText(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "host-a:111",
			OK:     false,
			Error:  "",
			Peers:  []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: "host-a:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false", row)
	}
	if row.Error != "peer reported ok=false" {
		t.Errorf("row.Error = %q, want %q", row.Error, "peer reported ok=false")
	}
}

// TestHandlePeers_ScopeAll_NoOutboundToken pins the no-token row: a
// configured host without an outbound Token is listed as a failed row
// without ever being dialed.
func TestHandlePeers_ScopeAll_NoOutboundToken(t *testing.T) {
	dir := t.TempDir()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "host-a", URL: "http://127.0.0.1:1", Token: "", HostID: "host-a:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if row.OK {
		t.Errorf("row = %+v, want ok=false", row)
	}
	if row.Error != "no outbound token" {
		t.Errorf("row.Error = %q, want %q", row.Error, "no outbound token")
	}
}

// TestNormalizeRemoteRows pins Item 2 at its v4 width: every row of a remote
// host's fan-out response is rewritten into this host's local view — Host
// becomes the configured alias, HostID the caller-supplied (verified/bounded)
// value, and Address is RECOMPUTED from the row's own validated fields in the
// same order applyIdentity uses locally.
//
// The remote's own address body is not read at all. It used to be: everything
// after the first "/" was kept and re-prefixed with the local alias, so a
// paired peer — which the module's own comments call attacker-controlled —
// chose what this host printed in its ADDRESS column.
//
// A row that yields none of the three forms is blanked rather than left
// holding an unusable value; Host and HostID stay set either way.
func TestNormalizeRemoteRows(t *testing.T) {
	liveCC := func(name string) *ipeers.AgentInfo {
		return &ipeers.AgentInfo{Type: "cc", PID: 4242, PeerName: name, SessionID: "sid", Version: "2.1"}
	}
	cases := []struct {
		name     string
		row      ipeers.PeerRecord
		alias    string
		hostID   string
		wantAddr string
	}{
		{
			name:     "a live cc row with a routable name takes the name form",
			row:      ipeers.PeerRecord{Address: "laptop/purdex-b0", Ref: "_q34psn", Agent: liveCC("purdex-b0")},
			alias:    "air",
			hostID:   "air:111",
			wantAddr: "air/purdex-b0",
		},
		{
			name:     "a live cc row whose name is unroutable falls back to its ref",
			row:      ipeers.PeerRecord{Address: "laptop/trusted:ops", Ref: "_q34psn", Agent: liveCC("trusted:ops")},
			alias:    "air",
			hostID:   "air:111",
			wantAddr: "air/_q34psn",
		},
		{
			name:     "a session row with no live cc agent takes the tmux form from SessionName",
			row:      ipeers.PeerRecord{Address: "laptop/tmux:mt1", RowKind: "session", SessionName: "mt1"},
			alias:    "air",
			hostID:   "air:111",
			wantAddr: "air/tmux:mt1",
		},
		{
			name:     "remote alias equal to the local alias still maps to h.Alias",
			row:      ipeers.PeerRecord{Address: "mlab/tmux:mt1", RowKind: "session", SessionName: "mt1"},
			alias:    "mlab",
			hostID:   "mlab-remote:1",
			wantAddr: "mlab/tmux:mt1",
		},
		{
			name:     "a proxy row yields no form and is blanked",
			row:      ipeers.PeerRecord{Address: "laptop/cc:helper-1", RowKind: "entry", Agent: &ipeers.AgentInfo{Type: "proxy", PID: 9}},
			alias:    "air",
			hostID:   "air:111",
			wantAddr: "",
		},
		{
			name:     "an owner-fallback row with no name and no valid ref is blanked",
			row:      ipeers.PeerRecord{Address: "laptop/_q34psn", Agent: &ipeers.AgentInfo{Type: "cc", SessionID: "sid"}},
			alias:    "air",
			hostID:   "air:111",
			wantAddr: "",
		},
		{
			name:     "a tmux name carrying a slash cannot parse and is blanked",
			row:      ipeers.PeerRecord{Address: "laptop/tmux:a-b", RowKind: "session", SessionName: "a/b"},
			alias:    "b",
			hostID:   "b:1",
			wantAddr: "",
		},
		{
			name:     "a row with nothing to derive from is blanked",
			row:      ipeers.PeerRecord{Address: "malformed"},
			alias:    "air",
			hostID:   "air:111",
			wantAddr: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeRemoteRows([]ipeers.PeerRecord{tc.row}, tc.alias, tc.hostID)
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			if got[0].Host != tc.alias {
				t.Errorf("Host = %q, want %q", got[0].Host, tc.alias)
			}
			if got[0].HostID != tc.hostID {
				t.Errorf("HostID = %q, want %q", got[0].HostID, tc.hostID)
			}
			if got[0].Address != tc.wantAddr {
				t.Errorf("Address = %q, want %q", got[0].Address, tc.wantAddr)
			}
		})
	}
}

// TestNormalizeRemoteRows_HostileEnvelope is the security property behind the
// recompute: a paired peer is attacker-controlled (this module's own comments
// say so about its error text and its host_id), so the string this host prints
// in its ADDRESS column must be one this host derived, not one the remote
// chose.
//
// The attack the old code allowed: publish peer_name "trusted:ops" and address
// "air/trusted:ops", and `pdx peers --all` renders "air/trusted:ops [q34psn]"
// — a pasteable address for a name §5.2 says can never be one. Every name
// below is unroutable, so every row must come back addressed by its ref, and
// the offered name must appear in no address at all.
func TestNormalizeRemoteRows_HostileEnvelope(t *testing.T) {
	for _, name := range []string{
		"trusted:ops",     // the address grammar's own separator
		"trusted ops",     // a space: the combined form splits on it
		"trusted[q34psn]", // brackets: it would forge the combined form
		"trusted\x07ops",  // a control character
		"q34psn",          // ref-shaped: it would shadow another row's ref
		"has/slash",       // a second '/': it would not even parse
		"",                // no name offered at all
	} {
		row := ipeers.PeerRecord{
			Host: "laptop", HostID: "laptop:1",
			Address: "laptop/" + name, RowKind: "entry", SessionName: name,
			Ref:   "_q34psn",
			Agent: &ipeers.AgentInfo{Type: "cc", PID: 7, PeerName: name, SessionID: "sid"},
		}
		got := normalizeRemoteRows([]ipeers.PeerRecord{row}, "air", "air:111")[0]
		if got.Address != "air/_q34psn" {
			t.Errorf("peer_name %q: Address = %q, want air/_q34psn", name, got.Address)
		}
		if got.Address == "air/"+name {
			t.Errorf("peer_name %q: the remote's name became the address %q", name, got.Address)
		}
		// The name itself is still carried, for the NAME column to show
		// (sanitized there). Refusing to route on it is the whole fix; hiding
		// it would only make the row unidentifiable.
		if got.Agent.PeerName != name {
			t.Errorf("peer_name %q: dropped from the row (%q)", name, got.Agent.PeerName)
		}
	}
}

// A remote that offers a ref of its own invention gets no address from it
// either: a ref is validated by grammar before it is printed as one.
func TestNormalizeRemoteRows_RefMustBeWellFormed(t *testing.T) {
	for _, ref := range []string{"q34psn", "_TOOLONG", "_q34ps", "_q34psn:x", "_q3/psn", "notaref"} {
		row := ipeers.PeerRecord{
			Address: "laptop/" + ref, RowKind: "entry", Ref: ref,
			Agent: &ipeers.AgentInfo{Type: "cc", PID: 7, PeerName: "has/slash", SessionID: "sid"},
		}
		if got := normalizeRemoteRows([]ipeers.PeerRecord{row}, "air", "air:111")[0]; got.Address != "" {
			t.Errorf("ref %q: Address = %q, want it blanked", ref, got.Address)
		}
	}
}

// TestNormalizeRemoteRows_NilRowsReturnsNonNilEmpty guards the fan-out
// row-shape invariant fetchHostResult relies on: a "peers" list is always
// a non-nil (possibly empty) slice, never null on the wire.
func TestNormalizeRemoteRows_NilRowsReturnsNonNilEmpty(t *testing.T) {
	got := normalizeRemoteRows(nil, "air", "air:1")
	if got == nil {
		t.Errorf("got nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// TestHandlePeers_ScopeAll_RemoteRowsNormalizedToLocalAlias is the
// integration proof that fetchHostResult actually wires normalizeRemoteRows
// in: a remote reporting its own alias ("laptop") and host_id must have
// its rows rewritten under how THIS host has the peer configured ("air"
// with the verified host_id), not the remote's self-reported values.
//
// The two rows are the v4 shapes a real remote emits — a live cc entry with
// a routable name, and a session row with no agent — because the address is
// now derived from those fields rather than copied out of what the remote
// published.
func TestHandlePeers_ScopeAll_RemoteRowsNormalizedToLocalAlias(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID:  "air:111",
			OK:      true,
			Partial: false,
			Peers: []ipeers.PeerRecord{
				{
					Host: "laptop", HostID: "air:111", Address: "laptop/purdex-b0", SessionCode: "remote-1",
					RowKind: "session", SessionName: "mt1", Ref: "_q34psn", Deliverable: true,
					Agent: &ipeers.AgentInfo{Type: "cc", PID: 4242, PeerName: "purdex-b0", SessionID: "sid-1"},
				},
				{Host: "laptop", HostID: "air:111", Address: "laptop/tmux:mt2", SessionCode: "remote-2", RowKind: "session", SessionName: "mt2"},
			},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "air", URL: srv.URL, Token: "tok-a", HostID: "air:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if !row.OK || len(row.Peers) != 2 {
		t.Fatalf("row = %+v, want ok=true with 2 peers", row)
	}
	if row.Peers[0].Host != "air" || row.Peers[0].Address != "air/purdex-b0" {
		t.Errorf("peers[0] = %+v, want Host=air Address=air/purdex-b0", row.Peers[0])
	}
	if row.Peers[1].Host != "air" || row.Peers[1].Address != "air/tmux:mt2" {
		t.Errorf("peers[1] = %+v, want Host=air Address=air/tmux:mt2", row.Peers[1])
	}
	if row.Peers[0].HostID != "air:111" || row.Peers[1].HostID != "air:111" {
		t.Errorf("peers = %+v, want HostID=air:111 on every row", row.Peers)
	}
}

// TestHandlePeers_ScopeAll_RemoteDaemonVersionBounded (F3) pins that
// fetchHostResult bounds a remote host's self-reported daemon_version the
// same way it already bounds env.Error and env.UnknownRegistryFiles: that
// field is exactly as attacker-controlled as the other two, and until this
// fix it passed through unbounded.
func TestHandlePeers_ScopeAll_RemoteDaemonVersionBounded(t *testing.T) {
	dir := t.TempDir()

	hugeVersion := strings.Repeat("v", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "air:111", OK: true, DaemonVersion: hugeVersion, Peers: []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "air", URL: srv.URL, Token: "tok-a", HostID: "air:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	row := got.Hosts[1]
	if len(row.DaemonVersion) >= len(hugeVersion) {
		t.Errorf("daemon_version len = %d, want bounded well below the remote's %d-byte report", len(row.DaemonVersion), len(hugeVersion))
	}
	if !strings.HasSuffix(row.DaemonVersion, "…") {
		t.Errorf("daemon_version = %q, want the truncation marker", row.DaemonVersion)
	}
}

// TestHandlePeers_ScopeAll_OversizedHostRowCapped pins the per-host
// aggregated size cap in fan-out: a single misbehaving/malicious remote
// host returning a huge inventory (here, one record with a 3 MiB cwd) must
// not be allowed to inflate the whole scope=all response — its row is
// replaced with a bounded failure row, while the local row and every other
// healthy host's row are left intact, and the total response stays well
// under the CLI's overall response cap.
func TestHandlePeers_ScopeAll_OversizedHostRowCapped(t *testing.T) {
	dir := t.TempDir()

	hugeCwd := strings.Repeat("<", 3*1024*1024)
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// SetEscapeHTML(false): the stock encoder HTML-escapes every "<" to
		// "<" (a 6x blowup), which would push this 3 MiB payload past
		// fetchRemote's own 16 MiB wire cap before it ever reaches the
		// per-host row cap this test targets.
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.Encode(ipeers.Envelope{
			HostID: "big:111",
			OK:     true,
			Peers: []ipeers.PeerRecord{
				{Address: "big/mt1", SessionCode: "big-1", Cwd: hugeCwd},
			},
		})
	}))
	defer oversized.Close()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "small:222",
			OK:     true,
			Peers: []ipeers.PeerRecord{
				{Address: "small/mt1", SessionCode: "small-1"},
			},
		})
	}))
	defer healthy.Close()

	sessions := &fakeSessions{sessions: []session.SessionInfo{{Code: "local-1", Name: "local-1"}}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "big", URL: oversized.URL, Token: "tok-big", HostID: "big:111"},
		{Alias: "small", URL: healthy.URL, Token: "tok-small", HostID: "small:222"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	if got := rr.Body.Len(); got > 2*1024*1024 {
		t.Fatalf("response body = %d bytes, want < 2 MiB", got)
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Hosts) != 3 {
		t.Fatalf("hosts = %+v, want 3 rows", got.Hosts)
	}

	local := got.Hosts[0]
	if local.Alias != "mlab" || !local.OK || len(local.Peers) != 1 || local.Peers[0].SessionCode != "local-1" {
		t.Errorf("local row = %+v, want intact with local-1", local)
	}

	bigRow := got.Hosts[1]
	if bigRow.OK {
		t.Errorf("big row = %+v, want ok=false (oversized)", bigRow)
	}
	if bigRow.Peers == nil || len(bigRow.Peers) != 0 {
		t.Errorf("big row peers = %+v, want empty non-nil slice", bigRow.Peers)
	}
	if !strings.Contains(bigRow.Error, "too large") {
		t.Errorf("big row error = %q, want mention of \"too large\"", bigRow.Error)
	}

	smallRow := got.Hosts[2]
	if smallRow.Alias != "small" || !smallRow.OK || len(smallRow.Peers) != 1 || smallRow.Peers[0].SessionCode != "small-1" {
		t.Errorf("small row = %+v, want intact with small-1", smallRow)
	}
}

// TestAllEnvelope_OverlapsRemoteFetchWithLocalInventory pins Item 4: the
// per-host remote fetches must start before (and run concurrently with)
// the local inventory build, not after it. Rather than bounding wall-clock
// elapsed time (flaky on a shared, possibly loaded machine, and worse
// still under -race), this records the instant each slow path actually
// began — remoteStart inside the httptest handler, localStart inside the
// fake owner resolver, both before their own 100ms sleep — and asserts
// the two started within 50ms of each other. A sequential implementation
// (local fully built, then remote fetches launched) would start them
// ~100ms apart; overlapped, they start together. A loose <1s bound on
// total elapsed time is kept only as a sanity check that the request
// actually completed and didn't hang.
func TestAllEnvelope_OverlapsRemoteFetchWithLocalInventory(t *testing.T) {
	dir := t.TempDir()
	const slowness = 100 * time.Millisecond

	var mu sync.Mutex
	var localStart, remoteStart time.Time

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if remoteStart.IsZero() {
			remoteStart = time.Now()
		}
		mu.Unlock()
		time.Sleep(slowness)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{HostID: "host-a:1", OK: true, Peers: []ipeers.PeerRecord{}})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: []session.SessionInfo{{Code: "s1", Name: "s1"}}}
	owners := &fakeOwners{
		owners: map[string]agent.PaneOwner{},
		delay:  slowness,
		onResolveStart: func() {
			mu.Lock()
			if localStart.IsZero() {
				localStart = time.Now()
			}
			mu.Unlock()
		},
	}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: "host-a:1"}}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	start := time.Now()
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 || !got.Hosts[1].OK {
		t.Fatalf("hosts = %+v, want 2 rows with host-a ok", got.Hosts)
	}

	mu.Lock()
	ls, rs := localStart, remoteStart
	mu.Unlock()
	if ls.IsZero() || rs.IsZero() {
		t.Fatalf("localStart=%v remoteStart=%v, want both recorded", ls, rs)
	}
	diff := ls.Sub(rs)
	if diff < 0 {
		diff = -diff
	}
	if diff >= 50*time.Millisecond {
		t.Errorf("|localStart - remoteStart| = %v, want < 50ms (the local inventory and the remote fetch should start together, not sequentially)", diff)
	}
	if elapsed >= time.Second {
		t.Errorf("elapsed = %v, want < 1s (sanity check: the request should complete promptly)", elapsed)
	}
}

// TestLocalEnvelope_UsesCallerSnapshot_NotLiveConfig pins the fix for the
// review finding: localEnvelope must build every PeerRecord from the
// hostID/alias the caller passes in, never by re-reading m.core.Cfg under
// its own CfgMu.RLock. The module's live config says alias "y"; calling
// localEnvelope directly with alias "x" must produce records whose Host is
// "x", not "y" — proving localEnvelope does not touch CfgMu at all (a
// concurrent config mutation between the handler's snapshot and this call
// would otherwise be observable as a live-config value leaking in).
func TestLocalEnvelope_UsesCallerSnapshot_NotLiveConfig(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "s1", Name: "s1", Cwd: "/a"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "live-host-id", "y") // live config: alias "y"
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	env := m.localEnvelope(context.Background(), "snapshot-host-id", "x")

	if !env.OK {
		t.Fatalf("ok = false, want true; error=%q", env.Error)
	}
	if env.HostID != "snapshot-host-id" {
		t.Errorf("env.HostID = %q, want the passed-in snapshot value %q", env.HostID, "snapshot-host-id")
	}
	if len(env.Peers) == 0 {
		t.Fatalf("peers empty, want at least one record")
	}
	for _, rec := range env.Peers {
		if rec.Host != "x" {
			t.Errorf("record.Host = %q, want snapshot alias %q (live config alias %q must not leak in)", rec.Host, "x", "y")
		}
		if rec.HostID != "snapshot-host-id" {
			t.Errorf("record.HostID = %q, want snapshot host_id %q", rec.HostID, "snapshot-host-id")
		}
	}
}

// TestLocalEnvelope_PublishesOwnAlias pins Phase C's premise (spec §7): the
// envelope says what this host calls itself, so a peer pairing with it can
// adopt that name instead of inventing a local one. Like HostID, the value
// comes from the caller's config snapshot, never from a second read of the
// live config — the module's live alias here is "y" and must not leak in.
func TestLocalEnvelope_PublishesOwnAlias(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "s1", Name: "s1", Cwd: "/a"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "live-host-id", "y")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	env := m.localEnvelope(context.Background(), "snapshot-host-id", "x")

	if !env.OK {
		t.Fatalf("ok = false, want true; error=%q", env.Error)
	}
	if env.Alias != "x" {
		t.Errorf("env.Alias = %q, want the snapshot alias %q (live config alias %q must not leak in)", env.Alias, "x", "y")
	}
}

// TestLocalEnvelope_PublishesOwnAliasOnFailure is the companion: a host
// that cannot build its inventory still knows its own name, and a reader
// that only pairs on ok=true loses nothing by being told it. The alias
// travels with every envelope, not only the successful ones.
func TestLocalEnvelope_PublishesOwnAliasOnFailure(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{err: errors.New("tmux unreachable")}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "live-host-id", "y")
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	env := m.localEnvelope(context.Background(), "snapshot-host-id", "x")

	if env.OK {
		t.Fatalf("ok = true, want false: the session listing fails in this fixture")
	}
	if env.Alias != "x" {
		t.Errorf("env.Alias = %q, want the snapshot alias %q even on a failed inventory", env.Alias, "x")
	}
}

// ---------------------------------------------------------------------------
// Label snapshot join, registry diagnosis, daemon_version (Task 5).
// ---------------------------------------------------------------------------

// newLabelJoinFixture builds a moduleFixture with one tmux session "mt0"
// (code "mt0code") owned by a cc conversation "sid-1" with a live registry
// entry at pid 10 — the common inventory every test below starts from. The
// fixture's registry dir (f.registryDir) is a plain t.TempDir(), so a test
// can drop extra files into it before calling localEnvelope.
func newLabelJoinFixture(t *testing.T) *moduleFixture {
	t.Helper()
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","tmux":"mt0:@1.%1","messagingSocketPath":"/tmp/x/10.sock","name":"purdex-x","status":"idle"}`)

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "mt0code", Name: "mt0", Cwd: "/w", TmuxInstance: "inst1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"mt0code": {AgentType: "cc", SessionID: "sid-1", Cwd: "/w", TmuxPaneID: "%1"},
	}}
	return newTestModuleWith(t, fixtureOpts{
		core:        newTestCore(t, "h:1", "a"),
		sessions:    sessions,
		owners:      owners,
		registryDir: dir,
		liveness:    allLiveLiveness(fixture76973ProcStart),
		budget:      2 * time.Second,
	})
}

// TestLocalEnvelope_LabelsJoinedAndVersion pins localEnvelope's join of the
// label snapshot (Task 3's peer_labels table) into the built rows, and that
// every response carries this daemon's own build version and a never-null
// (here empty) unknown_registry_files list.
func TestLocalEnvelope_LabelsJoinedAndVersion(t *testing.T) {
	f := newLabelJoinFixture(t)
	if _, err := f.titles.Claim("sid-1", "purdex-dev", time.Now()); err != nil {
		t.Fatal(err)
	}

	env := f.m.localEnvelope(context.Background(), "h:1", "a")

	if env.DaemonVersion != buildinfo.Version {
		t.Errorf("daemon_version = %q, want %q", env.DaemonVersion, buildinfo.Version)
	}
	if env.UnknownRegistryFiles == nil || len(env.UnknownRegistryFiles) != 0 {
		t.Errorf("unknown_registry_files = %#v, want empty non-nil", env.UnknownRegistryFiles)
	}

	var rec *ipeers.PeerRecord
	for i := range env.Peers {
		if env.Peers[i].SessionCode == "mt0code" {
			rec = &env.Peers[i]
		}
	}
	if rec == nil {
		t.Fatalf("mt0 record not found in peers: %+v", env.Peers)
	}
	if rec.Title != "purdex-dev" || rec.TitleSource != ipeers.TitleSourceUser || rec.TitleRev != 1 {
		t.Errorf("row = %+v, want label purdex-dev/user/rev 1", rec)
	}
}

// TestLocalEnvelope_UnknownRegistryFileMarksPartial is Task 4's suggested
// narrow test: an alive-but-undecodable registry file, at a pid distinct
// from the resolved session's own live entry, marks the whole response
// partial and is named in unknown_registry_files — even though the owner
// lookup for the one listed session succeeded outright.
func TestLocalEnvelope_UnknownRegistryFileMarksPartial(t *testing.T) {
	f := newLabelJoinFixture(t)
	writeRegistryFixture(t, f.registryDir, "4242.json", "{")

	env := f.m.localEnvelope(context.Background(), "h:1", "a")

	if !env.Partial {
		t.Fatal("partial = false, want true")
	}
	if len(env.UnknownRegistryFiles) != 1 || !strings.HasSuffix(env.UnknownRegistryFiles[0], "4242.json") {
		t.Errorf("unknown_registry_files = %v", env.UnknownRegistryFiles)
	}

	var rec *ipeers.PeerRecord
	for i := range env.Peers {
		if env.Peers[i].SessionCode == "mt0code" {
			rec = &env.Peers[i]
		}
	}
	if rec == nil || !rec.Deliverable {
		t.Errorf("mt0 record = %+v, want the owner lookup to have resolved and delivered despite the unrelated unknown file", rec)
	}
}

// TestLocalEnvelope_LabelStoreFailureIsPartial pins that a label store
// Snapshot failure marks the response partial (the label column is now
// unknown, so the rows are not the whole truth), signals it explicitly as
// titles_unavailable (X4 — the CLI renders the cause from this flag, never
// by inference from the other partial causes) and logs once, without
// touching UnknownRegistryFiles.
func TestLocalEnvelope_LabelStoreFailureIsPartial(t *testing.T) {
	f := newLabelJoinFixture(t)
	f.m.titles = failingTitles{}

	env := f.m.localEnvelope(context.Background(), "h:1", "a")

	if !env.Partial {
		t.Fatal("partial = false, want true on label store failure")
	}
	if !env.TitlesUnavailable {
		t.Error("titles_unavailable = false, want true on label store failure")
	}
	if len(env.UnknownRegistryFiles) != 0 {
		t.Errorf("unknown_registry_files = %v, want none", env.UnknownRegistryFiles)
	}
	for _, r := range env.Peers {
		if r.Agent != nil && r.Agent.Type == "cc" && (r.Title != "" || r.TitleSource != "") {
			t.Errorf("row %s label/source = %q/%q, want both empty: the store that holds them is unreadable", r.Address, r.Title, r.TitleSource)
		}
	}
	if !f.logs.contains("title store") {
		t.Error("expected one log line about the title store")
	}
}

// TestLocalEnvelope_LabelStoreFailureLeavesAddressesUnchanged is the v3
// inversion of the test that used to live here. Under v2 an unreadable
// label store changed what every row was reachable AT, so localEnvelope
// had to hand Build TitlesUnavailable to suppress the tmux-derived
// defaults. Under D2 the store feeds the label column and nothing else:
// the same fixture must render byte-identical addresses whether the store
// reads or fails, and only the label column goes blank.
func TestLocalEnvelope_LabelStoreFailureLeavesAddressesUnchanged(t *testing.T) {
	healthy := newLabelJoinFixture(t)
	addrs := map[string]string{}
	for _, r := range healthy.m.localEnvelope(context.Background(), "h:1", "a").Peers {
		if r.Agent != nil && r.Agent.Type == "cc" {
			addrs[r.Agent.SessionID] = r.Address
		}
	}
	if len(addrs) == 0 {
		t.Fatal("precondition: the fixture produced no live cc rows")
	}

	f := newLabelJoinFixture(t)
	f.m.titles = failingTitles{}

	env := f.m.localEnvelope(context.Background(), "h:1", "a")

	seen := 0
	for _, r := range env.Peers {
		if r.Agent == nil || r.Agent.Type != "cc" {
			continue
		}
		seen++
		if want := addrs[r.Agent.SessionID]; r.Address != want {
			t.Errorf("row for %s: address = %q with the store down, %q with it up; want identical", r.Agent.SessionID, r.Address, want)
		}
		if r.Ref == "" {
			t.Errorf("row %s: canonical = \"\" with the store down, want the sessionId-derived id", r.Address)
		}
	}
	if seen != len(addrs) {
		t.Errorf("live cc rows = %d with the store down, %d with it up", seen, len(addrs))
	}
}

// TestLocalEnvelope_LabelsAvailableFlagFalseWhenHealthy pins the negative:
// a healthy (or absent) label store never sets titles_unavailable, even
// when the response is partial for another reason.
func TestLocalEnvelope_LabelsAvailableFlagFalseWhenHealthy(t *testing.T) {
	f := newLabelJoinFixture(t)
	writeRegistryFixture(t, f.registryDir, "4242.json", "{") // partial for the registry's sake

	env := f.m.localEnvelope(context.Background(), "h:1", "a")

	if !env.Partial || len(env.UnknownRegistryFiles) != 1 {
		t.Fatalf("partial=%v unknown=%v, want partial with one unknown file", env.Partial, env.UnknownRegistryFiles)
	}
	if env.TitlesUnavailable {
		t.Error("titles_unavailable = true, want false: the label store read succeeded")
	}
}

// TestLocalEnvelope_LabelStoreFailureAndUnknownFile_BothSignalled pins
// that the two partial causes are independent signals: an unreadable
// registry file AND a failing label store are both reported, each in its
// own field, on one partial envelope.
func TestLocalEnvelope_LabelStoreFailureAndUnknownFile_BothSignalled(t *testing.T) {
	f := newLabelJoinFixture(t)
	f.m.titles = failingTitles{}
	writeRegistryFixture(t, f.registryDir, "4242.json", "{")

	env := f.m.localEnvelope(context.Background(), "h:1", "a")

	if !env.Partial {
		t.Fatal("partial = false, want true")
	}
	if !env.TitlesUnavailable {
		t.Error("titles_unavailable = false, want true")
	}
	if len(env.UnknownRegistryFiles) != 1 || !strings.HasSuffix(env.UnknownRegistryFiles[0], "4242.json") {
		t.Errorf("unknown_registry_files = %v, want the one unknown file", env.UnknownRegistryFiles)
	}
}

// TestAllEnvelope_LabelsUnavailableCopiedThrough pins that scope=all
// carries titles_unavailable on both kinds of row: the local row copies
// localEnvelope's flag, and a remote host's row copies the flag the remote
// envelope reported (fetchHostResult), next to its unknown files.
func TestAllEnvelope_LabelsUnavailableCopiedThrough(t *testing.T) {
	f := newLabelJoinFixture(t)
	f.m.titles = failingTitles{}
	f.m.fetch = func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		return ipeers.Envelope{
			HostID: "air:111", OK: true, Partial: true, Peers: []ipeers.PeerRecord{},
			UnknownRegistryFiles: []string{"/reg/9.json"}, TitlesUnavailable: true,
		}, nil
	}
	hosts := []config.PeerHost{{Alias: "air", URL: "http://air.invalid", Token: "tok", HostID: "air:111"}}

	all := f.m.allEnvelope(context.Background(), "h:1", "a", hosts)

	if len(all.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", all.Hosts)
	}
	local, remote := all.Hosts[0], all.Hosts[1]
	if !local.OK || !local.Partial || !local.TitlesUnavailable {
		t.Errorf("local row = ok %v partial %v titles_unavailable %v, want true/true/true", local.OK, local.Partial, local.TitlesUnavailable)
	}
	if !remote.OK || !remote.Partial || !remote.TitlesUnavailable {
		t.Errorf("remote row = ok %v partial %v titles_unavailable %v, want true/true/true", remote.OK, remote.Partial, remote.TitlesUnavailable)
	}
	if len(remote.UnknownRegistryFiles) != 1 || remote.UnknownRegistryFiles[0] != "/reg/9.json" {
		t.Errorf("remote unknown_registry_files = %v, want the reported file alongside titles_unavailable", remote.UnknownRegistryFiles)
	}
}

// failingTitles is a TitleStore whose every method fails: it stands in for
// a label store that is configured but unreachable (a locked/corrupt DB).
type failingTitles struct{}

func (failingTitles) Snapshot() ([]store.PeerLabel, error) { return nil, errors.New("boom") }
func (failingTitles) Claim(string, string, time.Time) (store.PeerLabel, error) {
	return store.PeerLabel{}, errors.New("boom")
}
func (failingTitles) Release(string, time.Time) (store.PeerLabel, bool, error) {
	return store.PeerLabel{}, false, errors.New("boom")
}

// writeFailingTitles reads fine but cannot write (Task 7 uses it for the
// claim/release write-failure rows of the matrix).
type writeFailingTitles struct{ real *store.PeerLabelStore }

func (w writeFailingTitles) Snapshot() ([]store.PeerLabel, error) { return w.real.Snapshot() }
func (writeFailingTitles) Claim(string, string, time.Time) (store.PeerLabel, error) {
	return store.PeerLabel{}, errors.New("disk full")
}
func (writeFailingTitles) Release(string, time.Time) (store.PeerLabel, bool, error) {
	return store.PeerLabel{}, false, errors.New("disk full")
}

func TestInit_MissingSessionProvider(t *testing.T) {
	c := core.New(core.CoreDeps{
		Config:   &config.Config{},
		Registry: core.NewServiceRegistry(),
	})
	m := New(nil, nil)
	err := m.Init(c)
	if err == nil {
		t.Fatalf("Init: want error, got nil")
	}
	if !strings.Contains(err.Error(), session.RegistryKey) {
		t.Errorf("Init error = %q, want it to mention %q", err.Error(), session.RegistryKey)
	}
}

func TestInit_MissingOwnerResolver(t *testing.T) {
	c := core.New(core.CoreDeps{
		Config:   &config.Config{},
		Registry: core.NewServiceRegistry(),
	})
	c.Registry.Register(session.RegistryKey, &fakeSessions{})

	m := New(nil, nil)
	err := m.Init(c)
	if err == nil {
		t.Fatalf("Init: want error, got nil")
	}
	if !strings.Contains(err.Error(), agent.OwnerResolverKey) {
		t.Errorf("Init error = %q, want it to mention %q", err.Error(), agent.OwnerResolverKey)
	}
}

// TestHandlePeers_ScopeAll_CarriesSelfAlias pins spec §7.4's input: the
// fan-out is the one caller that fetches every peer's envelope on each call,
// so it is the one that can hand the CLI a LIVE self-reported name to
// compare against the local one. Alias stays what we have the host
// configured as; SelfAlias is what the host says it is; the two are carried
// side by side and the local Alias is never overwritten by the report.
//
// The local row (Hosts[0]) fills both from the same snapshot alias, so it
// agrees with itself by construction and can never be flagged.
func TestHandlePeers_ScopeAll_CarriesSelfAlias(t *testing.T) {
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "air:111", Alias: "air26", OK: true, Peers: []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "air", URL: srv.URL, Token: "tok-a", HostID: "air:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}

	local := got.Hosts[0]
	if local.SelfAlias != local.Alias || local.Alias != "mlab" {
		t.Errorf("local row alias/self_alias = %q/%q, want both %q", local.Alias, local.SelfAlias, "mlab")
	}

	remote := got.Hosts[1]
	if remote.Alias != "air" {
		t.Errorf("remote alias = %q, want the locally configured %q — the peer's own report must not overwrite it", remote.Alias, "air")
	}
	if remote.SelfAlias != "air26" {
		t.Errorf("remote self_alias = %q, want the peer's own report %q", remote.SelfAlias, "air26")
	}
}

// TestHandlePeers_ScopeAll_RemoteSelfAliasBounded: self_alias is the
// remote's own text, exactly as attacker-controlled as env.Error and
// env.DaemonVersion, and it is bounded on the same terms before it is
// re-encoded or printed.
func TestHandlePeers_ScopeAll_RemoteSelfAliasBounded(t *testing.T) {
	dir := t.TempDir()

	hugeAlias := strings.Repeat("a", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ipeers.Envelope{
			HostID: "air:111", Alias: hugeAlias, OK: true, Peers: []ipeers.PeerRecord{},
		})
	}))
	defer srv.Close()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "air", URL: srv.URL, Token: "tok-a", HostID: "air:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	if n := len(got.Hosts[1].SelfAlias); n >= len(hugeAlias) {
		t.Errorf("self_alias len = %d, want bounded well below the remote's %d-byte report", n, len(hugeAlias))
	}
	if !strings.HasSuffix(got.Hosts[1].SelfAlias, "…") {
		t.Errorf("self_alias = %q, want the truncation marker", got.Hosts[1].SelfAlias)
	}
}

// TestHandlePeers_ScopeAll_UnreachableHostHasNoSelfAlias: a host we never
// reached reported nothing, so its self_alias stays "" — the CLI reads ""
// as "never said", not as "disagrees".
func TestHandlePeers_ScopeAll_UnreachableHostHasNoSelfAlias(t *testing.T) {
	dir := t.TempDir()

	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}

	hosts := []config.PeerHost{
		{Alias: "air", URL: "http://127.0.0.1:1", Token: "", HostID: "air:111"},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(t, c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	ctx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
	rr := doGetPeersWithContext(t, m, "/api/peers?scope=all", ctx)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.AllEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2 rows", got.Hosts)
	}
	if got.Hosts[1].OK {
		t.Fatalf("remote row ok = true, want a failed fetch in this fixture")
	}
	if got.Hosts[1].SelfAlias != "" {
		t.Errorf("self_alias = %q, want \"\" for a host that was never reached", got.Hosts[1].SelfAlias)
	}
}
