package workbook

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// collect sets the drainer's notification to a list and returns a function that reads and clears it, as
// "<unit key>|<project>|<what>" sorted.
func collect(d *Drainer) func() []string {
	var got []string
	d.SetNotify(func(c JournalChange) { got = append(got, c.UnitKey+"|"+c.ProjectID+"|"+c.What) })
	return func() []string {
		out := got
		got = nil
		slices.Sort(out)
		return out
	}
}

func joined(s []string) string { return strings.Join(s, " ") }

// A row that was written and marked announces what it changed, per kind, once: a solo turn that added a todo wrote an entry
// and the tasks file; a journal row with events wrote entries and one with roadmap statuses wrote the roadmap; a row that
// wrote nothing visible (a failed entry with no todos) announces nothing.
// Mutation gate: announce every kind for every row (red); announce nothing (red); the unit key missing (red).
func TestNotify_ARowAnnouncesWhatItChanged(t *testing.T) {
	k := newDrainKit(t)
	took := collect(k.d)

	a := k.turn("c", "t1", team.SeatNone, adds("寫測試"))
	k.okEntry(a, "第一輪。", false)
	k.drain()
	if got := joined(took()); got != "solo:c|proj1|entries solo:c|proj1|tasks" {
		t.Fatalf("a solo turn with a todo announced %q", got)
	}

	b := k.turn("c", "t2", team.SeatNone, TodoChanges{})
	if ok, err := k.st.Finish(b, StateFailed, ReasonStore, Output{}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n := k.drain(); n != 1 {
		t.Fatalf("drained %d", n)
	}
	if got := took(); len(got) != 0 {
		t.Fatalf("a failed entry with no todo announced %q", got)
	}

	events := JournalOutcome{EntryID: a, PromptVer: 1, Result: JournalResult{Events: []JournalEvent{{Kind: journal.KindComm, Text: "通知部署", Actor: "1f → 88"}}}}
	if err := k.st.RecordJournalResult(events); err != nil {
		t.Fatal(err)
	}
	k.drain()
	if got := joined(took()); got != "solo:c|proj1|entries" {
		t.Fatalf("a journal row with events announced %q", got)
	}

	roadmap := JournalOutcome{EntryID: b, PromptVer: 1, Result: JournalResult{Roadmap: []RoadmapChange{{Feature: "Journal", Stage: "J-1e", Status: "完成"}}}}
	if err := k.st.RecordJournalResult(roadmap); err != nil {
		t.Fatal(err)
	}
	k.drain()
	if got := joined(took()); got != "solo:c|proj1|roadmap" {
		t.Fatalf("a journal row with a roadmap status announced %q", got)
	}
}

// Nothing is announced for a row that was not written (its write failed) or not marked (the mark failed); the row that is
// retried and then done announces once, not once per attempt.
// Mutation gate: announce before the mark (red); announce on a failed write (red).
func TestNotify_OnlyARowThatIsDoneAnnounces(t *testing.T) {
	k := newDrainKit(t)
	took := collect(k.d)
	a := k.turn("c", "t1", team.SeatNone, adds("寫測試"))
	k.okEntry(a, "第一輪。", false)

	k.fp.err = errors.New("no cwd") // the project cannot be resolved: nothing is written
	k.drain()
	if got := took(); len(got) != 0 {
		t.Fatalf("a row that could not be written announced %q", got)
	}
	k.fp.err = nil

	fail := true
	k.d.markDone = func(id int64) error {
		if fail {
			fail = false
			return errors.New("crash before the mark")
		}
		return k.st.MarkOutboxDone(id)
	}
	k.now = k.now.Add(retryMax + time.Second)
	k.drain()
	if got := took(); len(got) != 0 {
		t.Fatalf("a row that was written but not marked announced %q", got)
	}
	k.now = k.now.Add(retryMax + time.Second)
	k.drain()
	if got := joined(took()); got != "solo:c|proj1|entries solo:c|proj1|tasks" {
		t.Fatalf("the retried row announced %q, want its two kinds once", got)
	}
	k.now = k.now.Add(retryMax + time.Second)
	k.drain()
	if got := took(); len(got) != 0 {
		t.Fatalf("a drained row announced again: %q", got)
	}
}

// A delayed task row that is retried after a newer one was written does not rewrite the tasks file (it would put an older list
// back), so it does not announce the tasks: only the entries it carries.
// Mutation gate: announce tasks for a row that did not rewrite the file (red).
func TestNotify_AnOlderRowThatSkippedTheTasksFileDoesNotAnnounceThem(t *testing.T) {
	k := newMirrorKit(t)
	took := collect(k.d)
	k.finishBaseline()
	k.drain()
	took()
	k.feed.set(k.task(1, "寫文案", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick() // row A: task 1 new
	k.feed.set(k.task(1, "寫文案", taskCompleted, time.Second, 11*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick() // row B: task 1 done
	failed := false
	k.d.markDone = func(id int64) error {
		if !failed {
			failed = true
			return errors.New("crash before the mark") // row A is written, then delayed
		}
		return k.st.MarkOutboxDone(id)
	}
	k.drain()
	if got := joined(took()); got != "team:"+mkTeam+"|proj1|entries team:"+mkTeam+"|proj1|tasks" {
		t.Fatalf("after row B: %q", got)
	}
	k.now = k.now.Add(retryMax + time.Second)
	k.drain() // row A is retried: its task_new is already there, its list is older than row B's
	if got := joined(took()); got != "team:"+mkTeam+"|proj1|entries" {
		t.Fatalf("the retried older row announced %q, want only its entries", got)
	}
}

// A drainer with no notification set drains as before.
func TestNotify_NoHookIsFine(t *testing.T) {
	k := newDrainKit(t)
	a := k.turn("c", "t1", team.SeatNone, adds("寫測試"))
	k.okEntry(a, "第一輪。", false)
	if n := k.drain(); n != 1 {
		t.Fatalf("drained %d", n)
	}
}

// The task mirror's rows announce too: a team's tasks file is rewritten by every row, its entries only when the row carries
// events or closing lines.
// Mutation gate: a task row announces nothing (red); a task row with no event announces entries (red).
func TestNotify_ATaskRowAnnouncesTasksAndItsEntries(t *testing.T) {
	k := newMirrorKit(t)
	took := collect(k.d)
	k.finishBaseline()
	k.drain()
	took()
	k.feed.set(k.task(1, "寫文案", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if got := joined(took()); got != "team:"+mkTeam+"|proj1|entries team:"+mkTeam+"|proj1|tasks" {
		t.Fatalf("a new task announced %q", got)
	}
	// a change that only edits the title: the tasks file is rewritten, no entry is written
	k.feed.set(k.task(1, "寫文案（改）", taskDoing, time.Second, 20*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if got := joined(took()); got != "team:"+mkTeam+"|proj1|tasks" {
		t.Fatalf("a retitled task announced %q", got)
	}
}
