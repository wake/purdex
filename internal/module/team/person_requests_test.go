// internal/module/team/person_requests_test.go — a person's typed /team (relay/team rules v2, RT-1b): the lead, adopt and spawn
// requests that carry a valid person token are granted at once whatever the switch; an agent's own are refused while unattended.
package teammod

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// personLead makes every lead request the fixture builds the person's own: each carries a freshly minted token for sid-1.
func (f *fixture) personLead() {
	prev := f.createReqEdit
	f.createReqEdit = func(r *team.CreateApprovalRequest) {
		if prev != nil {
			prev(r)
		}
		r.PersonToken = f.mintPerson("sid-1", team.PersonCmdLead).Token
	}
}

func assertDecidedByPerson(t *testing.T, a team.Approval, at int64) {
	t.Helper()
	if a.State != team.StateApproved || a.DecidedAt != at || a.DecidedBy == nil || *a.DecidedBy != team.PersonClient() {
		t.Fatalf("row = %s at %d by %+v, want approved at %d by %+v", a.State, a.DecidedAt, a.DecidedBy, at, team.PersonClient())
	}
}

func (f *fixture) assertRefusedByUnattended(code int, body []byte) {
	f.t.Helper()
	if e := decodeErr(f.t, body); code != http.StatusConflict || e.Error != team.ErrUnattendedRefused || e.Detail != team.UnattendedRefusedText {
		f.t.Fatalf("got %d %s, want 409 %s with the text", code, body, team.ErrUnattendedRefused)
	}
}

func (f *fixture) createLeadWith(id string, edit func(*team.CreateApprovalRequest)) (int, []byte) {
	f.t.Helper()
	r := f.createReq(id)
	if edit != nil {
		edit(&r)
	}
	return f.do(http.MethodPost, "/api/team/approvals", r)
}

// A person's /team over the agent's own open request approves THAT request (the person's grant, one team), whatever the switch.
// Mutation gate: answer request_open instead → 409 (red); open a second row → two rows (red).
func TestPersonLead_ApprovesTheAgentsOpenRequest(t *testing.T) {
	for _, on := range []bool{false, true} {
		f := newFixture(t)
		card := f.create(uid(1)) // the agent asked while the switch was off
		f.unatt.set(on)
		f.personLead()
		f.createReqEdit = chain(f.createReqEdit, func(r *team.CreateApprovalRequest) { r.MaxMembers, r.Roots = 8, []string{"/elsewhere"} })
		code, body := f.createLeadWith(uid(2), f.createReqEdit)
		if code != http.StatusCreated {
			t.Fatalf("switch %v: person over an open request: %d %s", on, code, body)
		}
		a := decodeApproval(t, body)
		if a.ID != card.ID {
			t.Fatalf("answered row %s, want the agent's %s", a.ID, card.ID)
		}
		assertDecidedByPerson(t, a, f.clock.Load())
		f.assertTeamGrant("sid-1", 3, []string{"/w"})
		if _, ok, _ := f.m.store.Get(uid(2)); ok {
			t.Fatal("a second row was written")
		}
	}
}

// A refused token writes nothing and costs nothing; an unrelated command's token is as bad as none.
func TestPersonLead_ATokenThatIsNoGoodIs409NothingWritten(t *testing.T) {
	f := newFixture(t)
	code, body := f.createLeadWith(uid(1), func(r *team.CreateApprovalRequest) { r.PersonToken = "deadbeef" })
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrPersonTokenInvalid {
		t.Fatalf("bad token: %d %s, want 409 %s", code, body, team.ErrPersonTokenInvalid)
	}
	tok := f.mintPerson("sid-1", team.PersonCmdRelay).Token // the /relay token is not a /team token
	if code, _ := f.createLeadWith(uid(1), func(r *team.CreateApprovalRequest) { r.PersonToken = tok }); code != http.StatusConflict {
		t.Fatalf("a relay token on a lead request: %d, want 409", code)
	}
	if _, ok, _ := f.m.store.Get(uid(1)); ok {
		t.Fatal("a row was written")
	}
}

// A member still cannot lead (S-17), and the refusal does not spend the token.
func TestPersonLead_AMemberIsRefusedAndKeepsTheToken(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	tok := f.mintPerson("sid-1", team.PersonCmdLead).Token
	code, body := f.createLeadWith(uid(1), func(r *team.CreateApprovalRequest) { r.PersonToken = tok })
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberCannotLead {
		t.Fatalf("member: %d %s, want 409 %s", code, body, team.ErrMemberCannotLead)
	}
	if !f.m.validPerson("sid-1", team.PersonCmdLead, tok) {
		t.Fatal("the refusal spent the token")
	}
}

// The token is spent by the create, and a replay of the same id answers the stored row.
func TestPersonLead_TheTokenIsSpentAndAReplayAnswersTheRow(t *testing.T) {
	f := newFixture(t)
	tok := f.mintPerson("sid-1", team.PersonCmdLead).Token
	set := func(r *team.CreateApprovalRequest) { r.PersonToken = tok }
	if code, body := f.createLeadWith(uid(1), set); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	if f.m.validPerson("sid-1", team.PersonCmdLead, tok) {
		t.Fatal("the token is good after it was used")
	}
	if code, _ := f.createLeadWith(uid(1), set); code != http.StatusOK {
		t.Fatalf("replay with the spent token: %d, want 200", code)
	}
	if code, _ := f.createLeadWith(uid(2), set); code != http.StatusConflict {
		t.Fatalf("a second request with the spent token: %d, want 409", code)
	}
}

// Adopt: a person's command over this lead's own open request approves it; another origin's open request is not theirs to approve.
// Mutation gate: answer request_open for the person too → 409 (red).
func TestPersonAdopt_ApprovesTheLeadsOpenRequestAndRefusesABadToken(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	opened := f.adoptOK(uid(10), "_def456")
	f.unatt.set(true)
	f.personAdopt = true
	a := f.adoptOK(uid(11), "_def456")
	if a.ID != opened.ID {
		t.Fatalf("answered %s, want the open request %s", a.ID, opened.ID)
	}
	assertDecidedByPerson(t, a, f.clock.Load())
	if mem := memberBySpawn(t, f.m.store, uid(10)); mem.SessionID != "sid-2" || mem.State != team.MemberActive {
		t.Fatalf("member = %+v", mem)
	}

	g := newFixture(t)
	g.approveLead(uid(1))
	r := g.adoptReq(uid(10), "_def456")
	r.PersonToken = "nope"
	code, body := g.do(http.MethodPost, "/api/team/approvals", r)
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrPersonTokenInvalid {
		t.Fatalf("bad adopt token: %d %s, want 409 %s", code, body, team.ErrPersonTokenInvalid)
	}
}

// The switch-on refuses the open adopt request too (cancelled / unattended_refused), and PUT reports refused.
func TestSweep_RefusesAnOpenAdoptRequest(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.adoptOK(uid(10), "_def456")
	f.unatt.set(true)
	f.sweep()
	a, _, _ := f.m.store.Get(uid(10))
	if a.State != team.StateCancelled || a.CloseReason != team.ErrUnattendedRefused {
		t.Fatalf("adopt row = %s (%s), want cancelled / %s", a.State, a.CloseReason, team.ErrUnattendedRefused)
	}
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = ?`, uid(10)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("members for the refused adopt = %d (%v), want none", n, err)
	}
}

// Spawn: the person's token marks the op; a bad one is 409 with no op; a re-post of the accepted id joins it although the token
// is spent. Mutation gate: check the token before the replay → the re-post is 409 (red); drop the mark → Person false (red).
func TestPersonSpawn_MarksTheOpAndJoinsOnARepost(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.holdRunners()
	tok := f.mintPerson("sid-1", team.PersonCmdSpawn).Token
	set := func(r *team.SpawnRequest) { r.PersonToken = tok }
	if code, _, e := f.spawn(1, root, set); code != http.StatusOK {
		t.Fatalf("person spawn: %d %+v", code, e)
	}
	row, ok, err := f.m.store.GetSpawnOp(spawnID(1))
	if err != nil || !ok || !row.Person {
		t.Fatalf("op = %+v ok=%v err=%v, want Person", row, ok, err)
	}
	if f.m.validPerson("sid-1", team.PersonCmdSpawn, tok) {
		t.Fatal("the token is good after the spawn was accepted")
	}
	if code, _, e := f.spawn(1, root, set); code != http.StatusOK {
		t.Fatalf("re-post of the accepted id with the spent token: %d %+v, want it joined", code, e)
	}
	code, _, e := f.spawn(2, root, func(r *team.SpawnRequest) { r.PersonToken = "nope" })
	if code != http.StatusConflict || e.Error != team.ErrPersonTokenInvalid {
		t.Fatalf("bad token: %d %+v, want 409 %s", code, e, team.ErrPersonTokenInvalid)
	}
	if _, ok, _ := f.m.store.GetSpawnOp(spawnID(2)); ok {
		t.Fatal("an op was written for the refused spawn")
	}
	// an agent's own spawn is not marked
	if code, _, e := f.spawn(3, root, nil); code != http.StatusOK {
		t.Fatalf("agent spawn: %d %+v", code, e)
	}
	if r3, _, _ := f.m.store.GetSpawnOp(spawnID(3)); r3.Person {
		t.Fatal("an agent's spawn is marked as the person's")
	}
}

// A team.db written before spawn_ops.person existed opens, keeps its rows and reads them as not the person's. Mutation gate: skip
// the migration → the select fails (red).
func TestMigrateSpawnPerson_AddsTheColumnKeepsRowsOnce(t *testing.T) {
	s := openTestStore(t)
	now := int64(1_000_000)
	op := spawnRow{ID: spawnID(5), TeamID: "team-1", HostID: "h:1", OriginSessionID: "sid-1", Cwd: "/w", Title: "t",
		TmuxName: "tm-0000000500", Step: team.StepAccepted, State: team.SpawnRunning, CreatedAt: now, UpdatedAt: now}
	name, err := team.SpawnTmuxName(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	op.TmuxName = name
	if _, _, _, err := insertSpawnOp(s.db, op, "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE spawn_ops DROP COLUMN person`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrateSpawnPerson(s.db); err != nil {
		t.Fatal(err)
	}
	if err := migrateSpawnPerson(s.db); err != nil { // twice: idempotent
		t.Fatal(err)
	}
	got, ok, err := s.GetSpawnOp(op.ID)
	if err != nil || !ok || got.Person || got.Cwd != "/w" {
		t.Fatalf("old row = %+v ok=%v err=%v, want it kept and not the person's", got, ok, err)
	}
}

// The token is not part of a spawn's identity: a re-post with another (or no) token is the same spawn.
func TestSpawnHash_IgnoresThePersonToken(t *testing.T) {
	a := team.SpawnRequest{ID: spawnID(1), Cwd: "/w", Title: "t"}
	b := a
	b.PersonToken = "tok"
	if spawnHash("sid-1", a) != spawnHash("sid-1", b) {
		t.Fatal("the person token changed the spawn's hash")
	}
}

// codex R1 P1: a person's request that approved the agent's open request has no row of its own; a retry of its id after a lost
// response is the same request and gets that row, not person_token_invalid. Mutation gate: do not record the alias → the retry is
// 409 (red); drop the hash check → a different request under the same id is answered (red).
func TestPersonLead_ARetryOfARequestThatApprovedAnOpenOneGetsThatRow(t *testing.T) {
	f := newFixture(t)
	card := f.create(uid(1))
	tok := f.mintPerson("sid-1", team.PersonCmdLead).Token
	set := func(r *team.CreateApprovalRequest) { r.PersonToken = tok }
	if code, body := f.createLeadWith(uid(2), set); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	code, body := f.createLeadWith(uid(2), set) // the response was lost: the same id, the spent token
	if a := decodeApproval(t, body); code != http.StatusOK || a.ID != card.ID {
		t.Fatalf("retry: %d %s, want 200 with the row %s", code, body, card.ID)
	}
	code, body = f.createLeadWith(uid(2), func(r *team.CreateApprovalRequest) { r.PersonToken = tok; r.Reason = "something else" })
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrIDConflict {
		t.Fatalf("same id, another request: %d %s, want 409 %s", code, body, team.ErrIDConflict)
	}
}

func TestPersonAdopt_ARetryOfARequestThatApprovedAnOpenOneGetsThatRow(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	opened := f.adoptOK(uid(10), "_def456")
	tok := f.mintPerson("sid-1", team.PersonCmdAdopt).Token
	post := func(target string) (int, []byte) {
		r := f.adoptReq(uid(11), target)
		r.PersonToken = tok
		return f.do(http.MethodPost, "/api/team/approvals", r)
	}
	if code, body := post("_def456"); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	code, body := post("_def456")
	if a := decodeApproval(t, body); code != http.StatusOK || a.ID != opened.ID {
		t.Fatalf("retry: %d %s, want 200 with the row %s", code, body, opened.ID)
	}
}

// codex R1 P2: a token on a remote spawn has no meaning there (S-8): refused, not silently ignored.
func TestPersonSpawn_ARemoteSpawnWithATokenIs400(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	code, _, e := f.spawn(1, root, func(r *team.SpawnRequest) { r.Host, r.PersonToken = "air26", "tok" })
	if code != http.StatusBadRequest || e.Error != team.ErrBadRequest {
		t.Fatalf("remote spawn with a token: %d %+v, want 400 %s", code, e, team.ErrBadRequest)
	}
}

// codex attack (high): the alias is written BEFORE the approval. When it cannot be written nothing is approved and the token is
// good; an alias left behind by a crash points at a still-open row and is no replay. Mutation gate: write the alias after the
// approve and ignore its error → the failing case approves anyway (red).
func TestPersonLead_AnAliasThatCannotBeWrittenApprovesNothing(t *testing.T) {
	f := newFixture(t)
	card := f.create(uid(1))
	if _, err := f.m.store.db.Exec(`CREATE TRIGGER no_alias BEFORE INSERT ON person_aliases BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	tok := f.mintPerson("sid-1", team.PersonCmdLead).Token
	code, body := f.createLeadWith(uid(2), func(r *team.CreateApprovalRequest) { r.PersonToken = tok })
	if code != http.StatusInternalServerError {
		t.Fatalf("alias write failed: %d %s, want 500", code, body)
	}
	if a, _, _ := f.m.store.Get(card.ID); a.State != team.StateOpen {
		t.Fatalf("the agent's request is %s, want still open", a.State)
	}
	if !f.m.validPerson("sid-1", team.PersonCmdLead, tok) {
		t.Fatal("the token was spent")
	}
}

func TestPersonLead_AnAliasLeftByACrashIsNoReplay(t *testing.T) {
	f := newFixture(t)
	card := f.create(uid(1))
	if err := f.m.store.RecordPersonAlias(uid(2), card.ID, "h", 1); err != nil {
		t.Fatal(err)
	}
	// the crash happened before the approve: the alias points at a row that is still open, so a retry is a fresh request
	rec := httptest.NewRecorder()
	if f.m.replayOfPersonAlias(rec, uid(2), "h") {
		t.Fatalf("an alias to an open row was answered as a replay: %d %s", rec.Code, rec.Body)
	}
	// once that row is closed the alias is the answer
	if code, body := f.decide(card.ID, "deny"); code != http.StatusOK {
		t.Fatalf("deny: %d %s", code, body)
	}
	rec = httptest.NewRecorder()
	if !f.m.replayOfPersonAlias(rec, uid(2), "h") || rec.Code != http.StatusOK {
		t.Fatalf("an alias to a closed row: answered=%v code=%d", rec.Code != 0, rec.Code)
	}
}

// codex attack (medium): S-7 gives the person's lead the origin's cwd; a session with none cannot be a lead by this route even if
// the request names an absolute root. Nothing is written and the token is good.
func TestPersonLead_ASessionWithNoCwdIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	f := newFixture(t)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Name: "n10", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: ""})
	tok := f.mintPerson("sid-1", team.PersonCmdLead).Token
	code, body := f.createLeadWith(uid(1), func(r *team.CreateApprovalRequest) { r.PersonToken, r.Roots = tok, []string{"/elsewhere"} })
	if e := decodeErr(t, body); code != http.StatusBadRequest || e.Error != team.ErrBadRequest {
		t.Fatalf("no cwd: %d %s, want 400 %s", code, body, team.ErrBadRequest)
	}
	if _, ok, _ := f.m.store.LiveTeamByLead("sid-1"); ok {
		t.Fatal("a team was created")
	}
	if !f.m.validPerson("sid-1", team.PersonCmdLead, tok) {
		t.Fatal("the token was spent")
	}
}

// codex critic: a retry answered from its alias must not need the member host (it may be down by then). Mutation gate: resolve
// the remote target before looking at the alias → the retry fails with the host's error (red).
func TestPersonAdopt_ARemoteRetryIsAnsweredFromTheAliasWithTheHostDown(t *testing.T) {
	f, _ := adoptFixture(t)
	opened := f.adoptOK(uid(40), remoteTarget)
	f.personAdopt = false
	tok := f.mintPerson("sid-1", team.PersonCmdAdopt).Token
	post := func() (int, []byte) {
		r := f.adoptReq(uid(41), remoteTarget)
		r.PersonToken = tok
		return f.do(http.MethodPost, "/api/team/approvals", r)
	}
	if code, body := post(); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return nil, errors.New("host down") }
	code, body := post()
	if a := decodeApproval(t, body); code != http.StatusOK || a.ID != opened.ID {
		t.Fatalf("retry with the host down: %d %s, want 200 with the row %s", code, body, opened.ID)
	}
}

// codex re-review: an alias a crash left on a still-open remote adopt row is a fresh request - the target IS resolved again (and with
// the host up it is not a 503). Mutation gate: skip the resolve for any alias → 503 (red).
func TestPersonAdopt_AnAliasToAnOpenRemoteRowStillResolvesTheTarget(t *testing.T) {
	f, _ := adoptFixture(t)
	opened := f.adoptOK(uid(50), remoteTarget)
	if err := f.m.store.RecordPersonAlias(uid(51), opened.ID, "h", 1); err != nil {
		t.Fatal(err)
	}
	tok := f.mintPerson("sid-1", team.PersonCmdAdopt).Token
	r := f.adoptReq(uid(51), remoteTarget)
	r.PersonToken = tok
	code, body := f.do(http.MethodPost, "/api/team/approvals", r)
	if code == http.StatusServiceUnavailable {
		t.Fatalf("the open alias was taken for a replay and the target was not resolved: %d %s", code, body)
	}
}
