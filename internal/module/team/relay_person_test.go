// internal/module/team/relay_person_test.go — a person's own /relay is direct (relay/team rules v2, RT-1a T2): no card, no quota.
package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func (f *fixture) beginPerson(sid, token string) (int, []byte) {
	f.t.Helper()
	r := manualReq(sid)
	r.PersonToken = token
	return f.do(http.MethodPost, "/api/relay/begin", r)
}

func (f *fixture) personOpened(sid string) team.RelayBeginResponse {
	f.t.Helper()
	tok := f.mintPerson(sid, team.PersonCmdRelay)
	code, body := f.beginPerson(sid, tok.Token)
	if code != http.StatusCreated {
		f.t.Fatalf("person begin %s: %d %s", sid, code, body)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

// approvedByPerson fails unless the op is claimed and its row approved by the person, with no card ever announced as open.
func (f *fixture) approvedByPerson(out team.RelayBeginResponse) {
	f.t.Helper()
	if op := f.op(out.Op.ID); op.State != team.RelayClaimed {
		f.t.Fatalf("op = %s, want claimed", op.State)
	}
	a, ok, err := f.m.store.Get(out.RequestID)
	if err != nil || !ok {
		f.t.Fatalf("row: %v %v", ok, err)
	}
	if a.State != team.StateApproved || a.DecidedBy == nil || a.DecidedBy.Kind != team.ClientKindPerson || a.DecidedBy.Label != team.PersonLabel {
		f.t.Fatalf("row = %s by %+v, want approved by the person", a.State, a.DecidedBy)
	}
}

// Mutation gate: route a person begin to the card path (ignore the token) → the row is open (red).
func TestPersonRelay_BeginIsApprovedAtOnce(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	code, body := f.beginPerson("sid-1", tok.Token)
	if code != http.StatusCreated {
		t.Fatalf("person begin: %d %s", code, body)
	}
	var out team.RelayBeginResponse
	_ = json.Unmarshal(body, &out)
	f.approvedByPerson(out)
	if f.m.validPerson("sid-1", team.PersonCmdRelay, tok.Token) {
		t.Fatal("the token is still good after the relay was written")
	}
	if got := f.countOps("opened"); got != 0 {
		t.Fatalf("%d opened broadcast(s): a person's relay is announced closed only", got)
	}
}

// Mutation gate: let the card path take the open card (open a second op) → ErrRelayOpen (red).
func TestPersonRelay_AnOpenCardIsApprovedByThePerson(t *testing.T) {
	f := newFixture(t)
	code0, body0 := f.beginRaw("sid-1") // the threshold card the mod asked for
	if code0 != http.StatusCreated {
		t.Fatalf("card: %d %s", code0, body0)
	}
	var card team.RelayBeginResponse
	_ = json.Unmarshal(body0, &card)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	code, body := f.beginPerson("sid-1", tok.Token)
	if code != http.StatusCreated {
		t.Fatalf("person begin over an open card: %d %s", code, body)
	}
	var out team.RelayBeginResponse
	_ = json.Unmarshal(body, &out)
	if out.RequestID != card.RequestID || out.Op.ID != card.Op.ID {
		t.Fatalf("a second op was opened: %+v vs the card's %+v", out, card)
	}
	f.approvedByPerson(out)
	if n := f.opCount(); n != 1 {
		t.Fatalf("%d ops, want one", n)
	}
	if f.m.validPerson("sid-1", team.PersonCmdRelay, tok.Token) {
		t.Fatal("the token is still good after it approved the card")
	}
}

// Mutation gate: send a person begin through the unattended branch → quota is spent / the session is paused (red).
func TestPersonRelay_UnattendedWithNoQuotaStillApprovedAndSpendsNothing(t *testing.T) {
	enablePauseEntry(t)
	f := newFixture(t)
	f.quotaWorld()
	f.setQuota("sid-1", 0)
	out := f.personOpened("sid-1")
	f.approvedByPerson(out)
	if got := f.selfLeft("sid-1"); got != 0 {
		t.Fatalf("self_left = %d, want 0 unchanged", got)
	}
	if p := f.pauses(); len(p) != 0 {
		t.Fatalf("pause rows %+v, want none", p)
	}
	// and with quota left, none is spent
	g := newFixture(t)
	g.quotaWorld()
	g.setQuota("sid-1", 2)
	g.approvedByPerson(g.personOpened("sid-1"))
	if got := g.selfLeft("sid-1"); got != 2 {
		t.Fatalf("self_left = %d, want 2 (nothing spent)", got)
	}
}

// Mutation gate: apply the switch refusals to a person begin → 409 self_relay_off / self_relay_paused (red).
func TestPersonRelay_TheSwitchesDoNotApply(t *testing.T) {
	for _, sw := range everySwitch {
		f := newFixture(t)
		f.switches.set(sw)
		f.approvedByPerson(f.personOpened("sid-1"))
	}
	f := newFixture(t)
	if err := f.m.store.SetSelfRelayPaused("sid-1", true, f.clock.Load()); err != nil { // /relay off
		t.Fatal(err)
	}
	f.approvedByPerson(f.personOpened("sid-1"))
}

func TestPersonRelay_AMemberIsApproved_AndOneBeingKilledIsRefused(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	f.approvedByPerson(f.personOpened("sid-1"))

	g := newFixture(t)
	g.makeMember("sid-1")
	if _, err := g.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE session_id = 'sid-1'`); err != nil {
		t.Fatal(err)
	}
	tok := g.mintPerson("sid-1", team.PersonCmdRelay)
	code, body := g.beginPerson("sid-1", tok.Token)
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrRelayUnsupported {
		t.Fatalf("a member being killed: %d %s, want 409 %s", code, body, team.ErrRelayUnsupported)
	}
}

// Mutation gate: keep the reask control on the person's release → a reask token is written (red).
func TestPersonRelay_ReleasesThePauseWithoutAReask(t *testing.T) {
	f := newFixture(t)
	f.modHello("sid-1", "5")
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_aaaaaa", "t", 1)
	out := f.personOpened("sid-1")
	f.approvedByPerson(out)
	if p := f.pauses(); len(p) != 0 {
		t.Fatalf("pause rows %+v, want none", p)
	}
	for _, o := range f.outbox() {
		if o.Kind == team.PauseOutboxReask {
			t.Fatalf("a reask control was written: %+v", o)
		}
	}
	if toks := f.tokens(); len(toks) != 0 {
		t.Fatalf("release tokens %v, want none", toks)
	}
}

func TestPersonRelay_ABadTokenWritesNothing_AndNoTokenIsACard(t *testing.T) {
	f := newFixture(t)
	code, body := f.beginPerson("sid-1", "deadbeef")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrPersonTokenInvalid {
		t.Fatalf("bad token: %d %s, want 409 %s", code, body, team.ErrPersonTokenInvalid)
	}
	if n := f.opCount(); n != 0 {
		t.Fatalf("%d op(s) after a refused token", n)
	}
	// a token for another command is just as bad
	tok := f.mintPerson("sid-1", team.PersonCmdLead)
	if code, body := f.beginPerson("sid-1", tok.Token); code != http.StatusConflict {
		t.Fatalf("a lead token on /relay: %d %s", code, body)
	}
	// manual without a token: a card, as before
	out := f.manualOpened("sid-1")
	if st := f.rowState(out.RequestID); st != team.StateOpen {
		t.Fatalf("manual without a token: row %s, want open", st)
	}
}

func TestPersonRelay_AReplayAfterTheTokenIsSpentReturnsTheSameOp(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	r := manualReq("sid-1")
	r.RequestID = uid(7)
	r.PersonToken = tok.Token
	code, body := f.do(http.MethodPost, "/api/relay/begin", r)
	if code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	var first team.RelayBeginResponse
	_ = json.Unmarshal(body, &first)
	code, body = f.do(http.MethodPost, "/api/relay/begin", r)
	var again team.RelayBeginResponse
	_ = json.Unmarshal(body, &again)
	if code != http.StatusCreated || again.Op.ID != first.Op.ID {
		t.Fatalf("replay: %d %s, want the first op %s", code, body, first.Op.ID)
	}
}

// A begin replayed with the same request id but without the person's token is another request, not a retry. Mutation gate:
// leave Person out of the comparator → the second begin answers 201 with the person's op (red).
func TestPersonRelay_AReplayWithoutThePersonIsAConflict(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	r := manualReq("sid-1")
	r.RequestID = uid(8)
	r.PersonToken = tok.Token
	if code, body := f.do(http.MethodPost, "/api/relay/begin", r); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	r.PersonToken = ""
	code, body := f.do(http.MethodPost, "/api/relay/begin", r)
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrBadRequest {
		t.Fatalf("replay without the token: %d %s, want 409 %s", code, body, team.ErrBadRequest)
	}
}

// The pauses end BEFORE the relay is written (codex R1): when they cannot, nothing is relayed, so no session is left relayed and
// paused for good. Mutation gate: release after the relay → the op exists after the 500 (red).
func TestPersonRelay_APauseThatCannotEndWritesNoRelay(t *testing.T) {
	f := newFixture(t)
	if _, err := f.m.store.db.Exec(`DROP TABLE pause_bounces`); err != nil { // ResumePauses' first statement now fails
		t.Fatal(err)
	}
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	code, body := f.beginPerson("sid-1", tok.Token)
	if code != http.StatusInternalServerError {
		t.Fatalf("person begin with a pause that cannot end: %d %s, want 500", code, body)
	}
	if n := f.opCount(); n != 0 {
		t.Fatalf("%d op(s) written, want none", n)
	}
	// the open-card path too
	g := newFixture(t)
	if code, body := g.beginRaw("sid-1"); code != http.StatusCreated {
		t.Fatalf("card: %d %s", code, body)
	}
	if _, err := g.m.store.db.Exec(`DROP TABLE pause_bounces`); err != nil {
		t.Fatal(err)
	}
	tok = g.mintPerson("sid-1", team.PersonCmdRelay)
	if code, body := g.beginPerson("sid-1", tok.Token); code != http.StatusInternalServerError {
		t.Fatalf("person begin over a card with a pause that cannot end: %d %s, want 500", code, body)
	}
	var open int
	if err := g.m.store.db.QueryRow(`SELECT COUNT(*) FROM approval_requests WHERE state = 'open'`).Scan(&open); err != nil || open != 1 {
		t.Fatalf("open rows = %d (%v), want the card still open", open, err)
	}
}

// The person's token is spent only when the relay is committed (codex attack): a refusal or a storage failure costs nothing.
// Mutation gate: spend at the check (consume up front) → the token is dead after the failure (red).
func TestPersonRelay_ATokenSurvivesARefusalAndAStorageFailure(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE session_id = 'sid-1'`); err != nil {
		t.Fatal(err)
	}
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	if code, _ := f.beginPerson("sid-1", tok.Token); code != http.StatusConflict {
		t.Fatalf("a member being killed: %d, want 409", code)
	}
	if !f.m.validPerson("sid-1", team.PersonCmdRelay, tok.Token) {
		t.Fatal("the token was spent by a refused request")
	}

	g := newFixture(t)
	if _, err := g.m.store.db.Exec(`DROP TABLE pause_bounces`); err != nil {
		t.Fatal(err)
	}
	tok = g.mintPerson("sid-1", team.PersonCmdRelay)
	if code, _ := g.beginPerson("sid-1", tok.Token); code != http.StatusInternalServerError {
		t.Fatalf("storage failure: %d, want 500", code)
	}
	if !g.m.validPerson("sid-1", team.PersonCmdRelay, tok.Token) {
		t.Fatal("the token was spent by a failed request")
	}
}

// The pauses end in the relay's transaction (codex R1 + attack): when the relay cannot be written the pauses stay, and when it
// is written they are gone. Mutation gate: end them in a transaction of their own, first → the pause row is gone after the
// refused begin (red).
func TestPersonRelay_ThePausesAndTheRelayAreOneTransaction(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing' WHERE session_id = 'sid-1'`); err != nil {
		t.Fatal(err)
	}
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_aaaaaa", "t", 1)
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	if code, _ := f.beginPerson("sid-1", tok.Token); code != http.StatusConflict {
		t.Fatalf("a member being killed: %d, want 409", code)
	}
	if _, ok := f.pauses()["sid-1/quota"]; !ok {
		t.Fatal("the pause ended although the relay was refused")
	}
	if n := f.opCount(); n != 0 {
		t.Fatalf("%d op(s) after the refusal", n)
	}
}

// A card another request decides between the read and the CAS is not the person's to approve (codex attack): the answer says what
// it became (never an empty 200), the pauses stay and the token is unspent. Mutation gate: ignore the lost CAS and answer 201 /
// resume anyway → red.
func TestPersonRelay_ACardDeniedAtTheSameMomentIsNotApproved(t *testing.T) {
	f := newFixture(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_aaaaaa", "t", 1)
	card := f.manualOpened("sid-1")
	f.m.afterPersonCardRead = func() {
		f.m.afterPersonCardRead = nil
		if code, body := f.decide(card.RequestID, "deny"); code != http.StatusOK {
			t.Errorf("deny: %d %s", code, body)
		}
	}
	tok := f.mintPerson("sid-1", team.PersonCmdRelay)
	code, body := f.beginPerson("sid-1", tok.Token)
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrRelayOpen || e.Op == nil || e.Op.State != team.RelayCancelled {
		t.Fatalf("a card denied at the same moment: %d %s, want 409 relay_open with the cancelled op", code, body)
	}
	if _, ok := f.pauses()["sid-1/quota"]; !ok {
		t.Fatal("the pause ended although the card was not approved")
	}
	if !f.m.validPerson("sid-1", team.PersonCmdRelay, tok.Token) {
		t.Fatal("the token was spent although nothing was approved")
	}
}

// The table does not keep dead tokens (codex attack): spent ones go at once, expired ones at the next touch. Mutation gate: keep
// consumed tokens in the table → the length stays above 1 (red).
func TestPerson_TheTableForgetsSpentAndExpiredTokens(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 50; i++ {
		f.mintPerson("sid-1", team.PersonCmdRelay) // the cap keeps four, but the other sessions are many
		f.m.persons.mint("churn-"+uid(i), team.PersonCmdRelay, 60, f.clock.Load())
	}
	f.clock.Add(61_000)
	last := f.mintPerson("sid-1", team.PersonCmdRelay)
	f.m.persons.mu.Lock()
	n := len(f.m.persons.live)
	f.m.persons.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d tokens live after everything else expired, want 1", n)
	}
	if !f.m.consumePerson("sid-1", team.PersonCmdRelay, last.Token) {
		t.Fatal("the live token was lost")
	}
	f.m.persons.mu.Lock()
	n = len(f.m.persons.live)
	f.m.persons.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d tokens after the last was spent, want 0", n)
	}
}
