package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
)

// TaskState is the state of a task in tasks.md.
type TaskState string

const (
	TaskDoing   TaskState = "doing"
	TaskTodo    TaskState = "todo"
	TaskDone    TaskState = "done"
	TaskDropped TaskState = "dropped"
)

var taskStates = []struct {
	State TaskState
	Label string
}{{TaskDoing, "進行中"}, {TaskTodo, "未開始"}, {TaskDone, "完成"}, {TaskDropped, "放棄"}}

// Task is one row of a unit's tasks file.
type Task struct {
	Key   string    `json:"key"` // "#<n>" for a solo todo, "<team id>-<seq>" for a team task
	Title string    `json:"title"`
	State TaskState `json:"state"`
	Owner string    `json:"owner,omitempty"`
	Refs  []string  `json:"refs,omitempty"`
}

const taskMarker = " <!-- t "

// cell makes free text safe inside a table cell: one line, no pipe, no comment opener.
func cell(s string) string {
	s = strings.ReplaceAll(oneLine(s), "|", `\|`)
	return strings.ReplaceAll(s, "<!--", "&lt;!--")
}

// WriteTasks writes the whole tasks file: a table per non-empty state, in the order 進行中 / 未開始 / 完成 / 放棄,
// each row carrying its task as JSON in a trailing comment (the visible cells are a rendering of it).
func WriteTasks(file string, tasks []Task) error {
	for _, t := range tasks {
		known := false
		for _, st := range taskStates {
			known = known || st.State == t.State
		}
		if !known {
			return fmt.Errorf("%w: task %q has state %q", ErrUnknownStatus, t.Key, t.State)
		}
	}
	defer lockFile(file)()
	var lines []string
	for _, st := range taskStates {
		var rows []string
		for _, t := range tasks {
			if t.State != st.State {
				continue
			}
			j, err := commentJSON(t)
			if err != nil {
				return err
			}
			rows = append(rows, "| "+cell(t.Key)+" | "+cell(t.Title)+" | "+cell(t.Owner)+" | "+cell(strings.Join(t.Refs, ", "))+" |"+taskMarker+j+" -->")
		}
		if len(rows) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "## "+st.Label, "", "| 編號 | 標題 | 負責 | 關聯 |", "|---|---|---|---|")
		lines = append(lines, rows...)
	}
	if len(lines) == 0 {
		lines = []string{"（沒有任務）"}
	}
	return writeLines(file, lines)
}

// ParseTasks reads the tasks of a tasks file from the row comments only, in file order. A row whose comment does not
// parse is skipped and logged with its line; a missing file has no tasks.
func ParseTasks(file string) ([]Task, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Task
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSuffix(l, "\r")
		if !strings.HasPrefix(l, "|") {
			continue
		}
		at := strings.LastIndex(l, taskMarker)
		if at < 0 {
			continue
		}
		var t Task
		body := strings.TrimSuffix(l[at+len(taskMarker):], " -->")
		if !strings.HasSuffix(l, " -->") || json.Unmarshal([]byte(body), &t) != nil || t.Key == "" {
			log.Printf("journal: %s:%d: skipping unreadable task row", file, i+1)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}
