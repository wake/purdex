package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// T-1d1: `pdx team` gains TASK (the member's current task: id, status, the
// subject cut to 30 runes ellipsis included) and LAST (the age of last_at)
// between EFFORT and CWD. "-" for a member with no task or a daemon that
// predates the fields.
func TestTeamTable_TaskAndLastColumns(t *testing.T) {
	fakeTaskClock(t)
	base := fakeView()
	mk := func(i int, task *team.MemberTask, lastAt int64) team.Member {
		m := base.Members[0]
		m.Ref = fmt.Sprintf("_r%s", strings.Repeat(string(rune('a'+i)), 5))
		m.Address = "mlab/" + m.Ref
		m.Title = "t"
		m.Task, m.LastAt = task, lastAt
		return m
	}
	exact30 := strings.Repeat("x", 30)
	v := base
	v.Members = []team.Member{
		mk(0, &team.MemberTask{ID: "8f2c0f-2", Subject: "build the thing", Status: team.TaskInProgress}, agoMs(12*time.Minute)),
		mk(1, &team.MemberTask{ID: "8f2c0f-3", Subject: strings.Repeat("a", 40), Status: team.TaskPending}, agoMs(45*time.Second)),
		mk(2, &team.MemberTask{ID: "8f2c0f-4", Subject: strings.Repeat("長", 40), Status: team.TaskPending}, agoMs(3*time.Hour)),
		mk(3, &team.MemberTask{ID: "8f2c0f-5", Subject: exact30, Status: team.TaskInProgress}, agoMs(49*time.Hour)),
		mk(4, &team.MemberTask{ID: "8f2c0f-6", Subject: "esc\x1b[31m!", Status: team.TaskInProgress}, 0),
		mk(5, nil, 0), // no task / an older daemon: both cells "-"
		mk(6, nil, agoMs(time.Minute)),
	}
	d := &fakeTeamCmdDaemon{view: answer{body: v}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	got := rows(stdout)
	if len(got) != 1+len(v.Members) {
		t.Fatalf("stdout = %q, want a header and %d rows", stdout, len(v.Members))
	}
	if h := strings.Join(got[0], " "); h != "ADDRESS REF TITLE STATE CTX CPU MEM MODEL EFFORT TASK LAST CWD TMUX" {
		t.Errorf("header = %q", h)
	}
	for i, want := range [][2]string{
		{"8f2c0f-2 in_progress build the thing", "12m"},
		{"8f2c0f-3 pending " + strings.Repeat("a", 29) + "…", "45s"},
		{"8f2c0f-4 pending " + strings.Repeat("長", 14) + "…", "3h"},
		{"8f2c0f-5 in_progress " + exact30, "2d"},
		{`8f2c0f-6 in_progress esc\x1b[31m!`, "-"},
		{"-", "-"},
		{"-", "1m"},
	} {
		row := got[i+1]
		if len(row) != 13 {
			t.Fatalf("row %d = %q, want 13 cells", i, row)
		}
		if row[9] != want[0] || row[10] != want[1] {
			t.Errorf("row %d TASK / LAST = %q / %q, want %q / %q", i, row[9], row[10], want[0], want[1])
		}
		if row[11] != "/w/a" || row[12] != "tm-1111111122" {
			t.Errorf("row %d CWD / TMUX = %q / %q: the new cells shifted the old ones", i, row[11], row[12])
		}
	}
	if strings.ContainsRune(stdout, 0x1b) {
		t.Errorf("a raw ESC reached the terminal: %q", stdout)
	}
}

// --json is the daemon's view as is, the new fields included.
func TestTeamTable_JSONPassesTheNewFieldsThrough(t *testing.T) {
	v := fakeView()
	v.Members[0].Task = &team.MemberTask{ID: "8f2c0f-2", Subject: "build the thing", Status: team.TaskInProgress}
	v.Members[0].LastAt = 1234567890123
	d := &fakeTeamCmdDaemon{view: answer{body: v}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d, "--json")
	if code != ExitOK || stderr != "" || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{`"task":{"id":"8f2c0f-2","subject":"build the thing","status":"in_progress"}`, `"last_at":1234567890123`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--json lacks %s: %q", want, stdout)
		}
	}
	var back team.TeamView
	if err := json.Unmarshal([]byte(stdout), &back); err != nil || back.Members[1].Task != nil || back.Members[1].LastAt != 0 {
		t.Errorf("second member should carry neither field: err=%v %+v", err, back.Members[1])
	}
}
