package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// MR-3b (member relay spec §3.3, D9): M applies the lead host's `relay` command to a remote member — an op of this host with
// the lead's op id, a control message through the notice seam — and answers a void of it from the op's state at that moment.

const relayOpID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func relayCmdFor(id, mk, opID string) team.TeamCommand {
	c := adoptCmd(id, mk, "")
	c.Kind, c.TargetRef, c.OpID, c.CreatedAt = team.CommandRelay, "", opID, 1000
	return c
}

func relayPlanFor(cmd team.TeamCommand) CommandPlan {
	p := plan(cmd, true, nil)
	p.HostID, p.HandoffDir = "h:1", "/d/relay"
	p.ModOK = func(string) bool { return true }
	return p
}

// seedLeadMember is a remote member of the lead host leadHostA's team-L.
func seedLeadMember(t *testing.T, s *Store, mk, sid string) {
	t.Helper()
	if err := s.InsertRemoteMember(newRemote(mk, sid, leadHostA, 1000)); err != nil {
		t.Fatal(err)
	}
}

func opState(t *testing.T, s *Store, id string) (team.RelayOp, bool) {
	t.Helper()
	op, ok, err := s.GetRelayOp(id)
	if err != nil {
		t.Fatal(err)
	}
	return op, ok
}

func relayOutcome(t *testing.T, res CommandResult) string {
	t.Helper()
	var o struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(res.Body, &o); err != nil {
		t.Fatalf("body %s: %v", res.Body, err)
	}
	return o.State
}

// The command opens the op with the lead's id, on this host, requested, for the member's session. Mutation gates: no
// kind → unsupported (red); the op under another id → red.
func TestRelayCmd_AcceptsAndOpensTheOpWithTheLeadsID(t *testing.T) {
	s := openTestStore(t)
	seedLeadMember(t, s, "mk-1", "sid-1")
	res := mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	if res.Status != http.StatusOK || relayOutcome(t, res) != team.RelayCommandAccepted {
		t.Fatalf("relay = %d %s", res.Status, res.Body)
	}
	op, ok := opState(t, s, relayOpID)
	if !ok || op.State != team.RelayRequested || op.SessionID != "sid-1" || op.Kind != team.RelayKindMember || op.TeamID != "team-L" || op.HostID != "h:1" {
		t.Fatalf("op = %+v ok=%v", op, ok)
	}
}

// A replay answers the stored outcome and opens no second op.
func TestRelayCmd_ReplayIsTheStoredOutcomeWithOneOp(t *testing.T) {
	s := openTestStore(t)
	seedLeadMember(t, s, "mk-1", "sid-1")
	p := relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID))
	mustApply(t, s, p)
	again := mustApply(t, s, p)
	if !again.Replayed || relayOutcome(t, again) != team.RelayCommandAccepted {
		t.Fatalf("replay = %+v %s", again, again.Body)
	}
	if active, _ := s.ListActiveRelayOps(); len(active) != 1 {
		t.Fatalf("%d active ops, want 1", len(active))
	}
}

// Each refusal of §3.3 with its code, and nothing opened. Mutations: drop the age check / AllowTeam / the row's state, team
// or lead host / the mod check / the open-op floor → that case red.
func TestRelayCmd_Refusals(t *testing.T) {
	type tc struct {
		name  string
		edit  func(p *CommandPlan, c *team.TeamCommand)
		setup func(t *testing.T, s *Store)
		code  string
	}
	cases := []tc{
		{name: "too old", edit: func(p *CommandPlan, c *team.TeamCommand) { p.Now = c.CreatedAt + commandExpiryMS + commandSkewMS + 1 }, code: team.ErrCommandExpired},
		{name: "AllowTeam off", edit: func(p *CommandPlan, c *team.TeamCommand) { p.Consent = false }, code: team.ErrCommandHostNotAllowed},
		{name: "unknown mk", edit: func(p *CommandPlan, c *team.TeamCommand) { c.MK = "mk-x" }, code: team.ErrCommandNotYourMember},
		{name: "another team", edit: func(p *CommandPlan, c *team.TeamCommand) { c.TeamID = "team-other" }, code: team.ErrCommandNotYourMember},
		{name: "another lead host", edit: func(p *CommandPlan, c *team.TeamCommand) { p.LeadHostID = "host-B" }, code: team.ErrCommandNotYourMember},
		{name: "member released", setup: func(t *testing.T, s *Store) {
			if _, err := casRemoteMemberStateIn(s.db, "mk-1", []string{remoteActive}, remoteReleased, 1500); err != nil {
				t.Fatal(err)
			}
		}, code: team.ErrCommandNotYourMember},
		{name: "old mod", edit: func(p *CommandPlan, c *team.TeamCommand) { p.ModOK = func(string) bool { return false } }, code: team.ErrRelayUnsupported},
		{name: "an op is open", setup: func(t *testing.T, s *Store) {
			op := team.RelayOp{ID: "op-open", Kind: team.RelayKindMember, HostID: "h:1", SessionID: "sid-1", Ref: "_rmk-1", TeamID: "team-L", State: team.RelayRequested, CreatedAt: 1000, UpdatedAt: 1000}
			if err := s.CreateRelayOp(op); err != nil {
				t.Fatal(err)
			}
		}, code: team.ErrRelayOpen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openTestStore(t)
			seedLeadMember(t, s, "mk-1", "sid-1")
			if c.setup != nil {
				c.setup(t, s)
			}
			cmd := relayCmdFor(cmdUUID1, "mk-1", relayOpID)
			p := relayPlanFor(cmd)
			if c.edit != nil {
				c.edit(&p, &cmd)
				p.Body, _ = json.Marshal(cmd)
			}
			res := mustApply(t, s, p)
			if res.Status == http.StatusOK || refusalCode(t, res) != c.code {
				t.Fatalf("relay = %d %s, want %s", res.Status, res.Body, c.code)
			}
			if _, ok := opState(t, s, relayOpID); ok {
				t.Fatal("a refused relay opened its op")
			}
		})
	}
}

// The lead's relay is the answer to an open ask of that session, accepted in the op's transaction.
func TestRelayCmd_AcceptsTheSessionsOpenAsk(t *testing.T) {
	s := openTestStore(t)
	seedLeadMember(t, s, "mk-1", "sid-1")
	if _, err := s.db.Exec(`INSERT INTO relay_asks (id, team_id, spawn_op, session_id, used_pct, window, state, created_at, expires_at)
		VALUES ('ask-1', 'team-L', 'mk-1', 'sid-1', 71, 200000, 'open', 900, 5000)`); err != nil {
		t.Fatal(err)
	}
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	var state, opID string
	if err := s.db.QueryRow(`SELECT state, op_id FROM relay_asks WHERE id = 'ask-1'`).Scan(&state, &opID); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || opID != relayOpID {
		t.Fatalf("ask = %s op %q, want accepted by %s", state, opID, relayOpID)
	}
}

func relayVoidSetup(t *testing.T) *Store {
	t.Helper()
	s := openRemoteStore(t)
	seedLeadMember(t, s, "mk-1", "sid-1")
	return s
}

// D9 void, the command never arrived: recorded, its late copy answers command_void, nothing opened.
// Mutation gate: no kind in the voided list → the late copy applies (red).
func TestRelayVoid_BeforeArrivalIsNotAppliedAndTheLateCopyIsVoid(t *testing.T) {
	s := relayVoidSetup(t)
	res := mustApply(t, s, plan(voidCmd(cmdUUID2, cmdUUID1), false, nil))
	if voidState(t, res) != team.VoidNotApplied {
		t.Fatalf("void = %d %s, want %s", res.Status, res.Body, team.VoidNotApplied)
	}
	late := mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	if late.Status != http.StatusConflict || refusalCode(t, late) != team.ErrCommandVoided {
		t.Fatalf("late copy = %d %s, want 409 command_void", late.Status, late.Body)
	}
	if _, ok := opState(t, s, relayOpID); ok {
		t.Fatal("a voided relay opened its op")
	}
}

// Applied and still requested: the op is cancelled (remote_unreachable), outcome undone, the late copy is void.
// Mutation gates: undone leaves the op requested → red.
func TestRelayVoid_WhileRequestedCancelsTheOp(t *testing.T) {
	s := relayVoidSetup(t)
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	res := mustApply(t, s, plan(voidCmd(cmdUUID2, cmdUUID1), false, nil))
	if voidState(t, res) != team.VoidUndone {
		t.Fatalf("void = %d %s, want %s", res.Status, res.Body, team.VoidUndone)
	}
	if op, _ := opState(t, s, relayOpID); op.State != team.RelayCancelled || op.Reason != "remote_unreachable" {
		t.Fatalf("op = %s (%s), want cancelled remote_unreachable", op.State, op.Reason)
	}
}

// Claimed, or already ended (the ack was lost): too_late and nothing touched. Mutations: cancel a claimed op → red; answer
// undone for an ended op → red.
func TestRelayVoid_AfterTheClaimOrTheEndIsTooLate(t *testing.T) {
	for _, to := range []team.RelayState{team.RelayClaimed, team.RelayCancelled, team.RelayFailed} {
		t.Run(string(to), func(t *testing.T) {
			s := relayVoidSetup(t)
			mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
			mustReport(t, s, relayOpID, RelayReport{State: to, Reason: "x", At: 2000})
			res := mustApply(t, s, plan(voidCmd(cmdUUID2, cmdUUID1), false, nil))
			if voidState(t, res) != team.VoidTooLate {
				t.Fatalf("void = %d %s, want %s", res.Status, res.Body, team.VoidTooLate)
			}
			if op, _ := opState(t, s, relayOpID); op.State != to {
				t.Fatalf("op = %s, want untouched %s", op.State, to)
			}
		})
	}
}

func relayFacts(t *testing.T, s *Store, kind string) []team.TeamFact {
	t.Helper()
	var out []team.TeamFact
	for _, fr := range factsOf(t, s, leadHostA) {
		if fr.Kind != kind {
			continue
		}
		var f team.TeamFact
		if err := json.Unmarshal([]byte(fr.BodyJSON), &f); err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

// M's op from a relay command ending failed or cancelled queues relay_failed{op_id, state, reason} in the same
// transaction; an op that did not come from a command says nothing. Mutations: no fact → red; every op → the person's op red.
func TestRelayFailedFact_OnlyForAnOpFromACommand(t *testing.T) {
	for _, to := range []team.RelayState{team.RelayFailed, team.RelayCancelled} {
		t.Run(string(to), func(t *testing.T) {
			s := relayVoidSetup(t)
			mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
			mustReport(t, s, relayOpID, RelayReport{State: to, Reason: "write_failed", At: 2000})
			got := relayFacts(t, s, team.FactRelayFailed)
			if len(got) != 1 || got[0].OpID != relayOpID || got[0].State != string(to) || got[0].Reason != "write_failed" || got[0].MK != "mk-1" || got[0].TeamID != "team-L" {
				t.Fatalf("facts = %+v", got)
			}
		})
	}
	s := relayVoidSetup(t)
	op := team.RelayOp{ID: "op-own", Kind: team.RelayKindSelf, HostID: "h:1", SessionID: "sid-1", Ref: "_rmk-1", State: team.RelayClaimed, CreatedAt: 1000, UpdatedAt: 1000}
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, "op-own", RelayReport{State: team.RelayFailed, Reason: "x", At: 2000})
	if got := relayFacts(t, s, team.FactRelayFailed); len(got) != 0 {
		t.Fatalf("a person's own relay announced relay_failed: %+v", got)
	}
}

// The cleared of a command's op says moved{op_id}, not manual (MR-2 wrote manual for the only op that could reach it).
// Mutation gate: always manual / never op_id → red.
func TestRelayMoved_FromACommandOpCarriesTheOpIDAndIsNotManual(t *testing.T) {
	s := relayVoidSetup(t)
	mustApply(t, s, relayPlanFor(relayCmdFor(cmdUUID1, "mk-1", relayOpID)))
	mustReport(t, s, relayOpID, RelayReport{State: team.RelayClaimed, At: 2000})
	mustReport(t, s, relayOpID, RelayReport{State: team.RelayCleared, NewSessionID: "sid-2", NewRef: "_new222", At: 3000})
	got := movedFactsOfHost(t, s, leadHostA)
	if len(got) != 1 || got[0].OpID != relayOpID || got[0].Manual {
		t.Fatalf("moved facts = %+v, want one with op_id %s and manual false", got, relayOpID)
	}
}

func movedFactsOfHost(t *testing.T, s *Store, host string) []team.TeamFact {
	t.Helper()
	var out []team.TeamFact
	for _, fr := range factsOf(t, s, host) {
		if fr.Kind != team.FactMoved {
			continue
		}
		var f team.TeamFact
		if err := json.Unmarshal([]byte(fr.BodyJSON), &f); err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}
