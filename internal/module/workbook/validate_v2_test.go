package workbook

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// WB-1b′-a: the seventh field of the answer (spec §5.3), the refresh answer, and reading the numbers through a job's map.

func turnJSON(todos string) string {
	return `{"skip":false,"thing":"事","push":"推播","entry":"做了。","status":"狀況","thing_done":false,"todos":` + todos + `}`
}

func TestParseModelJSON_Todos(t *testing.T) {
	s, err := ParseModelJSON(turnJSON(`{"done":[1,3],"dropped":[2],"add":[{"title":"寫測試","detail":"先紅"},{"title":"部署","detail":""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := ModelTodos{Done: []int{1, 3}, Dropped: []int{2}, Add: []TodoAdd{{Title: "寫測試", Detail: "先紅"}, {Title: "部署"}}}
	if !reflect.DeepEqual(s.Todos, want) {
		t.Fatalf("todos = %+v, want %+v", s.Todos, want)
	}
	// an empty object, or one with some lists left out, is "nothing"
	for _, in := range []string{`{}`, `{"done":[]}`, `{"add":[]}`} {
		if s, err := ParseModelJSON(turnJSON(in)); err != nil || len(s.Todos.Done)+len(s.Todos.Dropped)+len(s.Todos.Add) != 0 {
			t.Errorf("%s: %+v err=%v", in, s.Todos, err)
		}
	}
}

// All seven fields are required, todos included — and it must be an object.
// Mutation gate: leave todos out of the required list → red.
func TestParseModelJSON_TodosAreRequiredAndShaped(t *testing.T) {
	noTodos := `{"skip":false,"thing":"事","push":"推播","entry":"做了。","status":"狀況","thing_done":false}`
	if _, err := ParseModelJSON(noTodos); !errors.Is(err, ErrFormat) {
		t.Errorf("without todos: %v", err)
	}
	for name, todos := range map[string]string{
		"null":           `null`,
		"array":          `[]`,
		"string":         `"none"`,
		"done not list":  `{"done":3}`,
		"done strings":   `{"done":["1"]}`,
		"done fraction":  `{"done":[1.5]}`,
		"add not list":   `{"add":{"title":"x"}}`,
		"add item array": `{"add":[["x"]]}`,
		"title number":   `{"add":[{"title":5}]}`,
		"dup done":       `{"done":[1],"done":[2]}`,
		"dup in an add":  `{"add":[{"title":"a","title":"b"}]}`,
	} {
		if _, err := ParseModelJSON(turnJSON(todos)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
}

// skip: true keeps its todos (spec §5.4: a turn with no progress may still answer an open question).
func TestParseModelJSON_SkipKeepsTodos(t *testing.T) {
	in := `{"skip":true,"thing":"","push":"","entry":"","status":"前情","thing_done":false,"todos":{"done":[2],"dropped":[],"add":[]}}`
	s, err := ParseModelJSON(in)
	if err != nil || !s.Skip || !reflect.DeepEqual(s.Todos.Done, []int{2}) {
		t.Fatalf("%+v err=%v", s, err)
	}
}

func TestResolveTodos_ThroughTheJobsMap(t *testing.T) {
	ids := map[int]int64{1: 101, 2: 102, 3: 103}
	got := ResolveTodos(ModelTodos{Done: []int{1, 9, 3}, Dropped: []int{2, 0, -4}, Add: []TodoAdd{{Title: "x"}}}, ids)
	if !reflect.DeepEqual(got.Done, []int64{101, 103}) || !reflect.DeepEqual(got.Dropped, []int64{102}) || len(got.Adds) != 1 {
		t.Fatalf("resolved = %+v (an unknown number is ignored)", got)
	}
	if got := ResolveTodos(ModelTodos{Done: []int{1}}, nil); len(got.Done) != 0 {
		t.Fatalf("no map, no ids: %+v", got)
	}
}

func refreshJSON(status, todos string) string {
	return `{"status":"` + status + `","todos":` + todos + `}`
}

// The refresh answer is {status, todos}: no push, no thing, no entry.
func TestParseRefreshJSON(t *testing.T) {
	r, err := ParseRefreshJSON("```json\n" + refreshJSON("整理完", `{"done":[1],"dropped":[2],"add":[{"title":"新","detail":""}]}`) + "\n```")
	if err != nil || r.Status != "整理完" || !reflect.DeepEqual(r.Todos.Done, []int{1}) || len(r.Todos.Add) != 1 {
		t.Fatalf("%+v err=%v", r, err)
	}
	for name, in := range map[string]string{
		"no status":    `{"todos":{}}`,
		"no todos":     `{"status":"x"}`,
		"empty status": refreshJSON("", `{}`),
		"not json":     `ok`,
		"dup status":   `{"status":"a","status":"b","todos":{}}`,
		"bad todos":    refreshJSON("x", `{"done":"1"}`),
		"too large":    refreshJSON(strings.Repeat("字", 40000), `{}`),
	} {
		if _, err := ParseRefreshJSON(in); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v, want ErrFormat", name, err)
		}
	}
	// a model that also sends push / thing / entry: they are not read
	if r, err := ParseRefreshJSON(`{"status":"s","todos":{},"push":"不該有","thing":"x","entry":"y"}`); err != nil || r.Status != "s" {
		t.Errorf("extra fields: %+v err=%v", r, err)
	}
}

func TestRepairRefresh_StatusCutAndRedacted(t *testing.T) {
	long := strings.Repeat("甲。", 150)
	r := RepairRefresh(RefreshResult{Status: "  " + long + "  "})
	if runes(r.Status) > 200 || !strings.HasSuffix(r.Status, "。") {
		t.Fatalf("status = %d runes", runes(r.Status))
	}
	if got := RepairRefresh(RefreshResult{Status: "Bearer sk-ant-api03-AbCdEf123456"}).Status; strings.Contains(got, "AbCdEf") {
		t.Fatalf("status leaks: %q", got)
	}
}

// Repair keeps the todos, and redacts what a model echoed into a todo title.
func TestRepair_KeepsTodosAndRedactsThem(t *testing.T) {
	s := Summary{Thing: "t", Entry: "e。", Todos: ModelTodos{Done: []int{1}, Add: []TodoAdd{{Title: "用 sk-ant-api03-AbCdEf123456 重跑", Detail: "ok"}}}}
	got, _ := Repair(s)
	if !reflect.DeepEqual(got.Todos.Done, []int{1}) || len(got.Todos.Add) != 1 {
		t.Fatalf("todos = %+v", got.Todos)
	}
	if strings.Contains(got.Todos.Add[0].Title, "AbCdEf") {
		t.Fatalf("title leaks: %q", got.Todos.Add[0].Title)
	}
}
