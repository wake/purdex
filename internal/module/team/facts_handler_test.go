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
	factUUID4 = "a4444444-4444-4444-8444-444444444444"
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
	noMK := endedFact(factUUID4, "")
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
		"unknown kind":   {team.TeamFact{ID: factUUID3, Kind: "made_up", ToHostID: "h:1", TeamID: uid(1), MK: "mk1"}, 400, "unsupported_kind"},
	} {
		code, body := f.postFact(leadPrincipal(), tc.body)
		if code != tc.status || errCode(t, body) != tc.code {
			t.Fatalf("%s: %d %s, want %d %s", name, code, body, tc.status, tc.code)
		}
	}
	// wrong_host is a stored decision; unsupported_kind and bad_request are not.
	if n := factLogCount(t, f); n != 1 {
		t.Fatalf("%d logged, want 1 (the wrong_host)", n)
	}
}

// unsupported_kind is NOT stored (X4b-1, f4-ra): the member host drops a fact on a permanent refusal, so a stored refusal
// would lose a true fact that a later version applies. The id is not burned: the same id resent after the upgrade (here: as
// a kind this version applies) completes. Mutation gate: store the refusal → the resend answers id_conflict → red.
func TestFacts_AnUnsupportedKindIsNotStoredSoAResendAfterTheUpgradeCompletes(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	moved := team.TeamFact{ID: factUUID1, Kind: "moved", ToHostID: "h:1", TeamID: uid(1), MK: "mk1"}
	if code, body := f.postFact(leadPrincipal(), moved); code != 400 || errCode(t, body) != "unsupported_kind" || factLogCount(t, f) != 0 {
		t.Fatalf("first = %d %s, logged %d", code, body, factLogCount(t, f))
	}
	if code, body := f.postFact(leadPrincipal(), endedFact(factUUID1, "mk1")); code != 200 {
		t.Fatalf("resend after the upgrade = %d %s", code, body)
	}
	if st, _ := f.memberRowState("abc12"); st != "gone" {
		t.Fatalf("row = %s: the resent fact was not applied", st)
	}
}

// Rule 3: wrong_host is stored with the fact's hash: a replay answers it, another body under the id is id_conflict.
func TestFacts_AWrongHostRefusalIsStored(t *testing.T) {
	f := factFixture(t)
	wrong := endedFact(factUUID2, "mk1")
	wrong.ToHostID = "other:1"
	_, w1 := f.postFact(leadPrincipal(), wrong)
	code, w2 := f.postFact(leadPrincipal(), wrong)
	if code != 409 || !bytes.Equal(w1, w2) || factLogCount(t, f) != 1 {
		t.Fatalf("replay = %d %s vs %s, logged %d", code, w2, w1, factLogCount(t, f))
	}
	other := wrong
	other.MK = "mk2"
	if code, body := f.postFact(leadPrincipal(), other); code != 409 || errCode(t, body) != "id_conflict" {
		t.Fatalf("other body = %d %s", code, body)
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

// codex re-review: the stored answer is consulted first, then the addressing / kind refusal, then the field validation —
// the commands route's order (§6.1). A fact addressed to another host is wrong_host (stored) even when it is also
// malformed; a malformed fact addressed to us is answered 400 and not stored; a stored answer survives a validation that
// later rejects the same copy. Mutation gate: validation before the addressing refusal → red.
func TestFacts_StoredAnswerThenAddressingThenValidation(t *testing.T) {
	f := factFixture(t)
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	both := endedFact(factUUID1, "")
	both.ToHostID = "other:1"
	if code, body := f.postFact(leadPrincipal(), both); code != 409 || errCode(t, body) != "wrong_host" || factLogCount(t, f) != 1 {
		t.Fatalf("malformed+wrong host = %d %s, logged %d", code, body, factLogCount(t, f))
	}
	if code, body := f.postFact(leadPrincipal(), endedFact(factUUID3, "")); code != 400 || errCode(t, body) != "bad_request" || factLogCount(t, f) != 1 {
		t.Fatalf("malformed = %d %s, logged %d", code, body, factLogCount(t, f))
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
