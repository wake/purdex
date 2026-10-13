// internal/module/team/pause_resume_test.go — leaving a pause: the switch off, the reconciliation, the resume group, the old-mod
// fallback (unattended quota pause spec 2026-10-12 §3.4, UP-3a). The pause rows are written straight into the store: entering a
// pause is UP-2a / UP-2b's, and the release gate (pauseEntryEnabled) only guards the entering — leaving works with it closed.
package teammod

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

func (f *fixture) seedPause(sid, cause, role, ref, title string, onSince int64) {
	f.t.Helper()
	p := PauseRow{SessionID: sid, Cause: cause, Role: role, Ref: ref, Title: title, OnSince: onSince, Since: f.clock.Load()}
	if role == team.PauseRoleMember {
		p.TeamID, p.LeadSessionID = uid(1), "sid-1"
	} else if role == team.PauseRoleLead {
		p.TeamID, p.LeadSessionID = uid(1), "sid-1"
	}
	if _, err := f.m.store.AddPause(p); err != nil {
		f.t.Fatal(err)
	}
	f.clock.Add(10)
}

// modHello makes sid's mod say hello at ver (the session must be in the registry).
func (f *fixture) modHello(sid, ver string) {
	f.t.Helper()
	if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: sid, ModVersion: ver, Agent: "cc"}); code != http.StatusOK {
		f.t.Fatalf("hello %s: %d %s", sid, code, body)
	}
}

func (f *fixture) switchOff() {
	f.t.Helper()
	off := false
	if code, _, raw := f.putUnattended(team.UnattendedPutRequest{On: &off, Client: team.Client{Kind: "app", Label: "t"}}); code != http.StatusOK {
		f.t.Fatalf("switch off: %d %s", code, raw)
	}
}

func (f *fixture) reconcile() {
	f.t.Helper()
	f.m.reconcilePauses(true)
}

func (f *fixture) tokens() []string {
	f.t.Helper()
	rows, err := f.m.store.db.Query(`SELECT token FROM pause_release_tokens ORDER BY token`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// leadWorld: sid-1 leads a team with the live member sid-m1, unattended on (Since 1), both mods new (protocol 5).
func leadWorld(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.memberTeam("5")
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	f.modHello("sid-1", "5")
	f.unatt.set(true)
	return f
}

// Switch off resumes every row; the lead's rows go out before its member's (outbox FIFO is the order). The lead gets its notice, then
// a token and the reask control; the member of a paused lead gets 「lead 已恢復」 and nothing else.
// Mutation gate: enqueue the members before the lead → the order check is red.
func TestResume_SwitchOffResumesEveryRowLeadBeforeMembers(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-m1", team.PauseCauseLead, team.PauseRoleMember, "_mem001", "worker", 1) // the member's row is the older one
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.switchOff()
	if n := len(f.pauses()); n != 0 {
		t.Fatalf("%d pause rows left after the switch went off", n)
	}
	ob := f.outbox()
	if len(ob) != 3 {
		t.Fatalf("outbox = %+v, want lead notice, lead reask, member notice", ob)
	}
	toks := f.tokens()
	if len(toks) != 1 {
		t.Fatalf("tokens = %v", toks)
	}
	if ob[0].Session != "sid-1" || ob[0].Kind != team.PauseOutboxNotice || ob[0].Text != team.PauseResumedSolo ||
		ob[1].Session != "sid-1" || ob[1].Kind != team.PauseOutboxReask || ob[1].ReaskID != toks[0] || ob[1].Text != team.PauseReaskControlPrefix+toks[0] ||
		ob[2].Session != "sid-m1" || ob[2].Kind != team.PauseOutboxNotice || ob[2].Text != team.PauseResumedMemberLead {
		t.Fatalf("outbox = %+v (token %s)", ob, toks[0])
	}
	if _, err := uuid.Parse(toks[0]); err != nil {
		t.Fatalf("token %q is not a UUID", toks[0])
	}
}

// A member that still has another cause is told nothing; only the last cause going away sends the resume notice.
// Mutation gate: send the resume notice for every deleted row → the member's notice appears (red).
func TestResume_OnlyTheLastCauseSendsTheResumeNotice(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 0) // an earlier on-period: resumed
	f.seedPause("sid-m1", team.PauseCauseLead, team.PauseRoleMember, "_mem001", "worker", 0)
	f.seedPause("sid-m1", team.PauseCauseQuota, team.PauseRoleMember, "_mem001", "worker", 1) // this on-period: stays
	f.reconcile()
	ps := f.pauses()
	if _, ok := ps["sid-m1/quota"]; !ok || len(ps) != 1 {
		t.Fatalf("pauses = %+v, want only the member's current quota row", ps)
	}
	for _, r := range f.outbox() {
		if r.Session == "sid-m1" {
			t.Fatalf("the member still has a cause and was sent %+v", r)
		}
	}
	if len(f.outboxOf("sid-1")) != 2 {
		t.Fatalf("lead outbox = %+v", f.outboxOf("sid-1"))
	}
}

// The messages bounced during the pause are listed to the session when it resumes, in the outbox, and cleared in the same transaction.
func TestResume_TheBounceListIsSentAndCleared(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	for _, from := range []string{"mlab/w-one", "mlab/w-two"} {
		if paused, _, err := f.m.store.PauseVerdict("sid-1", from, "請看一下\n第二行", f.clock.Load()); err != nil || !paused {
			t.Fatalf("verdict: %v %v", paused, err)
		}
	}
	f.switchOff()
	var list string
	for _, r := range f.outboxOf("sid-1") {
		if strings.Contains(r.Text, "暫停期間有 2 則訊息被退回") {
			list = r.Text
		}
	}
	if !strings.Contains(list, "mlab/w-one") || !strings.Contains(list, "mlab/w-two") || !strings.Contains(list, "請看一下") || strings.Contains(list, "第二行") {
		t.Fatalf("bounce list = %q", list)
	}
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM pause_bounces`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("bounces left = %d (%v)", n, err)
	}
	// order for the session: resume notice, bounce list, reask
	ob := f.outboxOf("sid-1")
	if len(ob) != 3 || ob[0].Text != team.PauseResumedSolo || ob[1].Text != list || ob[2].Kind != team.PauseOutboxReask {
		t.Fatalf("outbox = %+v", ob)
	}
}

// A member that was paused for its own quota is told, and its lead gets one notice listing them.
func TestResume_AMemberQuotaPauseIsToldAndItsLeadGetsTheList(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-m1", team.PauseCauseQuota, team.PauseRoleMember, "_mem001", "worker", 1)
	f.switchOff()
	m := f.outboxOf("sid-m1")
	if len(m) != 1 || m[0].Text != team.PauseResumedMemberQuota {
		t.Fatalf("member outbox = %+v", m)
	}
	l := f.outboxOf("sid-1")
	if len(l) != 1 || l[0].Kind != team.PauseOutboxNotice || !strings.Contains(l[0].Text, "這些 member 之前因額度用完暫停：worker（_mem001），要不要接力由你決定。") {
		t.Fatalf("lead outbox = %+v", l)
	}
	if len(f.tokens()) != 0 {
		t.Fatalf("a member gets no release token: %v", f.tokens())
	}
}

// off CAS, then a crash before the resume, then the switch goes on again (a new Since): the old rows are resumed by their on_since.
// Mutation gate: reconcile only while the switch is off → the old rows stay (red).
func TestResume_AfterACrashTheOldOnPeriodIsResumedByOnSince(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.unatt.set(false) // the off CAS committed; the process died before the resume group
	f.unatt.set(true)
	f.unatt.setSince(2) // turned on again: a new on-period
	f.reconcile()
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v, the old on-period's row must be resumed", f.pauses())
	}
	if ob := f.outboxOf("sid-1"); len(ob) != 2 || ob[0].Text != team.PauseResumedSolo {
		t.Fatalf("outbox = %+v", ob)
	}
}

// The switch is on and the row is of this on-period: the reconciliation leaves it.
// Mutation gate: resume every row whenever the reconciliation runs → red.
func TestResume_ReconcileWhileOnWithTheCurrentOnSinceDoesNothing(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.reconcile()
	if len(f.pauses()) != 1 || len(f.outbox()) != 0 || len(f.tokens()) != 0 {
		t.Fatalf("pauses %d, outbox %d, tokens %d: want 1, 0, 0", len(f.pauses()), len(f.outbox()), len(f.tokens()))
	}
}

// Off, on, then the reconciliation: the rows were resumed once, at the off; nothing more is sent.
func TestResume_OffThenOnResumesOnlyOnce(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.switchOff()
	n := len(f.outbox())
	f.unatt.set(true)
	f.unatt.setSince(2)
	f.reconcile()
	f.reconcile()
	if got := len(f.outbox()); got != n || n == 0 {
		t.Fatalf("outbox %d → %d, want the same (resumed once)", n, got)
	}
}

// An old mod (hello below the reask protocol) gets the fallback text, no token and no reask control; a new one gets both.
// Mutation gate: send the reask to an old mod → red.
func TestResume_AnOldModGetsTheFallbackAndNoToken(t *testing.T) {
	f := leadWorld(t)
	f.modHello("sid-1", "4") // below team.MinPauseReaskModVersion
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.switchOff()
	ob := f.outboxOf("sid-1")
	if len(ob) != 1 || ob[0].Kind != team.PauseOutboxNotice || ob[0].Text != team.PauseResumedSoloOldMod || len(f.tokens()) != 0 {
		t.Fatalf("outbox = %+v, tokens = %v", ob, f.tokens())
	}
}

// A session whose mod never said hello is an old mod too.
func TestResume_ASessionWithoutAHelloGetsTheFallback(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	f.seedPause("sid-9", team.PauseCauseQuota, team.PauseRoleSolo, "_nnn999", "solo", 1)
	f.switchOff()
	ob := f.outboxOf("sid-9")
	if len(ob) != 1 || ob[0].Text != team.PauseResumedSoloOldMod || len(f.tokens()) != 0 {
		t.Fatalf("outbox = %+v, tokens = %v", ob, f.tokens())
	}
}

// Tokens older than 24 h are deleted by the reconciliation; younger ones stay.
func TestResume_ReconcileDeletesTokensOlderThan24Hours(t *testing.T) {
	f := leadWorld(t)
	now := f.clock.Load()
	old, young := uuid.NewString(), uuid.NewString()
	for tok, at := range map[string]int64{old: now - team.PauseTokenTTLMs - 1, young: now - team.PauseTokenTTLMs + 60_000} {
		if _, err := f.m.store.db.Exec(`INSERT INTO pause_release_tokens (token, session_id, created_at) VALUES (?, 'sid-1', ?)`, tok, at); err != nil {
			t.Fatal(err)
		}
	}
	f.reconcile()
	if got := f.tokens(); len(got) != 1 || got[0] != young {
		t.Fatalf("tokens = %v, want only %s", got, young)
	}
}

// The rate: the reconciliation is every 30 s, not every tick; boot forces it.
func TestResume_TheTickReconcilesEvery30Seconds(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 0) // a stale on-period
	f.m.tick()
	if len(f.pauses()) != 0 {
		t.Fatal("the first tick must reconcile")
	}
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 0)
	f.clock.Add(5_000)
	f.m.tick()
	if len(f.pauses()) != 1 {
		t.Fatal("a tick 5 s later must not reconcile again")
	}
	f.clock.Add(30_000)
	f.m.tick()
	if len(f.pauses()) != 0 {
		t.Fatal("a tick 35 s later must reconcile")
	}
}

// A reask row goes out as a flow message from the session's own inbox, its text the control line, and is then deleted.
// Mutation gate: drop the reask kind from the outbox's switch → the row is dropped unsent (red).
func TestResume_TheOutboxSendsTheReaskControlAsAFlowMessage(t *testing.T) {
	f := newFixture(t)
	flow, _ := f.outboxWorld()
	id := uuid.NewString()
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_outbox (session_id, kind, text, reask_id, created_at) VALUES ('sid-1', ?, ?, ?, 500)`,
		team.PauseOutboxReask, team.PauseReaskControlPrefix+id, id); err != nil {
		t.Fatal(err)
	}
	f.m.drainPauseOutbox()
	got := flow.sentTexts()
	if len(got) != 1 || got[0] != "h/_abc123|/tmp/10.sock|"+team.PauseReaskControlPrefix+id || len(f.outbox()) != 0 {
		t.Fatalf("flow sends = %q, outbox = %+v", got, f.outbox())
	}
}

// A token whose reask is still waiting in the outbox is not pruned however old it is (a daemon down for a day); once the reask is
// sent the token's day starts from the send.
// Mutation gates: prune without looking at the outbox → red; do not touch the token at the send → red.
func TestResume_AWaitingReaskKeepsItsTokenAndTheSendRestartsTheDay(t *testing.T) {
	f := newFixture(t)
	flow, _ := f.outboxWorld()
	_ = flow
	id := uuid.NewString()
	old := f.clock.Load() - 2*team.PauseTokenTTLMs
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_release_tokens (token, session_id, created_at) VALUES (?, 'sid-1', ?)`, id, old); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_outbox (session_id, kind, text, reask_id, created_at) VALUES ('sid-1', ?, ?, ?, ?)`,
		team.PauseOutboxReask, team.PauseReaskControlPrefix+id, id, old); err != nil {
		t.Fatal(err)
	}
	f.reconcile()
	if got := f.tokens(); len(got) != 1 {
		t.Fatalf("tokens = %v: a token whose reask has not gone out was pruned", got)
	}
	f.m.drainPauseOutbox() // the reask goes out
	f.reconcile()
	if got := f.tokens(); len(got) != 1 {
		t.Fatalf("tokens = %v: the day must start at the send", got)
	}
}

// The token update and the row's delete are one transaction: when the token cannot be updated the sent reask stays in the outbox
// (it will be sent again) and the token stays protected from pruning.
// Mutation gate: delete the row even when the token update failed → the row is gone, the token unprotected (red).
func TestResume_AReaskWhoseTokenCannotBeTouchedStaysInTheOutbox(t *testing.T) {
	f := newFixture(t)
	f.outboxWorld()
	id := uuid.NewString()
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_outbox (session_id, kind, text, reask_id, created_at) VALUES ('sid-1', ?, ?, ?, 500)`,
		team.PauseOutboxReask, team.PauseReaskControlPrefix+id, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.store.db.Exec(`DROP TABLE pause_release_tokens`); err != nil { // the update will fail
		t.Fatal(err)
	}
	f.m.drainPauseOutbox()
	if rows := f.outbox(); len(rows) != 1 {
		t.Fatalf("outbox = %+v, want the reask kept for a repeat", rows)
	}
}

// The switch off with no pause row at all writes nothing.
func TestResume_NothingPausedNothingWritten(t *testing.T) {
	f := leadWorld(t)
	f.switchOff()
	if len(f.outbox()) != 0 || len(f.tokens()) != 0 {
		t.Fatalf("outbox %d, tokens %d", len(f.outbox()), len(f.tokens()))
	}
}
