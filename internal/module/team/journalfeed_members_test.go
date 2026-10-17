package teammod

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Members lists the active members of the team only, oldest first: a released, killed or gone member is not current.
// Mutation: drop `state = 'active'` from the query → the three non-active rows are listed and the test goes red.
func TestJournalFeed_MembersListsCurrentMembersOnly(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedTeam(t, s, tTeamB, "lead-b", 1)
	seedMember(t, s, "op-1", tTeamA, "sess-1", 10)
	seedMember(t, s, "op-2", tTeamA, "sess-2", 20)
	seedMember(t, s, "op-rel", tTeamA, "sess-rel", 30)
	seedMember(t, s, "op-kill", tTeamA, "sess-kill", 40)
	seedMember(t, s, "op-gone", tTeamA, "sess-gone", 50)
	seedMember(t, s, "op-other", tTeamB, "sess-other", 60)
	for op, st := range map[string]team.MemberState{"op-rel": team.MemberReleased, "op-kill": team.MemberKilled, "op-gone": team.MemberGone} {
		if err := s.SetMemberState(op, st, 99); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Members(tTeamA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].MemberKey != "op-1" || got[1].MemberKey != "op-2" {
		t.Fatalf("members = %+v, want op-1 and op-2 only", got)
	}
	if m := got[0]; m.SessionID != "sess-1" || m.Ref != "_memop-1" || m.Title != "worker" || m.State != team.MemberActive {
		t.Errorf("member fields = %+v", m)
	}
	if none, err := s.Members("no-such-team"); err != nil || len(none) != 0 {
		t.Errorf("unknown team = %+v err=%v, want none", none, err)
	}
}

// CurrentTask is currentTaskOf over the member's open tasks: in progress before a newer pending, completed never; a
// member with no open task, or an empty key, has none; another member's tasks do not count.
// Mutation: read the whole team's tasks (ownerKey "") → the other member's in-progress task wins and the test goes red.
func TestJournalFeed_CurrentTaskFollowsCurrentTaskOf(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedMember(t, s, "op-a", tTeamA, "sess-a", 2)
	seedMember(t, s, "op-b", tTeamA, "sess-b", 3)
	t1 := mustCreateTask(t, s, newTask(tTeamA, "op-a", "started", 10))
	mustCreateTask(t, s, newTask(tTeamA, "op-a", "newer pending", 20))
	t3 := mustCreateTask(t, s, newTask(tTeamA, "op-a", "finished", 30))
	tb := mustCreateTask(t, s, newTask(tTeamA, "op-b", "b's", 40))
	if _, err := s.SetTaskStatus(tTeamA, t1.Seq, team.TaskInProgress, team.TaskByLead, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskStatus(tTeamA, tb.Seq, team.TaskInProgress, team.TaskByLead, 41); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskStatus(tTeamA, t3.Seq, team.TaskInProgress, team.TaskByLead, 31); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTaskStatus(tTeamA, t3.Seq, team.TaskCompleted, team.TaskByLead, 32); err != nil {
		t.Fatal(err)
	}
	cur, ok, err := s.CurrentTask(tTeamA, "op-a")
	if err != nil || !ok || cur.Seq != t1.Seq || cur.Subject != "started" || cur.Status != team.TaskInProgress {
		t.Fatalf("current task of op-a = %+v ok=%v err=%v, want the in-progress one", cur, ok, err)
	}
	if _, ok, err := s.CurrentTask(tTeamA, ""); ok || err != nil {
		t.Errorf("empty key: ok=%v err=%v, want none", ok, err)
	}
	if _, ok, err := s.CurrentTask(tTeamA, "op-none"); ok || err != nil {
		t.Errorf("unknown member: ok=%v err=%v, want none", ok, err)
	}
}

// The journal's CurrentTask and /api/team answer the same task for every member. Mutation: make CurrentTask prefer the
// newest pending over in progress → it differs from the team view and the test goes red.
func TestJournalFeed_CurrentTaskAgreesWithTheTeamView(t *testing.T) {
	w := newTaskWorld(t)
	started := w.mustTask(leadInbox, w.ma.Ref, "started", nil)
	w.start(started.ID)
	w.clock.Add(5)
	w.mustTask(leadInbox, w.ma.Ref, "newer pending", nil)
	w.mustTask(leadInbox, w.mb.Ref, "only pending", nil)
	code, view, e := call[team.TeamView](w.fixture, http.MethodGet, "/api/team?origin_inbox="+leadInbox, nil)
	if code != 200 {
		t.Fatalf("%d %+v", code, e)
	}
	checked := 0
	for _, mv := range view.Members {
		cur, ok, err := w.m.store.CurrentTask(view.Team.ID, mv.SpawnOp)
		if err != nil {
			t.Fatal(err)
		}
		if (mv.Task != nil) != ok {
			t.Fatalf("member %s: team view task %+v, feed ok=%v", mv.SpawnOp, mv.Task, ok)
		}
		if ok {
			checked++
			if mv.Task.ID != team.TaskDisplayID(view.Team.ID, cur.Seq) || mv.Task.Subject != cur.Subject || mv.Task.Status != cur.Status {
				t.Errorf("member %s: team view %+v, feed %+v", mv.SpawnOp, mv.Task, cur)
			}
		}
	}
	if checked < 2 {
		t.Fatalf("only %d members had a task to compare", checked)
	}
}

// LastReports: one row per member_key, the newest; created_at ties go to the greater rowid (later stored), not the
// greater report id (a UUID, no order); another team's reports are not mixed in; a team with none answers an empty list; a store error is an error.
// Mutation: drop `rowid DESC` from the ORDER BY → the tie case reads the wrong report and goes red.
func TestJournalFeed_LastReports(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, tTeamA, "lead-a", 1)
	seedTeam(t, s, tTeamB, "lead-b", 1)
	seedMember(t, s, "op-1", tTeamA, "sess-1", 2)
	seedMember(t, s, "op-2", tTeamA, "sess-2", 3)
	seedMember(t, s, "op-b", tTeamB, "sess-b", 4)
	mustCreateTask(t, s, newTask(tTeamA, "op-1", "t", 5))
	mustCreateTask(t, s, newTask(tTeamB, "op-b", "t", 5))
	// op-1: three reports, the newest inserted in the middle; op-2: a tie on created_at where the greater id is the later stored one (greater rowid) (the primary key order would pick the smaller)
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-1", team.ReportProgress, 1, 100))
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-1", team.ReportReady, 2, 300))
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-1", team.ReportProgress, 3, 200))
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-2", team.ReportProgress, 4, 500))
	mustInsertReport(t, s, newReport(tTeamA, 1, "op-2", team.ReportBlocked, 9, 500))
	mustInsertReport(t, s, newReport(tTeamB, 1, "op-b", team.ReportDone, 5, 900))
	got, err := s.LastReports(tTeamA)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]team.ReportRow{}
	for _, r := range got {
		by[r.MemberKey] = r
	}
	if len(got) != 2 || by["op-1"].ID != reportID(2) || by["op-1"].Kind != "ready" || by["op-1"].CreatedAt != 300 {
		t.Fatalf("last reports = %+v, want op-1's newest and op-2's", got)
	}
	if r := by["op-2"]; r.ID != reportID(9) || r.Kind != "blocked" || r.RowID == 0 || r.MemberRef != "_memop-2" || r.TeamID != tTeamA {
		t.Errorf("op-2 tie = %+v, want the later-inserted report (greater rowid)", r)
	}
	// a member that is no longer active still has its row; the caller filters
	if err := s.SetMemberState("op-2", team.MemberReleased, 99); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LastReports(tTeamA); len(got) != 2 {
		t.Errorf("after a release: %d rows, want 2", len(got))
	}
	if none, err := s.LastReports("no-such-team"); err != nil || len(none) != 0 {
		t.Errorf("unknown team = %+v err=%v", none, err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LastReports(tTeamA); err == nil {
		t.Error("LastReports on a closed store answered without an error")
	}
}
