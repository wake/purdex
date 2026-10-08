package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func payloadOf(t *testing.T, a team.Approval) team.LeadPayload {
	t.Helper()
	var p team.LeadPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return p
}

// N1 / D-N2: the requested name is normalised into the payload; the payload
// always carries the key, "" when there is no name.
func TestCreate_TeamNameLandsInThePayload(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "  驗收 team  " }
	a := f.create(uid(1))
	if p := payloadOf(t, a); p.TeamName != "驗收 team" {
		t.Fatalf("payload name = %q, want it trimmed", p.TeamName)
	}

	g := newFixture(t)
	b := g.create(uid(1)) // no name
	if !strings.Contains(string(b.Payload), `"team_name":""`) {
		t.Fatalf("payload of an unnamed request = %s, want team_name \"\" present", b.Payload)
	}
	// A whitespace-only name is no name.
	h := newFixture(t)
	h.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "   " }
	if p := payloadOf(t, h.create(uid(1))); p.TeamName != "" {
		t.Fatalf("blank name = %q, want none", p.TeamName)
	}
}

func TestCreate_InvalidTeamNameIs400AndStoresNothing(t *testing.T) {
	for name, bad := range map[string]string{
		"too long": strings.Repeat("a", 65),
		"control":  "a\x07b",
		"newline":  "a\nb",
	} {
		f := newFixture(t)
		f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = bad }
		code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
		e := decodeErr(t, body)
		if code != http.StatusBadRequest || e.Error != team.ErrBadRequest || !strings.Contains(e.Detail, "team_name") {
			t.Errorf("%s: %d %s, want 400 bad_request naming team_name", name, code, body)
		}
		if _, ok, err := f.m.store.Get(uid(1)); err != nil || ok {
			t.Errorf("%s: a row was stored (ok=%v err=%v)", name, ok, err)
		}
		if n := len(f.events()); n != 0 {
			t.Errorf("%s: %d events", name, n)
		}
	}
}

// D-N8: the name is part of the request, so the same id with another name is
// id_conflict, with the same name is the existing row.
func TestCreate_TeamNameIsPartOfIdempotency(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "build" }
	f.create(uid(1))
	f.events()

	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if code != http.StatusOK || decodeApproval(t, body).ID != uid(1) || len(f.events()) != 0 {
		t.Fatalf("same name again: %d %s", code, body)
	}
	// The same name in other white space is the same request.
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = " build " }
	if code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1))); code != http.StatusOK {
		t.Fatalf("same name, padded: %d %s", code, body)
	}
	for _, other := range []string{"other", ""} {
		f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = other }
		code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
		if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
			t.Fatalf("name %q on a named id: %d %s, want id_conflict", other, code, body)
		}
	}
}

// Hash compatibility: a request opened before names were deployed has its
// hash stored over the old payload bytes (no team_name key). Retried after
// the deploy with no name, it must be answered, not id_conflict.
func TestCreate_UnnamedRetryMatchesAHashStoredBeforeNames(t *testing.T) {
	f := newFixture(t)
	type oldLeadPayload struct { // LeadPayload as it was before names
		Reason     string   `json:"reason"`
		MaxMembers int      `json:"max_members"`
		Roots      []string `json:"roots"`
	}
	oldPayload, err := json.Marshal(oldLeadPayload{Reason: "split the work", MaxMembers: 3, Roots: []string{"/w"}})
	if err != nil {
		t.Fatal(err)
	}
	row := openApproval(uid(1), "sid-1", 1_000_000)
	row.Payload = oldPayload
	oldHash := requestHash(team.KindLead, "sid-1", team.DefaultWaitS, oldPayload)
	if _, _, _, err := f.m.store.Create(row, oldHash); err != nil {
		t.Fatal(err)
	}

	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if code != http.StatusOK || decodeApproval(t, body).ID != uid(1) {
		t.Fatalf("unnamed retry of a pre-names row: %d %s, want the existing row", code, body)
	}
	// A named request on that id is a different request.
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "late" }
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
		t.Fatalf("named request on a pre-names id: %d %s, want id_conflict", code, body)
	}
}
