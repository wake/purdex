package teammod

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/team"
)

// assertTeamLabelled fails unless sid leads a live team whose final label is
// want and whose grant records explicit (the label as approved, "" when the
// team's label was derived).
func (f *fixture) assertTeamLabelled(sid, want, explicit string) {
	f.t.Helper()
	tm, ok, err := f.m.store.LiveTeamByLead(sid)
	if err != nil || !ok {
		f.t.Fatalf("team of %s: ok=%v err=%v", sid, ok, err)
	}
	if tm.TeamLabel != want {
		f.t.Fatalf("teams.team_label = %q, want %q", tm.TeamLabel, want)
	}
	if tm.Grant.TeamLabel == nil || *tm.Grant.TeamLabel != explicit {
		f.t.Fatalf("grant_json team_label = %v, want %q", tm.Grant.TeamLabel, explicit)
	}
}

// D-L4's table: the label as requested × the grant as the App sent it. The
// name 「A 線：資源租約」 derives to 「A 線」. Mutation gate: a nil grant label
// treated as "" turns the "absent" column red (an older App would wipe a lead's
// request).
func TestDecide_TeamLabelTruthTable(t *testing.T) {
	const name = "A 線：資源租約"
	for _, c := range []struct {
		name      string
		requested string
		body      string
		final     string // teams.team_label
		explicit  string // grant_json.team_label
	}{
		{"none requested, grant absent -> derived", "", `{"decision":"approve",` + nameClient + `}`, "A 線", ""},
		{"none requested, an older App's grant (no label key) -> derived", "", `{"decision":"approve","grant":{"max_members":2},` + nameClient + `}`, "A 線", ""},
		{"none requested, X -> X", "", `{"decision":"approve","grant":{"team_label":"X"},` + nameClient + `}`, "X", "X"},
		{"none requested, empty -> derived", "", `{"decision":"approve","grant":{"team_label":""},` + nameClient + `}`, "A 線", ""},
		{"requested R, grant absent -> R", "R", `{"decision":"approve",` + nameClient + `}`, "R", "R"},
		{"requested R, an older App's grant -> R (a lead's request is not wiped)", "R", `{"decision":"approve","grant":{"max_members":2,"roots":["/w"]},` + nameClient + `}`, "R", "R"},
		{"requested R, X -> X", "R", `{"decision":"approve","grant":{"team_label":"X"},` + nameClient + `}`, "X", "X"},
		{"requested R, empty -> derived", "R", `{"decision":"approve","grant":{"team_label":""},` + nameClient + `}`, "A 線", ""},
		{"requested R, white space only -> derived", "R", `{"decision":"approve","grant":{"team_label":"   "},` + nameClient + `}`, "A 線", ""},
		{"X is trimmed", "", `{"decision":"approve","grant":{"team_label":"  X  "},` + nameClient + `}`, "X", "X"},
		{"derived from the APPROVED name when the approver renames", "", `{"decision":"approve","grant":{"team_name":"介面線：右邊"},` + nameClient + `}`, "介面線", ""},
		{"renamed to a name that derives nothing -> no label", "", `{"decision":"approve","grant":{"team_name":"資源租約與派工回報"},` + nameClient + `}`, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName, r.TeamLabel = name, c.requested }
			f.create(uid(1))
			code, body := f.decideRaw(uid(1), c.body)
			a := decodeApproval(t, body)
			if code != 200 || a.State != team.StateApproved || a.Grant == nil || a.Grant.TeamLabel == nil || *a.Grant.TeamLabel != c.explicit {
				t.Fatalf("approve: %d %s, want grant.team_label %q", code, body, c.explicit)
			}
			f.assertTeamLabelled("sid-1", c.final, c.explicit)
		})
	}
}

// A label that breaks the rule is 400 naming team_label, the approval stays
// pending and no team exists.
func TestDecide_InvalidTeamLabelIs400AndStaysPending(t *testing.T) {
	for name, bad := range map[string]string{
		"11 wide":           "01234567890",
		"six Chinese":       "資源租約派工",
		"control char":      `a\u0007`,
		"newline":           `a\nb`,
		"invisible (VS-16)": `️`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamLabel = "R" }
			f.create(uid(1))
			f.events()
			code, body := f.decideRaw(uid(1), `{"decision":"approve","grant":{"team_label":"`+bad+`"},`+nameClient+`}`)
			e := decodeErr(t, body)
			if code != http.StatusBadRequest || e.Error != team.ErrBadRequest || !strings.Contains(e.Detail, "team_label") {
				t.Fatalf("%d %s, want 400 bad_request naming team_label", code, body)
			}
			if a, ok, err := f.m.store.Get(uid(1)); err != nil || !ok || a.State != team.StateOpen {
				t.Fatalf("approval after the refusal = %+v ok=%v err=%v, want open", a, ok, err)
			}
			if _, ok, _ := f.m.store.LiveTeamByLead("sid-1"); ok {
				t.Fatal("a team exists after the refusal")
			}
			if n := len(f.events()); n != 0 {
				t.Fatalf("%d events after a refused decide", n)
			}
		})
	}
}

// The payload always carries the key; the label is normalised.
func TestCreate_TeamLabelLandsInThePayload(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamLabel = "  A 線  " }
	if p := payloadOf(t, f.create(uid(1))); p.TeamLabel != "A 線" {
		t.Fatalf("payload label = %q, want it trimmed", p.TeamLabel)
	}
	g := newFixture(t)
	if b := g.create(uid(1)); !strings.Contains(string(b.Payload), `"team_label":""`) {
		t.Fatalf("payload of an unlabelled request = %s, want team_label \"\" present", b.Payload)
	}
}

func TestCreate_InvalidTeamLabelIs400AndStoresNothing(t *testing.T) {
	for name, bad := range map[string]string{"11 wide": "01234567890", "six Chinese": "資源租約派工", "control": "a\x07b", "invisible": "️"} {
		f := newFixture(t)
		f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamLabel = bad }
		code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
		e := decodeErr(t, body)
		if code != http.StatusBadRequest || e.Error != team.ErrBadRequest || !strings.Contains(e.Detail, "team_label") {
			t.Errorf("%s: %d %s, want 400 naming team_label", name, code, body)
		}
		if _, ok, _ := f.m.store.Get(uid(1)); ok || len(f.events()) != 0 {
			t.Errorf("%s: a row or an event was written", name)
		}
	}
}

// D-L6: the label is part of the request.
func TestCreate_TeamLabelIsPartOfIdempotency(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamLabel = "A 線" }
	f.create(uid(1))
	if code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1))); code != http.StatusOK {
		t.Fatalf("same label again: %d %s", code, body)
	}
	for _, other := range []string{"B 線", ""} {
		f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamLabel = other }
		code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
		if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
			t.Fatalf("label %q on a labelled id: %d %s, want id_conflict", other, code, body)
		}
	}
}

// Hash compatibility (D-L6): a request opened by a version with names but no
// labels has its hash over a payload without team_label; the same request,
// retried after the upgrade with no label, is answered. Mutation gate: the hash
// including an empty label turns this red.
func TestCreate_UnlabelledRetryMatchesAHashStoredBeforeLabels(t *testing.T) {
	f := newFixture(t)
	type namedLeadPayload struct { // LeadPayload as it was with names and no labels
		Reason     string   `json:"reason"`
		MaxMembers int      `json:"max_members"`
		Roots      []string `json:"roots"`
		TeamName   string   `json:"team_name,omitempty"`
	}
	old, err := json.Marshal(namedLeadPayload{Reason: "split the work", MaxMembers: 3, Roots: []string{"/w"}, TeamName: "build"})
	if err != nil {
		t.Fatal(err)
	}
	row := openApproval(uid(1), "sid-1", 1_000_000)
	row.Payload = old
	if _, _, _, err := f.m.store.Create(row, requestHash(team.KindLead, "sid-1", team.DefaultWaitS, old)); err != nil {
		t.Fatal(err)
	}
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "build" }
	if code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1))); code != http.StatusOK || decodeApproval(t, body).ID != uid(1) {
		t.Fatalf("unlabelled retry of a pre-labels row: %d %s, want the existing row", code, body)
	}
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName, r.TeamLabel = "build", "late" }
	if code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1))); code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
		t.Fatalf("labelled request on a pre-labels id: %d %s, want id_conflict", code, body)
	}
}

// D-L4: unattended approval uses the requested label, else the derived one.
func TestUnattended_UsesTheRequestedOrTheDerivedLabel(t *testing.T) {
	for _, c := range []struct{ name, label, want, explicit string }{
		{"A 線：資源租約", "資源線", "資源線", "資源線"},
		{"A 線：資源租約", "", "A 線", ""},
		{"資源租約與派工回報", "", "", ""},
	} {
		f := newFixture(t)
		f.unatt.set(true)
		f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName, r.TeamLabel = c.name, c.label }
		if code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1))); code != http.StatusCreated {
			t.Fatalf("create: %d %s", code, body)
		}
		f.assertTeamLabelled("sid-1", c.want, c.explicit)
	}
}

// leadTeamOf reads Grant.TeamLabel, a pointer: a grant that never went through
// leadGrantOf (nil) derives from the name, and never panics.
func TestLeadTeamOf_LabelFromGrantOrName(t *testing.T) {
	a := team.Approval{ID: "id-1", HostID: "h:1", Origin: team.Origin{SessionID: "sid-1", Ref: "_abc123"}, State: team.StateApproved}
	name, explicit := "A 線：x", "X"
	if tm := leadTeamOf(a, team.Grant{TeamName: &name}, 5); tm.TeamLabel != "A 線" {
		t.Errorf("nil grant label -> %q, want the derived 「A 線」", tm.TeamLabel)
	}
	if tm := leadTeamOf(a, team.Grant{TeamName: &name, TeamLabel: &explicit}, 5); tm.TeamLabel != "X" {
		t.Errorf("explicit -> %q", tm.TeamLabel)
	}
	if tm := leadTeamOf(a, team.Grant{MaxMembers: 3}, 5); tm.TeamLabel != "" {
		t.Errorf("no name, no label -> %q", tm.TeamLabel)
	}
}

// The roster carries each live team's final label, "" when there is none.
func TestRoster_ListsTheTeamLabel(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName, r.TeamLabel = "資源租約：派工", "資源線" }
	f.approveLead(uid(1))
	seedTeam(t, f.m.store, uid(2), "sid-2", 5000)
	byID := map[string]team.TeamRoster{}
	for _, tr := range f.getRoster().Teams {
		byID[tr.ID] = tr
	}
	if got := byID[uid(1)].TeamLabel; got != "資源線" {
		t.Errorf("labelled team in the roster = %q", got)
	}
	if got := byID[uid(2)].TeamLabel; got != "" {
		t.Errorf("unlabelled team in the roster = %q", got)
	}
}

// GET /api/team serves the label, and the key is always there.
func TestTeamGet_ShowsTheTeamLabel(t *testing.T) {
	f := newFixture(t)
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.TeamName = "資源線：派工" }
	f.create(uid(1))
	if code, body := f.decideRaw(uid(1), `{"decision":"approve",`+nameClient+`}`); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	code, v, e := f.teamView("/tmp/10.sock")
	if code != http.StatusOK || v.Team.TeamLabel != "資源線" {
		t.Fatalf("GET /api/team = %d %+v %+v, want the derived label 資源線", code, v.Team, e)
	}
	g := newFixture(t)
	g.approveLead(uid(1))
	if _, raw := g.do(http.MethodGet, "/api/team?origin_inbox=%2Ftmp%2F10.sock", ""); !strings.Contains(string(raw), `"team_label":""`) {
		t.Fatalf("unlabelled team body = %s, want team_label \"\" present", raw)
	}
}

// team.db as alpha.615 left it on mlab (schema dumped read-only): opening it
// adds the column, an old row reads "" and its grant (no team_label key)
// decodes with a nil label; opening again is a no-op.
func TestOpenStore_AddsTeamLabelToTheLiveSchema(t *testing.T) {
	schema, err := os.ReadFile("testdata/team-alpha615.schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "team.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if columnsOf(t, old, "teams")["team_label"] {
		t.Fatal("setup: the live schema already has team_label")
	}
	if _, err := old.Exec(`INSERT INTO teams (id, host_id, lead_session_id, lead_ref, grant_json, request_id, created_at, team_name)
		VALUES ('t1', 'h:1', 'sid-1', '_abc123', '{"max_members":3,"roots":["/w"],"team_name":"舊 team"}', 't1', 5, '舊 team')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	for i := 0; i < 2; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		if !columnsOf(t, s.db, "teams")["team_label"] {
			t.Fatalf("open %d: no team_label column", i+1)
		}
		got, ok := getTeam(t, s, "t1")
		if !ok || got.TeamLabel != "" || got.TeamName != "舊 team" || got.Grant.TeamLabel != nil {
			t.Fatalf("open %d: old row = %+v ok=%v, want name kept, no label, a nil grant label", i+1, got, ok)
		}
		s.Close()
	}
}
