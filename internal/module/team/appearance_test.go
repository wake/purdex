package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// TR-1: PUT /api/team/appearance — a live team's name, label and colour.

// appearanceBody is a full body; a test removes or changes keys of it.
func appearanceBody(over map[string]any) map[string]any {
	b := map[string]any{"team_id": uid(1), "team_name": "資源線：租約", "team_label": "資源線", "team_color": 3,
		"client": map[string]any{"kind": "app", "label": "Purdex.app @ air26"}}
	for k, v := range over {
		if v == missing {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

const missing = "\x00missing"

func (f *fixture) putAppearance(body map[string]any) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPut, team.AppearanceRoute, body)
}

func (f *fixture) teamRow() team.Team {
	f.t.Helper()
	tm, ok := getTeam(f.t, f.m.store, uid(1))
	if !ok {
		f.t.Fatal("no team row")
	}
	return tm
}

func (f *fixture) colorOf() *int {
	f.t.Helper()
	c, err := f.m.store.TeamColors([]string{uid(1)})
	if err != nil {
		f.t.Fatal(err)
	}
	if v, ok := c[uid(1)]; ok {
		return &v
	}
	return nil
}

func TestAppearancePut_ChangesNameLabelAndColour(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	code, body := f.putAppearance(appearanceBody(nil))
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	var v team.AppearanceView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.TeamID != uid(1) || v.TeamName != "資源線：租約" || v.TeamLabel != "資源線" || v.TeamColor == nil || *v.TeamColor != 3 {
		t.Fatalf("answer = %+v", v)
	}
	if tm := f.teamRow(); tm.TeamName != "資源線：租約" || tm.TeamLabel != "資源線" {
		t.Fatalf("row = %+v", tm)
	}
	if c := f.colorOf(); c == nil || *c != 3 {
		t.Fatalf("colour = %v", c)
	}
}

// A label left empty is derived from the name by the creation rule; it is not cleared.
// Mutation gate: store "" as it came → red.
func TestAppearancePut_EmptyLabelIsDerivedFromTheName(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	code, body := f.putAppearance(appearanceBody(map[string]any{"team_name": "介面線：團隊面板", "team_label": ""}))
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	if tm := f.teamRow(); tm.TeamLabel != team.DeriveTeamLabel("介面線：團隊面板") || tm.TeamLabel == "" {
		t.Fatalf("label = %q, want the derived one", tm.TeamLabel)
	}
	// an empty name with an empty label: no name, no label
	if code, body = f.putAppearance(appearanceBody(map[string]any{"team_name": "  ", "team_label": ""})); code != 200 {
		t.Fatalf("empty: %d %s", code, body)
	}
	if tm := f.teamRow(); tm.TeamName != "" || tm.TeamLabel != "" {
		t.Fatalf("row = %+v", tm)
	}
}

func TestAppearancePut_ColourNullIsAutomaticAndRangeIsChecked(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	if code, body := f.putAppearance(appearanceBody(map[string]any{"team_color": 0})); code != 200 || f.colorOf() == nil || *f.colorOf() != 0 {
		t.Fatalf("colour 0: %d %s %v", code, body, f.colorOf())
	}
	if code, body := f.putAppearance(appearanceBody(map[string]any{"team_color": 7})); code != 200 || *f.colorOf() != 7 {
		t.Fatalf("colour 7: %d %s", code, body)
	}
	code, body := f.putAppearance(appearanceBody(map[string]any{"team_color": nil}))
	if code != 200 || f.colorOf() != nil {
		t.Fatalf("null: %d %s colour=%v", code, body, f.colorOf())
	}
	var v team.AppearanceView
	_ = json.Unmarshal(body, &v)
	if v.TeamColor != nil || !strings.Contains(string(body), `"team_color":null`) {
		t.Fatalf("the answer must say team_color null: %s", body)
	}
	// out of range, not an integer, or not a number: 400 naming the field, nothing stored
	f.putAppearance(appearanceBody(map[string]any{"team_color": 5}))
	for name, c := range map[string]any{"8": 8, "-1": -1, "1.5": 1.5, "string": "3", "bool": true, "array": []int{1}} {
		code, body := f.putAppearance(appearanceBody(map[string]any{"team_color": c}))
		if code != 400 || decodeErr(t, body).Error != team.ErrBadRequest || !strings.Contains(decodeErr(t, body).Detail, "team_color") {
			t.Errorf("%s: %d %s, want 400 naming team_color", name, code, body)
		}
	}
	if c := f.colorOf(); c == nil || *c != 5 {
		t.Fatalf("a refused colour changed the stored one: %v", c)
	}
}

// Every field is required: a body that leaves one out must not clear it.
// Mutation gate: treat a missing field as "" / automatic → red.
func TestAppearancePut_EveryFieldIsRequired(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	f.putAppearance(appearanceBody(nil))
	for _, k := range []string{"team_id", "team_name", "team_label", "team_color"} {
		code, body := f.putAppearance(appearanceBody(map[string]any{k: missing}))
		if code != 400 || !strings.Contains(decodeErr(t, body).Detail, k) {
			t.Errorf("without %s: %d %s, want 400 naming it", k, code, body)
		}
	}
	if tm := f.teamRow(); tm.TeamName != "資源線：租約" || *f.colorOf() != 3 {
		t.Fatalf("a refused request changed the team: %+v", tm)
	}
	// null for a string field is not a value either
	for _, k := range []string{"team_name", "team_label"} {
		if code, body := f.putAppearance(appearanceBody(map[string]any{k: nil})); code != 400 {
			t.Errorf("%s null: %d %s", k, code, body)
		}
	}
}

func TestAppearancePut_NormalisationErrorsNameTheField(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	f.putAppearance(appearanceBody(nil))
	long := strings.Repeat("字", 40) // 120 bytes: over the 64-byte title rule
	for field, val := range map[string]string{"team_name": long, "team_label": strings.Repeat("字", 30)} {
		code, body := f.putAppearance(appearanceBody(map[string]any{field: val}))
		if code != 400 || decodeErr(t, body).Error != team.ErrBadRequest || !strings.Contains(decodeErr(t, body).Detail, field) {
			t.Errorf("%s: %d %s, want 400 naming it", field, code, body)
		}
	}
	if code, body := f.putAppearance(appearanceBody(map[string]any{"team_name": "壞\x07名"})); code != 400 {
		t.Errorf("control char: %d %s", code, body)
	}
	if tm := f.teamRow(); tm.TeamName != "資源線：租約" || tm.TeamLabel != "資源線" {
		t.Fatalf("a refused request changed the team: %+v", tm)
	}
}

func TestAppearancePut_UnknownIs404AndEndedIs409(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	if code, body := f.putAppearance(appearanceBody(map[string]any{"team_id": "no-such-team"})); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Errorf("unknown: %d %s", code, body)
	}
	if ok, err := f.m.store.EndTeam(uid(1), "sid-1", "lead_gone", 5); err != nil || !ok {
		t.Fatal(ok, err)
	}
	code, body := f.putAppearance(appearanceBody(nil))
	if code != 409 || decodeErr(t, body).Error != team.ErrNotLive {
		t.Errorf("ended: %d %s, want 409 not_live", code, body)
	}
	if c := f.colorOf(); c != nil {
		t.Fatalf("an ended team was recoloured: %v", *c)
	}
}

// The client is recorded, not checked: any kind (or none) passes, and the log line carries it.
func TestAppearancePut_ClientIsRecordedNotChecked(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	logs := f.logs()
	for _, c := range []any{map[string]any{"kind": "terminal", "label": "pdx"}, map[string]any{}, missing} {
		if code, body := f.putAppearance(appearanceBody(map[string]any{"client": c})); code != 200 {
			t.Errorf("client %v: %d %s", c, code, body)
		}
	}
	f.putAppearance(appearanceBody(map[string]any{"team_name": "新名字", "team_label": "新", "client": map[string]any{"kind": "app", "label": "Purdex.app @ air26"}}))
	joined := strings.Join(logs(), "\n")
	if !strings.Contains(joined, uid(1)) || !strings.Contains(joined, "新名字") || !strings.Contains(joined, `"app"`) {
		t.Fatalf("no audit line naming team, new name and client kind: %v", logs())
	}
}

// The new values arrive in the next roster frame, and the colour is omitted while automatic.
// Mutation gate: do not signal the roster, or leave team_color off the roster → red.
func TestAppearancePut_AnnouncesTheRosterWithTheNewValues(t *testing.T) {
	f, _ := newSpawnFixture(t, 3)
	f.rosterBaselineNow()
	w := f.watchRoster()
	w.drain()
	if code, body := f.putAppearance(appearanceBody(map[string]any{"team_color": 6})); code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	ev := w.one("appearance")
	if len(ev.Teams) != 1 || ev.Teams[0].TeamName != "資源線：租約" || ev.Teams[0].TeamLabel != "資源線" || ev.Teams[0].TeamColor == nil || *ev.Teams[0].TeamColor != 6 {
		t.Fatalf("roster event = %+v", ev.Teams)
	}
	if r := f.getRoster(); len(r.Teams) != 1 || r.Teams[0].TeamColor == nil || *r.Teams[0].TeamColor != 6 {
		t.Fatalf("GET roster = %+v", r.Teams)
	}
	// automatic: the key is absent from the wire
	w.drain()
	f.putAppearance(appearanceBody(map[string]any{"team_color": nil}))
	w.one("automatic")
	code, raw := f.do(http.MethodGet, "/api/team/roster", nil)
	if code != 200 || strings.Contains(string(raw), "team_color") {
		t.Fatalf("an automatic colour must be omitted: %d %s", code, raw)
	}
}

// A team.db written before the column existed opens, and its teams read as automatic.
func TestAppearance_MigrationAddsTheColumnToAnExistingDB(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`ALTER TABLE teams DROP COLUMN team_color`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := migrateTeamColor(s.db); err != nil {
		t.Fatal(err)
	}
	if err := migrateTeamColor(s.db); err != nil { // twice: idempotent
		t.Fatal(err)
	}
	seedTeam(t, s, "team-m", "lead-m", 1)
	if c, err := s.TeamColors([]string{"team-m"}); err != nil || len(c) != 0 {
		t.Fatalf("a new team's colour = %v err=%v, want automatic", c, err)
	}
}
