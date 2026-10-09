// internal/module/team/facts_handler_test.go
package teammod

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/team"
)

// The facts route on L (cross-host team spec §4.5, §6.1, §6.3, §3.1 rule 3, §11; plan X3b-2): the same binding as the
// commands route, idempotency by id and content with refusals stored, and `ended` as a CAS on the member row.

const (
	factRoute = "/api/peers/team/facts"
	factUUID1 = "a1111111-1111-4111-8111-111111111111"
	factUUID2 = "a2222222-2222-4222-8222-222222222222"
	factUUID3 = "a3333333-3333-4333-8333-333333333333"
)

func (f *fixture) postFact(p *middleware.Principal, body any) (int, []byte) {
	f.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(http.MethodPost, factRoute, rd)
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func endedFact(id, mk string) team.TeamFact {
	return team.TeamFact{ID: id, Kind: team.FactEnded, ToHostID: "h:1", TeamID: uid(1), MK: mk, Reason: team.FactReasonSessionGone}
}

func factLogCount(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_fact_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func factFixture(t *testing.T) *fixture {
	t.Helper()
	f, _ := cmdFixture(t)
	f.setLeadHost(true)
	return f
}

func TestFacts_BindingRefusals(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	good := endedFact(factUUID1, "mk1")
	for name, tc := range map[string]struct {
		p      *middleware.Principal
		status int
		code   string
	}{
		"no principal":              {nil, 403, "host_unverified"},
		"admin":                     {&middleware.Principal{Kind: middleware.PrincipalAdmin}, 403, "admin_not_allowed"},
		"unverified host":           {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead"}, 403, "host_unverified"},
		"entry re-created":          {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "lead", HostID: "other:9"}, 403, "host_unverified"},
		"alias no longer in config": {&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "gone", HostID: "gone:1"}, 403, "host_unverified"},
	} {
		code, body := f.postFact(tc.p, good)
		if code != tc.status || errCode(t, body) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s", name, code, body, tc.status, tc.code)
		}
	}
	if factLogCount(t, f) != 0 {
		t.Fatal("a refused binding was logged")
	}
	if st, _ := f.memberRowState("abc12"); st != rowActive {
		t.Fatalf("row = %s after refused bindings", st)
	}
}

func TestFacts_ShapeAndAddressing(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	wrong := endedFact(factUUID1, "mk1")
	wrong.ToHostID = "other:1"
	noMK := endedFact(factUUID2, "")
	badID := endedFact("nope", "mk1")
	for name, tc := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"wrong host":     {wrong, 409, "wrong_host"},
		"no mk":          {noMK, 400, "bad_request"},
		"bad id":         {badID, 400, "bad_request"},
		"not JSON":       {"{", 400, "bad_request"},
		"reserved moved": {team.TeamFact{ID: factUUID2, Kind: "moved", ToHostID: "h:1", TeamID: uid(1), MK: "mk1"}, 400, "unsupported_kind"},
		"registered":     {team.TeamFact{ID: factUUID3, Kind: "registered", ToHostID: "h:1", TeamID: uid(1), MK: "mk1"}, 400, "unsupported_kind"},
	} {
		code, body := f.postFact(leadPrincipal(), tc.body)
		if code != tc.status || errCode(t, body) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s", name, code, body, tc.status, tc.code)
		}
	}
	// wrong_host and unsupported_kind are stored decisions (wrong host, moved, registered); bad_request is not.
	if n := factLogCount(t, f); n != 3 {
		t.Fatalf("%d logged, want 3", n)
	}
}

// Rule 3 (codex): an unsupported_kind refusal is stored; a copy resent after this host learned the kind meets the stored
// refusal, and another body under the id is id_conflict. Mutation gate: do not store the refusal → red.
func TestFacts_AnUnsupportedKindRefusalIsStoredAndSurvivesAnUpgrade(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	reg := team.TeamFact{ID: factUUID1, Kind: "registered", ToHostID: "h:1", TeamID: uid(1), MK: "mk1"}
	code, first := f.postFact(leadPrincipal(), reg)
	if code != 400 || errCode(t, first) != "unsupported_kind" || factLogCount(t, f) != 1 {
		t.Fatalf("first = %d %s, logged %d", code, first, factLogCount(t, f))
	}
	// the upgrade: the same copy would now be an applicable kind; the stored refusal still answers
	if _, err := f.m.store.db.Exec(`UPDATE team_fact_log SET kind = kind`); err != nil {
		t.Fatal(err)
	}
	code, again := f.postFact(leadPrincipal(), reg)
	if code != 400 || !bytes.Equal(first, again) {
		t.Fatalf("replay = %d %s, want the stored %s", code, again, first)
	}
	other := reg
	other.MK = "mk2"
	if code, body := f.postFact(leadPrincipal(), other); code != 409 || errCode(t, body) != "id_conflict" {
		t.Fatalf("other body = %d %s", code, body)
	}
	// wrong_host is stored the same way
	wrong := endedFact(factUUID2, "mk1")
	wrong.ToHostID = "other:1"
	_, w1 := f.postFact(leadPrincipal(), wrong)
	_, w2 := f.postFact(leadPrincipal(), wrong)
	if !bytes.Equal(w1, w2) || factLogCount(t, f) != 2 {
		t.Fatalf("wrong_host replay %s vs %s, logged %d", w1, w2, factLogCount(t, f))
	}
}

func TestFacts_EndedMovesALiveRowToGoneAndAnswersApplied(t *testing.T) {
	for _, from := range []string{rowJoining, rowActive, "releasing", "killing"} {
		f := factFixture(t)
		f.remoteRow("abc12", "lead:1", "mk1", from)
		code, body := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1"))
		if code != 200 {
			t.Fatalf("from %s: %d %s", from, code, body)
		}
		var ans team.TeamFactAnswer
		if err := json.Unmarshal(body, &ans); err != nil || ans.ID != factUUID1 || ans.HostID != "h:1" {
			t.Fatalf("answer = %+v %v", ans, err)
		}
		if st, reason := f.memberRowState("abc12"); st != "gone" || reason != team.FactReasonSessionGone {
			t.Fatalf("from %s: row = %s{%s}, want gone{session_gone}", from, st, reason)
		}
		if factLogCount(t, f) != 1 {
			t.Fatalf("from %s: fact not logged", from)
		}
	}
}

// §4.2: `ended` may arrive before the adopt's own answer; the late `applied` then finds `gone` and is ignored (the
// monotonic CAS of §3.1 rule 5). Mutation gate: ended only from active → the joining case is red.
func TestFacts_EndedBeforeTheAdoptAnswerLeavesTheRowGone(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowJoining)
	if code, body := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1")); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if st, _ := f.memberRowState("abc12"); st != "gone" {
		t.Fatalf("row = %s", st)
	}
}

// Rule 5 / D4: terminal rows and ended teams are not rewritten; the fact is answered and logged (ignored).
func TestFacts_EndedLeavesTerminalRowsAndEndedTeamsAlone(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", "released")
	f.remoteRow("def34", "lead:1", "mk2", rowActive)
	if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	for i, mk := range []string{"mk1", "mk2"} {
		id := []string{factUUID1, factUUID2}[i]
		code, body := f.postFact(leadPrincipal(), endedFact(id, mk))
		if code != 200 {
			t.Fatalf("%s: %d %s", mk, code, body)
		}
	}
	if st, _ := f.memberRowState("abc12"); st != "released" {
		t.Fatalf("terminal row = %s", st)
	}
	if st, _ := f.memberRowState("def34"); st != rowActive {
		t.Fatalf("row of an ended team = %s, want it left as it was", st)
	}
}

// §4.5 binding: the row must be THIS host's, with this mk and team. Another host's mk, another team, or a local row is
// not_your_member (stored: a refusal is a stored answer too).
func TestFacts_EndedBindsToHostMKAndTeam(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "hostN", "mk1", rowActive) // another host's membership with the same mk
	f.remoteRow("def34", "lead:1", "mk2", rowActive)
	wrongTeam := endedFact(factUUID2, "mk2")
	wrongTeam.TeamID = uid(2)
	for name, fact := range map[string]team.TeamFact{
		"another host's mk": endedFact(factUUID1, "mk1"),
		"another team":      wrongTeam,
		"unknown mk":        endedFact(factUUID3, "nope"),
	} {
		code, body := f.postFact(leadPrincipal(), fact)
		if code != 409 || errCode(t, body) != "not_your_member" {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	for _, op := range []string{"abc12", "def34"} {
		if st, _ := f.memberRowState(op); st != rowActive {
			t.Fatalf("row %s = %s: a fact moved a row it is not bound to", op, st)
		}
	}
	if factLogCount(t, f) != 3 {
		t.Fatalf("%d logged, want 3 (refusals are stored)", factLogCount(t, f))
	}
}

// Membership generation (§11): an old `ended` for a previous mk does not touch a re-adopted row of the same session.
func TestFacts_AnOldMKDoesNotTouchTheReAdoptedRow(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk-old", "released")
	f.remoteRow("def34", "lead:1", "mk-new", rowActive)
	if code, body := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk-old")); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if st, _ := f.memberRowState("def34"); st != rowActive {
		t.Fatalf("re-adopted row = %s", st)
	}
}

// Rule 3: the same id and content answers the stored outcome (and applies nothing twice); the same id with another
// body is 409 id_conflict; a refusal replayed after its condition changed still answers the refusal.
func TestFacts_IdempotentByIdAndContent(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	_, first := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1"))
	// the row is put back to a live state behind the fact's back: a replay must not move it again
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'active' WHERE spawn_op = 'abc12'`); err != nil {
		t.Fatal(err)
	}
	code, again := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1"))
	if code != 200 || !bytes.Equal(first, again) {
		t.Fatalf("replay = %d %s, want the stored %s", code, again, first)
	}
	if st, _ := f.memberRowState("abc12"); st != rowActive {
		t.Fatalf("a replay re-applied the fact: row = %s", st)
	}
	other := endedFact(factUUID1, "mk1")
	other.Reason = team.FactReasonLocalEnd
	if code, body := f.postFact(leadPrincipal(), other); code != 409 || errCode(t, body) != "id_conflict" {
		t.Fatalf("other body = %d %s", code, body)
	}
	// a stored refusal survives the row appearing later
	_, r1 := f.postFact(leadPrincipal(), endedFact(factUUID2, "mk-late"))
	f.remoteRow("def34", "lead:1", "mk-late", rowActive)
	code, r2 := f.postFact(leadPrincipal(), endedFact(factUUID2, "mk-late"))
	if code != 409 || !bytes.Equal(r1, r2) {
		t.Fatalf("refusal replay = %d %s, want the stored %s", code, r2, r1)
	}
}

// §11 crash cut: applying a fact and logging its id are ONE transaction. Mutation gate: log after commit → the row has
// moved although the log failed → red.
func TestFacts_TheRowDoesNotMoveWithoutItsLogEntry(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	f.m.store.failBeforeFactLog = func() error { return errors.New("injected crash") }
	if code, _ := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1")); code != http.StatusInternalServerError {
		t.Fatalf("status = %d", code)
	}
	if st, _ := f.memberRowState("abc12"); st != rowActive {
		t.Fatalf("row = %s: the fact applied without its log entry", st)
	}
	f.m.store.failBeforeFactLog = nil
	if code, body := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1")); code != 200 {
		t.Fatalf("retry = %d %s", code, body)
	}
	if st, _ := f.memberRowState("abc12"); st != "gone" {
		t.Fatalf("retry: row = %s", st)
	}
}

// Rule 8: a flood of fresh fact ids is rate-limited before decode.
func TestFacts_AFloodOfFreshIdsIsRateLimited(t *testing.T) {
	f := factFixture(t)
	limited := false
	for i := 0; i < 500 && !limited; i++ {
		id := fmt.Sprintf("b%07x-1111-4111-8111-111111111111", i)
		code, _ := f.postFact(leadPrincipal(), endedFact(id, "nope"))
		limited = code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("500 fresh fact ids were never rate-limited")
	}
}

// codex re-review: the stored answer is consulted before the shape check, and the shape check before the addressing
// refusal (§6.1 order). A malformed fact is answered 400 and never stored — even when it is also addressed to another
// host; a stored answer survives a validation that later rejects the same copy.
// Mutation gate: validate before the stored lookup → the replay answers bad_request → red.
func TestFacts_AStoredAnswerPrecedesValidationAndShapePrecedesAddressing(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	// malformed AND addressed elsewhere: bad_request, not stored
	both := endedFact(factUUID1, "")
	both.ToHostID = "other:1"
	if code, body := f.postFact(leadPrincipal(), both); code != 400 || errCode(t, body) != "bad_request" || factLogCount(t, f) != 0 {
		t.Fatalf("malformed+wrong host = %d %s, logged %d", code, body, factLogCount(t, f))
	}
	// a stored decision whose copy would now fail validation keeps its answer
	stored := endedFact(factUUID2, "mk1")
	stored.ToHostID = "other:1"
	raw, _ := json.Marshal(stored)
	ref := refusal(http.StatusConflict, team.ErrCommandWrongHost, "stored")
	if _, err := f.m.store.ApplyTeamFact(FactPlan{FromHostID: "lead:1", Body: raw, Now: 1, Refusal: &ref}); err != nil {
		t.Fatal(err)
	}
	res, err := f.m.store.ApplyTeamFact(FactPlan{FromHostID: "lead:1", Body: raw, Now: 2, Invalid: "a rule of a later version"})
	if err != nil || !res.Replayed || res.Status != http.StatusConflict {
		t.Fatalf("replay = %+v %v, want the stored refusal", res, err)
	}
}
