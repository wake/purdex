// internal/module/team/pause_release_test.go — a person releases one paused session, and the release token the re-ask carries
// (unattended quota pause spec 2026-10-12 §3.4 "Release one" / "Release token", UP-3b). The release gate (pauseEntryEnabled) is
// opened in the tests that need a pause to be possible, to show the re-ask is not paused when it would otherwise be.
package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

func (f *fixture) releasePaused(sid string, body any) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/team/paused/"+sid+"/release", body)
}

func appRelease() team.PauseReleaseRequest {
	return team.PauseReleaseRequest{Client: team.Client{Kind: "app", Label: "Purdex.app @ air26"}}
}

// ---- the release route ----

// The claim is audited like PUT /api/team/roots: a missing, blank or non-app client is a 400 that changes nothing.
// Mutation gate: skip the claim check → a blank client releases (red).
func TestRelease_TheClientClaimIsChecked(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	for name, body := range map[string]any{
		"no body":      nil,
		"no client":    map[string]any{},
		"blank label":  team.PauseReleaseRequest{Client: team.Client{Kind: "app", Label: "  "}},
		"another kind": team.PauseReleaseRequest{Client: team.Client{Kind: "cli", Label: "x"}},
		"empty kind":   team.PauseReleaseRequest{Client: team.Client{Label: "x"}},
	} {
		if code, raw := f.releasePaused("sid-1", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, code, raw)
		}
	}
	if len(f.pauses()) != 1 || len(f.outbox()) != 0 {
		t.Fatalf("a refused claim changed something: pauses %d, outbox %d", len(f.pauses()), len(f.outbox()))
	}
}

func TestRelease_ASessionThatIsNotPausedIs404(t *testing.T) {
	f := leadWorld(t)
	code, raw := f.releasePaused("sid-1", appRelease())
	if code != http.StatusNotFound || decodeErr(t, raw).Error != team.ErrNotPaused {
		t.Fatalf("%d %s, want 404 not_paused", code, raw)
	}
	if code, _ := f.releasePaused("sid-nobody", appRelease()); code != http.StatusNotFound {
		t.Fatalf("unknown session: %d, want 404", code)
	}
}

// A solo session: its pause ends with 「已解除暫停」 (not 「管理者已回位」), a token and the reask; the switch stays on.
func TestRelease_ASoloSessionIsReleasedWithTheReleaseText(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleSolo, "_abc123", "boss", 1)
	code, raw := f.releasePaused("sid-1", appRelease())
	var res team.PauseReleaseResponse
	if code != http.StatusOK || json.Unmarshal(raw, &res) != nil || res.Released != 1 {
		t.Fatalf("release: %d %s", code, raw)
	}
	ob := f.outboxOf("sid-1")
	toks := f.tokens()
	if len(ob) != 2 || ob[0].Text != team.PauseReleasedSolo || ob[1].Kind != team.PauseOutboxReask || len(toks) != 1 || ob[1].ReaskID != toks[0] {
		t.Fatalf("outbox = %+v, tokens = %v", ob, toks)
	}
	if st, _ := f.unatt.Unattended(); !st.On {
		t.Fatal("the release switched unattended off")
	}
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v", f.pauses())
	}
}

// A lead takes the members it paused with it (their `lead` rows); a member that is also paused for its own quota stays paused.
// Mutation gate: release only the lead's own row → the member keeps its `lead` row (red).
func TestRelease_ALeadReleasesItsMembersLeadCauseOnly(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.seedPause("sid-m1", team.PauseCauseLead, team.PauseRoleMember, "_mem001", "worker", 1)
	f.seedPause("sid-m2", team.PauseCauseLead, team.PauseRoleMember, "_mem002", "second", 1)
	f.seedPause("sid-m2", team.PauseCauseQuota, team.PauseRoleMember, "_mem002", "second", 1) // its own quota too
	code, raw := f.releasePaused("sid-1", appRelease())
	var res team.PauseReleaseResponse
	if code != http.StatusOK || json.Unmarshal(raw, &res) != nil || res.Released != 2 {
		t.Fatalf("release: %d %s (want the lead and sid-m1 released)", code, raw)
	}
	ps := f.pauses()
	if _, ok := ps["sid-m2/quota"]; !ok || len(ps) != 1 {
		t.Fatalf("pauses = %+v, want only sid-m2's own quota row", ps)
	}
	ob := f.outbox()
	if len(ob) == 0 || ob[0].Session != "sid-1" || ob[0].Text != team.PauseReleasedSolo {
		t.Fatalf("outbox = %+v, the lead goes first", ob)
	}
	if got := f.outboxOf("sid-m1"); len(got) != 1 || got[0].Text != team.PauseResumedMemberLead {
		t.Fatalf("sid-m1 = %+v", got)
	}
	if got := f.outboxOf("sid-m2"); len(got) != 0 {
		t.Fatalf("sid-m2 still has a cause and was told %+v", got)
	}
}

// The lead's members are those of ITS team and the same on-period: a `lead` row naming the same lead session but another team (the
// session id led another team later) or another on-period is a stale row and stays.
// Mutation gate: select by lead_session_id alone → the stale rows are released (red).
func TestRelease_OnlyTheLeadsOwnTeamAndOnPeriodAreTakenAlong(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-1", team.PauseCauseQuota, team.PauseRoleLead, "_abc123", "boss", 1)
	f.seedPause("sid-m1", team.PauseCauseLead, team.PauseRoleMember, "_mem001", "worker", 1)
	for sid, p := range map[string]PauseRow{
		"sid-old-team":   {SessionID: "sid-old-team", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(2), LeadSessionID: "sid-1", OnSince: 1, Since: 5},
		"sid-old-period": {SessionID: "sid-old-period", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 0, Since: 5},
	} {
		if _, err := f.m.store.AddPause(p); err != nil {
			t.Fatal(sid, err)
		}
	}
	if code, raw := f.releasePaused("sid-1", appRelease()); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, raw)
	}
	ps := f.pauses()
	if _, a := ps["sid-old-team/lead"]; !a {
		t.Fatalf("pauses = %+v: another team's row was released", ps)
	}
	if _, a := ps["sid-old-period/lead"]; !a {
		t.Fatalf("pauses = %+v: an earlier on-period's row was released", ps)
	}
	if _, a := ps["sid-m1/lead"]; a || len(ps) != 2 {
		t.Fatalf("pauses = %+v, want sid-m1 released and the two stale rows left", ps)
	}
}

// The team and the on-period must belong to the SAME row of the released session: a member row that pairs team A with the period of
// another row of the lead (a cross pairing) is not its.
// Mutation gate: match team and on-period independently → the cross-paired row is released (red).
func TestRelease_ATeamAndPeriodFromDifferentOwnerRowsDoNotMatch(t *testing.T) {
	f := leadWorld(t)
	for _, p := range []PauseRow{
		{SessionID: "sid-1", Cause: team.PauseCauseQuota, Role: team.PauseRoleLead, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 1, Since: 1},
		{SessionID: "sid-1", Cause: team.PauseCauseLead, Role: team.PauseRoleLead, TeamID: uid(2), LeadSessionID: "sid-1", OnSince: 2, Since: 1},
		{SessionID: "sid-cross", Cause: team.PauseCauseLead, Role: team.PauseRoleMember, TeamID: uid(1), LeadSessionID: "sid-1", OnSince: 2, Since: 1}, // team of row 1, period of row 2
	} {
		if _, err := f.m.store.AddPause(p); err != nil {
			t.Fatal(err)
		}
	}
	if code, raw := f.releasePaused("sid-1", appRelease()); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, raw)
	}
	if _, ok := f.pauses()["sid-cross/lead"]; !ok {
		t.Fatalf("pauses = %+v: the cross-paired row was released", f.pauses())
	}
}

// A member paused for its own quota: released alone; its lead gets the one list.
func TestRelease_AMemberQuotaPauseIsReleasedAlone(t *testing.T) {
	f := leadWorld(t)
	f.seedPause("sid-m1", team.PauseCauseQuota, team.PauseRoleMember, "_mem001", "worker", 1)
	if code, raw := f.releasePaused("sid-m1", appRelease()); code != http.StatusOK {
		t.Fatalf("release: %d %s", code, raw)
	}
	if len(f.pauses()) != 0 {
		t.Fatalf("pauses = %+v", f.pauses())
	}
	m, l := f.outboxOf("sid-m1"), f.outboxOf("sid-1")
	if len(m) != 1 || m[0].Text != team.PauseResumedMemberQuota || len(l) != 1 || !strings.Contains(l[0].Text, "worker（_mem001）") {
		t.Fatalf("member %+v, lead %+v", m, l)
	}
}

// ---- the release token ----

// tokenWorld: a solo session sid-1 with the quota gone, unattended on, the rule on, and the gate OPEN — so without a token the begin
// would be paused.
func tokenWorld(t *testing.T) *fixture {
	t.Helper()
	enablePauseEntry(t)
	f := newFixture(t)
	f.quotaWorld()
	f.setQuota("sid-1", 0)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	return f
}

func (f *fixture) giveToken(sid string, at int64) string {
	f.t.Helper()
	tok := uuid.NewString()
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_release_tokens (token, session_id, created_at) VALUES (?, ?, ?)`, tok, sid, at); err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fixture) beginWith(sid, token string) (int, []byte) {
	f.t.Helper()
	r := beginReq(sid)
	r.ReaskID = token
	return f.do(http.MethodPost, "/api/relay/begin", r)
}

// A valid token with no quota and unattended on: a card for a person — not a pause, not approved by the daemon (not now, not at the
// next sweep, not after the quota is raised) — and the token is spent in the same step.
// Mutation gates: ignore the token → the begin is paused (red); do not spend it → the token is still there (red).
func TestReask_AValidTokenOpensACardForAPersonAndIsSpent(t *testing.T) {
	f := tokenWorld(t)
	tok := f.giveToken("sid-1", f.clock.Load())
	code, raw := f.beginWith("sid-1", tok)
	var b team.RelayBeginResponse
	if code != http.StatusCreated || json.Unmarshal(raw, &b) != nil || b.Op.State != team.RelayAwaitingApproval {
		t.Fatalf("begin: %d %s", code, raw)
	}
	if f.rowState(b.RequestID) != team.StateOpen || len(f.pauses()) != 0 || len(f.outbox()) != 0 {
		t.Fatalf("row %s, pauses %d, outbox %d: want an open card and nothing paused", f.rowState(b.RequestID), len(f.pauses()), len(f.outbox()))
	}
	if got := f.tokens(); len(got) != 0 {
		t.Fatalf("tokens = %v, the token must be spent", got)
	}
	row, _, _ := f.m.store.Get(b.RequestID)
	if !strings.Contains(string(row.Payload), `"reask":true`) {
		t.Fatalf("payload = %s", row.Payload)
	}
	f.m.tick()
	f.setQuota("sid-1", 3) // the quota is raised: still no daemon approval
	f.m.tick()
	f.m.createMu.Lock()
	f.m.sweepUnattended("switch on")
	f.m.createMu.Unlock()
	if f.rowState(b.RequestID) != team.StateOpen || f.op(b.Op.ID).State != team.RelayAwaitingApproval {
		t.Fatalf("row %s, op %s: a reask card is a person's alone", f.rowState(b.RequestID), f.op(b.Op.ID).State)
	}
	_, v, _ := f.getUnattended("")
	if len(v.Held) != 0 {
		t.Fatalf("held = %+v, a person's card is not waiting for quota", v.Held)
	}
	if code, _ := f.decide(b.RequestID, "approve"); code != http.StatusOK {
		t.Fatalf("a person's approve: %d", code)
	}
}

// With the quota there, a valid token is still a person's card (the notice promised 「等使用者核准」).
func TestReask_AValidTokenWithQuotaStillOpensACardForAPerson(t *testing.T) {
	f := tokenWorld(t)
	f.setQuota("sid-1", 5)
	tok := f.giveToken("sid-1", f.clock.Load())
	code, raw := f.beginWith("sid-1", tok)
	var b team.RelayBeginResponse
	if code != http.StatusCreated || json.Unmarshal(raw, &b) != nil || b.Op.State != team.RelayAwaitingApproval || f.rowState(b.RequestID) != team.StateOpen {
		t.Fatalf("begin: %d %s", code, raw)
	}
}

// One use: once spent, the same token is only an unknown token and the ordinary rules apply (here: the pause).
// Mutation gate: do not delete the token on use → the second begin is a card again (red).
func TestReask_ATokenIsSpentOnce(t *testing.T) {
	f := tokenWorld(t)
	tok := f.giveToken("sid-1", f.clock.Load())
	code, raw := f.beginWith("sid-1", tok)
	var b team.RelayBeginResponse
	if code != http.StatusCreated || json.Unmarshal(raw, &b) != nil {
		t.Fatalf("first: %d %s", code, raw)
	}
	if code, _ := f.decide(b.RequestID, "deny"); code != http.StatusOK {
		t.Fatal("deny")
	}
	code, raw = f.beginWith("sid-1", tok)
	if code != http.StatusConflict || decodeErr(t, raw).Error != team.ErrPausedNoQuota {
		t.Fatalf("second: %d %s, want the ordinary pause", code, raw)
	}
}

// A duplicate reask delivered to the mod: two begins with the same token while the first card is open: only one op, the token spent once.
func TestReask_ADuplicateBeginSpendsTheTokenOnce(t *testing.T) {
	f := tokenWorld(t)
	tok := f.giveToken("sid-1", f.clock.Load())
	if code, raw := f.beginWith("sid-1", tok); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, raw)
	}
	code, raw := f.beginWith("sid-1", tok)
	if code != http.StatusConflict || decodeErr(t, raw).Error != team.ErrRelayOpen {
		t.Fatalf("second: %d %s, want relay_open", code, raw)
	}
	if f.opCount() != 1 || len(f.tokens()) != 0 {
		t.Fatalf("ops %d, tokens %v", f.opCount(), f.tokens())
	}
}

// Another session's token is not this one's: ordinary rules, and the owner keeps its token.
// Mutation gate: do not check the session → the begin is a card (red).
func TestReask_AnotherSessionsTokenIsIgnored(t *testing.T) {
	f := tokenWorld(t)
	tok := f.giveToken("sid-other", f.clock.Load())
	code, raw := f.beginWith("sid-1", tok)
	if code != http.StatusConflict || decodeErr(t, raw).Error != team.ErrPausedNoQuota {
		t.Fatalf("begin: %d %s, want the ordinary pause", code, raw)
	}
	if got := f.tokens(); len(got) != 1 || got[0] != tok {
		t.Fatalf("tokens = %v, the owner's token must stay", got)
	}
}

// A token older than 24 h is no token; an unknown one neither.
// Mutation gate: ignore the age → the old token opens a card (red).
func TestReask_AnExpiredOrUnknownTokenIsIgnored(t *testing.T) {
	f := tokenWorld(t)
	old := f.giveToken("sid-1", f.clock.Load()-team.PauseTokenTTLMs-1)
	for name, tok := range map[string]string{"expired": old, "unknown": uuid.NewString()} {
		code, raw := f.beginWith("sid-1", tok)
		if code != http.StatusConflict || decodeErr(t, raw).Error != team.ErrPausedNoQuota {
			t.Fatalf("%s: %d %s, want the ordinary pause", name, code, raw)
		}
		f.m.store.db.Exec(`DELETE FROM session_pauses`)
		f.m.store.db.Exec(`DELETE FROM pause_outbox`)
	}
}

// The gate closed or the switch off: a valid token is still a person's card and is still spent.
func TestReask_AValidTokenIsACardWhateverTheSwitchSays(t *testing.T) {
	for _, unattended := range []bool{true, false} {
		f := tokenWorld(t)
		f.unatt.set(unattended)
		tok := f.giveToken("sid-1", f.clock.Load())
		code, raw := f.beginWith("sid-1", tok)
		if code != http.StatusCreated || len(f.tokens()) != 0 || len(f.pauses()) != 0 {
			t.Fatalf("unattended=%v: %d %s tokens %v", unattended, code, raw, f.tokens())
		}
	}
}

// A request_id replayed with a valid token the first begin did not carry is a conflicting reuse (409), not a retry: the token stays
// unspent. A real retry of a reask begin (same id, the token already spent) still gets its op.
// Mutation gate: drop the check in sameBeginPayload → the replay answers 201 with the ordinary op (red).
func TestReask_ATokenOnAReplayedRequestIDIsAConflictNotARetry(t *testing.T) {
	f := newFixture(t)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	rid1 := uuid.NewString()
	first := beginReq("sid-1")
	first.RequestID = rid1
	if code, raw := f.do(http.MethodPost, "/api/relay/begin", first); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, raw)
	}
	tok := f.giveToken("sid-1", f.clock.Load())
	again := first
	again.ReaskID = tok
	if code, raw := f.do(http.MethodPost, "/api/relay/begin", again); code != http.StatusConflict {
		t.Fatalf("replay with a token: %d %s, want 409", code, raw)
	}
	if got := f.tokens(); len(got) != 1 {
		t.Fatalf("tokens = %v, must be unspent", got)
	}
	// a lost-response retry of a reask begin: same id, token already spent
	g := newFixture(t)
	g.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "self/boss", PID: 10})
	tok2 := g.giveToken("sid-1", g.clock.Load())
	r := beginReq("sid-1")
	r.RequestID, r.ReaskID = uuid.NewString(), tok2
	if code, raw := g.do(http.MethodPost, "/api/relay/begin", r); code != http.StatusCreated {
		t.Fatalf("reask first: %d %s", code, raw)
	}
	if code, raw := g.do(http.MethodPost, "/api/relay/begin", r); code != http.StatusCreated {
		t.Fatalf("reask retry: %d %s, want the same op", code, raw)
	}
	if g.opCount() != 1 {
		t.Fatalf("ops = %d", g.opCount())
	}
}

// A re-ask replayed with another token, or none, is not the same request (409); the same token is the retry.
// Mutation gate: do not compare the stored token → the replay with token B answers 201 (red).
func TestReask_AReplayMustCarryTheSameToken(t *testing.T) {
	f := tokenWorld(t)
	a, bt := f.giveToken("sid-1", f.clock.Load()), f.giveToken("sid-1", f.clock.Load())
	req := beginReq("sid-1")
	req.RequestID, req.ReaskID = uuid.NewString(), a
	if code, raw := f.do(http.MethodPost, "/api/relay/begin", req); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, raw)
	}
	for name, tok := range map[string]string{"another token": bt, "no token": ""} {
		r := req
		r.ReaskID = tok
		if code, raw := f.do(http.MethodPost, "/api/relay/begin", r); code != http.StatusConflict {
			t.Fatalf("%s: %d %s, want 409", name, code, raw)
		}
	}
	if got := f.tokens(); len(got) != 1 || got[0] != bt {
		t.Fatalf("tokens = %v, token B must stay unspent", got)
	}
}

// Two identical re-asks that arrive together (both miss the request_id lookup before the lock): the one that waited gets the op the
// first opened (201), not relay_open 409; one op, one card, the token spent once.
// Mutation gate: drop the lookup under the lock → the second answers 409 (red).
func TestReask_TwoIdenticalBeginsInFlightGetTheSameOp(t *testing.T) {
	f := tokenWorld(t)
	tok := f.giveToken("sid-1", f.clock.Load())
	req := beginReq("sid-1")
	req.RequestID, req.ReaskID = uuid.NewString(), tok
	f.m.createMu.Lock() // both begins pass their early checks and queue on the lock
	type result struct {
		code int
		raw  []byte
	}
	out := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			code, raw := f.do(http.MethodPost, "/api/relay/begin", req)
			out <- result{code, raw}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	f.m.createMu.Unlock()
	a, b := <-out, <-out
	if a.code != http.StatusCreated || b.code != http.StatusCreated {
		t.Fatalf("answers %d %s / %d %s, want two 201", a.code, a.raw, b.code, b.raw)
	}
	var ra, rb team.RelayBeginResponse
	if json.Unmarshal(a.raw, &ra) != nil || json.Unmarshal(b.raw, &rb) != nil || ra.Op.ID != rb.Op.ID || f.opCount() != 1 || len(f.tokens()) != 0 {
		t.Fatalf("ops %s / %s (%d in all), tokens %v", ra.Op.ID, rb.Op.ID, f.opCount(), f.tokens())
	}
}

// reask_id must be a UUID.
func TestReask_ABadTokenIsA400(t *testing.T) {
	f := tokenWorld(t)
	if code, raw := f.beginWith("sid-1", "nope"); code != http.StatusBadRequest {
		t.Fatalf("%d %s, want 400", code, raw)
	}
}
