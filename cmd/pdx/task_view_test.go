package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The clock of the age columns: tests never read the real time.
var taskTestNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func fakeTaskClock(t *testing.T) {
	t.Helper()
	old := taskNow
	taskNow = func() time.Time { return taskTestNow }
	t.Cleanup(func() { taskNow = old })
}

func agoMs(d time.Duration) int64 { return taskTestNow.Add(-d).UnixMilli() }

var cellGap = regexp.MustCompile(` {2,}`)

// rows splits a table into rows of cells (two or more spaces separate cells).
func rows(out string) [][]string {
	var r [][]string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		r = append(r, cellGap.Split(strings.TrimRight(l, " "), -1))
	}
	return r
}

func TestTaskLs_TableAndJSON(t *testing.T) {
	fakeTaskClock(t)
	blocked := fakeTask("8f2c0f-1", team.TaskPending, "Blocked one")
	blocked.Blocked, blocked.BlockedBy = true, []string{"8f2c0f-9"}
	busyBlocked := fakeTask("8f2c0f-2", team.TaskInProgress, "Started although blocked")
	busyBlocked.Blocked = true
	busyBlocked.LastTurn = &team.TaskTurnStamp{Summary: "x", At: agoMs(45 * time.Second)}
	gone := fakeTask("8f2c0f-3", team.TaskPending, "Owner went away")
	gone.Owner.State = "gone"
	newest := fakeTask("8f2c0f-4", team.TaskInProgress, "Newest of turn and report")
	newest.LastTurn = &team.TaskTurnStamp{Summary: "x", At: agoMs(3 * time.Hour)}
	newest.LastReport = &team.TaskReportStamp{Kind: "progress", Summary: "y", At: agoMs(12 * time.Minute)}
	old := fakeTask("8f2c0f-5", team.TaskInProgress, strings.Repeat("長", 60))
	old.LastReport = &team.TaskReportStamp{Kind: "ack", Summary: "y", At: agoMs(49 * time.Hour)}
	weird := fakeTask("8f2c0f-6", team.TaskPending, "esc\x1b[31m")
	weird.Owner.Ref = "_m1\x1bm1"
	list := team.TaskList{Tasks: []team.Task{blocked, busyBlocked, gone, newest, old, weird}}

	d := &fakeTaskDaemon{list: answer{body: list}}
	code, stdout, stderr := driveTask(t, d, "ls")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	want := [][]string{
		{"ID", "STATUS", "OWNER", "SUBJECT", "LAST"},
		{"8f2c0f-1", "pending (blocked)", "_m1m1m1", "Blocked one", "-"},
		{"8f2c0f-2", "in_progress", "_m1m1m1", "Started although blocked", "45s"},
		{"8f2c0f-3", "pending", "_m1m1m1 (gone)", "Owner went away", "-"},
		{"8f2c0f-4", "in_progress", "_m1m1m1", "Newest of turn and report", "12m"},
		{"8f2c0f-5", "in_progress", "_m1m1m1", strings.Repeat("長", 39) + "…", "2d"},
		{"8f2c0f-6", "pending", `_m1\x1bm1`, `esc\x1b[31m`, "-"},
	}
	got := rows(stdout)
	if len(got) != len(want) {
		t.Fatalf("table:\n%s", stdout)
	}
	for i := range want {
		if strings.Join(got[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
	if q := d.listQueries[0]; q.Get("origin_inbox") != fakeInbox || q.Has("member") || q.Has("all") {
		t.Errorf("query = %v", q)
	}

	// The remaining ages, and the filters.
	for age, want := range map[time.Duration]string{0: "0s", 59 * time.Second: "59s", 60 * time.Second: "1m", 59 * time.Minute: "59m",
		time.Hour: "1h", 23 * time.Hour: "23h", 24 * time.Hour: "1d", -5 * time.Second: "0s"} {
		if got := taskAge(agoMs(age)); got != want {
			t.Errorf("taskAge(%v ago) = %q, want %q", age, got, want)
		}
	}
	if taskAge(0) != "-" {
		t.Errorf("taskAge(0) = %q, want -", taskAge(0))
	}
	d = &fakeTaskDaemon{list: answer{body: team.TaskList{Tasks: []team.Task{}}}}
	if code, stdout, _ := driveTask(t, d, "ls", "--member", "_m1m1m1", "--all"); code != ExitOK || len(rows(stdout)) != 1 {
		t.Errorf("empty list: code=%d stdout=%q", code, stdout)
	}
	if q := d.listQueries[0]; q.Get("member") != "_m1m1m1" || q.Get("all") != "1" {
		t.Errorf("filter query = %v", q)
	}

	// --json is the daemon's answer, compact and unchanged.
	d = &fakeTaskDaemon{list: answer{body: list}}
	code, stdout, _ = driveTask(t, d, "ls", "--json")
	var back team.TaskList
	raw, _ := json.Marshal(list)
	if code != ExitOK || stdout != string(raw)+"\n" || json.Unmarshal([]byte(stdout), &back) != nil {
		t.Errorf("--json: code=%d stdout=%q", code, stdout)
	}
}

func detailFixture() team.TaskDetail {
	task := fakeTask("8f2c0f-3", team.TaskInProgress, "Fix the build")
	task.Description = "Line one.\nLine two."
	task.DoneWhen = []string{"tests pass", "PR open"}
	task.BlockedBy, task.Blocks = []string{"8f2c0f-1"}, []string{"8f2c0f-7"}
	task.Metadata = team.TaskMetadata{Branch: "feat/x", PRs: []int{12, 15}, SHAs: []string{"abc1234"}}
	task.LastReport = &team.TaskReportStamp{Kind: "progress", Summary: "half way", At: agoMs(12 * time.Minute)}
	task.LastTurn = &team.TaskTurnStamp{Summary: "Working on it.", At: agoMs(45 * time.Second)}
	return team.TaskDetail{Task: task, Reports: []team.Report{
		{ID: "r2", Task: task.ID, Kind: team.ReportProgress, Summary: "half way", CreatedAt: agoMs(12 * time.Minute)},
		{ID: "r1", Task: task.ID, Kind: team.ReportAck, Summary: "starting", CreatedAt: agoMs(2 * time.Hour)},
	}}
}

func TestTaskShow_HumanAndJSON(t *testing.T) {
	fakeTaskClock(t)
	det := detailFixture()
	d := &fakeTaskDaemon{detail: answer{body: det}}
	code, stdout, stderr := driveTask(t, d, "show", "8f2c0f-3")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if len(d.detailPaths) != 1 || d.detailPaths[0] != "/api/team/tasks/8f2c0f-3?origin_inbox=%2Ftmp%2Fcc-socks%2F1.sock" {
		t.Errorf("detail request = %v", d.detailPaths)
	}
	want := `8f2c0f-3  Fix the build
  owner:        _m1m1m1 (active)  mlab/_m1m1m1
  status:       in_progress
  Line one.
  Line two.
  done when:
    - tests pass
    - PR open
  blocked by:   8f2c0f-1
  blocks:       8f2c0f-7
  branch:       feat/x
  prs:          #12, #15
  shas:         abc1234
  last report:  progress half way (12m ago)
  last turn:    Working on it. (45s ago)
  reports:
    12m progress half way
    2h ack starting
`
	if stdout != want {
		t.Errorf("show:\n%s\nwant:\n%s", stdout, want)
	}

	code, stdout, _ = driveTask(t, d, "show", "8f2c0f-3", "--json")
	raw, _ := json.Marshal(det)
	if code != ExitOK || stdout != string(raw)+"\n" {
		t.Errorf("--json: code=%d stdout=%q", code, stdout)
	}

	// A bare task prints no empty sections.
	bare := team.TaskDetail{Task: fakeTask("8f2c0f-4", team.TaskPending, "Bare"), Reports: []team.Report{}}
	_, stdout, _ = driveTask(t, &fakeTaskDaemon{detail: answer{body: bare}}, "show", "8f2c0f-4")
	if want := "8f2c0f-4  Bare\n  owner:        _m1m1m1 (active)  mlab/_m1m1m1\n  status:       pending\n"; stdout != want {
		t.Errorf("bare show:\n%q\nwant\n%q", stdout, want)
	}
}

// show --message prints what add sent, byte for byte (plus the final newline):
// the same composer, fed the stored task.
func TestTaskShow_MessageEqualsWhatAddSends(t *testing.T) {
	d := &fakeTaskDaemon{}
	if code, _, stderr := driveTask(t, d, addArgs...); code != ExitOK {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}
	sent := d.sendReq[0].Text
	// The daemon stores what add posted; show reads it back.
	var stored team.Task
	{
		req := d.createReq[0]
		stored = fakeTask(fakeTaskID, team.TaskPending, req.Subject)
		stored.Description, stored.DoneWhen = req.Description, req.DoneWhen
	}
	d2 := &fakeTaskDaemon{detail: answer{body: team.TaskDetail{Task: stored, Reports: []team.Report{}}}}
	code, stdout, stderr := driveTask(t, d2, "show", fakeTaskID, "--message")
	if code != ExitOK || stderr != "" || stdout != sent+"\n" {
		t.Errorf("code=%d stderr=%q\nshow --message: %q\nadd sent:       %q", code, stderr, stdout, sent)
	}
	if len(d2.sendReq) != 0 {
		t.Errorf("show --message sent a message")
	}
}
