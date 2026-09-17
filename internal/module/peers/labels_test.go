// internal/module/peers/labels_test.go
package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
	"github.com/wake/purdex/internal/store"
)

// labelHelperPIDFloor is the first pid proxyhelpertest hands out to a
// spawned fake helper (mirrors e2eFakeHelperPID in e2e_test.go): every pid
// at or above it is a fake helper process, whose registry entry (a real
// helper spawn writes its own <pid>.json — proxyhelper.Run, not the test's
// doing) carries proxyhelpertest.ProcStart(pid), not targetProcStart.
const labelHelperPIDFloor = 900000

// labelLiveness is the Liveness the self-route fixture shares across its
// two fake registry entries: every pid is alive (Stat always succeeds,
// procStart always matches its own entry) unless markDead has been called
// for it, modelled on e2eLiveness (e2e_test.go). StartTime must answer
// each pid's OWN procStart — not one fixed value — or a spawned helper's
// entry (whose file carries proxyhelpertest.ProcStart(pid), a different
// string per pid) is classified confirmed-dead by the mismatch and never
// reaches findOriginEntry at all, making the proxy exclusion untested.
type labelLiveness struct {
	dead sync.Map // pid → struct{}
}

func (l *labelLiveness) markDead(pid int) { l.dead.Store(pid, struct{}{}) }

func (l *labelLiveness) startOf(pid int) time.Time {
	if pid >= labelHelperPIDFloor {
		s, _ := proxyhelpertest.ProcStart(pid)
		ts, _ := ipeers.ParseProcStart(s)
		return ts
	}
	ts, _ := ipeers.ParseProcStart(targetProcStart)
	return ts
}

func (l *labelLiveness) liveness() ipeers.Liveness {
	return ipeers.Liveness{
		Stat: func(path string) error { return nil },
		PidAlive: func(pid int) bool {
			_, dead := l.dead.Load(pid)
			return !dead
		},
		StartTime: func(pid int) (time.Time, error) {
			return l.startOf(pid), nil
		},
	}
}

// labelFixture is the self-route test fixture: a moduleFixture whose
// registry holds two live entries — pid 10, inside tmux session "mt0"
// (registry name "n10", session id "sid-1"), and pid 20 with no tmux, the
// Desktop stand-in (registry name "n20", session id "sid-2").
type labelFixture struct {
	*moduleFixture
	t       *testing.T
	live    *labelLiveness
	inboxes map[int]string
}

func newLabelFixture(t *testing.T) *labelFixture {
	t.Helper()
	dir := t.TempDir()
	inbox10 := dir + "/10.sock"
	inbox20 := dir + "/20.sock"
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","tmux":"mt0:@1.%1","messagingSocketPath":"`+inbox10+`","name":"n10","status":"idle"}`)
	writeRegistryFixture(t, dir, "20.json", `{"pid":20,"sessionId":"sid-2","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","messagingSocketPath":"`+inbox20+`","name":"n20","status":"idle"}`)

	sessions := &fakeSessions{sessions: []session.SessionInfo{
		{Code: "c1", Name: "mt0", Cwd: "/w", TmuxInstance: "inst1"},
	}}
	owners := &fakeOwners{owners: map[string]agent.PaneOwner{
		"c1": {AgentType: "cc", SessionID: "sid-1", Cwd: "/w", TmuxPaneID: "%1"},
	}}

	live := &labelLiveness{}
	mf := newTestModuleWith(t, fixtureOpts{
		core:        newTestCore(t, "h:1", "a"),
		sessions:    sessions,
		owners:      owners,
		registryDir: dir,
		liveness:    live.liveness(),
		budget:      2 * time.Second,
	})

	return &labelFixture{
		moduleFixture: mf,
		t:             t,
		live:          live,
		inboxes:       map[int]string{10: inbox10, 20: inbox20},
	}
}

func (f *labelFixture) inbox(pid int) string { return f.inboxes[pid] }

// doAs serves one request under mux with principal p, decoding a
// non-nil body as JSON.
func (f *labelFixture) doAs(p middleware.Principal, method, path string, body any) (int, []byte) {
	f.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatalf("marshal request body: %v", err)
	}
	mux := http.NewServeMux()
	f.m.RegisterRoutes(mux)
	ctx := middleware.WithPrincipal(context.Background(), p)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func (f *labelFixture) self(req ipeers.SelfRequest) (int, []byte) {
	f.t.Helper()
	return f.doAs(middleware.Principal{Kind: middleware.PrincipalAdmin}, http.MethodPost, "/api/peers/self", req)
}

func (f *labelFixture) claim(inbox, label string) (int, []byte) {
	f.t.Helper()
	return f.doAs(middleware.Principal{Kind: middleware.PrincipalAdmin}, http.MethodPut, "/api/peers/self/label", ipeers.ClaimLabelRequest{OriginInbox: inbox, Label: label})
}

func (f *labelFixture) release(inbox string) (int, []byte) {
	f.t.Helper()
	return f.doAs(middleware.Principal{Kind: middleware.PrincipalAdmin}, http.MethodDelete, "/api/peers/self/label", ipeers.SelfRequest{OriginInbox: inbox})
}

// assertAPIError requires status == wantStatus and decodes body as an
// ipeers.APIError with Error == wantCode, returning it for further checks.
func (f *labelFixture) assertAPIError(status int, body []byte, wantStatus int, wantCode string) ipeers.APIError {
	f.t.Helper()
	if status != wantStatus {
		f.t.Fatalf("status = %d, want %d; body=%s", status, wantStatus, body)
	}
	var ae ipeers.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		f.t.Fatalf("decode APIError: %v; body=%s", err, body)
	}
	if ae.Error != wantCode {
		f.t.Fatalf("error = %q, want %q; body=%s", ae.Error, wantCode, body)
	}
	return ae
}

// spawnHelper acquires a fake `pdx peer-proxy` helper from the fixture's
// helper manager, so its pid shows up in m.proxyPIDs() — used to prove a
// proxy cannot name itself as a self-route origin.
func (f *labelFixture) spawnHelper(t *testing.T) *helper {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := ipeers.OriginKey{
		HostID:         "peer:1",
		AgentSessionID: "00000000-0000-4000-8000-000000000099",
		PID:            99999,
		ProcStart:      "Mon Sep 14 10:00:00 2026",
	}
	h, err := f.m.helpers.Acquire(ctx, key, "x/y", revUnapplied)
	if err != nil {
		t.Fatalf("spawnHelper: Acquire: %v", err)
	}
	return h
}

// decodeSelf fails the test unless status == 200 and body decodes as the
// self-route envelope (spec §6.3): { "peer": …, "warning": … }.
func decodeSelf(t *testing.T, status int, body []byte) ipeers.SelfResponse {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp ipeers.SelfResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode SelfResponse: %v; body=%s", err, body)
	}
	return resp
}

// decodeRecord is decodeSelf for the callers that only want the record,
// and it refuses a warning rather than ignoring one: every self-route 200
// in this file is a clean answer except the duplicate-label claims, which
// go through decodeSelf and assert the warning themselves.
func decodeRecord(t *testing.T, status int, body []byte) ipeers.PeerRecord {
	t.Helper()
	resp := decodeSelf(t, status, body)
	if resp.Warning != nil {
		t.Fatalf("unexpected warning %+v; body=%s", resp.Warning, body)
	}
	return resp.Peer
}

func TestSelf_Whoami(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec := decodeRecord(t, status, body)
	want := "a/n20"
	if rec.Address != want || rec.Label != "" || rec.LabelSource != "" || rec.RowKind != "entry" || rec.Agent.PID != 20 {
		t.Errorf("record = %+v, want address %s", rec, want)
	}
	if rec.Ref != ipeers.RefID("sid-2") {
		t.Errorf("ref = %q, want %q", rec.Ref, ipeers.RefID("sid-2"))
	}
	// A session inside tmux renders the same address the listing shows.
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(10)})
	rec = decodeRecord(t, status, body)
	if rec.Address != "a/n10" || rec.Ref != ipeers.RefID("sid-1") {
		t.Errorf("tmux session address/ref = %q/%q", rec.Address, rec.Ref)
	}
	status, body = f.self(ipeers.SelfRequest{OriginInbox: "/nope.sock"})
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
	status, body = f.self(ipeers.SelfRequest{})
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
}

func TestClaim_Matrix(t *testing.T) {
	f := newLabelFixture(t)
	cases := []struct {
		label      string
		wantStatus int
		wantCode   string
	}{
		{"Bad Label", 400, ipeers.ErrCodeLabelInvalid},
		{"cc", 400, ipeers.ErrCodeLabelReserved},
		{"tmux", 400, ipeers.ErrCodeLabelReserved},
		{"_abc123", 400, ipeers.ErrCodeLabelInvalid},
		{"purdex-tester", 200, ""},
	}
	for _, c := range cases {
		status, body := f.claim(f.inbox(20), c.label)
		if c.wantStatus == 200 {
			rec := decodeRecord(t, status, body)
			if rec.Label != c.label || rec.LabelSource != "user" || rec.LabelRev != 1 || rec.Address != "a/n20" {
				t.Errorf("%q: %+v", c.label, rec)
			}
			continue
		}
		f.assertAPIError(status, body, c.wantStatus, c.wantCode)
	}
	// Same label again: 200, rev unchanged.
	status, body := f.claim(f.inbox(20), "purdex-tester")
	rec := decodeRecord(t, status, body)
	if rec.LabelRev != 1 {
		t.Errorf("re-claim bumped rev to %d", rec.LabelRev)
	}
	// A conversation renaming itself replaces its own row and bumps rev.
	status, body = f.claim(f.inbox(20), "purdex-tester-2")
	rec = decodeRecord(t, status, body)
	if rec.Label != "purdex-tester-2" || rec.LabelRev != 2 {
		t.Errorf("rename = %+v, want purdex-tester-2 at rev 2", rec)
	}
	if rows, _ := f.labels.Snapshot(); len(rows) != 1 {
		t.Errorf("rename wrote a second row: %+v", rows)
	}
}

// TestClaim_DuplicateLabelWarnsAndSucceeds is D5/D7's regression test.
// Claiming a label another LIVE session holds now succeeds — a label is a
// display name, and nothing routes on it — and the answer carries the
// warning that makes the serial-number convention a one-step fix: the
// other holders, plus every label held on this host.
//
// The half that matters most is the incumbent: it is not evicted, not
// renamed, not re-revisioned. Under v2 the loser of a collision lost its
// name; under v3 there is no loser, because there is nothing to lose — its
// address never came from the label (spec §4.5, D7).
func TestClaim_DuplicateLabelWarnsAndSucceeds(t *testing.T) {
	f := newLabelFixture(t)

	status, body := f.claim(f.inbox(20), "purdex-tester")
	first := decodeRecord(t, status, body)
	// pid 10 takes a label of its own first, so the claim below is a
	// rename and live_labels has to account for the label it gives up.
	status, body = f.claim(f.inbox(10), "purdex-dev")
	decodeRecord(t, status, body)

	status, body = f.claim(f.inbox(10), "purdex-tester")
	resp := decodeSelf(t, status, body)
	if resp.Peer.Label != "purdex-tester" || resp.Peer.LabelSource != "user" {
		t.Errorf("claim record = %+v, want the label actually set", resp.Peer)
	}
	if want := "a/n10"; resp.Peer.Address != want {
		t.Errorf("claimant address = %q, want its own unchanged %q", resp.Peer.Address, want)
	}

	w := resp.Warning
	if w == nil || w.Code != ipeers.WarnLabelInUse {
		t.Fatalf("warning = %+v, want code %q", w, ipeers.WarnLabelInUse)
	}
	if len(w.Holders) != 1 || w.Holders[0].Address != first.Address || w.Holders[0].Agent == nil || w.Holders[0].Agent.PID != 20 {
		t.Errorf("warning holders = %+v, want the pid 20 incumbent", w.Holders)
	}
	// Every label a live session holds AFTER this claim: both sessions are
	// now on purdex-tester, and pid 10's purdex-dev is gone with the
	// rename (see TestClaim_LiveLabelsDescribeTheClaimJustMade).
	if !reflect.DeepEqual(w.LiveLabels, []string{"purdex-tester", "purdex-tester"}) {
		t.Errorf("live_labels = %v, want every label a live session holds", w.LiveLabels)
	}

	// D7: the incumbent is exactly where it was.
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	still := decodeRecord(t, status, body)
	if still.Label != first.Label || still.Ref != first.Ref || still.Address != first.Address || still.LabelRev != first.LabelRev {
		t.Errorf("incumbent = %+v, want the untouched %+v", still, first)
	}
	rows, _ := f.labels.Snapshot()
	if len(rows) != 2 {
		t.Errorf("rows = %+v, want both holders — a duplicate claim evicts nobody", rows)
	}

	// The convention the warning exists to prompt: the next serial is free,
	// so claiming it is clean and says nothing.
	status, body = f.claim(f.inbox(10), "purdex-tester-2")
	resp = decodeSelf(t, status, body)
	if resp.Warning != nil {
		t.Errorf("warning on a label nobody else holds: %+v", resp.Warning)
	}
	if resp.Peer.Label != "purdex-tester-2" {
		t.Errorf("record = %+v, want purdex-tester-2", resp.Peer)
	}
}

// TestSelf_EnvelopeShape pins spec §6.3: all three self routes answer the
// same envelope, and `warning` is omitted entirely — not null, not an
// empty object — when there is nothing to warn about.
func TestSelf_EnvelopeShape(t *testing.T) {
	f := newLabelFixture(t)
	for _, c := range []struct {
		name string
		call func() (int, []byte)
	}{
		{"whoami", func() (int, []byte) { return f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)}) }},
		{"claim", func() (int, []byte) { return f.claim(f.inbox(20), "purdex-tester") }},
		{"release", func() (int, []byte) { return f.release(f.inbox(20)) }},
	} {
		status, body := c.call()
		var raw map[string]json.RawMessage
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d; body=%s", c.name, status, body)
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("%s: decode: %v; body=%s", c.name, err, body)
		}
		if _, ok := raw["peer"]; !ok {
			t.Errorf("%s: body carries no peer envelope: %s", c.name, body)
		}
		if _, ok := raw["warning"]; ok {
			t.Errorf("%s: warning present on a clean answer: %s", c.name, body)
		}
		if rec := decodeRecord(t, status, body); rec.HostID != "h:1" {
			t.Errorf("%s: peer = %+v", c.name, rec)
		}
	}
}

// TestClaim_UnknownLiveFileNoLongerBlocks pins the gate spec §4.2 removed.
// A registry file the daemon cannot decode, belonging to a live pid, used
// to make claim 503 not_ready: a label "could not be proven free". Under
// D5 a label never has to be free, so the proof is meaningless and the
// refusal that waited on it is gone. Release never had the requirement.
func TestClaim_UnknownLiveFileNoLongerBlocks(t *testing.T) {
	f := newLabelFixture(t)
	writeRegistryFixture(t, f.registryDir, "4242.json", "{") // pid 4242 alive per fake
	status, body := f.claim(f.inbox(20), "purdex-tester")
	rec := decodeRecord(t, status, body)
	if rec.Label != "purdex-tester" || rec.LabelSource != "user" {
		t.Errorf("claim under an undecodable registry file = %+v", rec)
	}
	writeRegistryFixture(t, f.registryDir, "4243.json", "{")
	status, body = f.release(f.inbox(20))
	decodeRecord(t, status, body)
}

func TestClaim_OriginMustBeLiveNonProxy(t *testing.T) {
	f := newLabelFixture(t)
	f.live.markDead(20)
	status, body := f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
	// A helper (proxy) entry cannot name itself: register one via the
	// fixture's helper manager (Acquire) and present its inbox. The
	// helper's own registry entry is genuinely LIVE here (labelLiveness
	// answers its own procStart) — the refusal below must come from
	// findOriginEntry's !proxyPIDs[e.PID] exclusion, not from the entry
	// having been dropped as dead.
	h := f.spawnHelper(t)
	status, body = f.claim(h.sock, "purdex-tester")
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
	status, body = f.self(ipeers.SelfRequest{OriginInbox: h.sock})
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
}

func TestClaim_StoreFailures(t *testing.T) {
	f := newLabelFixture(t)
	f.m.labels = failingLabels{} // read fails
	status, body := f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)

	f.m.labels = writeFailingLabels{real: f.labels} // read ok, write fails
	status, body = f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	status, body = f.release(f.inbox(20))
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	if rows, _ := f.labels.Snapshot(); len(rows) != 0 {
		t.Errorf("rows written despite failure: %+v", rows)
	}
	// whoami only reads: still fine.
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	decodeRecord(t, status, body)
}

func TestRelease(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.claim(f.inbox(20), "purdex-tester")
	decodeRecord(t, status, body)
	status, body = f.release(f.inbox(20))
	rec := decodeRecord(t, status, body)
	if rec.LabelSource != "" || rec.LabelRev != 2 || rec.Label != "" {
		t.Errorf("released = %+v, want no label at rev 2", rec)
	}
	if want := "a/n20"; rec.Address != want {
		t.Errorf("released address = %q, want the unchanged %q — releasing a label does not move a conversation", rec.Address, want)
	}
	// Release with no row: 200, default, rev 0, nothing written.
	status, body = f.release(f.inbox(10))
	rec = decodeRecord(t, status, body)
	if rec.LabelRev != 0 {
		t.Errorf("no-row release = %+v", rec)
	}
	if rows, _ := f.labels.Snapshot(); len(rows) != 1 {
		t.Errorf("rows = %+v, want only sid-2's released row", rows)
	}
}

// TestClaim_ConcurrentSameLabel_BothSucceed: labelMu still serialises two
// live sessions racing for one label, but the one that arrives second is
// no longer a loser — it gets the label too, and (whichever order the
// scheduler picked) exactly one of the two answers carries the warning.
func TestClaim_ConcurrentSameLabel_BothSucceed(t *testing.T) {
	f := newLabelFixture(t)
	var wg sync.WaitGroup
	results := make([]int, 2)
	warned := make([]bool, 2)
	for i, pid := range []int{10, 20} {
		wg.Add(1)
		go func(i, pid int) {
			defer wg.Done()
			status, body := f.claim(f.inbox(pid), "purdex-tester")
			results[i] = status
			if status == http.StatusOK {
				var resp ipeers.SelfResponse
				_ = json.Unmarshal(body, &resp)
				warned[i] = resp.Warning != nil
			}
		}(i, pid)
	}
	wg.Wait()
	if results[0] != 200 || results[1] != 200 {
		t.Fatalf("statuses = %v, want both 200", results)
	}
	if warned[0] == warned[1] {
		t.Errorf("warnings = %v, want exactly one of the two to be warned", warned)
	}
	rows, _ := f.labels.Snapshot()
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want one per session", rows)
	}
	for _, r := range rows {
		if r.Label != "purdex-tester" {
			t.Errorf("row %+v lost its label", r)
		}
	}
}

func TestSelfRoutes_DenyHostPrincipal(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/peers/self"}, {"PUT", "/api/peers/self/label"}, {"DELETE", "/api/peers/self/label"},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		if HostRoutePolicy(r) {
			t.Errorf("%s %s allowed for a host principal", c.method, c.path)
		}
	}
	// And the handlers themselves refuse a host principal in depth.
	f := newLabelFixture(t)
	status, _ := f.doAs(middleware.Principal{Kind: middleware.PrincipalHost, Alias: "x", HostID: "x:1"}, "POST", "/api/peers/self", ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	if status != 403 {
		t.Errorf("host principal got %d", status)
	}
}

// releaseCountingLabels is a LabelStore whose read always fails while every
// write is counted. failingLabels/writeFailingLabels can show that a write
// FAILED; only a counter can show that no write was ever ATTEMPTED, which
// is what spec §3.4 requires of a release whose label-store read failed.
type releaseCountingLabels struct {
	releases int
	claims   int
}

func (l *releaseCountingLabels) Snapshot() ([]store.PeerLabel, error) {
	return nil, errors.New("snapshot boom")
}

func (l *releaseCountingLabels) Claim(string, string, time.Time) (store.PeerLabel, error) {
	l.claims++
	return store.PeerLabel{}, nil
}

func (l *releaseCountingLabels) Release(string, time.Time) (store.PeerLabel, bool, error) {
	l.releases++
	return store.PeerLabel{}, false, nil
}

// listingRecord is the row GET /api/peers renders for sessionID, built by
// running this very fixture through localEnvelope → ipeers.Build. It is
// the other half of the spec §3.2 agreement: whatever the self routes
// answer for a conversation, this is what the listing says about it.
func (f *labelFixture) listingRecord(sessionID string) ipeers.PeerRecord {
	f.t.Helper()
	snap := f.m.configSnapshot()
	env := f.m.localEnvelope(context.Background(), snap.hostID, snap.alias)
	if !env.OK {
		f.t.Fatalf("localEnvelope: %s", env.Error)
	}
	for _, rec := range env.Peers {
		if rec.Agent != nil && rec.Agent.SessionID == sessionID {
			return rec
		}
	}
	f.t.Fatalf("no listing row for session %q; peers=%+v", sessionID, env.Peers)
	return ipeers.PeerRecord{}
}

// TestSelf_AddressMatchesListing is the tripwire for the promise that whoami
// and the listing cannot disagree: both build a row from the conversation's
// own registry entry and sessionId, so for one live conversation they must
// render byte-identical labels and addresses.
func TestSelf_AddressMatchesListing(t *testing.T) {
	f := newLabelFixture(t)

	// Guard against the test passing vacuously: sid-1 must actually have
	// an address to compare.
	if listed := f.listingRecord("sid-1"); listed.Ref == "" || listed.Address == "" {
		t.Fatalf("listing row for sid-1 = %+v, want a ref and an address", listed)
	}

	for _, c := range []struct {
		pid int
		sid string
	}{{10, "sid-1"}, {20, "sid-2"}} {
		listed := f.listingRecord(c.sid)
		status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(c.pid)})
		self := decodeRecord(t, status, body)
		if self.Address != listed.Address || self.Label != listed.Label || self.LabelSource != listed.LabelSource {
			t.Errorf("pid %d: whoami %q/%q/%q, listing %q/%q/%q",
				c.pid, self.Address, self.Label, self.LabelSource,
				listed.Address, listed.Label, listed.LabelSource)
		}
	}
}

// TestSelf_AddressMatchesListingAfterATmuxRename is what v3's
// TestSelf_SuffixDivergesFromListingAfterATmuxRename became. That test pinned a
// known, bounded divergence: the listing rendered a SESSION row and put the
// daemon's LIVE tmux name in the display suffix, while whoami answered from one
// frozen registry entry and put the name Claude Code recorded at startup there
// instead. After a rename the two interfaces printed different suffixes for one
// conversation, and its comment said the test to write when the divergence
// closed was this one.
//
// v4 closes it by deletion rather than by making the self routes live: with
// PeerRecord.Suffix gone, no tmux name — live or frozen — reaches a record at
// all, so the one field that could disagree no longer exists. The address is
// the registry name, which both interfaces read from the same file.
//
// The rename is still performed, because "they agree" is only worth asserting
// under the conditions that used to make them disagree.
func TestSelf_AddressMatchesListingAfterATmuxRename(t *testing.T) {
	f := newLabelFixture(t)

	// tmux renames mt0 to mt0zz. The registry file for pid 10 keeps
	// "tmux":"mt0:@1.%1" — Claude Code rewrites that file on status changes
	// and copies the field through unchanged. A rename does not touch the
	// tmux session id either, so the daemon's session code stays "c1" and
	// the owner map still resolves this session to sid-1.
	f.m.sessions = &fakeSessions{sessions: []session.SessionInfo{
		{Code: "c1", Name: "mt0zz", Cwd: "/w", TmuxInstance: "inst1"},
	}}

	listed := f.listingRecord("sid-1")
	status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(10)})
	self := decodeRecord(t, status, body)

	// The two rows still come from different places — that has not changed,
	// and it is why the agreement below is worth pinning.
	if listed.RowKind != "session" || self.RowKind != "entry" {
		t.Fatalf("row kinds = listing %q / whoami %q, want session / entry", listed.RowKind, self.RowKind)
	}
	if self.Address != listed.Address {
		t.Errorf("whoami %q, listing %q; a tmux rename must no longer move either", self.Address, listed.Address)
	}
	if self.Ref == "" || self.Ref != listed.Ref {
		t.Fatalf("ref: whoami %q, listing %q, want one non-empty ref", self.Ref, listed.Ref)
	}

	// And the ref both rows agree on still resolves to this conversation.
	// The NAME tier arrives with Resolve's v4 rewrite; the ref tier is what
	// exists at this point and is the half a rename could never have moved.
	snap := f.m.configSnapshot()
	env := f.m.localEnvelope(context.Background(), snap.hostID, snap.alias)
	if !env.OK {
		t.Fatalf("localEnvelope: %s", env.Error)
	}
	if _, sess, ok := ipeers.SplitAddress(listed.Host + "/" + listed.Ref); !ok {
		t.Fatalf("SplitAddress of the ref form failed")
	} else {
		rec, err := ipeers.Resolve(env.Peers, sess, ipeers.ResolveSnapshot{Partial: env.Partial})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", sess, err)
		}
		if rec.Agent == nil || rec.Agent.SessionID != "sid-1" {
			t.Errorf("Resolve(%q) landed on %+v, want the sid-1 conversation", sess, rec.Agent)
		}
	}
}

// TestClaim_RecordsMatchListing pins Task 4 item 3: both records claim
// renders — the envelope's own peer and the label_in_use warning's holders
// — are built the same way the listing builds its rows, so neither can
// drift from it.
func TestClaim_RecordsMatchListing(t *testing.T) {
	f := newLabelFixture(t)

	// Claiming the name of your own tmux session is allowed (spec §2.2):
	// the default it displaces is your own.
	status, body := f.claim(f.inbox(10), "mt0")
	rec := decodeRecord(t, status, body)
	if rec.LabelSource != "user" || rec.Label != "mt0" {
		t.Fatalf("claim record = %+v, want the user label mt0", rec)
	}
	listed := f.listingRecord("sid-1")
	if rec.Address != listed.Address || rec.Label != listed.Label || rec.LabelSource != listed.LabelSource {
		t.Errorf("claim 200 %q/%q/%q != listing %q/%q/%q",
			rec.Address, rec.Label, rec.LabelSource, listed.Address, listed.Label, listed.LabelSource)
	}

	// pid 20 takes the same label: 200, with the other holder rendered
	// exactly as the listing renders it.
	status, body = f.claim(f.inbox(20), "mt0")
	resp := decodeSelf(t, status, body)
	if resp.Warning == nil || len(resp.Warning.Holders) != 1 {
		t.Fatalf("label_in_use carried no holder: %+v", resp.Warning)
	}
	h := resp.Warning.Holders[0]
	if h.Address != listed.Address || h.Label != listed.Label || h.LabelSource != listed.LabelSource {
		t.Errorf("holder %q/%q/%q != listing %q/%q/%q",
			h.Address, h.Label, h.LabelSource,
			listed.Address, listed.Label, listed.LabelSource)
	}
}

// TestRelease_StoreReadFailure_WritesNothing pins Task 4 item 5: release
// now takes a label snapshot BEFORE the write, and a failed read is
// store_unavailable with no Release attempted at all — the response is
// never a default the daemon could not vouch for.
func TestRelease_StoreReadFailure_WritesNothing(t *testing.T) {
	f := newLabelFixture(t)
	fake := &releaseCountingLabels{}
	f.m.labels = fake

	status, body := f.release(f.inbox(20))
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	if fake.releases != 0 {
		t.Errorf("release wrote %d times after a failed snapshot, want 0", fake.releases)
	}
}

// storedLabels is every non-empty label in the label store, sorted — the
// ground truth a claim's live_labels is checked against. Both fixture
// sessions (sid-1, sid-2) are live, so on this fixture "held by a live
// session" and "present in the store" are the same set, which is exactly
// what makes it usable as an independent oracle.
func (f *labelFixture) storedLabels() []string {
	f.t.Helper()
	rows, err := f.labels.Snapshot()
	if err != nil {
		f.t.Fatalf("snapshot: %v", err)
	}
	out := []string{}
	for _, r := range rows {
		if r.Label != "" {
			out = append(out, r.Label)
		}
	}
	sort.Strings(out)
	return out
}

// labelInUse fails unless resp carries the label_in_use warning, and
// returns it.
func labelInUse(t *testing.T, resp ipeers.SelfResponse) *ipeers.SelfWarning {
	t.Helper()
	w := resp.Warning
	if w == nil || w.Code != ipeers.WarnLabelInUse {
		t.Fatalf("warning = %+v, want code %q", w, ipeers.WarnLabelInUse)
	}
	return w
}

// TestClaim_LiveLabelsDescribeTheClaimJustMade pins the one thing
// live_labels is for: letting an agent pick the next free serial in ONE
// step (spec §4.2, §8). That only works if the list describes the state
// the claim PRODUCED. Computed before the write, it answers with the state
// the claim replaced — so the envelope announces a new label in `peer`
// while `live_labels` still shows the old one and omits the new one, and
// the agent avoids a serial this very call just freed.
//
// Here pid 10 renames itself from purdex-tester-3 onto purdex-tester,
// which pid 20 holds: afterwards purdex-tester-3 belongs to nobody and
// purdex-tester belongs to both.
func TestClaim_LiveLabelsDescribeTheClaimJustMade(t *testing.T) {
	f := newLabelFixture(t)

	status, body := f.claim(f.inbox(20), "purdex-tester")
	incumbent := decodeRecord(t, status, body)
	status, body = f.claim(f.inbox(10), "purdex-tester-3")
	decodeRecord(t, status, body)

	status, body = f.claim(f.inbox(10), "purdex-tester")
	resp := decodeSelf(t, status, body)
	if resp.Peer.Label != "purdex-tester" {
		t.Fatalf("claim record = %+v, want the label actually set", resp.Peer)
	}
	w := labelInUse(t, resp)

	// Both live sessions now hold purdex-tester; purdex-tester-3 is free,
	// and it is free BECAUSE of this call — the whole point of the list.
	if want := []string{"purdex-tester", "purdex-tester"}; !reflect.DeepEqual(w.LiveLabels, want) {
		t.Errorf("live_labels = %v, want %v — the set this claim produced", w.LiveLabels, want)
	}
	for _, l := range w.LiveLabels {
		if l == "purdex-tester-3" {
			t.Errorf("live_labels = %v still lists the caller's released label", w.LiveLabels)
		}
	}
	// And it agrees with the store, which is the state the caller will see
	// on any later read.
	if !reflect.DeepEqual(w.LiveLabels, f.storedLabels()) {
		t.Errorf("live_labels = %v, store holds %v", w.LiveLabels, f.storedLabels())
	}

	// holders keeps its own meaning: the OTHER live sessions on the label,
	// never the caller itself.
	if len(w.Holders) != 1 || w.Holders[0].Address != incumbent.Address {
		t.Errorf("holders = %+v, want only the pid 20 incumbent", w.Holders)
	}
}

// TestClaim_LiveLabelsIncludeANewlyNamedCaller is the same rule for a
// caller that had no label at all: it contributes nothing to the list
// before the write and must contribute the claimed label after it.
func TestClaim_LiveLabelsIncludeANewlyNamedCaller(t *testing.T) {
	f := newLabelFixture(t)

	status, body := f.claim(f.inbox(20), "purdex-tester")
	decodeRecord(t, status, body)

	status, body = f.claim(f.inbox(10), "purdex-tester")
	w := labelInUse(t, decodeSelf(t, status, body))
	if want := []string{"purdex-tester", "purdex-tester"}; !reflect.DeepEqual(w.LiveLabels, want) {
		t.Errorf("live_labels = %v, want %v — the caller's own new label included", w.LiveLabels, want)
	}
	if !reflect.DeepEqual(w.LiveLabels, f.storedLabels()) {
		t.Errorf("live_labels = %v, store holds %v", w.LiveLabels, f.storedLabels())
	}
}

// TestClaim_LiveLabelsOnAlreadyOurs covers the path that writes nothing:
// re-claiming a label the caller already holds. The list must still be the
// live set, with the caller's own label present exactly once — this is the
// path where counting the caller twice, or dropping it, would be easiest.
func TestClaim_LiveLabelsOnAlreadyOurs(t *testing.T) {
	f := newLabelFixture(t)

	status, body := f.claim(f.inbox(20), "purdex-tester")
	decodeRecord(t, status, body)
	status, body = f.claim(f.inbox(10), "purdex-tester")
	first := decodeSelf(t, status, body)
	labelInUse(t, first)

	// Again: already ours, no write, rev unchanged, same list.
	status, body = f.claim(f.inbox(10), "purdex-tester")
	resp := decodeSelf(t, status, body)
	if resp.Peer.LabelRev != first.Peer.LabelRev {
		t.Errorf("re-claim bumped rev %d → %d", first.Peer.LabelRev, resp.Peer.LabelRev)
	}
	w := labelInUse(t, resp)
	if want := []string{"purdex-tester", "purdex-tester"}; !reflect.DeepEqual(w.LiveLabels, want) {
		t.Errorf("live_labels = %v, want %v — once per live holder", w.LiveLabels, want)
	}
	if !reflect.DeepEqual(w.LiveLabels, f.storedLabels()) {
		t.Errorf("live_labels = %v, store holds %v", w.LiveLabels, f.storedLabels())
	}
}
