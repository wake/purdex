// internal/module/team/pause_enter_test.go — entering a pause at a self / idle relay begin (unattended quota pause spec §3.3, UP-2a).
package teammod

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

type outboxRow struct {
	Seq           int64
	Session, Kind string
	Text, ReaskID string
	Attempts      int
}

func (f *fixture) outbox() []outboxRow {
	f.t.Helper()
	rows, err := f.m.store.db.Query(`SELECT seq, session_id, kind, text, reask_id, attempts FROM pause_outbox ORDER BY seq`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.Seq, &r.Session, &r.Kind, &r.Text, &r.ReaskID, &r.Attempts); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// pausesBySession lists the pause rows, one entry per (session, cause).
func (f *fixture) pauses() map[string]PauseRow {
	f.t.Helper()
	rows, err := f.m.store.Pauses()
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]PauseRow{}
	for _, r := range rows {
		out[r.SessionID+"/"+r.Cause] = r
	}
	return out
}

// quotaWorld: the host is unattended, the relay-quota rule is on.
func (f *fixture) quotaWorld() {
	f.t.Helper()
	f.unatt.set(true)
	f.qrule.set(true, nil)
}

func (f *fixture) beginRaw(sid string) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/relay/begin", beginReq(sid))
}

func (f *fixture) opCount() int {
	f.t.Helper()
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM relay_ops`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) approvalRows() int {
	f.t.Helper()
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM approval_requests WHERE kind = 'self_relay'`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// Quota left: today's begin — the op claimed, the unit spent, nothing paused.
func TestPauseEnter_QuotaLeftOpensTodaysOp(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	f.setQuota("sid-1", 1)
	if code, body := f.beginRaw("sid-1"); code != http.StatusCreated {
		t.Fatalf("begin: %d %s", code, body)
	}
	if f.selfLeft("sid-1") != 0 || f.opCount() != 1 || len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("self_left %d, ops %d, pauses %v, outbox %v", f.selfLeft("sid-1"), f.opCount(), f.pauses(), f.outbox())
	}
}

// No quota, unattended on: 409 paused_no_quota, no op and no approval row, the session paused (ref / title recorded, the switch's
// since), and the outbox holds the notice then the push.
func TestPauseEnter_NoQuotaPausesTheSessionInsteadOfOpeningACard(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	code, body := f.beginRaw("sid-1")
	if c, _ := wireErr(t, body); code != http.StatusConflict || c != team.ErrPausedNoQuota {
		t.Fatalf("begin: %d %s", code, body)
	}
	if f.opCount() != 0 || f.approvalRows() != 0 {
		t.Fatalf("ops %d, approval rows %d, want none", f.opCount(), f.approvalRows())
	}
	p, ok := f.pauses()["sid-1/quota"]
	if !ok || len(f.pauses()) != 1 || p.Role != team.PauseRoleSolo || p.Ref != "_abc123" || p.Title != "boss" || p.OnSince != 1 || p.Since == 0 || p.TeamID != "" {
		t.Fatalf("pauses = %+v", f.pauses())
	}
	ob := f.outbox()
	if len(ob) != 2 ||
		ob[0].Session != "sid-1" || ob[0].Kind != team.PauseOutboxNotice || ob[0].Text != team.PauseNoticeSelf ||
		ob[1].Session != "sid-1" || ob[1].Kind != team.PauseOutboxPush || ob[1].Text != "boss 已暫停：接力額度用完，等你接手" {
		t.Fatalf("outbox = %+v", ob)
	}
}

// A lead takes its live local members with it: each gets a `lead` row and the notice; the push counts them.
// Mutation gate: skip the cascade → the members are not paused (red).
func TestPauseEnter_ALeadPausesItsLiveLocalMembers(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, f.m.store, "op-2", uid(1), "sid-m2", 3000)
	seedMember(t, f.m.store, "op-k", uid(1), "sid-killing", 3500)
	if err := f.m.store.SetMemberState("op-k", team.MemberKilling, 3600); err != nil {
		t.Fatal(err)
	}
	remote := newMember("op-r", uid(1), "sid-remote", "_remote", 3700)
	remote.HostID = "other:2"
	if err := f.m.store.InsertMember(remote); err != nil {
		t.Fatal(err)
	}
	if code, body := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatalf("begin: %d %s", code, body)
	}
	ps := f.pauses()
	if len(ps) != 3 || ps["sid-1/quota"].Role != team.PauseRoleLead || ps["sid-1/quota"].TeamID != uid(1) {
		t.Fatalf("pauses = %+v", ps)
	}
	for _, sid := range []string{"sid-m1", "sid-m2"} {
		p, ok := ps[sid+"/lead"]
		if !ok || p.Role != team.PauseRoleMember || p.TeamID != uid(1) || p.LeadSessionID != "sid-1" || p.OnSince != 1 || p.Ref == "" {
			t.Fatalf("%s: %+v (all %+v)", sid, p, ps)
		}
	}
	ob := f.outbox()
	want := []outboxRow{
		{Session: "sid-1", Kind: team.PauseOutboxNotice, Text: team.PauseNoticeSelf},
		{Session: "sid-m1", Kind: team.PauseOutboxNotice, Text: team.PauseNoticeLeadPaused},
		{Session: "sid-m2", Kind: team.PauseOutboxNotice, Text: team.PauseNoticeLeadPaused},
		{Session: "sid-1", Kind: team.PauseOutboxPush, Text: "boss 已暫停：接力額度用完，等你接手，含 2 個 member"},
	}
	if len(ob) != len(want) {
		t.Fatalf("outbox = %+v", ob)
	}
	for i := range want {
		if ob[i].Session != want[i].Session || ob[i].Kind != want[i].Kind || ob[i].Text != want[i].Text {
			t.Fatalf("outbox[%d] = %+v, want %+v", i, ob[i], want[i])
		}
	}
}

// Unattended off: nothing about quota applies — the request opens as a card, as always. The rule off: the begin is approved
// without spending, so there is nothing to run out of.
func TestPauseEnter_OnlyWithTheSwitchAndTheRuleOn(t *testing.T) {
	f := newFixture(t)
	f.qrule.set(true, nil) // unattended off
	if code, body := f.beginRaw("sid-1"); code != http.StatusCreated {
		t.Fatalf("switch off: %d %s", code, body)
	}
	if f.opCount() != 1 || len(f.pauses()) != 0 {
		t.Fatalf("switch off: ops %d, pauses %v", f.opCount(), f.pauses())
	}

	g := newFixture(t)
	g.unatt.set(true) // rule off
	if code, body := g.beginRaw("sid-1"); code != http.StatusCreated {
		t.Fatalf("rule off: %d %s", code, body)
	}
	if len(g.pauses()) != 0 {
		t.Fatalf("rule off: pauses %v", g.pauses())
	}
}

// Already paused: 409, no new rows, no second outbox batch.
func TestPauseEnter_AnAlreadyPausedSessionGetsTheSameAnswerAndNoNewRows(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	if code, _ := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatal(code)
	}
	before, n := len(f.pauses()), len(f.outbox())
	code, body := f.beginRaw("sid-1")
	if c, _ := wireErr(t, body); code != http.StatusConflict || c != team.ErrPausedNoQuota {
		t.Fatalf("second begin: %d %s", code, body)
	}
	// even with quota available now: the session stays paused until it is released
	f.setQuota("sid-1", 3)
	if code, _ := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatalf("with quota raised: %d", code)
	}
	if len(f.pauses()) != before || len(f.outbox()) != n || f.opCount() != 0 || f.selfLeft("sid-1") != 3 {
		t.Fatalf("pauses %d→%d, outbox %d→%d, ops %d, self_left %d", before, len(f.pauses()), n, len(f.outbox()), f.opCount(), f.selfLeft("sid-1"))
	}
}

// The quota check is inside the transaction that holds the writer lock: a quota PUT cannot slip in between the check and the
// pause. The hook runs right after the lock is taken; a PUT started there stays blocked until the begin has committed.
// Mutation gate: take the lock after the quota check (or not at all) → the PUT completes inside the hook (red).
func TestPauseEnter_ABeginHoldsTheWriterLockWhileItChecksTheQuota(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	putDone := make(chan struct{})
	var once sync.Once
	f.m.store.afterPauseLock = func() {
		once.Do(func() {
			go func() {
				three := 3
				_, _, _ = f.m.store.SetRelayQuota("sid-1", &three, nil, 5, "app")
				close(putDone)
			}()
			select {
			case <-putDone:
				t.Error("a quota PUT completed while the begin held the writer lock")
			case <-time.After(150 * time.Millisecond):
			}
		})
	}
	code, body := f.beginRaw("sid-1")
	f.m.store.afterPauseLock = nil
	if c, _ := wireErr(t, body); code != http.StatusConflict || c != team.ErrPausedNoQuota {
		t.Fatalf("begin: %d %s (the PUT had not landed yet: no quota)", code, body)
	}
	select {
	case <-putDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the PUT never completed after the begin committed")
	}
	if f.selfLeft("sid-1") != 3 {
		t.Fatalf("self_left = %d, want the PUT's 3", f.selfLeft("sid-1"))
	}
}

// The other order: the PUT first, the begin spends. And a race of the two never ends in both an op and a pause.
func TestPauseEnter_BeginAndQuotaPutRaceForTheLastUnit(t *testing.T) {
	f := newFixture(t)
	f.quotaWorld()
	f.setQuota("sid-1", 1)
	if code, body := f.beginRaw("sid-1"); code != http.StatusCreated {
		t.Fatalf("put first: %d %s", code, body)
	}
	if f.selfLeft("sid-1") != 0 || len(f.pauses()) != 0 {
		t.Fatalf("self_left %d, pauses %v", f.selfLeft("sid-1"), f.pauses())
	}
	for round := 0; round < 25; round++ {
		g := newFixture(t)
		g.quotaWorld()
		g.setQuota("sid-1", 1)
		var wg sync.WaitGroup
		var code int
		wg.Add(2)
		go func() { defer wg.Done(); code, _ = g.beginRaw("sid-1") }()
		go func() {
			defer wg.Done()
			zero := 0
			_, _, _ = g.m.store.SetRelayQuota("sid-1", &zero, nil, 9, "app")
		}()
		wg.Wait()
		approved, paused := g.opCount() == 1, len(g.pauses()) > 0
		if approved == paused || (approved && code != http.StatusCreated) || (paused && code != http.StatusConflict) || g.selfLeft("sid-1") < 0 {
			t.Fatalf("round %d: code %d, ops %d, pauses %v, self_left %d", round, code, g.opCount(), g.pauses(), g.selfLeft("sid-1"))
		}
	}
}
