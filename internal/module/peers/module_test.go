package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
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
// directly (bypassing Init), for handler-level tests. client/fetch default
// to production values (newRemoteClient/fetchRemote); use
// newTestModuleWithFetch to inject a fake fetch for scope=all tests.
func newTestModule(c *core.Core, sessions session.SessionProvider, owners agent.OwnerResolver, registryDir string, liveness ipeers.Liveness, clock *fakeClock, budget time.Duration) *Module {
	return &Module{
		core:        c,
		sessions:    sessions,
		owners:      owners,
		registryDir: registryDir,
		liveness:    liveness,
		budget:      budget,
		now:         clock.Now,
		client:      newRemoteClient(),
		fetch:       fetchRemote,
	}
}

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, missingDir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, filePath, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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

// TestHandlePeers_ScopeAll_UnpairedLearnedHostIDBounded pins the follow-up
// fix to fetchHostResult's success path: when the configured host entry
// has no HostID yet (unpaired), resultHostID falls back to the remote's
// own reported env.HostID — attacker-controlled, and unbounded before the
// fix. A 300-byte remote host_id must be truncated in the row.
func TestHandlePeers_ScopeAll_UnpairedLearnedHostIDBounded(t *testing.T) {
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
	// fetchHostResult must fall back to the remote's own reported value.
	hosts := []config.PeerHost{
		{Alias: "host-a", URL: srv.URL, Token: "tok-a", HostID: ""},
	}
	c := newTestCoreWithHosts(t, "mlab:abc123", "mlab", hosts)
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	if !row.OK {
		t.Errorf("row = %+v, want ok=true", row)
	}
	if row.HostID == longHostID {
		t.Errorf("row.HostID contains the full 300-byte remote host_id unbounded: %q", row.HostID)
	}
	if !strings.HasSuffix(row.HostID, "…") {
		t.Errorf("row.HostID = %q, want to end with an ellipsis", row.HostID)
	}
	if len(row.HostID) > 210 {
		t.Errorf("row.HostID length = %d bytes, want bounded; host_id=%q", len(row.HostID), row.HostID)
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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

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

func TestInit_MissingSessionProvider(t *testing.T) {
	c := core.New(core.CoreDeps{
		Config:   &config.Config{},
		Registry: core.NewServiceRegistry(),
	})
	m := New()
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

	m := New()
	err := m.Init(c)
	if err == nil {
		t.Fatalf("Init: want error, got nil")
	}
	if !strings.Contains(err.Error(), agent.OwnerResolverKey) {
		t.Errorf("Init error = %q, want it to mention %q", err.Error(), agent.OwnerResolverKey)
	}
}
