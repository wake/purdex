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
	out := f.personOpened("sid-1")
	f.approvedByPerson(out)
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
