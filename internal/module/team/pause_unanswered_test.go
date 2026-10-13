// internal/module/team/pause_unanswered_test.go — the final self-relay card and the pause cause relay_unanswered (RT-2a1).
package teammod

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// T14. Mutation gate: skip the rebuild → the insert of a relay_unanswered row fails on the old CHECK (red); drop a column from the
// copy → the old rows lose it (red).
func TestMigratePauseCauses_RebuildsAnOldTableKeepsRows(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`DROP TABLE session_pauses`,
		`CREATE TABLE session_pauses (
			session_id      TEXT    NOT NULL,
			cause           TEXT    NOT NULL CHECK (cause IN ('quota', 'lead')),
			role            TEXT    NOT NULL CHECK (role IN ('solo', 'lead', 'member')),
			team_id         TEXT    NOT NULL DEFAULT '',
			lead_session_id TEXT    NOT NULL DEFAULT '',
			ref             TEXT    NOT NULL DEFAULT '',
			title           TEXT    NOT NULL DEFAULT '',
			on_since        INTEGER NOT NULL,
			since           INTEGER NOT NULL,
			PRIMARY KEY (session_id, cause)
		)`,
		`INSERT INTO session_pauses VALUES ('s-1', 'quota', 'lead', 't-1', 's-1', '_aaaaaa', 'one', 5, 10)`,
		`INSERT INTO session_pauses VALUES ('s-2', 'lead', 'member', 't-1', 's-1', '_bbbbbb', 'two', 5, 11)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO session_pauses VALUES ('s-3', 'relay_unanswered', 'solo', '', '', '', '', 0, 12)`); err == nil {
		t.Fatal("the old table accepted relay_unanswered: the fixture is not an old table")
	}
	for i := 0; i < 2; i++ { // twice: the second finds the cause and does nothing
		if err := migratePauseCauses(s.db); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Pauses()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows after the rebuild = %+v err=%v, want the two old ones", rows, err)
	}
	if rows[0] != (PauseRow{SessionID: "s-1", Cause: "quota", Role: "lead", TeamID: "t-1", LeadSessionID: "s-1", Ref: "_aaaaaa", Title: "one", OnSince: 5, Since: 10}) ||
		rows[1].SessionID != "s-2" || rows[1].Cause != "lead" || rows[1].Role != "member" || rows[1].Title != "two" {
		t.Fatalf("rows changed by the rebuild: %+v", rows)
	}
	if created, err := s.AddPause(PauseRow{SessionID: "s-3", Cause: team.PauseCauseUnanswered, Role: team.PauseRoleSolo, Since: 12}); err != nil || !created {
		t.Fatalf("a relay_unanswered row after the rebuild: created=%v err=%v", created, err)
	}
	if _, err := s.db.Exec(`INSERT INTO session_pauses VALUES ('s-4', 'bogus', 'solo', '', '', '', '', 0, 12)`); err == nil {
		t.Fatal("the rebuilt table accepts any cause: the CHECK is gone")
	}
}

func TestApprovals_UnansweredAtColumnOnAnOldTable(t *testing.T) {
	s := openTestStore(t)
	a := team.Approval{ID: "a-1", Kind: team.KindSelfRelay, HostID: "h", Origin: team.Origin{SessionID: "sid-1"}, Payload: []byte(`{}`), State: team.StateOpen, CreatedAt: 1, DeadlineAt: 2, LeaseUntil: 3}
	if _, _, _, err := s.Create(a, "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE approval_requests DROP COLUMN unanswered_at`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := ensureColumn(s.db, "approval_requests", "unanswered_at", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("a-1")
	if err != nil || !ok || got.UnansweredAt != 0 || got.DeadlineAt != 2 {
		t.Fatalf("old row = %+v ok=%v err=%v, want it kept with unanswered_at 0", got, ok, err)
	}
}

// ---- T15: a final card at its deadline ----

// finalCard opens a self-relay card of sid at pct and returns it.
func (f *fixture) finalCard(sid string, pct float64) team.RelayBeginResponse {
	f.t.Helper()
	r := beginReq(sid)
	r.UsedPercentage = pct
	code, body := f.do(http.MethodPost, "/api/relay/begin", r)
	if code != http.StatusCreated {
		f.t.Fatalf("begin %s at %.0f%%: %d %s", sid, pct, code, body)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

// pastDeadline moves the clock past a self relay's deadline and runs one sweep.
func (f *fixture) pastDeadline() {
	f.t.Helper()
	f.clock.Add(team.SelfRelayDeadlineS*1000 + 1)
	f.m.tick()
}

func (f *fixture) row(id string) team.Approval {
	f.t.Helper()
	a, ok, err := f.m.store.Get(id)
	if err != nil || !ok {
		f.t.Fatalf("row %s: ok=%v err=%v", id, ok, err)
	}
	return a
}

// Mutation gate: let the final card time out as any other → the row is `timeout` and there is no pause (red); mark twice → a second
// notice (red).
func TestFinalCard_AtItsDeadlineStaysOpenAndPausesTheSession(t *testing.T) {
	f := newFixture(t)
	card := f.finalCard("sid-1", 91)
	f.events()
	f.pastDeadline()
	a := f.row(card.RequestID)
	if a.State != team.StateOpen || a.UnansweredAt != f.clock.Load() || a.DeadlineAt != team.UnansweredDeadlineMs {
		t.Fatalf("card = %s unanswered_at=%d deadline=%d, want open, marked now, the sentinel deadline", a.State, a.UnansweredAt, a.DeadlineAt)
	}
	if p, ok := f.pauses()["sid-1/relay_unanswered"]; !ok || p.Role != team.PauseRoleSolo || p.Since != f.clock.Load() {
		t.Fatalf("pause rows = %+v, want a solo relay_unanswered row now", f.pauses())
	}
	var kinds []string
	for _, o := range f.outbox() {
		kinds = append(kinds, o.Kind+":"+o.Text)
	}
	if len(kinds) != 2 || kinds[0] != "notice:"+team.PauseNoticeUnanswered || !strings.HasPrefix(kinds[1], "push:_abc123") || !strings.HasSuffix(kinds[1], "已暫停：接力申請沒有回應，等你核准") {
		t.Fatalf("outbox = %v, want the notice then the push", kinds)
	}
	if ops := f.opsOf(); len(ops) != 0 {
		t.Fatalf("events = %v, want none (no new approval op)", ops)
	}
	f.m.tick() // once
	f.clock.Add(1000)
	f.m.tick()
	if n := len(f.outbox()); n != 2 {
		t.Fatalf("outbox has %d rows after more ticks, want 2 (the mark happens once)", n)
	}
	if op := f.op(card.Op.ID); op.State != team.RelayAwaitingApproval {
		t.Fatalf("op = %s, want still awaiting approval", op.State)
	}
}

// The cards that are not final time out as before. Mutation gate: let a manual card be final → it stays open (red).
func TestFinalCard_OthersTimeOutAsToday(t *testing.T) {
	cases := []struct {
		name string
		open func(f *fixture) team.RelayBeginResponse
	}{
		{"89 %", func(f *fixture) team.RelayBeginResponse { return f.finalCard("sid-1", 89) }},
		{"an idle ask at 95 %", func(f *fixture) team.RelayBeginResponse {
			r := beginReq("sid-1")
			r.UsedPercentage, r.Reason, r.IdleMinutes = 95, team.RelayBeginReasonIdle, 40
			code, body := f.do(http.MethodPost, "/api/relay/begin", r)
			if code != http.StatusCreated {
				t.Fatalf("idle begin: %d %s", code, body)
			}
			var out team.RelayBeginResponse
			_ = json.Unmarshal(body, &out)
			return out
		}},
		{"a manual card at 95 %", func(f *fixture) team.RelayBeginResponse {
			r := manualReq("sid-1")
			r.UsedPercentage = 95
			code, body := f.do(http.MethodPost, "/api/relay/begin", r)
			if code != http.StatusCreated {
				t.Fatalf("manual begin: %d %s", code, body)
			}
			var out team.RelayBeginResponse
			_ = json.Unmarshal(body, &out)
			return out
		}},
	}
	for _, c := range cases {
		f := newFixture(t)
		card := c.open(f)
		f.pastDeadline()
		if a := f.row(card.RequestID); a.State != team.StateTimeout || a.UnansweredAt != 0 {
			t.Fatalf("%s: card = %s unanswered_at=%d, want timeout", c.name, a.State, a.UnansweredAt)
		}
		if len(f.pauses()) != 0 {
			t.Fatalf("%s: pause rows %+v, want none", c.name, f.pauses())
		}
	}
}

func TestIsFinalCard_TheRule(t *testing.T) {
	row := func(p team.SelfRelayPayload) team.Approval {
		b, _ := json.Marshal(p)
		return team.Approval{Kind: team.KindSelfRelay, Payload: b}
	}
	for _, c := range []struct {
		name string
		a    team.Approval
		want bool
	}{
		{"90 %", row(team.SelfRelayPayload{UsedPercentage: 90}), true},
		{"89.9 %", row(team.SelfRelayPayload{UsedPercentage: 89.9}), false},
		{"a re-ask card is final whatever the use", row(team.SelfRelayPayload{UsedPercentage: 50, Reask: true}), true},
		{"idle", row(team.SelfRelayPayload{UsedPercentage: 99, Reason: team.RelayBeginReasonIdle}), false},
		{"manual", row(team.SelfRelayPayload{UsedPercentage: 99, Manual: true}), false},
		{"a lead request", team.Approval{Kind: team.KindLead, Payload: []byte(`{"used_percentage":99}`)}, false},
		{"junk payload", team.Approval{Kind: team.KindSelfRelay, Payload: []byte(`junk`)}, false},
	} {
		if got := isFinalCard(c.a); got != c.want {
			t.Errorf("%s: isFinalCard = %v, want %v", c.name, got, c.want)
		}
	}
}

// A lead stops alone: its members are not paused (S-6). Mutation gate: pause the members too → red.
func TestFinalCard_ALeadStopsAloneItsMembersKeepWorking(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", f.clock.Load())
	seedMember(t, f.m.store, "op-m", uid(1), "sid-m1", f.clock.Load())
	f.finalCard("sid-1", 95)
	f.pastDeadline()
	got := f.pauses()
	if p, ok := got["sid-1/relay_unanswered"]; !ok || p.Role != team.PauseRoleLead || p.TeamID != uid(1) || p.LeadSessionID != "sid-1" {
		t.Fatalf("pause rows = %+v, want the lead's relay_unanswered row", got)
	}
	if len(got) != 1 {
		t.Fatalf("pause rows = %+v, want only the lead's", got)
	}
}

// Never abandoned for its lease (S-13); abandoned when the session is gone. Mutation gate: drop the guard → the lease closes it (red).
func TestFinalCard_IsNotAbandonedForItsLeaseButForAGoneSession(t *testing.T) {
	f := newFixture(t)
	card := f.finalCard("sid-1", 91)
	f.pastDeadline()
	f.clock.Add(10 * 60_000)
	f.m.tick()
	if a := f.row(card.RequestID); a.State != team.StateOpen {
		t.Fatalf("card after 10 minutes = %s, want still open", a.State)
	}
	f.origins.markDead("sid-1")
	f.m.tickN = livenessEvery - 1
	f.m.tick()
	if a := f.row(card.RequestID); a.State != team.StateAbandoned {
		t.Fatalf("card with its session gone = %s, want abandoned", a.State)
	}
	if _, ok := f.pauses()["sid-1/relay_unanswered"]; ok {
		t.Fatal("the pause outlived the card")
	}
}

// A GET /api/relay/wait in flight returns at the mark, state open with unanswered_at.
func TestFinalCard_AWaitInFlightReturnsAtTheMark(t *testing.T) {
	f := newFixture(t)
	card := f.finalCard("sid-1", 91)
	done := make(chan team.Approval, 1)
	go func() {
		code, body := f.do(http.MethodGet, "/api/relay/wait/"+card.RequestID+"?wait=25", nil)
		var a team.Approval
		if code == http.StatusOK {
			_ = json.Unmarshal(body, &a)
		}
		done <- a
	}()
	waitFor(t, func() bool { return f.waitersOn(card.RequestID) == 1 })
	f.pastDeadline()
	select {
	case a := <-done:
		if a.State != team.StateOpen || a.UnansweredAt == 0 {
			t.Fatalf("wait answered %s unanswered_at=%d, want open and marked", a.State, a.UnansweredAt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the long poll was not woken by the mark")
	}
}

// ---- T16: leaving relay_unanswered, one test per way out ----

func (f *fixture) unanswered(sid string) team.RelayBeginResponse {
	f.t.Helper()
	card := f.finalCard(sid, 91)
	f.pastDeadline()
	if _, ok := f.pauses()[sid+"/relay_unanswered"]; !ok {
		f.t.Fatal("setup: the card did not pause the session")
	}
	return card
}

// notices lists the pause_outbox texts after the setup ones.
func (f *fixture) noticesOf(sid string) []string {
	f.t.Helper()
	var out []string
	for _, o := range f.outbox() {
		if o.Session == sid && o.Kind == team.PauseOutboxNotice {
			out = append(out, o.Text)
		}
	}
	return out
}

func TestUnanswered_EveryWayOut_EndsThePauseWithTheRightNotice(t *testing.T) {
	cases := []struct {
		name string
		out  func(f *fixture, card team.RelayBeginResponse)
		want string
	}{
		{"approve", func(f *fixture, c team.RelayBeginResponse) {
			if code, body := f.decide(c.RequestID, "approve"); code != http.StatusOK {
				t.Fatalf("approve: %d %s", code, body)
			}
		}, team.PauseResumedUnansweredApproved},
		{"deny", func(f *fixture, c team.RelayBeginResponse) {
			if code, body := f.decide(c.RequestID, "deny"); code != http.StatusOK {
				t.Fatalf("deny: %d %s", code, body)
			}
		}, team.PauseResumedUnansweredDenied},
		{"DELETE", func(f *fixture, c team.RelayBeginResponse) {
			if code, body := f.do(http.MethodDelete, "/api/team/approvals/"+c.RequestID, nil); code != http.StatusOK {
				t.Fatalf("delete: %d %s", code, body)
			}
		}, team.PauseResumedUnansweredEnded},
		{"a compaction report", func(f *fixture, c team.RelayBeginResponse) {
			code, body := f.do(http.MethodPost, "/api/relay/ops/"+c.Op.ID+"/report", team.RelayReportRequest{State: team.RelayCancelled, Error: "compacted"})
			if code != http.StatusOK {
				t.Fatalf("report: %d %s", code, body)
			}
		}, team.PauseResumedUnansweredEnded},
		{"the session is gone", func(f *fixture, c team.RelayBeginResponse) {
			f.origins.markDead("sid-1")
			f.m.tickN = livenessEvery - 1
			f.m.tick()
		}, team.PauseResumedUnansweredEnded},
	}
	for _, c := range cases {
		f := newFixture(t)
		card := f.unanswered("sid-1")
		if _, err := f.m.store.db.Exec(`INSERT INTO pause_bounces (session_id, from_addr, text_hash, at, first_line) VALUES ('sid-1', 'a', 'h1', 1, 'x')`); err != nil {
			t.Fatal(err)
		}
		c.out(f, card)
		if _, ok := f.pauses()["sid-1/relay_unanswered"]; ok {
			t.Fatalf("%s: the pause row is still there", c.name)
		}
		notices := f.noticesOf("sid-1")
		if len(notices) < 2 || notices[len(notices)-2] != c.want || !strings.HasPrefix(notices[len(notices)-1], "[pdx team] 暫停期間有 1 則訊息被退回") {
			t.Fatalf("%s: notices = %q, want the resume text then the bounce list", c.name, notices)
		}
		var bounced int
		_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM pause_bounces WHERE session_id = 'sid-1'`).Scan(&bounced)
		if bounced != 0 {
			t.Fatalf("%s: %d bounces left", c.name, bounced)
		}
		for _, o := range f.outbox() {
			if o.Kind == team.PauseOutboxReask {
				t.Fatalf("%s: a reask control was written", c.name)
			}
		}
	}
}

// A session that also holds `quota` stays paused and is told nothing yet. Mutation gate: tell it anyway → red.
func TestUnanswered_AnotherCauseStandsNothingIsToldYet(t *testing.T) {
	f := newFixture(t)
	card := f.unanswered("sid-1")
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_aaaaaa", "t", 1)
	before := len(f.noticesOf("sid-1"))
	if code, body := f.decide(card.RequestID, "approve"); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	if _, ok := f.pauses()["sid-1/quota"]; !ok {
		t.Fatal("the quota pause went with it")
	}
	if _, ok := f.pauses()["sid-1/relay_unanswered"]; ok {
		t.Fatal("the unanswered pause stayed")
	}
	if got := len(f.noticesOf("sid-1")); got != before {
		t.Fatalf("notices grew from %d to %d: told too soon", before, got)
	}
}

// ---- T17: hello hands an unanswered card to a reloaded mod ----

func TestRelayHello_CarriesAnUnansweredCardOnly(t *testing.T) {
	f := newFixture(t)
	card := f.finalCard("sid-1", 91)
	if _, h, _ := f.hello("sid-1"); h.Waiting != nil {
		t.Fatalf("waiting = %+v for an ordinary open card, want none", h.Waiting)
	}
	f.pastDeadline()
	_, h, body := f.hello("sid-1")
	if h.Waiting == nil || h.Waiting.RequestID != card.RequestID || h.Waiting.Op.ID != card.Op.ID || h.Waiting.UnansweredAt != f.clock.Load() {
		t.Fatalf("waiting = %+v (%s), want the unanswered card", h.Waiting, body)
	}
	if code, _ := f.decide(card.RequestID, "approve"); code != http.StatusOK {
		t.Fatal("approve")
	}
	if _, h, _ := f.hello("sid-1"); h.Waiting != nil {
		t.Fatalf("waiting = %+v after the card was answered, want none", h.Waiting)
	}
}

// codex R1 P1: the earliest session_pauses (no ref, no title, the two-cause CHECK) opens: ref/title are added before the rebuild
// copies them. Mutation gate: run the rebuild first → OpenStore fails on the missing columns (red).
func TestOpenStore_TheEarliestPauseSchemaOpensAndKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE session_pauses`,
		`CREATE TABLE session_pauses (
			session_id      TEXT    NOT NULL,
			cause           TEXT    NOT NULL CHECK (cause IN ('quota', 'lead')),
			role            TEXT    NOT NULL CHECK (role IN ('solo', 'lead', 'member')),
			team_id         TEXT    NOT NULL DEFAULT '',
			lead_session_id TEXT    NOT NULL DEFAULT '',
			on_since        INTEGER NOT NULL,
			since           INTEGER NOT NULL,
			PRIMARY KEY (session_id, cause)
		)`,
		`INSERT INTO session_pauses VALUES ('s-1', 'quota', 'solo', '', '', 5, 10)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatalf("open an old team.db: %v", err)
	}
	defer s.Close()
	rows, err := s.Pauses()
	if err != nil || len(rows) != 1 || rows[0].SessionID != "s-1" || rows[0].Cause != "quota" || rows[0].Since != 10 {
		t.Fatalf("rows = %+v err=%v, want the old one kept", rows, err)
	}
	if created, err := s.AddPause(PauseRow{SessionID: "s-2", Cause: team.PauseCauseUnanswered, Role: team.PauseRoleSolo, Since: 11}); err != nil || !created {
		t.Fatalf("relay_unanswered after the upgrade: created=%v err=%v", created, err)
	}
}
