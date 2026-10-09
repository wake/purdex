package main

import (
	"net/http"
	"strings"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Plan T-2: pdx spawn --task-subject makes the brief the member's first task;
// pdx task mine lists a member's own, --seed in the relay notice's words.

const spawnTaskID = "8f2c0f-1"

func spawnDoneWithTask(req team.SpawnRequest) answer {
	a := spawnDone(req)
	op := a.body.(team.SpawnOp)
	op.TaskID = spawnTaskID
	return answer{body: op}
}

// The request carries the task (the brief is its description), and the one
// message is the member prefix, then the task's down message — header first,
// done-when, the report line — from the same composer as `pdx task add`.
func TestSpawnCmd_TaskSubjectMakesTheBriefTheFirstTask(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDoneWithTask}, send: answer{body: ipeers.SendResponse{MsgID: "m1"}}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief", "做 P4-7",
		"--task-subject", "P4-7 收尾", "--done-when", "PR merged", "--done-when", "tests green")
	if code != ExitOK || !strings.Contains(stdout, `"task_id":"`+spawnTaskID+`"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	want := &team.SpawnTask{Subject: "P4-7 收尾", Description: "做 P4-7", DoneWhen: []string{"PR merged", "tests green"}}
	if len(d.spawnReq) != 1 || d.spawnReq[0].Task == nil || d.spawnReq[0].Task.Subject != want.Subject ||
		d.spawnReq[0].Task.Description != want.Description || strings.Join(d.spawnReq[0].Task.DoneWhen, "|") != "PR merged|tests green" {
		t.Fatalf("request task = %+v", d.spawnReq)
	}
	text := spawnBriefPrefix + "\n" + team.TaskDownMessage(team.Task{ID: spawnTaskID, Subject: "P4-7 收尾", Description: "做 P4-7", DoneWhen: want.DoneWhen})
	if len(d.sendReq) != 1 || d.sendReq[0].Text != text || d.sendReq[0].To != "mlab/_m1m1m1" {
		t.Fatalf("sends = %+v\nwant text %q", d.sendReq, text)
	}
	if !strings.HasPrefix(d.sendReq[0].Text, spawnBriefPrefix+"\n[pdx task "+spawnTaskID+"] P4-7 收尾\n") {
		t.Errorf("the task header is not right after the prefix: %q", d.sendReq[0].Text)
	}
}

// A task without a brief still sends the task message; without --task-subject
// nothing about the request or the message changes.
func TestSpawnCmd_TaskWithoutBriefAndBriefWithoutTask(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDoneWithTask}, send: answer{body: ipeers.SendResponse{MsgID: "m1"}}}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--task-subject", "只有主旨"); code != ExitOK || len(d.sendReq) != 1 ||
		!strings.Contains(d.sendReq[0].Text, "[pdx task "+spawnTaskID+"] 只有主旨") {
		t.Fatalf("code=%d sends=%+v stderr=%q", code, d.sendReq, stderr)
	}
	plain := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}, send: answer{body: ipeers.SendResponse{MsgID: "m1"}}}
	if code, stdout, _ := driveTeamCmd(t, runSpawnCmd, plain, "--model", "sonnet", "--brief", "x"); code != ExitOK || plain.spawnReq[0].Task != nil ||
		strings.Contains(stdout, "task_id") || plain.sendReq[0].Text != spawnBriefPrefix+"\nx" {
		t.Fatalf("a plain spawn changed: req=%+v stdout=%q sends=%+v", plain.spawnReq, stdout, plain.sendReq)
	}
}

// --done-when needs --task-subject; bad task text is exit 2 before the daemon is asked.
func TestSpawnCmd_TaskUsageErrorsExit2(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDoneWithTask}}
	for _, args := range [][]string{
		{"--done-when", "x"}, {"--task-subject", ""}, {"--task-subject", "bad\x1bsubject"},
		{"--task-subject", "s", "--done-when", ""}, {"--task-subject", "s", "--brief", strings.Repeat("x", 40000)},
	} {
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx spawn: ") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if n := d.count(); n != 0 {
		t.Errorf("the daemon saw %d request(s), want 0", n)
	}
}

// A daemon that answers no task id for a task the spawn asked for (an old
// daemon) is named on stderr; the member is already on stdout.
func TestSpawnCmd_NoTaskIDFromTheDaemon(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}, send: answer{body: ipeers.SendResponse{MsgID: "m1"}}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--task-subject", "s")
	if code != ExitError || !strings.Contains(stdout, `"ref":"_m1m1m1"`) || lastToken(stderr) != "invalid_response" || len(d.sendReq) != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q sends=%d", code, stdout, stderr, len(d.sendReq))
	}
}

// pdx task mine asks with mine=1; --seed prints the relay notice's lines for
// the open tasks only, nothing when there is none; a lead is exit 13.
func TestTaskMine_SeedFormatAndMemberOnly(t *testing.T) {
	open1 := fakeTask("8f2c0f-3", team.TaskInProgress, "接 U1-3")
	open2 := fakeTask("8f2c0f-5", team.TaskPending, "esc\x1b[31m")
	done := fakeTask("8f2c0f-4", team.TaskCompleted, "已完成")
	d := &fakeTaskDaemon{list: answer{body: team.TaskList{Tasks: []team.Task{open1, done, open2}}}}
	code, stdout, stderr := driveTask(t, d, "mine", "--seed")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	want := "你手上的任務：\n- 8f2c0f-3 in_progress 接 U1-3\n- 8f2c0f-5 pending " + sanitizeCell("esc\x1b[31m") + "\n"
	if stdout != want {
		t.Errorf("seed = %q, want %q", stdout, want)
	}
	if strings.ContainsRune(stdout, 0x1b) {
		t.Errorf("an escape reached the notice: %q", stdout)
	}
	if len(d.listQueries) != 1 || d.listQueries[0].Get("mine") != "1" {
		t.Errorf("queries = %v", d.listQueries)
	}
	none := &fakeTaskDaemon{list: answer{body: team.TaskList{Tasks: []team.Task{done}}}}
	if code, stdout, _ := driveTask(t, none, "mine", "--seed"); code != ExitOK || stdout != "" {
		t.Errorf("no open task: code=%d stdout=%q", code, stdout)
	}
	lead := &fakeTaskDaemon{list: answer{status: http.StatusConflict, body: team.APIError{Error: team.ErrNotMember, Detail: "not a member"}}}
	if code, _, stderr := driveTask(t, lead, "mine", "--seed"); code != ExitRefused || lastToken(stderr) != team.ErrNotMember {
		t.Errorf("lead: code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := driveTask(t, d, "mine", "--json", "--seed"); code != ExitUsage {
		t.Errorf("--json with --seed: code=%d", code)
	}
}
