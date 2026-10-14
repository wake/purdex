package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTasksRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "d", "tasks.md")
	tasks := []Task{
		{Key: "7bcf08-12", Title: "pipe | and <!-- j { --> and <!-- t {\"key\":\"fake\"} --> x\nnewline", State: TaskDone, Owner: "purdex-88", Refs: []string{"#2829", "PR #2830"}},
		{Key: "#3", Title: "solo todo", State: TaskDoing},
		{Key: "#4", Title: "later", State: TaskTodo, Owner: "x"},
		{Key: "#5", Title: "dropped", State: TaskDropped},
		{Key: "#6", Title: "second done", State: TaskDone},
	}
	if err := WriteTasks(file, tasks); err != nil {
		t.Fatal(err)
	}
	got, err := ParseTasks(file)
	if err != nil {
		t.Fatal(err)
	}
	// rows come back grouped by state, in the order of the tables
	want := []Task{tasks[1], tasks[2], tasks[0], tasks[4], tasks[3]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	b, _ := os.ReadFile(file)
	s := string(b)
	if strings.Index(s, "## 進行中") > strings.Index(s, "## 未開始") || strings.Index(s, "## 完成") > strings.Index(s, "## 放棄") {
		t.Errorf("state order wrong:\n%s", s)
	}
	for _, l := range strings.Split(s, "\n") {
		if i := strings.Index(l, taskMarker); i >= 0 && strings.Contains(l[i+len(taskMarker):len(l)-4], "--") {
			t.Errorf("row comment could close early: %s", l)
		}
	}
}

func TestTasksRewriteAndEdgeCases(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.md")
	if got, err := ParseTasks(file); err != nil || got != nil {
		t.Fatal(got, err)
	}
	if err := WriteTasks(file, []Task{{Key: "#1", Title: "a", State: TaskTodo}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteTasks(file, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := ParseTasks(file); len(got) != 0 {
		t.Fatalf("whole-file rewrite kept old rows: %+v", got)
	}
	// a hand-edited visible cell is not read back; a broken row is skipped
	if err := WriteTasks(file, []Task{{Key: "#1", Title: "a", State: TaskTodo}, {Key: "#2", Title: "b", State: TaskTodo}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	edited := strings.Replace(string(b), "| #1 | a |", "| #1 | HAND |", 1)
	edited = strings.Replace(edited, `<!-- t {"key":"#2"`, `<!-- t {"key":`, 1)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := ParseTasks(file)
	if len(got) != 1 || got[0].Title != "a" {
		t.Fatalf("%+v", got)
	}
}
