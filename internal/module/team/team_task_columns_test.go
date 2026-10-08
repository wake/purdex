package teammod

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// T-1d1 (D-T6): GET /api/team gives each member its current task and the time
// of its latest turn or report on it. A killed member keeps showing the task
// it had; a member with nothing open (completed only, or no task) has neither
// field. last_at is the later of last_turn_at and last_report_at (the member
// row's own last turn joins with T-3a2). Mutation gate: LastAt from
// last_turn_at alone -> red (m2's report is newer than its turn).
func TestTeam_MembersCarryTheirCurrentTask(t *testing.T) {
	f, root := newTeamFixture(t, 4)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	m2 := f.member(2, root, "sid-m2", "w-two", nil)
	m3 := f.member(3, root, "sid-m3", "w-three", nil)
	f.member(4, root, "sid-m4", "w-four", nil)
	s := f.m.store
	tm := uid(1)

	// m1: an in-progress task (turn 500 is later than its report 300) and an older pending one.
	a := mustCreateTask(t, s, newTask(tm, m1.SpawnOp, "build the thing", 10)) // seq 1
	mustCreateTask(t, s, newTask(tm, m1.SpawnOp, "write the docs", 20))       // seq 2
	if _, err := s.SetTaskStatus(tm, a.Seq, team.TaskInProgress, team.TaskByLead, 100); err != nil {
		t.Fatal(err)
	}
	mustInsertReport(t, s, newReport(tm, a.Seq, m1.SpawnOp, team.ReportProgress, 1, 300))
	if _, err := s.db.Exec(`UPDATE tasks SET last_turn_at = 500 WHERE team_id = ? AND seq = ?`, tm, a.Seq); err != nil {
		t.Fatal(err)
	}

	// m2: a pending task whose report (700) is later than its turn (400); then m2 is killed.
	b := mustCreateTask(t, s, newTask(tm, m2.SpawnOp, "review it", 30)) // seq 3
	mustInsertReport(t, s, newReport(tm, b.Seq, m2.SpawnOp, team.ReportProgress, 2, 700))
	if _, err := s.db.Exec(`UPDATE tasks SET last_turn_at = 400 WHERE team_id = ? AND seq = ?`, tm, b.Seq); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemberState(m2.SpawnOp, team.MemberKilled, 800); err != nil {
		t.Fatal(err)
	}

	// m3: only a finished task. m4: no task.
	c := mustCreateTask(t, s, newTask(tm, m3.SpawnOp, "already done", 40)) // seq 4
	if _, err := s.SetTaskStatus(tm, c.Seq, team.TaskCompleted, team.TaskByLead, 900); err != nil {
		t.Fatal(err)
	}

	code, v, e := f.teamView("/tmp/10.sock")
	if code != 200 || len(v.Members) != 4 {
		t.Fatalf("team = %d %+v %+v", code, v, e)
	}
	by := map[string]team.Member{}
	for _, m := range v.Members {
		by[m.SessionID] = m
	}
	want1 := &team.MemberTask{ID: team.TaskDisplayID(tm, 1), Subject: "build the thing", Status: team.TaskInProgress}
	if g := by["sid-m1"]; !reflect.DeepEqual(g.Task, want1) || g.LastAt != 500 {
		t.Errorf("m1 = task %+v last_at %d, want %+v / 500", g.Task, g.LastAt, want1)
	}
	want2 := &team.MemberTask{ID: team.TaskDisplayID(tm, 3), Subject: "review it", Status: team.TaskPending}
	if g := by["sid-m2"]; g.State != team.MemberKilled || !reflect.DeepEqual(g.Task, want2) || g.LastAt != 700 {
		t.Errorf("m2 (killed) = state %s task %+v last_at %d, want killed / %+v / 700", g.State, g.Task, g.LastAt, want2)
	}
	for _, sid := range []string{"sid-m3", "sid-m4"} {
		if g := by[sid]; g.Task != nil || g.LastAt != 0 {
			t.Errorf("%s = task %+v last_at %d, want none", sid, g.Task, g.LastAt)
		}
	}

	// The wire omits both keys when there is nothing to say (an old CLI sees no change).
	_, body := f.do(http.MethodGet, "/api/team?origin_inbox="+url.QueryEscape("/tmp/10.sock"), "")
	var raw struct {
		Members []map[string]json.RawMessage `json:"members"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range raw.Members {
		if string(m["session_id"]) != `"sid-m4"` {
			continue
		}
		seen = true
		if _, has := m["task"]; has {
			t.Errorf("m4 carries a task key: %s", body)
		}
		if _, has := m["last_at"]; has {
			t.Errorf("m4 carries a last_at key: %s", body)
		}
	}
	if !seen {
		t.Fatalf("m4 missing from the wire: %s", body)
	}
}
