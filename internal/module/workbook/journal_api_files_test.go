package workbook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

type jTask struct {
	Number string   `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Owner  string   `json:"owner"`
	Refs   []string `json:"refs"`
	Detail string   `json:"detail"`
}

// The tasks route is the unit's tasks file as the App's strict parser reads it: a display number (a team task keeps its
// `<team>-<seq>`, a solo todo loses the `#`), the state in the App's three words (doing and not started are both open), the
// owner and refs; a unit with no file has no tasks (an empty list, not null).
// Mutation gate: the states pass through unmapped -> the App drops every doing row (red); the solo `#` kept (red); a missing
// file is null (red).
func TestJournalAPI_TasksAreTheUnitsTasksFile(t *testing.T) {
	j := newJournalAPI(t)
	team1 := j.unit("team:t1", "日誌")
	if err := journal.WriteTasks(unitHome(team1).tasksFile, []journal.Task{
		{Key: "t1-12", Title: "寫文案", State: journal.TaskDoing, Owner: "Adam", Refs: []string{"#2829"}},
		{Key: "t1-13", Title: "待辦", State: journal.TaskTodo},
		{Key: "t1-9", Title: "完成了", State: journal.TaskDone, Owner: "Kenji", Refs: []string{"PR #2830"}},
		{Key: "t1-10", Title: "放棄", State: journal.TaskDropped},
	}); err != nil {
		t.Fatal(err)
	}
	j.seats.seats["m1"] = teamSeat("t1")
	code, body := j.request(http.MethodGet, "/api/journal/tasks?session_id=m1", nil)
	if code != http.StatusOK {
		t.Fatalf("code %d %s", code, body)
	}
	var got struct {
		Tasks []jTask `json:"tasks"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	byNumber := map[string]jTask{}
	for _, tk := range got.Tasks {
		byNumber[tk.Number] = tk
	}
	want := map[string]struct{ state, owner string }{"t1-12": {"open", "Adam"}, "t1-13": {"open", ""}, "t1-9": {"done", "Kenji"}, "t1-10": {"dropped", ""}}
	if len(byNumber) != len(want) {
		t.Fatalf("tasks = %+v", got.Tasks)
	}
	for number, w := range want {
		tk := byNumber[number]
		if tk.State != w.state || tk.Owner != w.owner {
			t.Errorf("task %s = %+v, want state %s owner %s", number, tk, w.state, w.owner)
		}
	}
	if !slices.Equal(byNumber["t1-12"].Refs, []string{"#2829"}) || byNumber["t1-12"].Title != "寫文案" {
		t.Errorf("task t1-12 = %+v", byNumber["t1-12"])
	}

	solo := j.unit("solo:s9", "專案 A")
	if err := journal.WriteTasks(unitHome(solo).tasksFile, []journal.Task{{Key: "#7", Title: "改實作", State: journal.TaskDoing}}); err != nil {
		t.Fatal(err)
	}
	_, body = j.request(http.MethodGet, "/api/journal/tasks?session_id=s9", nil)
	if !strings.Contains(string(body), `"number":"7"`) || strings.Contains(string(body), "#7") {
		t.Errorf("solo task: %s", body)
	}

	j.unit("solo:quiet", "專案 B")
	code, body = j.request(http.MethodGet, "/api/journal/tasks?session_id=quiet", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"tasks":[]`) {
		t.Errorf("a unit without a tasks file: code %d body %s", code, body)
	}
	if code, _ := j.request(http.MethodGet, "/api/journal/tasks?session_id=nobody", nil); code != http.StatusNotFound {
		t.Errorf("no unit: code %d", code)
	}
}

func teamSeat(id string) team.Seat { return team.Seat{TeamID: id, Role: team.SeatMember} }

// rawPut sends a body to the confirm route as it is and answers the status code.
func (j *journalEnv) rawPut(body string) int {
	w := httptest.NewRecorder()
	j.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/journal/confirm", strings.NewReader(body)))
	return w.Code
}

type jStage struct {
	Stage       string `json:"stage"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type jFeature struct {
	Name   string   `json:"name"`
	Stages []jStage `json:"stages"`
}

const apiRoadmap = "# Roadmap\n\n## Journal\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| J-0a | team feed | 完成 |\n| J-1e | API | 進行中 |\n\n## Name pool\n\n| 階段 | 說明 | 狀態 |\n|---|---|---|\n| NP-1 | 名字 | 未開始 |\n"

// The roadmap route is the project's roadmap.md, a feature with its stages (the App's names: stage, description, status; the
// five Chinese statuses as written). No file, or a table nobody can parse, is no features, not an error the App has to guess at.
// Mutation gate: the stage's fields keep the Go names (red); a missing file is a 500 (red); an unparsable table is a 500 (red).
func TestJournalAPI_RoadmapIsTheProjectsRoadmapFile(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("solo:s1", "專案 A")
	if code, body := j.request(http.MethodGet, "/api/journal/roadmap?session_id=s1", nil); code != http.StatusOK || !strings.Contains(string(body), `"features":[]`) {
		t.Fatalf("no roadmap file: code %d body %s", code, body)
	}
	home := unitHome(u)
	if err := os.MkdirAll(filepath.Dir(home.roadmapFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.roadmapFile, []byte(apiRoadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := j.request(http.MethodGet, "/api/journal/roadmap?session_id=s1", nil)
	var got struct {
		Features []jFeature `json:"features"`
	}
	if err := json.Unmarshal(body, &got); err != nil || code != http.StatusOK {
		t.Fatalf("code %d err %v body %s", code, err, body)
	}
	want := []jFeature{
		{Name: "Journal", Stages: []jStage{{"J-0a", "team feed", "完成"}, {"J-1e", "API", "進行中"}}},
		{Name: "Name pool", Stages: []jStage{{"NP-1", "名字", "未開始"}}},
	}
	if len(got.Features) != 2 || !slices.Equal(got.Features[0].Stages, want[0].Stages) || got.Features[0].Name != "Journal" || !slices.Equal(got.Features[1].Stages, want[1].Stages) {
		t.Fatalf("features = %+v, want %+v", got.Features, want)
	}
	if err := os.WriteFile(home.roadmapFile, []byte("## F\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, body := j.request(http.MethodGet, "/api/journal/roadmap?session_id=s1", nil); code != http.StatusOK || !strings.Contains(string(body), `"features":[]`) {
		t.Errorf("unparsable roadmap: code %d body %s", code, body)
	}
}

// tasks.md and roadmap.md come from the journal branch, which can come from a remote: a symlink or a huge file there is not
// read (the same guard as a month file), and the route answers none, not the file's target.
// Mutation gate: read the file whatever it is -> the symlink target's tasks come back (red).
func TestJournalAPI_TheSideFilesAreNotTrusted(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("solo:s1", "專案 A")
	home := unitHome(u)
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := journal.WriteTasks(target, []journal.Task{{Key: "#1", Title: "從別處讀到的", State: journal.TaskDoing}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(home.tasksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, home.tasksFile); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(home.roadmapFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.roadmapFile, []byte(apiRoadmap+strings.Repeat("x", journal.MaxSideFileBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, body := j.request(http.MethodGet, "/api/journal/tasks?session_id=s1", nil); code != http.StatusOK || strings.Contains(string(body), "從別處讀到的") || !strings.Contains(string(body), `"tasks":[]`) {
		t.Errorf("a symlinked tasks file: code %d body %s", code, body)
	}
	if code, body := j.request(http.MethodGet, "/api/journal/roadmap?session_id=s1", nil); code != http.StatusOK || !strings.Contains(string(body), `"features":[]`) {
		t.Errorf("an oversized roadmap: code %d body %s", code, body)
	}
}

type changedEvent struct {
	Unit struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"unit"`
	Project string `json:"project"`
	What    string `json:"what"`
}

func (j *journalEnv) changed() []changedEvent {
	j.t.Helper()
	var out []changedEvent
	for _, ev := range j.sent() {
		if ev.typ != "journal.changed" {
			continue
		}
		var c changedEvent
		if err := json.Unmarshal([]byte(ev.value), &c); err != nil {
			j.t.Fatalf("journal.changed value %q: %v", ev.value, err)
		}
		out = append(out, c)
	}
	return out
}

// A hand check sets item idx in the entry's month file (whichever month it is in) and answers 204; journal.changed (entries)
// is sent only when something changed; a member checks in its team's journal; an entry or item that is not there is a 404,
// an entry that is not a confirm a 400, and a body the App did not mean a 400. All items checked -> the entry leaves confirms[]
// and open_confirms drops.
// Mutation gate: map every error to 500 (red); always announce -> a no-op check sends an event (red); never announce (red);
// look in the newest month only -> an older entry is not found (red); drop Manual from the write (red).
func TestJournalAPI_ConfirmChecksAndUnchecksAndAnnounces(t *testing.T) {
	j := newJournalAPI(t)
	u := j.unit("team:t1", "日誌")
	j.write(u,
		journal.Entry{ID: "old", Kind: journal.KindConfirm, At: tm(time.October, 5, 1), Actor: "A", Text: "in an older month", Items: []journal.Item{{Text: "甲?"}, {Text: "乙?"}}},
		journal.Entry{ID: "new", Kind: journal.KindExec, At: tm(time.November, 5, 1), Actor: "A", Text: "in the newest month"},
	)
	j.seats.seats["m1"] = teamSeat("t1")
	put := func(entry string, idx int, done bool) (int, string) {
		code, body := j.request(http.MethodPut, "/api/journal/confirm", map[string]any{"session_id": "m1", "entry_id": entry, "idx": idx, "done": done})
		return code, string(body)
	}
	if code, body := put("old", 0, true); code != http.StatusNoContent || body != "" {
		t.Fatalf("check: code %d body %q", code, body)
	}
	evs := j.changed()
	if len(evs) != 1 || evs[0].Unit.Kind != "team" || evs[0].Unit.ID != "t1" || evs[0].What != "entries" || evs[0].Project != "p1" {
		t.Fatalf("events after a check = %+v", evs)
	}
	es, _, err := journal.ReadUnit(unitHome(u).journalDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.ID == "old" && (!e.Items[0].Done || !e.Items[0].Manual || e.Items[1].Done || e.Items[1].Manual) {
			t.Errorf("items = %+v", e.Items)
		}
	}
	_, p, _ := j.page("/api/journal?session_id=m1")
	if !slices.Equal(ids(p.Confirms), []string{"old"}) || p.OpenConfirms != 1 {
		t.Errorf("confirms = %v, open_confirms = %d", ids(p.Confirms), p.OpenConfirms)
	}
	if code, _ := put("old", 0, true); code != http.StatusNoContent {
		t.Errorf("checking twice: code %d", code)
	}
	if evs := j.changed(); len(evs) != 0 {
		t.Errorf("a check that changed nothing announced %+v", evs)
	}
	if code, _ := put("old", 1, true); code != http.StatusNoContent {
		t.Fatalf("check the last: code %d", code)
	}
	_, p, _ = j.page("/api/journal?session_id=m1")
	if len(p.Confirms) != 0 || p.OpenConfirms != 0 {
		t.Errorf("all checked: confirms = %v, open_confirms = %d", ids(p.Confirms), p.OpenConfirms)
	}
	j.changed()
	if code, _ := put("old", 1, false); code != http.StatusNoContent {
		t.Fatalf("uncheck: code %d", code)
	}
	if evs := j.changed(); len(evs) != 1 {
		t.Errorf("events after an uncheck = %+v", evs)
	}
	_, p, _ = j.page("/api/journal?session_id=m1")
	if !slices.Equal(ids(p.Confirms), []string{"old"}) || p.OpenConfirms != 1 {
		t.Errorf("after the uncheck: confirms = %v, open_confirms = %d", ids(p.Confirms), p.OpenConfirms)
	}
	j.changed()
	for name, c := range map[string]struct {
		entry string
		idx   int
		want  int
	}{
		"unknown entry":  {"nobody", 0, http.StatusNotFound},
		"index past end": {"old", 2, http.StatusNotFound},
		"negative index": {"old", -1, http.StatusNotFound},
		"not a confirm":  {"new", 0, http.StatusBadRequest},
	} {
		if code, body := put(c.entry, c.idx, true); code != c.want {
			t.Errorf("%s: code %d body %s, want %d", name, code, body, c.want)
		}
	}
	if evs := j.changed(); len(evs) != 0 {
		t.Errorf("a refused check announced %+v", evs)
	}
	for name, body := range map[string]any{
		"no idx":       map[string]any{"session_id": "m1", "entry_id": "old", "done": true},
		"no done":      map[string]any{"session_id": "m1", "entry_id": "old", "idx": 0},
		"no entry":     map[string]any{"session_id": "m1", "idx": 0, "done": true},
		"no session":   map[string]any{"entry_id": "old", "idx": 0, "done": true},
		"wrong type":   map[string]any{"session_id": "m1", "entry_id": "old", "idx": 0, "done": "yes"},
		"unknown unit": map[string]any{"session_id": "ghost", "entry_id": "old", "idx": 0, "done": true},
	} {
		code, out := j.request(http.MethodPut, "/api/journal/confirm", body)
		want := http.StatusBadRequest
		if name == "unknown unit" {
			want = http.StatusNotFound
		}
		if code != want {
			t.Errorf("%s: code %d body %s, want %d", name, code, out, want)
		}
	}
	if code := j.rawPut("{not json"); code != http.StatusBadRequest {
		t.Errorf("a body that is not JSON: code %d", code)
	}
	if code, _ := j.request(http.MethodPut, "/api/journal/pin", map[string]any{"session_id": "m1", "entry_id": "old", "pinned": true}); code == http.StatusOK || code == http.StatusNoContent {
		t.Errorf("the pin route still answers: code %d", code)
	}
}
