package teammod

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The relay quota, RQ-1a: storage keyed by the chain root, the setting route, the displays. Nothing spends a quota here.

func lineage(t *testing.T, s *Store, sid, pred string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES (?, ?, '_old000', 'op', 1)`, sid, pred); err != nil {
		t.Fatal(err)
	}
}

func TestChainRootIn_WalksTheLineageToItsRoot(t *testing.T) {
	s := openTestStore(t)
	root, hops, err := chainRootIn(s.db, "solo")
	if err != nil || root != "solo" || hops != 0 {
		t.Fatalf("a session never relayed = %q %d %v, want itself", root, hops, err)
	}
	lineage(t, s, "b", "a")
	lineage(t, s, "c", "b")
	lineage(t, s, "d", "c")
	for _, sid := range []string{"a", "b", "c", "d"} {
		if root, _, err := chainRootIn(s.db, sid); err != nil || root != "a" {
			t.Errorf("root of %s = %q %v, want a", sid, root, err)
		}
	}
}

// No fixed depth: R3 holds on the 300th relay too. Mutation gate: a 256-hop bound → red.
func TestChainRootIn_HasNoFixedDepth(t *testing.T) {
	s := openTestStore(t)
	for i := 1; i <= 300; i++ {
		lineage(t, s, fmt.Sprintf("s%d", i), fmt.Sprintf("s%d", i-1))
	}
	root, hops, err := chainRootIn(s.db, "s300")
	if err != nil || root != "s0" || hops != 300 {
		t.Fatalf("root of the 300th = %q after %d hops (%v), want s0 after 300", root, hops, err)
	}
}

func TestChainRootIn_RefusesACycle(t *testing.T) {
	s := openTestStore(t)
	lineage(t, s, "b", "a")
	// session_id is the primary key, so a loop needs a hand-edited db: a -> b -> a
	if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES ('a', 'b', '_x', 'op2', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := chainRootIn(s.db, "b"); !errors.Is(err, ErrLineageCycle) {
		t.Fatalf("a cycle = %v, want ErrLineageCycle", err)
	}
}

func TestSetRelayQuota_WritesTheChainRootAndKeepsTheFieldsLeftOut(t *testing.T) {
	s := openTestStore(t)
	lineage(t, s, "b", "a")
	three, one := 3, 1
	root, row, err := s.SetRelayQuota("b", &three, nil, 1000, "Purdex.app")
	if err != nil || root != "a" || row.SelfLeft != 3 || row.MemberPoolLeft != 0 {
		t.Fatalf("set = %q %+v %v", root, row, err)
	}
	if _, row, err = s.SetRelayQuota("a", nil, &one, 2000, "Purdex.app"); err != nil || row.SelfLeft != 3 || row.MemberPoolLeft != 1 || row.UpdatedAt != 2000 {
		t.Fatalf("second set = %+v %v, want self kept at 3, pool 1", row, err)
	}
	for _, sid := range []string{"a", "b"} { // every session of the chain reads the one row
		q, r, err := s.RelayQuotaOf(sid)
		if err != nil || r != "a" || q != (team.RelayQuota{SelfLeft: 3, MemberPoolLeft: 1, Rev: 2}) {
			t.Errorf("%s reads %+v root %s %v", sid, q, r, err)
		}
	}
	if q, _, _ := s.RelayQuotaOf("never-seen"); q != (team.RelayQuota{}) {
		t.Errorf("no row = %+v, want both 0", q)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relay_quotas`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d (%v), want the chain's one", n, err)
	}
}

func (f *fixture) putQuota(req team.RelayQuotaPutRequest) (int, team.RelayQuotaView, []byte) {
	f.t.Helper()
	code, body := f.do(http.MethodPut, team.RelayQuotaRoute, req)
	var v team.RelayQuotaView
	if code == http.StatusOK {
		_ = json.Unmarshal(body, &v)
	}
	return code, v, body
}

var appClient2 = team.Client{Kind: "app", Label: "Purdex.app @ air26"}

func ip(n int) *int { return &n }

func TestRelayQuotaPut_ValidatesAndTellsTheApp(t *testing.T) {
	f := newFixture(t)
	for name, req := range map[string]team.RelayQuotaPutRequest{
		"not the app":     {SessionID: "sid-1", SelfLeft: ip(1), Client: team.Client{Kind: "terminal", Label: "x"}},
		"no label":        {SessionID: "sid-1", SelfLeft: ip(1), Client: team.Client{Kind: "app"}},
		"an agent posing": {SessionID: "sid-1", SelfLeft: ip(1), Client: team.Client{Kind: "unattended", Label: "無人值守模式"}},
		"no session":      {SelfLeft: ip(1), Client: appClient2},
		"no field":        {SessionID: "sid-1", Client: appClient2},
		"negative":        {SessionID: "sid-1", SelfLeft: ip(-1), Client: appClient2},
		"over 99":         {SessionID: "sid-1", MemberPoolLeft: ip(100), Client: appClient2},
	} {
		if code, _, body := f.putQuota(req); code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
	}
	if code, _, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "no-such-session", SelfLeft: ip(1), Client: appClient2}); code != http.StatusNotFound {
		t.Errorf("unknown session: %d %s, want 404", code, body)
	}
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_quotas`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused request wrote %d row(s)", n)
	}
	for _, v := range []int{0, 99} {
		if code, _, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(v), Client: appClient2}); code != http.StatusOK {
			t.Errorf("value %d: %d %s, want 200", v, code, body)
		}
	}
}

func TestRelayQuotaPut_SetsTheChainAndShowsOnEveryDisplay(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1)) // sid-1 leads
	lineage(t, f.m.store, "sid-1", "sid-0")
	f.rosterBaselineNow()
	w := f.watchRoster()
	w.drain()
	sub := f.core.Events.AddTestSubscriber()
	defer f.core.Events.RemoveTestSubscriber(sub)

	code, v, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(3), MemberPoolLeft: ip(2), Client: appClient2})
	if code != http.StatusOK || v.RootSessionID != "sid-0" || v.SelfLeft != 3 || v.MemberPoolLeft != 2 || v.UpdatedBy != appClient2.Label {
		t.Fatalf("put = %d %s", code, body)
	}
	// the roster: the lead's numbers, announced once
	ev := w.one("relay quota")
	if len(ev.Teams) != 1 || ev.Teams[0].Lead.RelayQuota != (team.RelayQuota{SelfLeft: 3, MemberPoolLeft: 2, Rev: 1}) {
		t.Fatalf("roster lead = %+v", ev.Teams)
	}
	// the event
	found := false
	for {
		select {
		case raw := <-sub.SendCh():
			if string(raw) != "" && containsAll(string(raw), team.RelayQuotaEventType, `\"root_session_id\":\"sid-0\"`, `\"self_left\":3`) {
				found = true
			}
			continue
		default:
		}
		break
	}
	if !found {
		t.Error("no team.relay_quota event for the chain")
	}
	// GET /api/team: the member rows carry theirs (a member is a session of the chain of its own)
	f.makeMemberOfLead("sid-2")
	if code, tv, e := f.teamView("/tmp/10.sock"); code != http.StatusOK || len(tv.Members) != 1 || tv.Members[0].RelayQuota != (team.RelayQuota{}) {
		t.Fatalf("GET /api/team = %d %+v %+v", code, tv, e)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func TestUnattendedView_ListsLiveSessionsWithTheirChainsNumbers(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	lineage(t, f.m.store, "sid-2", "sid-9") // sid-2's chain root is sid-9
	if code, _, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-2", SelfLeft: ip(4), Client: appClient2}); code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	_, v, raw := f.getUnattended("")
	if len(v.Quotas) != 2 {
		t.Fatalf("quotas = %+v (%s), want the two live fixture sessions", v.Quotas, raw)
	}
	by := map[string]team.SessionQuota{}
	for _, q := range v.Quotas {
		by[q.SessionID] = q
	}
	if q := by["sid-1"]; !q.IsLead || q.RootSessionID != "sid-1" || q.RelayQuota != (team.RelayQuota{}) {
		t.Errorf("sid-1 = %+v, want the lead, own root, 0/0", q)
	}
	if q := by["sid-2"]; q.IsLead || q.RootSessionID != "sid-9" || q.SelfLeft != 4 {
		t.Errorf("sid-2 = %+v, want a plain session of chain sid-9 with 4", q)
	}
	f.origins.setReadErr(true) // a registry that cannot be read: the page stays, the quotas are absent
	if code, v2, _ := f.getUnattended(""); code != http.StatusOK || v2.Quotas != nil {
		t.Errorf("registry unreadable: %d quotas=%+v, want 200 and none", code, v2.Quotas)
	}
}

// pending_lineage: the new session id exists, the relay in flight has not written its lineage yet.
func TestRelayQuotaPut_PendingLineageFlagsAProvisionalRoot(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	if _, v, _ := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(1), Client: appClient2}); v.PendingLineage {
		t.Fatal("no relay in flight: pending_lineage must be false")
	}
	claimedOp(t, f.m.store, "relay-1", "sid-1", "_abc123")
	f.origins.markDead("sid-1") // /clear: the old session left the registry...
	// a stranger: sid-2 has no lineage either, and a relay is in flight on the host — but not in its process
	if _, v, _ := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-2", SelfLeft: ip(1), Client: appClient2}); v.PendingLineage {
		t.Fatal("an unrelated session was flagged pending_lineage")
	}
	// ...and the new one (sid-1b, the same process) is there, with no lineage row yet
	code, v, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1b", SelfLeft: ip(5), Client: appClient2})
	if code != http.StatusOK || !v.PendingLineage || v.RootSessionID != "sid-1b" {
		t.Fatalf("put = %d %s, want pending_lineage on the provisional root", code, body)
	}
	// the same pid with another start time is another process (the OS reused the pid): not flagged
	f.origins.show(team.Origin{SessionID: "sid-1b", Ref: "_1b0000", PID: 10, ProcStart: "Mon Sep 14 09:00:00 2026"})
	if _, v, _ := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1b", SelfLeft: ip(5), Client: appClient2}); v.PendingLineage {
		t.Fatal("a reused pid (another start time) was flagged pending_lineage")
	}
	// once the lineage is written the real root is read, and the orphan is never looked at
	lineage(t, f.m.store, "sid-1b", "sid-1")
	if q, root, _ := f.m.store.RelayQuotaOf("sid-1b"); root != "sid-1" || q.SelfLeft != 1 {
		t.Fatalf("after the lineage: root %s %+v, want the old root's numbers", root, q)
	}
}

// team.db is deployed: a database from before the quotas gets the table when it is opened, and keeps its data.
func TestOpenStore_AddsRelayQuotasToAnOlderDatabase(t *testing.T) {
	path := t.TempDir() + "/team.db"
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE relay_quotas`); err != nil { // what a pre-#2062 database looks like
		t.Fatal(err)
	}
	if _, _, _, err := s.Create(openApproval(uid(1), "sid-1", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.SetRelayQuota("sid-1", ip(2), nil, 1, "app"); err != nil {
		t.Fatalf("the table was not added: %v", err)
	}
	if _, ok, err := s.Get(uid(1)); err != nil || !ok {
		t.Fatalf("the older data is gone: %v %v", ok, err)
	}
}

// Two PUTs racing: the last COMMIT is the last EVENT. A is held just after its commit, inside the section; B must
// wait for it, so B's commit and event follow A's. Mutation gate: drop quotaMu → B commits and publishes first, A's
// stale event comes last → red.
func TestRelayQuotaPut_ConcurrentPutsPublishInCommitOrder(t *testing.T) {
	f := newFixture(t)
	sub := f.core.Events.AddTestSubscriber()
	defer f.core.Events.RemoveTestSubscriber(sub)
	inA, releaseA := make(chan struct{}), make(chan struct{})
	first := true
	var mu sync.Mutex
	f.m.afterQuotaSet = func() {
		mu.Lock()
		hold := first
		first = false
		mu.Unlock()
		if hold {
			close(inA)
			<-releaseA
		}
	}
	doneA, doneB := make(chan struct{}), make(chan struct{})
	go func() {
		f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(1), Client: appClient2})
		close(doneA)
	}()
	<-inA
	go func() {
		f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(2), Client: appClient2})
		close(doneB)
	}()
	time.Sleep(150 * time.Millisecond) // B reaches the section and waits (or, without the lock, commits and publishes)
	close(releaseA)
	<-doneA
	<-doneB
	var seen []string
	for {
		select {
		case raw := <-sub.SendCh():
			switch {
			case strings.Contains(string(raw), `self_left\":1`):
				seen = append(seen, "1")
			case strings.Contains(string(raw), `self_left\":2`):
				seen = append(seen, "2")
			}
			continue
		default:
		}
		break
	}
	if strings.Join(seen, ",") != "1,2" {
		t.Fatalf("quota events = %v, want 1 then 2 (the commit order)", seen)
	}
	if q, _, _ := f.m.store.RelayQuotaOf("sid-1"); q.SelfLeft != 2 {
		t.Fatalf("stored = %+v, want the last commit's 2", q)
	}
}

// The roster's batch read costs two queries however deep the chains are, and agrees with the single walk.
func TestRelayQuotasOf_AgreesWithTheSingleWalkInTwoReads(t *testing.T) {
	s := openTestStore(t)
	for i := 1; i <= 40; i++ {
		lineage(t, s, fmt.Sprintf("a%d", i), fmt.Sprintf("a%d", i-1))
	}
	lineage(t, s, "b1", "b0")
	if _, _, err := s.SetRelayQuota("a40", ip(7), ip(3), 1, "app"); err != nil {
		t.Fatal(err)
	}
	sids := []string{"a40", "a0", "a20", "b1", "b0", "lone", "a40", ""}
	quotas, roots, err := s.RelayQuotasOf(sids)
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range sids[:6] {
		q, root, err := s.RelayQuotaOf(sid)
		if err != nil || quotas[sid] != q || roots[sid] != root {
			t.Errorf("%s: batch %+v root %q, single %+v root %q (%v)", sid, quotas[sid], roots[sid], q, root, err)
		}
	}
	if quotas["a20"] != (team.RelayQuota{SelfLeft: 7, MemberPoolLeft: 3, Rev: 1}) || roots["b1"] != "b0" || roots["lone"] != "lone" {
		t.Errorf("quotas=%v roots=%v", quotas, roots)
	}
	// a looping chain is left out and reported, the others still read
	if _, err := s.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES ('a0', 'a40', '_x', 'opx', 1)`); err != nil {
		t.Fatal(err)
	}
	q2, r2, err := s.RelayQuotasOf([]string{"a20", "lone"})
	if !errors.Is(err, ErrLineageCycle) || len(q2) != 1 || r2["lone"] != "lone" {
		t.Fatalf("cycle: %v %v %v", q2, r2, err)
	}
}

// RQ-1a2: the chain row's version. It counts the writes of the row (a chain root's, not a session's), strictly
// increasing; no row reads 0; every carrier shows it. Mutation gate: not incrementing → red.
func TestSetRelayQuota_RevCountsTheWritesOfTheChain(t *testing.T) {
	s := openTestStore(t)
	lineage(t, s, "b", "a")
	if q, _, _ := s.RelayQuotaOf("b"); q.Rev != 0 {
		t.Fatalf("no row: rev %d, want 0", q.Rev)
	}
	for want, sid := range []string{"a", "b", "a"} { // writes through any session of the chain bump the one row
		if _, row, err := s.SetRelayQuota(sid, ip(want), nil, int64(10+want), "app"); err != nil || row.Rev != int64(want+1) {
			t.Fatalf("write %d via %s: rev %d (%v), want %d", want+1, sid, row.Rev, err, want+1)
		}
	}
	if q, _, _ := s.RelayQuotaOf("b"); q.Rev != 3 {
		t.Fatalf("rev %d, want 3", q.Rev)
	}
	if _, row, _ := s.SetRelayQuota("other", ip(1), nil, 99, "app"); row.Rev != 1 {
		t.Fatalf("another chain starts at 1, got %d", row.Rev)
	}
}

// The PUT answer, the event, the roster and the list all carry the version. Mutation gate: leave rev out of the event → red.
func TestRelayQuota_EveryCarrierShowsTheRev(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	sub := f.core.Events.AddTestSubscriber()
	defer f.core.Events.RemoveTestSubscriber(sub)
	f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(1), Client: appClient2})
	_, v, body := f.putQuota(team.RelayQuotaPutRequest{SessionID: "sid-1", SelfLeft: ip(2), Client: appClient2})
	if v.Rev != 2 {
		t.Fatalf("PUT answer rev %d (%s), want 2", v.Rev, body)
	}
	got := ""
	for {
		select {
		case raw := <-sub.SendCh():
			if strings.Contains(string(raw), team.RelayQuotaEventType) {
				got = string(raw) // the last one
			}
			continue
		default:
		}
		break
	}
	if !strings.Contains(got, `rev\":2`) {
		t.Fatalf("last event = %s, want rev 2", got)
	}
	if rs := f.getRoster(); rs.Teams[0].Lead.RelayQuota.Rev != 2 {
		t.Fatalf("roster lead rev %d, want 2", rs.Teams[0].Lead.RelayQuota.Rev)
	}
	_, uv, _ := f.getUnattended("")
	revs := map[string]int64{}
	for _, q := range uv.Quotas {
		revs[q.SessionID] = q.Rev
	}
	if revs["sid-1"] != 2 || revs["sid-2"] != 0 {
		t.Fatalf("list revs = %v, want sid-1 2 and sid-2 0 (no row)", revs)
	}
}

// The list says whether it could be read: null when it could not (the client keeps what it had), [] when it was read
// and there is nothing, never absent. Mutation gate: omitempty back on quotas → red.
func TestUnattendedView_QuotasNullWhenUnreadableEmptyWhenNone(t *testing.T) {
	f := newFixture(t)
	f.origins.hide("sid-1")
	f.origins.hide("sid-2")
	_, raw := f.do(http.MethodGet, UnattendedRoute, nil)
	if !strings.Contains(string(raw), `"quotas":[]`) {
		t.Fatalf("read, none live: %s, want \"quotas\":[]", raw)
	}
	f.origins.setReadErr(true)
	_, raw = f.do(http.MethodGet, UnattendedRoute, nil)
	if !strings.Contains(string(raw), `"quotas":null`) {
		t.Fatalf("unreadable: %s, want \"quotas\":null", raw)
	}
}

// The table is deployed without rev (RQ-1a, alpha.634): a database of that shape gets the column when it is opened,
// keeps its rows (rev 0) and counts from there. Mutation gate: drop migrateRelayQuotaRev → red.
func TestOpenStore_AddsRelayQuotaRevToTheDeployedShape(t *testing.T) {
	path := t.TempDir() + "/team.db"
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE relay_quotas;
		CREATE TABLE relay_quotas (
			root_session_id  TEXT PRIMARY KEY,
			self_left        INTEGER NOT NULL DEFAULT 0,
			member_pool_left INTEGER NOT NULL DEFAULT 0,
			updated_at       INTEGER NOT NULL,
			updated_by       TEXT    NOT NULL DEFAULT ''
		);
		INSERT INTO relay_quotas VALUES ('r', 3, 1, 5, 'app')`); err != nil { // the shape on the host's disk
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q, _, err := s.RelayQuotaOf("r")
	if err != nil || q != (team.RelayQuota{SelfLeft: 3, MemberPoolLeft: 1, Rev: 0}) {
		t.Fatalf("after the migration: %+v (%v), want the row kept at rev 0", q, err)
	}
	if _, row, err := s.SetRelayQuota("r", ip(2), nil, 9, "app"); err != nil || row.Rev != 1 || row.SelfLeft != 2 || row.MemberPoolLeft != 1 {
		t.Fatalf("first write after: %+v (%v), want rev 1", row, err)
	}
	// and opening it once more is a no-op
	_ = s.Close()
	if s, err = OpenStore(path); err != nil {
		t.Fatalf("second open: %v", err)
	}
}

// A quota read that fails in the store is null too, not an empty or partial list. Mutation gate: continue past the
// error → red.
func TestUnattendedView_QuotasNullWhenTheStoreCannotBeRead(t *testing.T) {
	f := newFixture(t)
	if _, err := f.m.store.db.Exec(`DROP TABLE relay_quotas`); err != nil {
		t.Fatal(err)
	}
	_, raw := f.do(http.MethodGet, UnattendedRoute, nil)
	if !strings.Contains(string(raw), `"quotas":null`) {
		t.Fatalf("store unreadable: %s, want \"quotas\":null", raw)
	}
	// a looping chain in one session: the same
	f2 := newFixture(t)
	lineage(t, f2.m.store, "sid-2", "sid-9")
	if _, err := f2.m.store.db.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at) VALUES ('sid-9', 'sid-2', '_x', 'opx', 1)`); err != nil {
		t.Fatal(err)
	}
	_, raw = f2.do(http.MethodGet, UnattendedRoute, nil)
	if !strings.Contains(string(raw), `"quotas":null`) {
		t.Fatalf("looping chain: %s, want \"quotas\":null", raw)
	}
}
