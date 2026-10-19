package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// A person's /team <name> (name pool v2 spec §5.3, NP-4c C2): the token carries the name the person typed, or asks for a card; the
// request that uses it is granted with that name, or waits on the card, whatever the unattended switch.

func (f *fixture) mintPersonWith(req team.PersonRequest) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, team.PersonRoute, req)
}

func TestPersonLeadName_MintEchoesWhatTheTokenIsBoundTo(t *testing.T) {
	f := newFixture(t)
	withLeadNames(f)
	var r team.PersonResponse
	code, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "kenji"})
	if err := json.Unmarshal(body, &r); err != nil || code != 200 || r.Token == "" || r.LeadName != "kenji" || r.Card {
		t.Fatalf("lead_name mint: %d %s", code, body)
	}
	code, body = f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, Card: true})
	r = team.PersonResponse{}
	if err := json.Unmarshal(body, &r); err != nil || code != 200 || r.Token == "" || r.LeadName != "" || !r.Card {
		t.Fatalf("card mint: %d %s", code, body)
	}
	code, body = f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead})
	r = team.PersonResponse{}
	if err := json.Unmarshal(body, &r); err != nil || code != 200 || r.LeadName != "" || r.Card {
		t.Fatalf("plain mint: %d %s", code, body)
	}
}

func TestPersonLeadName_MintRefusals(t *testing.T) {
	f := newFixture(t)
	withLeadNames(f)
	for name, req := range map[string]team.PersonRequest{
		"both":                  {SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "kenji", Card: true},
		"bad shape":             {SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "Kenji"},
		"name on relay":         {SessionID: "sid-1", Command: team.PersonCmdRelay, LeadName: "kenji"},
		"card on spawn":         {SessionID: "sid-1", Command: team.PersonCmdSpawn, Card: true},
		"name on adopt":         {SessionID: "sid-1", Command: team.PersonCmdAdopt, LeadName: "kenji"},
		"too short lead_name":   {SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "ke"},
		"punctuated lead_name":  {SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "ke-nji"},
		"too long lead_name":    {SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "abcdefghijklm"},
		"digits in a lead_name": {SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "k3nji"},
	} {
		if code, body := f.mintPersonWith(req); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
	}
	g := newFixture(t) // no name service: a daemon that cannot rename takes no name
	if code, body := g.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "kenji"}); code != 503 || decodeErr(t, body).Error != team.ErrNotReady {
		t.Errorf("no name service: %d %s, want 503 not_ready", code, body)
	}
}

// The token's name is the grant's: the request that uses it cannot drop it, and the rename happens before the team row.
func TestPersonLeadName_NoOpenRequestCreatesAnApprovedLeadWithTheName(t *testing.T) {
	f := newFixture(t)
	n := withLeadNames(f)
	n.rename = func(sid, code string) error {
		if _, found, _ := f.m.store.LiveTeamByLead(sid); found {
			t.Error("the team row exists before the rename")
		}
		return nil
	}
	tok := f.mintPersonWith
	_, body := tok(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "kenji"})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	code, body := f.createLeadWith(uid(1), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	a := decodeApproval(t, body)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	assertDecidedByPerson(t, a, 1_000_000)
	if a.Grant == nil || a.Grant.LeadName != "kenji" {
		t.Fatalf("grant = %+v", a.Grant)
	}
	if n.count("rename:sid-1:kenji") != 1 || n.count("record:sid-1") != 1 || n.count("undo:") != 0 {
		t.Fatalf("calls = %v", n.log())
	}
	if f.m.validPerson("sid-1", team.PersonCmdLead, r.Token) {
		t.Fatal("the token is still good after it was used")
	}
}

func TestPersonLeadName_OverTheAgentsOpenRequest(t *testing.T) {
	f := newFixture(t)
	n := withLeadNames(f)
	open := f.create(uid(1))
	_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "emma"})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	code, body := f.createLeadWith(uid(2), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	a := decodeApproval(t, body)
	if code != http.StatusCreated || a.ID != open.ID || a.Grant == nil || a.Grant.LeadName != "emma" {
		t.Fatalf("create: %d %s", code, body)
	}
	assertDecidedByPerson(t, a, 1_000_000)
	if n.count("rename:sid-1:emma") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
}

// A name another session holds by now: the person's command is refused with the fresh list, nothing is written, the token is kept.
func TestPersonLeadName_NameTakenKeepsTheTokenAndWritesNothing(t *testing.T) {
	for _, withOpen := range []bool{false, true} {
		f := newFixture(t)
		n := withLeadNames(f)
		if withOpen {
			f.create(uid(1))
		}
		n.rename = func(string, string) error { return ipeers.ErrNameTaken }
		_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "kenji"})
		var r team.PersonResponse
		_ = json.Unmarshal(body, &r)
		code, body := f.createLeadWith(uid(2), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
		e := decodeErr(t, body)
		if code != http.StatusConflict || e.Error != team.ErrNameTaken || len(e.LeadNames) != 3 {
			t.Fatalf("withOpen=%v: %d %s", withOpen, code, body)
		}
		if !f.m.validPerson("sid-1", team.PersonCmdLead, r.Token) {
			t.Fatalf("withOpen=%v: the token was spent by a refusal", withOpen)
		}
		if _, found, _ := f.m.store.LiveTeamByLead("sid-1"); found {
			t.Fatalf("withOpen=%v: a team was written", withOpen)
		}
		if !withOpen {
			if open, found, _ := f.m.store.OpenByOrigin("sid-1", team.KindLead); found {
				t.Fatalf("a row was left behind: %+v", open)
			}
		}
	}
}

// A failure after the rename gives the name back and costs the token nothing.
func TestPersonLeadName_FailureAfterTheRenameUndoesIt(t *testing.T) {
	f := newFixture(t)
	n := withLeadNames(f)
	n.rename = func(string, string) error {
		_, err := f.m.store.db.Exec(`DROP TABLE teams`)
		return err
	}
	_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, LeadName: "kenji"})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	code, _ := f.createLeadWith(uid(1), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	if code != http.StatusInternalServerError {
		t.Fatalf("create: %d, want 500", code)
	}
	if n.count("undo:sid-1:"+n.old) != 1 || n.count("record:") != 0 {
		t.Fatalf("calls = %v", n.log())
	}
	if !f.m.validPerson("sid-1", team.PersonCmdLead, r.Token) {
		t.Fatal("the token was spent by a failure")
	}
}

// --card: a lead request that waits on the card (the person picks the name there), whatever the unattended switch.
func TestPersonLeadName_CardOpensARequestThatWaits(t *testing.T) {
	for _, on := range []bool{false, true} {
		f := newFixture(t)
		withLeadNames(f)
		f.unatt.set(on)
		_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, Card: true})
		var r team.PersonResponse
		_ = json.Unmarshal(body, &r)
		code, body := f.createLeadWith(uid(1), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
		a := decodeApproval(t, body)
		if code != http.StatusCreated || a.State != team.StateOpen {
			t.Fatalf("on=%v: create: %d %s", on, code, body)
		}
		var p team.LeadPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil || len(p.LeadNames) != 3 {
			t.Fatalf("on=%v: payload lead_names = %+v (%v)", on, p.LeadNames, err)
		}
		if f.m.validPerson("sid-1", team.PersonCmdLead, r.Token) {
			t.Fatalf("on=%v: the token is still good", on)
		}
		// the card is decided like any other
		code, body = f.decideLead(a.ID, "kenji")
		if code != 200 {
			t.Fatalf("on=%v: decide: %d %s", on, code, body)
		}
	}
}

// A session that already has an open lead request is answered with that one; the person's command is served and the token spent.
func TestPersonLeadName_CardOverAnOpenRequestAnswersIt(t *testing.T) {
	f := newFixture(t)
	withLeadNames(f)
	open := f.create(uid(1))
	_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, Card: true})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	code, body := f.createLeadWith(uid(2), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	a := decodeApproval(t, body)
	if code != http.StatusOK || a.ID != open.ID || a.State != team.StateOpen {
		t.Fatalf("create: %d %s", code, body)
	}
	if f.m.validPerson("sid-1", team.PersonCmdLead, r.Token) {
		t.Fatal("the token is still good")
	}
}

// A card request of a session that already leads is refused like any lead request, and costs the token nothing.
func TestPersonLeadName_CardOfALeadIsRefused(t *testing.T) {
	f := newFixture(t)
	withLeadNames(f)
	f.create(uid(1))
	if code, body := f.decideLead(uid(1), "kenji"); code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, Card: true})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	code, body := f.createLeadWith(uid(2), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrAlreadyLead {
		t.Fatalf("create: %d %s", code, body)
	}
	if !f.m.validPerson("sid-1", team.PersonCmdLead, r.Token) {
		t.Fatal("the token was spent by a refusal")
	}
}

// The switch-on sweep refuses an agent's open lead request but leaves a person's card request for the person (mutation: drop the
// personLeadCard exemption → the card is cancelled).
func TestPersonLeadName_SweepLeavesTheCardAndRefusesTheAgentsRequest(t *testing.T) {
	f := newFixture(t)
	withLeadNames(f)
	_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, Card: true})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	_, body = f.createLeadWith(uid(1), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	card := decodeApproval(t, body)
	agents := f.createReq(uid(2))
	agents.OriginInbox = "/tmp/20.sock"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", agents); code != http.StatusCreated {
		t.Fatalf("agent's create: %d %s", code, body)
	}
	f.unatt.set(true)
	f.sweep()
	if row, _, _ := f.m.store.Get(card.ID); row.State != team.StateOpen {
		t.Fatalf("the card request is %s after the sweep, want open", row.State)
	}
	if row, _, _ := f.m.store.Get(uid(2)); row.State != team.StateCancelled || row.CloseReason != team.ErrUnattendedRefused {
		t.Fatalf("the agent's request is %s (%s) after the sweep, want cancelled unattended_refused", row.State, row.CloseReason)
	}
}

// A card over the agent's own open request makes that request the person's: the sweep, switched on later, leaves it open
// (mutation: drop MarkLeadPerson → the sweep cancels it).
func TestPersonLeadName_CardOverAnOpenRequestSurvivesTheSweep(t *testing.T) {
	f := newFixture(t)
	withLeadNames(f)
	open := f.create(uid(1))
	_, body := f.mintPersonWith(team.PersonRequest{SessionID: "sid-1", Command: team.PersonCmdLead, Card: true})
	var r team.PersonResponse
	_ = json.Unmarshal(body, &r)
	code, body := f.createLeadWith(uid(2), func(c *team.CreateApprovalRequest) { c.PersonToken = r.Token })
	var p team.LeadPayload
	if err := json.Unmarshal(decodeApproval(t, body).Payload, &p); err != nil || code != http.StatusOK || !p.Person {
		t.Fatalf("create: %d %s (person=%v)", code, body, p.Person)
	}
	f.unatt.set(true)
	f.sweep()
	if row, _, _ := f.m.store.Get(open.ID); row.State != team.StateOpen {
		t.Fatalf("the request is %s after the sweep, want open", row.State)
	}
}

// MarkLeadPerson on a request a decide closed first writes nothing and says so (the handler then keeps the token and answers
// already_decided). Mutation: drop `state = 'open'` from the UPDATE → marked is true and the closed row is rewritten.
func TestPersonLeadName_MarkingAClosedRequestIsNotAMark(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", team.DecideRequest{Decision: "deny", Client: leadAppClient}); code != 200 {
		t.Fatalf("deny: %d %s", code, body)
	}
	row, marked, err := f.m.store.MarkLeadPerson(uid(1))
	var p team.LeadPayload
	if err != nil || marked || row.State != team.StateDenied || json.Unmarshal(row.Payload, &p) != nil || p.Person {
		t.Fatalf("marked=%v err=%v row=%s person=%v", marked, err, row.State, p.Person)
	}
}

var _ = errors.New
