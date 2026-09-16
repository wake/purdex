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
	"strings"
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
func (l *labelLiveness) revive(pid int)   { l.dead.Delete(pid) }

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

// decodeRecord fails the test unless status == 200 and body decodes as an
// ipeers.PeerRecord.
func decodeRecord(t *testing.T, status int, body []byte) ipeers.PeerRecord {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var rec ipeers.PeerRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("decode PeerRecord: %v; body=%s", err, body)
	}
	return rec
}

func TestSelf_Whoami(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec := decodeRecord(t, status, body)
	want := "a/" + ipeers.DefaultLabel("sid-2") + ":n20"
	if rec.Address != want || rec.LabelSource != "default" || rec.RowKind != "entry" || rec.Agent.PID != 20 {
		t.Errorf("record = %+v, want address %s", rec, want)
	}
	// A session inside tmux renders the same address the listing shows.
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(10)})
	rec = decodeRecord(t, status, body)
	if rec.Suffix != "mt0-n10" {
		t.Errorf("tmux session suffix = %q", rec.Suffix)
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
			if rec.Label != c.label || rec.LabelSource != "user" || rec.LabelRev != 1 || rec.Address != "a/purdex-tester:n20" {
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
	// Another live session: taken, with holder + live_labels (the caller's
	// own live label is listed too — spec §3.3 says every held label).
	status, body = f.claim(f.inbox(10), "purdex-dev")
	decodeRecord(t, status, body)
	status, body = f.claim(f.inbox(10), "purdex-tester")
	ae := f.assertAPIError(status, body, 409, ipeers.ErrLabelTaken)
	if ae.Holder == nil || ae.Holder.Agent.PID != 20 || ae.Holder.Address != "a/purdex-tester:n20" {
		t.Errorf("taken holder = %+v", ae.Holder)
	}
	if !reflect.DeepEqual(ae.LiveLabels, []string{"purdex-dev", "purdex-tester"}) {
		t.Errorf("live_labels = %v", ae.LiveLabels)
	}
	// Holder dies ⇒ claim succeeds, old row evicted, caller's previous label replaced.
	f.live.markDead(20)
	status, body = f.claim(f.inbox(10), "purdex-tester")
	rec = decodeRecord(t, status, body)
	if rec.Agent.PID != 10 || rec.Label != "purdex-tester" || rec.LabelRev != 3 {
		t.Errorf("take-over: %+v", rec)
	}
	rows, _ := f.labels.Snapshot()
	if len(rows) != 1 || rows[0].SessionID != "sid-1" || rows[0].Label != "purdex-tester" {
		t.Errorf("rows after take-over = %+v", rows)
	}
	// The dead one comes back (resume): whoami shows the default label.
	f.live.revive(20)
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec = decodeRecord(t, status, body)
	if rec.LabelSource != "default" || rec.LabelRev != 0 {
		t.Errorf("resumed holder = %+v, want default label, rev 0", rec)
	}
}

func TestClaim_NotReadyOnUnknownLiveFile(t *testing.T) {
	f := newLabelFixture(t)
	writeRegistryFixture(t, f.registryDir, "4242.json", "{") // pid 4242 alive per fake
	status, body := f.claim(f.inbox(20), "purdex-tester")
	ae := f.assertAPIError(status, body, 503, ipeers.ErrNotReady)
	if len(ae.Skipped) != 1 || !strings.HasSuffix(ae.Skipped[0], "4242.json") {
		t.Errorf("skipped = %v", ae.Skipped)
	}
	f.live.markDead(4242) // now the unknown file belongs to a dead pid: ignored
	status, body = f.claim(f.inbox(20), "purdex-tester")
	decodeRecord(t, status, body)
	// Release has no completeness requirement.
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
	if rec.LabelSource != "default" || rec.LabelRev != 2 || rec.Label != ipeers.DefaultLabel("sid-2") {
		t.Errorf("released = %+v", rec)
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

func TestClaim_ConcurrentSameLabel_OneWins(t *testing.T) {
	f := newLabelFixture(t)
	var wg sync.WaitGroup
	results := make([]int, 2)
	for i, pid := range []int{10, 20} {
		wg.Add(1)
		go func(i, pid int) {
			defer wg.Done()
			results[i], _ = f.claim(f.inbox(pid), "purdex-tester")
		}(i, pid)
	}
	wg.Wait()
	sort.Ints(results)
	if results[0] != 200 || results[1] != 409 {
		t.Fatalf("statuses = %v, want [200 409]", results)
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

// TestSelf_Whoami_DefaultFromTmuxSessionName pins Task 4 item 1 and item 6:
// the default label of the one live agent in a tmux session is that
// session's name, and an agent outside tmux keeps the v2 hash.
func TestSelf_Whoami_DefaultFromTmuxSessionName(t *testing.T) {
	f := newLabelFixture(t)

	// pid 10 is the only live agent in tmux session "mt0", unnamed.
	status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(10)})
	rec := decodeRecord(t, status, body)
	if rec.Label != "mt0" || rec.LabelSource != "default" {
		t.Errorf("in-tmux whoami label = %q/%q, want mt0/default", rec.Label, rec.LabelSource)
	}
	if rec.Address != "a/mt0:mt0-n10" {
		t.Errorf("in-tmux whoami address = %q, want a/mt0:mt0-n10", rec.Address)
	}

	// pid 20 has no tmux field: nothing to derive from, so the hash form
	// is still the answer (spec §3.3 rule 1).
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec = decodeRecord(t, status, body)
	if rec.Label != ipeers.DefaultLabel("sid-2") || rec.LabelSource != "default" {
		t.Errorf("outside-tmux whoami = %+v, want the hash default", rec)
	}
}

// TestSelf_AddressMatchesListing is the spec §3.2 tripwire: the self
// routes and the listing resolve defaults over the same population, so for
// the same live conversation they must render byte-identical labels and
// addresses. It fails the moment either path derives a default the other
// does not.
func TestSelf_AddressMatchesListing(t *testing.T) {
	f := newLabelFixture(t)

	// Guard against the test passing because BOTH paths fell back to the
	// hash: sid-1 must actually be exercising the tmux-derived form.
	if listed := f.listingRecord("sid-1"); listed.Label != "mt0" {
		t.Fatalf("listing label for sid-1 = %q, want the tmux-derived mt0", listed.Label)
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

// TestClaim_RecordsMatchListing pins Task 4 item 3: both records claim
// renders — the 200 body and the 409 label_taken holder — go through the
// resolved defaults, so neither can drift from the listing.
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

	// pid 20 wants the same label: 409, with the holder rendered exactly
	// as the listing renders it.
	status, body = f.claim(f.inbox(20), "mt0")
	ae := f.assertAPIError(status, body, 409, ipeers.ErrLabelTaken)
	if ae.Holder == nil {
		t.Fatalf("label_taken carried no holder: %+v", ae)
	}
	if ae.Holder.Address != listed.Address || ae.Holder.Label != listed.Label || ae.Holder.LabelSource != listed.LabelSource {
		t.Errorf("holder %q/%q/%q != listing %q/%q/%q",
			ae.Holder.Address, ae.Holder.Label, ae.Holder.LabelSource,
			listed.Address, listed.Label, listed.LabelSource)
	}
}

// TestRelease_OwnTmuxLabelBecomesItsDefault pins Task 4 item 4 and the
// "other" in spec §3.3 rule 3: an agent in tmux "mt0" that had claimed
// "mt0" gets "mt0" back as its DEFAULT, because the label it is releasing
// is its own and must not count as a competitor against its own candidate.
func TestRelease_OwnTmuxLabelBecomesItsDefault(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.claim(f.inbox(10), "mt0")
	decodeRecord(t, status, body)

	status, body = f.release(f.inbox(10))
	rec := decodeRecord(t, status, body)
	if rec.Label != "mt0" || rec.LabelSource != "default" {
		t.Errorf("released = %q/%q, want mt0/default (not a hash)", rec.Label, rec.LabelSource)
	}
	if rec.Address != "a/mt0:mt0-n10" {
		t.Errorf("released address = %q, want a/mt0:mt0-n10", rec.Address)
	}
	if listed := f.listingRecord("sid-1"); rec.Address != listed.Address || rec.Label != listed.Label {
		t.Errorf("release %q/%q != listing %q/%q", rec.Address, rec.Label, listed.Address, listed.Label)
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
