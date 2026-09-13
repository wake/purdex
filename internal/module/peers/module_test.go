package peers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
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
	cfg := &config.Config{
		HostID: hostID,
		Peers:  config.PeersConfig{Alias: alias},
	}
	return core.New(core.CoreDeps{
		Config:   cfg,
		Registry: core.NewServiceRegistry(),
	})
}

// newTestModule builds a *Module with the given collaborators wired
// directly (bypassing Init), for handler-level tests.
func newTestModule(c *core.Core, sessions session.SessionProvider, owners agent.OwnerResolver, registryDir string, liveness ipeers.Liveness, clock *fakeClock, budget time.Duration) *Module {
	return &Module{
		core:        c,
		sessions:    sessions,
		owners:      owners,
		registryDir: registryDir,
		liveness:    liveness,
		budget:      budget,
		now:         clock.Now,
	}
}

func doGetPeers(t *testing.T, m *Module, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, target, nil)
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

	var got response
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
	var got response
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
	var got response
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
	var got response
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

	var got response
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
	var got response
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
	var got response
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

func TestHandlePeers_ScopeAllRejected(t *testing.T) {
	dir := t.TempDir()
	sessions := &fakeSessions{sessions: nil}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{}}
	clock := &fakeClock{times: []time.Time{time.Unix(0, 0)}}
	c := newTestCore(t, "mlab:abc123", "mlab")
	m := newTestModule(c, sessions, owners, dir, allLiveLiveness(fixture76973ProcStart), clock, 2*time.Second)

	rr := doGetPeers(t, m, "/api/peers?scope=all")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "scope=all not supported yet") {
		t.Errorf("body = %s, want message about scope=all", rr.Body.String())
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
