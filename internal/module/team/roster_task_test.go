package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Plan T-3b (D-7): the roster's member carries its current task, subject only.

func (w *taskWorld) rosterMember(ref string) (team.RosterMember, bool) {
	w.t.Helper()
	for _, tr := range w.getRoster().Teams {
		if tr.ID != uid(1) {
			continue
		}
		for _, rm := range tr.Members {
			if rm.Ref == ref {
				return rm, true
			}
		}
	}
	return team.RosterMember{}, false
}

// The in_progress task is the member's task, over a newer pending one; another
// member's task is not shown; nothing of its last turn or report is.
func TestRoster_MemberCarriesItsCurrentTask(t *testing.T) {
	w := newTaskWorld(t)
	pending := w.mustTask(leadInbox, w.ma.Ref, "pending, newer", nil)
	busy := w.mustTask(leadInbox, w.ma.Ref, "in progress", nil)
	w.start(busy.ID)
	w.turn("sid-ma", "a secret sentence.", 100, 1) // a last turn on the task: never in the roster
	_ = pending
	rm, ok := w.rosterMember(w.ma.Ref)
	if !ok || rm.Task == nil {
		t.Fatalf("member = %+v ok=%v", rm, ok)
	}
	if *rm.Task != (team.RosterTask{ID: busy.ID, Subject: "in progress", Status: team.TaskInProgress}) {
		t.Fatalf("task = %+v", *rm.Task)
	}
	if mb, _ := w.rosterMember(w.mb.Ref); mb.Task != nil {
		t.Fatalf("another member's roster entry carries a task: %+v", *mb.Task)
	}
	raw, _ := json.Marshal(rm)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "last_turn") {
		t.Fatalf("the roster member leaks the last turn: %s", raw)
	}
}

// With nothing in progress the newest pending one shows; finished and deleted ones never do.
func TestRoster_PendingWhenNothingIsInProgressAndFinishedNever(t *testing.T) {
	w := newTaskWorld(t)
	old := w.mustTask(leadInbox, w.ma.Ref, "old pending", nil)
	newer := w.mustTask(leadInbox, w.ma.Ref, "newer pending", nil)
	if _, err := w.m.store.db.Exec(`UPDATE tasks SET updated_at = 10 WHERE team_id = ? AND seq = 1`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.m.store.db.Exec(`UPDATE tasks SET updated_at = 20 WHERE team_id = ? AND seq = 2`, uid(1)); err != nil {
		t.Fatal(err)
	}
	_ = old
	if rm, _ := w.rosterMember(w.ma.Ref); rm.Task == nil || rm.Task.ID != newer.ID || rm.Task.Status != team.TaskPending {
		t.Fatalf("task = %+v", rm.Task)
	}
	w.start(newer.ID)
	if code, _, e := w.setStatus(leadInbox, newer.ID, team.TaskCompleted); code != http.StatusOK {
		t.Fatalf("%d %+v", code, e)
	}
	if code, _, e := w.setStatus(leadInbox, old.ID, team.TaskDeleted); code != http.StatusOK {
		t.Fatalf("%d %+v", code, e)
	}
	if rm, _ := w.rosterMember(w.ma.Ref); rm.Task != nil {
		t.Fatalf("a finished or deleted task shows: %+v", *rm.Task)
	}
}

// A member with no task has no `task` key at all (additive).
func TestRoster_NoTaskNoField(t *testing.T) {
	w := newTaskWorld(t)
	code, body := w.do(http.MethodGet, RosterRoute, nil)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if strings.Contains(string(body), `"task"`) {
		t.Fatalf("a roster with no task has a task key: %s", body)
	}
}

// Every task change that moves a member's current task announces the roster: create, start, finish,
// reassign, and a report that moves the task. Mutation gate: no rosterChanged in respondTask → red.
func TestRoster_ChangedOnTaskChanges(t *testing.T) {
	w := newTaskWorld(t)
	w.m.rosterBaseline()
	watch := w.watchRoster()

	tk := w.mustTask(leadInbox, w.ma.Ref, "first", nil)
	ev := watch.one("create")
	if got := rosterTaskOf(ev, w.ma.Ref); got == nil || got.Status != team.TaskPending || got.ID != tk.ID {
		t.Fatalf("after create: %+v", got)
	}
	w.start(tk.ID)
	if got := rosterTaskOf(watch.one("start"), w.ma.Ref); got == nil || got.Status != team.TaskInProgress {
		t.Fatalf("after start: %+v", got)
	}
	if code, _, e := w.reassign(leadInbox, tk.ID, w.mb.Ref); code != http.StatusOK {
		t.Fatalf("reassign %d %+v", code, e)
	}
	ev = watch.one("reassign")
	if rosterTaskOf(ev, w.ma.Ref) != nil || rosterTaskOf(ev, w.mb.Ref) == nil {
		t.Fatalf("after reassign: ma %+v mb %+v", rosterTaskOf(ev, w.ma.Ref), rosterTaskOf(ev, w.mb.Ref))
	}
	if code, _, e := w.setStatus(leadInbox, tk.ID, team.TaskInProgress); code != http.StatusOK {
		t.Fatalf("%d %+v", code, e)
	}
	watch.one("start again")
	if code, _, e := w.setStatus(leadInbox, tk.ID, team.TaskCompleted); code != http.StatusOK {
		t.Fatalf("%d %+v", code, e)
	}
	if rosterTaskOf(watch.one("finish"), w.mb.Ref) != nil {
		t.Fatal("a finished task still shows")
	}
}

// A no-op changes nothing on the wire: the same roster is not announced twice.
func TestRoster_AnUnchangedCurrentTaskIsNotReannounced(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "first", nil)
	w.start(tk.ID)
	w.m.rosterBaseline()
	watch := w.watchRoster()
	w.turn("sid-ma", "a turn.", 100, 1) // a task write signals; the roster did not change
	watch.none("a last-turn write")
}

func rosterTaskOf(ev team.RosterEventValue, ref string) *team.RosterTask {
	for _, tr := range ev.Teams {
		for _, rm := range tr.Members {
			if rm.Ref == ref {
				return rm.Task
			}
		}
	}
	return nil
}

// A member's own report moves its task (ack starts it, done finishes it): the roster follows.
// Mutation gate: no rosterChanged in the report route → red.
func TestRoster_ChangedOnAReportThatMovesTheTask(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	w.m.rosterBaseline()
	watch := w.watchRoster()
	w.clock.Add(1)
	w.mustReport(maInbox, rreq(1, team.ReportAck, tk.ID))
	if got := rosterTaskOf(watch.one("ack"), w.ma.Ref); got == nil || got.Status != team.TaskInProgress {
		t.Fatalf("after ack: %+v", got)
	}
	w.clock.Add(1)
	w.mustReport(maInbox, rreq(2, team.ReportDone, tk.ID))
	if rosterTaskOf(watch.one("done"), w.ma.Ref) != nil {
		t.Fatal("a finished task still shows")
	}
}

// The roster reads every live team's open tasks in one narrow statement, keyed by team: another team's
// task never lands on this team's member, finished and deleted ones are not read, and an unknown team
// is nothing (T-3b attack review: no per-team query, no JSON payload decoded).
func TestOpenTaskBriefs_OneReadKeyedByTeamAndOnlyOpenTasks(t *testing.T) {
	w := newTaskWorld(t)
	a := w.mustTask(leadInbox, w.ma.Ref, "team one, open", nil)
	done := w.mustTask(leadInbox, w.ma.Ref, "team one, done", nil)
	w.start(done.ID)
	if code, _, e := w.setStatus(leadInbox, done.ID, team.TaskCompleted); code != http.StatusOK {
		t.Fatalf("%d %+v", code, e)
	}
	x := w.mustTask(lead2, w.mx.Ref, "team two, open", nil) // the same display prefix as team one
	got, err := w.m.store.OpenTaskBriefs([]string{uid(1), uid(2), "no-such-team"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[uid(1)][w.ma.SpawnOp]) != 1 || len(got[uid(2)][w.mx.SpawnOp]) != 1 {
		t.Fatalf("briefs = %+v", got)
	}
	one, two := got[uid(1)][w.ma.SpawnOp][0], got[uid(2)][w.mx.SpawnOp][0]
	if team.TaskDisplayID(uid(1), one.Seq) != a.ID || one.Subject != "team one, open" || one.Status != team.TaskPending {
		t.Fatalf("team one brief = %+v", one)
	}
	if team.TaskDisplayID(uid(2), two.Seq) != x.ID || two.Subject != "team two, open" {
		t.Fatalf("team two brief = %+v", two)
	}
	if empty, err := w.m.store.OpenTaskBriefs(nil); err != nil || len(empty) != 0 {
		t.Fatalf("no teams: %+v %v", empty, err)
	}
}
