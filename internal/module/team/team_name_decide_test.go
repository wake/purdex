package teammod

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

const nameClient = `"client":{"kind":"app","label":"Purdex.app @ air26"}`

// decideRaw posts a raw JSON decide body, so a key the test leaves out is
// really absent from the wire.
func (f *fixture) decideRaw(id, body string) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide", body)
}

// assertTeamNamed fails unless sid leads a live team whose current name and
// recorded grant name are both want.
func (f *fixture) assertTeamNamed(sid, want string) {
	f.t.Helper()
	tm, ok, err := f.m.store.LiveTeamByLead(sid)
	if err != nil || !ok {
		f.t.Fatalf("team of %s: ok=%v err=%v", sid, ok, err)
	}
	if tm.TeamName != want {
		f.t.Fatalf("teams.team_name = %q, want %q", tm.TeamName, want)
	}
	if tm.Grant.TeamName == nil || *tm.Grant.TeamName != want {
		f.t.Fatalf("grant_json team_name = %v, want %q", tm.Grant.TeamName, want)
	}
}

// D-N3, one case per shape of the decide body. Mutation gate: a nil
// Grant.TeamName treated as "" → the "older App" cases turn red.
func TestDecide_TeamName(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{"no grant keeps the requested name", `{"decision":"approve",` + nameClient + `}`, "requested"},
		{"an older App's grant (no team_name key) keeps the requested name",
			`{"decision":"approve","grant":{"max_members":2,"roots":["/w"]},` + nameClient + `}`, "requested"},
		{"team_name alone renames", `{"decision":"approve","grant":{"team_name":"renamed"},` + nameClient + `}`, "renamed"},
		{"the empty string clears", `{"decision":"approve","grant":{"team_name":""},` + nameClient + `}`, ""},
		{"the new name is trimmed", `{"decision":"approve","grant":{"team_name":"  spaced  "},` + nameClient + `}`, "spaced"},
		{"white space only clears", `{"decision":"approve","grant":{"team_name":"   "},` + nameClient + `}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "requested" }
			f.create(uid(1))
			code, body := f.decideRaw(uid(1), c.body)
			a := decodeApproval(t, body)
			if code != 200 || a.State != team.StateApproved || a.Grant == nil || a.Grant.TeamName == nil || *a.Grant.TeamName != c.want {
				t.Fatalf("approve: %d %s, want grant.team_name %q", code, body, c.want)
			}
			f.assertTeamNamed("sid-1", c.want)
			if strings.Contains(c.body, "max_members") && a.Grant.MaxMembers != 2 {
				t.Fatalf("max members = %d, want the edit's 2", a.Grant.MaxMembers)
			}
		})
	}
}

// A name that breaks the rule is 400, the approval stays pending and no
// team exists. Mutation gate: skip NormaliseTeamName in decide → red.
func TestDecide_InvalidTeamNameIs400AndStaysPending(t *testing.T) {
	for name, bad := range map[string]string{
		"control char": `a\u0007`,
		"too long":     strings.Repeat("a", 65),
		"newline":      `a\nb`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "requested" }
			f.create(uid(1))
			f.events()
			code, body := f.decideRaw(uid(1), `{"decision":"approve","grant":{"team_name":"`+bad+`"},`+nameClient+`}`)
			e := decodeErr(t, body)
			if code != http.StatusBadRequest || e.Error != team.ErrBadRequest || !strings.Contains(e.Detail, "team_name") {
				t.Fatalf("%d %s, want 400 bad_request naming team_name", code, body)
			}
			if a, ok, err := f.m.store.Get(uid(1)); err != nil || !ok || a.State != team.StateOpen {
				t.Fatalf("approval after the refusal = %+v ok=%v err=%v, want open", a, ok, err)
			}
			if _, ok, err := f.m.store.LiveTeamByLead("sid-1"); err != nil || ok {
				t.Fatalf("a team exists after the refusal (ok=%v err=%v)", ok, err)
			}
			if n := len(f.events()); n != 0 {
				t.Fatalf("%d events after a refused decide", n)
			}
			// The approval can still be approved afterwards.
			if code, body := f.decideRaw(uid(1), `{"decision":"approve",`+nameClient+`}`); code != 200 {
				t.Fatalf("approve after the refusal: %d %s", code, body)
			}
			f.assertTeamNamed("sid-1", "requested")
		})
	}
}

// A request without a name approves into a team without one, and the grant
// still says so: a decided approval of this version always carries the key.
func TestDecide_UnnamedRequestStaysUnnamed(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	code, body := f.decideRaw(uid(1), `{"decision":"approve",`+nameClient+`}`)
	if code != 200 || !strings.Contains(string(body), `"grant":{"max_members":3,"roots":["/w"],"roots_canonical":true,"team_name":"","team_label":""}`) {
		t.Fatalf("approve: %d %s, want grant.team_name \"\" present", code, body)
	}
	f.assertTeamNamed("sid-1", "")
}

// leadTeamOf and teamNote read Grant.TeamName, a pointer: a grant that never
// went through leadGrantOf (nil) is an unnamed team, not a panic.
func TestLeadTeamOf_NilGrantNameIsUnnamed(t *testing.T) {
	a := team.Approval{ID: "id-1", HostID: "h:1", Origin: team.Origin{SessionID: "sid-1", Ref: "_abc123"}, State: team.StateApproved}
	if tm := leadTeamOf(a, team.Grant{MaxMembers: 3}, 5); tm.TeamName != "" {
		t.Fatalf("team name = %q, want none", tm.TeamName)
	}
	named := "x"
	if tm := leadTeamOf(a, team.Grant{TeamName: &named}, 5); tm.TeamName != "x" {
		t.Fatalf("team name = %q, want x", tm.TeamName)
	}
	a.Kind, a.Grant = team.KindLead, &team.Grant{MaxMembers: 3}
	if note := teamNote(a); strings.Contains(note, `"`) {
		t.Fatalf("note of an unnamed grant = %q, want no name", note)
	}
}

// D-N4 / D-N11: unattended approval keeps the requested name, and the
// decision log line names the team.
func TestUnattended_KeepsTheRequestedNameAndLogsIt(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var lines []string
	f.m.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	f.unatt.set(true)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "夜班 team" }
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	f.assertTeamNamed("sid-1", "夜班 team")
	mu.Lock()
	defer mu.Unlock()
	var found bool
	for _, l := range lines {
		if strings.Contains(l, "approved by unattended") && strings.Contains(l, `"夜班 team"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no decision log line names the team; lines = %q", lines)
	}
}
