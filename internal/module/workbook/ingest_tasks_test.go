package workbook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// fakeFeed is a team.JournalFeed over slices, ordered and filtered the way the team module's store answers.
type fakeFeed struct {
	tasks   []team.TaskChange
	reports []team.ReportRow
	teams   map[string]team.TeamInfo
}

func (f *fakeFeed) TasksChangedAfter(c team.TaskCursor, limit int) ([]team.TaskChange, error) {
	var out []team.TaskChange
	for _, t := range f.tasks {
		if after(team.TaskCursor{UpdatedAt: t.UpdatedAt, TeamID: t.TeamID, Seq: t.Seq}, c) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return after(team.TaskCursor{UpdatedAt: out[j].UpdatedAt, TeamID: out[j].TeamID, Seq: out[j].Seq},
			team.TaskCursor{UpdatedAt: out[i].UpdatedAt, TeamID: out[i].TeamID, Seq: out[i].Seq})
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeFeed) ReportsAfter(afterRowID, sinceMS int64, limit int) ([]team.ReportRow, error) {
	var out []team.ReportRow
	for _, r := range f.reports {
		if r.RowID > afterRowID && r.CreatedAt >= sinceMS {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RowID < out[j].RowID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeFeed) TeamInfo(id string) (team.TeamInfo, bool, error) {
	i, ok := f.teams[id]
	return i, ok, nil
}

// set replaces the task with that team and seq, or adds it.
func (f *fakeFeed) set(c team.TaskChange) {
	for i, t := range f.tasks {
		if t.TeamID == c.TeamID && t.Seq == c.Seq {
			f.tasks[i] = c
			return
		}
	}
	f.tasks = append(f.tasks, c)
}

type mirrorKit struct {
	*drainKit
	feed  *fakeFeed
	m     *TaskMirror
	local map[string]bool
	t0    time.Time
}

const (
	mkTeam   = "7bcf08"
	mkLead   = "lead-sess"
	mkMember = "adam-sess"
)

func newMirrorKit(t *testing.T) *mirrorKit {
	t.Helper()
	k := &mirrorKit{drainKit: newDrainKit(t), feed: &fakeFeed{teams: map[string]team.TeamInfo{
		mkTeam: {TeamID: mkTeam, Label: "日誌", LeadSessionID: mkLead}}}, local: map[string]bool{mkLead: true}, t0: time.Unix(1_700_000_000, 0)}
	k.m = NewTaskMirror(k.st, k.feed, func(s string) bool { return k.local[s] }, nil, func(f string, a ...any) { k.logs = append(k.logs, fmt.Sprintf(f, a...)) })
	k.m.now = func() time.Time { return k.now }
	k.d.SetNames(func(sid string) string { return "mlab/purdex-3f-" + strings.TrimSuffix(sid, "-sess") }, nil)
	k.now = k.t0
	return k
}

func (k *mirrorKit) tick() {
	k.t.Helper()
	if err := k.m.Tick(context.Background()); err != nil {
		k.t.Fatal(err)
	}
}

// ms is the time d after the kit's start, in milliseconds.
func (k *mirrorKit) ms(d time.Duration) int64 { return k.t0.Add(d).UnixMilli() }

func (k *mirrorKit) task(seq int, subject, status string, created, updated time.Duration) team.TaskChange {
	return team.TaskChange{TeamID: mkTeam, Seq: seq, Subject: subject, Status: status, OwnerSessionID: mkMember,
		CreatedAt: k.ms(created), UpdatedAt: k.ms(updated)}
}

// finishBaseline runs the first scan on an empty feed, so later ticks write entries.
func (k *mirrorKit) finishBaseline() {
	k.tick()
	if v, ok, _ := k.st.JournalCursor(curReady); !ok || v == "" {
		k.t.Fatal("the first scan did not finish")
	}
}

func (k *mirrorKit) unitDir() string { return k.dir(UnitTeam + ":" + mkTeam) }

func (k *mirrorKit) entries() []journal.Entry {
	k.t.Helper()
	files, _ := filepath.Glob(filepath.Join(k.unitDir(), "*.md"))
	var all []journal.Entry
	for _, f := range files {
		es, err := journal.Parse(f)
		if err != nil {
			k.t.Fatal(err)
		}
		all = append(all, es...)
	}
	return all
}

func (k *mirrorKit) entry(id string) (journal.Entry, bool) {
	for _, e := range k.entries() {
		if e.ID == id {
			return e, true
		}
	}
	return journal.Entry{}, false
}

func (k *mirrorKit) tasksFile() []journal.Task {
	k.t.Helper()
	slug := unitSlug("專案 A", UnitTeam+":"+mkTeam)
	ts, err := journal.ParseTasks(filepath.Join(k.fp.wt, ".purdex", "tasks", slug+".md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		k.t.Fatal(err)
	}
	return ts
}

func (k *mirrorKit) outboxRows() int {
	var n int
	if err := k.st.db.QueryRow(`SELECT COUNT(*) FROM wb_journal_outbox WHERE kind = 'task'`).Scan(&n); err != nil {
		k.t.Fatal(err)
	}
	return n
}

// The first scan writes no entries for the past; its tasks file shows the open tasks and those finished in the last 30
// days, and nothing older.
// Mutation gate: write entries during the baseline -> red; keep every finished task in the file -> red.
func TestTaskMirror_TheFirstScanWritesNoHistoryButShowsTheCurrentTasks(t *testing.T) {
	k := newMirrorKit(t)
	day := 24 * time.Hour
	k.feed.set(k.task(1, "進行中的事", taskDoing, -60*day, -day))
	k.feed.set(k.task(2, "上週做完", taskCompleted, -20*day, -10*day))
	k.feed.set(k.task(3, "去年做完", taskCompleted, -90*day, -40*day))
	k.feed.set(k.task(4, "剛才開始", taskDoing, -10*time.Minute, -10*time.Minute)) // within the hour: still no entry
	k.tick()
	k.drain()
	if es := k.entries(); len(es) != 0 {
		t.Fatalf("the baseline wrote %d entries: %+v", len(es), es)
	}
	got := map[string]journal.TaskState{}
	for _, tk := range k.tasksFile() {
		got[tk.Key] = tk.State
	}
	want := map[string]journal.TaskState{mkTeam + "-1": journal.TaskDoing, mkTeam + "-2": journal.TaskDone, mkTeam + "-4": journal.TaskDoing}
	if len(got) != 3 || got[mkTeam+"-1"] != want[mkTeam+"-1"] || got[mkTeam+"-2"] != want[mkTeam+"-2"] || got[mkTeam+"-4"] != want[mkTeam+"-4"] {
		t.Fatalf("tasks file = %v, want %v", got, want)
	}
	// What was recorded is not written later either, and a closing line for a task that has no entry fills nothing.
	k.feed.reports = append(k.feed.reports, team.ReportRow{RowID: 1, TeamID: mkTeam, Kind: "merged", TaskSeq: 2, Summary: "晚到的摘要", CreatedAt: k.ms(time.Minute)})
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if es := k.entries(); len(es) != 0 {
		t.Fatalf("a later tick wrote history: %+v", es)
	}
	for _, l := range k.logs {
		if strings.Contains(l, "not there to receive") {
			t.Fatalf("a baseline task was sent a closing line for an entry that was never written: %s", l)
		}
	}
}

// A task created after the first scan is one task_new with its number, title, refs and the owner's display name; reading
// it again (the rewind does, every tick) changes nothing.
// Mutation gate: no state check -> a second outbox row per tick (red); entry id without the task seq -> red.
func TestTaskMirror_ACreatedTaskIsOneTaskNewAndAReReadChangesNothing(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	c := k.task(12, "RA-2 額度文案 (#2829)", taskPending, time.Second, time.Second)
	c.MetadataPRs = []int{2831}
	k.feed.set(c)
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	e, ok := k.entry(entryID(mkTeam, 12, "new"))
	if !ok || e.Kind != journal.KindTaskNew || e.Text != "#12 RA-2 額度文案 (#2829)" || e.Actor != "Adam" ||
		strings.Join(e.Refs, ",") != "#2829,PR #2831" || !e.At.Equal(time.UnixMilli(c.CreatedAt)) {
		t.Fatalf("task_new = %+v, %v", e, ok)
	}
	rows := k.outboxRows()
	for i := 0; i < 4; i++ {
		k.now = k.now.Add(mirrorEvery)
		k.tick()
	}
	if got := k.outboxRows(); got != rows {
		t.Fatalf("re-reading queued %d more row(s)", got-rows)
	}
	k.drain()
	if n := len(k.entries()); n != 1 {
		t.Fatalf("%d entries after re-reads, want 1", n)
	}
}

// A completion becomes one task_done; the closing line is the report summary, whichever of the two arrives first.
// Mutation gate: append-only (no fill) -> no closing line when the report is late (red); the line not remembered -> none when
// the report is early (red).
func TestTaskMirror_TheClosingLineReachesTheTaskDoneInEitherOrder(t *testing.T) {
	for _, reportFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("reportFirst=%v", reportFirst), func(t *testing.T) {
			k := newMirrorKit(t)
			k.finishBaseline()
			k.feed.set(k.task(3, "寫文案", taskDoing, time.Second, time.Second))
			k.now = k.now.Add(mirrorEvery)
			k.tick()
			report := team.ReportRow{RowID: 1, TeamID: mkTeam, ID: "r1", Kind: "merged", TaskSeq: 3, Summary: "  已\n合併 #2829。 ", CreatedAt: k.ms(10 * time.Second)}
			complete := k.task(3, "寫文案", taskCompleted, time.Second, 11*time.Second)
			if reportFirst {
				k.feed.reports = append(k.feed.reports, report)
				k.now = k.now.Add(mirrorEvery)
				k.tick()
				k.feed.set(complete)
			} else {
				k.feed.set(complete)
				k.now = k.now.Add(mirrorEvery)
				k.tick()
				k.drain()
				if e, ok := k.entry(entryID(mkTeam, 3, "done")); !ok || e.Detail != "" {
					t.Fatalf("task_done before the report = %+v, %v", e, ok)
				}
				k.feed.reports = append(k.feed.reports, report)
			}
			k.now = k.now.Add(mirrorEvery)
			k.tick()
			k.drain()
			e, ok := k.entry(entryID(mkTeam, 3, "done"))
			if !ok || e.Kind != journal.KindTaskDone || e.Text != "#3 寫文案" || e.Detail != "已 合併 #2829。" {
				t.Fatalf("task_done = %+v, %v", e, ok)
			}
			n := 0
			for _, x := range k.entries() {
				if x.Kind == journal.KindTaskDone {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%d task_done entries, want 1", n)
			}
		})
	}
}

// A task created and completed between two ticks gets both entries; a second report with another summary does not replace
// the first closing line; a deleted task has no entry and shows struck through.
func TestTaskMirror_CreatedAndCompletedInOneTickDeletedAndASecondReport(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(1, "很快", taskCompleted, time.Second, 2*time.Second))
	k.feed.set(k.task(2, "取消", taskDeleted, time.Second, 2*time.Second))
	k.feed.reports = []team.ReportRow{
		{RowID: 1, TeamID: mkTeam, Kind: "progress", TaskSeq: 1, Summary: "進度不算", CreatedAt: k.ms(time.Second)},
		{RowID: 2, TeamID: mkTeam, Kind: "done", TaskSeq: 1, Summary: "第一句", CreatedAt: k.ms(time.Second)},
		{RowID: 3, TeamID: mkTeam, Kind: "merged", TaskSeq: 1, Summary: "第二句", CreatedAt: k.ms(2 * time.Second)},
		{RowID: 4, TeamID: mkTeam, Kind: "done", TaskSeq: 0, Summary: "不屬於任何任務", CreatedAt: k.ms(2 * time.Second)},
	}
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if _, ok := k.entry(entryID(mkTeam, 1, "new")); !ok {
		t.Error("no task_new")
	}
	if e, ok := k.entry(entryID(mkTeam, 1, "done")); !ok || e.Detail != "第一句" {
		t.Errorf("task_done = %+v, %v", e, ok)
	}
	if _, ok := k.entry(entryID(mkTeam, 2, "done")); ok {
		t.Error("a deleted task has a task_done")
	}
	states := map[string]journal.TaskState{}
	for _, tk := range k.tasksFile() {
		states[tk.Key] = tk.State
	}
	if states[mkTeam+"-2"] != journal.TaskDropped || states[mkTeam+"-1"] != journal.TaskDone {
		t.Fatalf("tasks file = %v", states)
	}
}

// A row that commits behind the stored cursor is found by the rewind: 10 s every tick, an hour back once an hour.
// Mutation gate: no rewind -> the late row is never found (red); no hourly rewind -> the 30-minute-late row is never found (red).
func TestTaskMirror_ARowBehindTheCursorIsFoundByTheRewind(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(1, "先到", taskPending, 100*time.Second, 100*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.feed.set(k.task(2, "晚三秒才寫入", taskPending, 97*time.Second, 97*time.Second)) // sorts before the cursor, inside 10 s
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if _, ok := k.entry(entryID(mkTeam, 2, "new")); !ok {
		t.Fatal("a row 3 s behind the cursor was missed")
	}
	k.feed.set(k.task(3, "晚半小時", taskPending, 100*time.Second-30*time.Minute, 100*time.Second-30*time.Minute))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if _, ok := k.entry(entryID(mkTeam, 3, "new")); ok {
		t.Fatal("a row 30 minutes behind was found by an ordinary tick")
	}
	k.now = k.now.Add(time.Hour)
	k.tick()
	k.drain()
	if _, ok := k.entry(entryID(mkTeam, 3, "new")); !ok {
		t.Fatal("the hourly rewind did not find a row 30 minutes behind")
	}
}

// The cursors and the rows of a page commit together: a page that cannot be stored moves nothing, and the next tick finds it.
// Mutation gate: advance the cursor before the rows are stored -> the page is lost (red).
func TestTaskMirror_APageThatCannotBeStoredIsReadAgain(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(1, "會失敗的一頁", taskPending, time.Second, time.Second))
	if _, err := k.st.db.Exec(`CREATE TRIGGER no_task_rows BEFORE INSERT ON wb_journal_outbox BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	before, _, _ := k.st.JournalCursor(curTasks)
	k.now = k.now.Add(mirrorEvery)
	if err := k.m.Tick(context.Background()); err == nil {
		t.Fatal("the tick did not report the failed page")
	}
	if after, _, _ := k.st.JournalCursor(curTasks); after != before {
		t.Fatalf("the cursor moved from %s to %s although the page was not stored", before, after)
	}
	if _, err := k.st.db.Exec(`DROP TRIGGER no_task_rows`); err != nil {
		t.Fatal(err)
	}
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if _, ok := k.entry(entryID(mkTeam, 1, "new")); !ok {
		t.Fatal("the page was lost")
	}
}

// A lead relay changes the lead's session, not the unit: the folder stays, and the project is resolved once. A team whose
// lead is on another host is skipped altogether.
func TestTaskMirror_ALeadRelayKeepsTheUnitAndAForeignTeamIsSkipped(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(1, "第一件", taskPending, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	k.feed.teams[mkTeam] = team.TeamInfo{TeamID: mkTeam, Label: "日誌", LeadSessionID: "lead-2-sess"} // the lead relayed
	k.local["lead-2-sess"] = true
	k.feed.set(k.task(2, "第二件", taskPending, 2*time.Second, 2*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if len(k.entries()) != 2 || len(k.fp.calls) != 1 {
		t.Fatalf("%d entries, project resolved %d time(s); want 2 and 1", len(k.entries()), len(k.fp.calls))
	}
	if k.fp.calls[0].SessionID != mkLead || k.fp.calls[0].TeamID != mkTeam || k.fp.calls[0].Label != "日誌" || k.fp.calls[0].Kind != UnitTeam {
		t.Fatalf("project asked for %+v", k.fp.calls[0])
	}

	// Another host's team.
	k.feed.teams["other"] = team.TeamInfo{TeamID: "other", Label: "別台", LeadSessionID: "remote-lead"}
	o := k.task(1, "別台的任務", taskPending, 3*time.Second, 3*time.Second)
	o.TeamID = "other"
	k.feed.set(o)
	rows := k.outboxRows()
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	if k.outboxRows() != rows {
		t.Fatal("a team led on another host was mirrored")
	}
	if _, ok, _ := k.st.JournalCursor(curTeam + "other"); ok {
		t.Fatal("a state was kept for a team led on another host")
	}
}

// More tasks than a page: every one is mirrored, in several pages.
func TestTaskMirror_SeveralPagesAreAllMirrored(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	n := mirrorPage + 30 // two pages
	for i := 1; i <= n; i++ {
		k.feed.set(k.task(i, fmt.Sprintf("任務 %d", i), taskPending, time.Duration(i)*time.Millisecond, time.Duration(i)*time.Millisecond))
	}
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	for k.drain() > 0 {
	}
	if got := len(k.entries()); got != n {
		t.Fatalf("%d entries for %d tasks", got, n)
	}
}

func TestSummaryLine_OneTrimmedLineCutAtTheLimit(t *testing.T) {
	if got := summaryLine("  a\n\tb  c \n"); got != "a b c" {
		t.Errorf("summaryLine = %q", got)
	}
	long := strings.Repeat("字", summaryRunes+30)
	if got := summaryLine(long); len([]rune(got)) != summaryRunes {
		t.Errorf("summaryLine kept %d runes, want %d", len([]rune(got)), summaryRunes)
	}
	if got := summaryLine("   \n "); got != "" {
		t.Errorf("a blank summary = %q", got)
	}
}

// The tasks file shows each task's owner by display name and its references: an issue number in the title and a PR from
// the task's metadata.
// Mutation gate: no owner in the tasks file -> red; no references in the tasks file -> red.
func TestTaskMirror_TheTasksFileShowsTheOwnerAndTheRefsOfEachTask(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	c := k.task(4, "修 #2829", taskDoing, time.Second, time.Second)
	c.MetadataPRs = []int{2830}
	k.feed.set(c)
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	ts := k.tasksFile()
	if len(ts) != 1 {
		t.Fatalf("tasks file = %+v", ts)
	}
	if want := journal.DisplayName("mlab/purdex-3f-adam"); want == "" || ts[0].Owner != want {
		t.Errorf("owner = %q, want %q", ts[0].Owner, want)
	}
	if got := strings.Join(ts[0].Refs, ","); got != "#2829,PR #2830" {
		t.Errorf("refs = %q", got)
	}
}

// While a team's lead is elsewhere the cursors pass its tasks and reports. When the lead comes to this host, the team's next
// change reads what the cursors passed: the tasks file shows every open task and those finished lately; tasks changed within
// the last hour are written as entries like any new activity, older ones only recorded (no history is written).
// Mutation gate: no catch-up -> the tasks file shows only the new task (red); every task an entry -> the old ones get
// entries (red); recent tasks recorded only -> the recent one has no entry (red); no reports read -> the finished task has
// no closing line state (red).
func TestTaskMirror_ATeamWhoseLeadComesLaterIsCaughtUpAtItsNextChange(t *testing.T) {
	k := newMirrorKit(t)
	k.local[mkLead] = false
	k.finishBaseline()
	k.feed.set(k.task(1, "舊的進行中", taskDoing, -3*time.Hour, -3*time.Hour))
	k.feed.set(k.task(2, "舊的完成", taskCompleted, -3*time.Hour, -3*time.Hour))
	k.feed.set(k.task(3, "最近的", taskCompleted, -20*time.Minute, -10*time.Minute))
	k.feed.set(team.TaskChange{TeamID: "other", Seq: 9, Subject: "別組的", Status: taskDoing, OwnerSessionID: "x", CreatedAt: k.ms(-2 * time.Hour), UpdatedAt: k.ms(-2 * time.Hour)})
	k.feed.reports = []team.ReportRow{
		{RowID: 1, TeamID: mkTeam, ID: "r1", Kind: "merged", TaskSeq: 2, Summary: "舊的已合併", CreatedAt: k.ms(-3 * time.Hour)},
		{RowID: 2, TeamID: mkTeam, ID: "r2", Kind: "merged", TaskSeq: 3, Summary: "最近的已合併", CreatedAt: k.ms(-10 * time.Minute)},
	}
	k.now = k.now.Add(mirrorEvery)
	k.tick() // the lead is elsewhere: the cursors pass all of it
	k.drain()
	if len(k.entries()) != 0 || len(k.tasksFile()) != 0 {
		t.Fatalf("a team led elsewhere was mirrored: %d entries, %d tasks", len(k.entries()), len(k.tasksFile()))
	}
	k.local[mkLead] = true
	k.feed.set(k.task(4, "新任務", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	states := map[string]journal.TaskState{}
	for _, tk := range k.tasksFile() {
		states[tk.Key] = tk.State
	}
	want := map[string]journal.TaskState{mkTeam + "-1": journal.TaskDoing, mkTeam + "-2": journal.TaskDone, mkTeam + "-3": journal.TaskDone, mkTeam + "-4": journal.TaskDoing}
	if len(states) != len(want) {
		t.Fatalf("tasks file = %v, want %v", states, want)
	}
	for key, st := range want {
		if states[key] != st {
			t.Errorf("tasks file = %v, want %v", states, want)
			break
		}
	}
	var ids []string
	for _, e := range k.entries() {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	if got, wantIDs := strings.Join(ids, ","), strings.Join([]string{entryID(mkTeam, 3, "done"), entryID(mkTeam, 3, "new"), entryID(mkTeam, 4, "new")}, ","); got != wantIDs {
		t.Fatalf("entries = %s, want %s", got, wantIDs)
	}
	if e, ok := k.entry(entryID(mkTeam, 3, "done")); !ok || e.Detail != "最近的已合併" {
		t.Errorf("the recent task_done = %+v, %v: the report the cursor had passed was not read", e, ok)
	}
	k.feed.set(k.task(2, "舊的完成", taskCompleted, -3*time.Hour, -3*time.Hour)) // read again: nothing is queued
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if len(k.entries()) != 3 {
		t.Fatalf("a re-read of an old task wrote %d entries, want 3", len(k.entries()))
	}
}

// The catch-up reads every page of the feed, not the first only.
// Mutation gate: stop after the first page -> the tasks file lacks the rest (red).
func TestTaskMirror_TheCatchUpReadsEveryPage(t *testing.T) {
	k := newMirrorKit(t)
	k.local[mkLead] = false
	k.finishBaseline()
	n := 2*mirrorPage + 50
	for i := 1; i <= n; i++ { // a minute apart and all older than an hour: the rewinds cannot reach any of them
		k.feed.set(k.task(i, "舊的", taskDoing, -20*time.Hour, -20*time.Hour+time.Duration(i)*time.Minute))
	}
	k.now = k.now.Add(mirrorEvery)
	k.tick() // the lead is elsewhere: the cursors pass all of it
	k.local[mkLead] = true
	k.feed.set(k.task(n+1, "新任務", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	for k.drain() > 0 {
	}
	if got := len(k.tasksFile()); got != n+1 {
		t.Fatalf("the tasks file shows %d tasks, want %d", got, n+1)
	}
	if got := len(k.entries()); got != 1 {
		t.Fatalf("%d entries, want only the new task's: the old tasks are recorded, not written", got)
	}
}

// A task row carries the whole list as of its time. When an older row is delayed (its mark failed) and a newer one is
// written first, the older row's retry must not put the older list back.
// Mutation gate: write every row's list whatever came before -> the retry restores the old list (red).
func TestTaskMirror_ARetriedOlderRowDoesNotRestoreAnOlderTaskList(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(1, "寫文案", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick() // row A: the list with task 1 doing
	k.feed.set(k.task(1, "寫文案", taskCompleted, time.Second, 11*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick() // row B: the list with task 1 done
	failed := false
	k.d.markDone = func(id int64) error {
		if !failed {
			failed = true
			return errors.New("crash before the mark") // row A is written, then delayed
		}
		return k.st.MarkOutboxDone(id)
	}
	k.drain()
	k.now = k.now.Add(retryMax + time.Second)
	if n := k.drain(); n != 1 {
		t.Fatalf("the delayed row was handled %d times, want once", n)
	}
	states := map[string]journal.TaskState{}
	for _, tk := range k.tasksFile() {
		states[tk.Key] = tk.State
	}
	if states[mkTeam+"-1"] != journal.TaskDone {
		t.Fatalf("tasks file = %v: the retried older row put its list back", states)
	}
}

// The guard survives a restart: the newest row written is kept in the store, not in the drainer.
// Mutation gate: keep the newest row in memory only -> a restarted drainer retries the older row and puts its list back (red).
func TestTaskMirror_TheOlderRowStaysSkippedAfterARestart(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(1, "寫文案", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick() // row A: task 1 doing
	k.feed.set(k.task(1, "寫文案", taskCompleted, time.Second, 11*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick() // row B: task 1 done
	failed := false
	k.d.markDone = func(id int64) error {
		if !failed {
			failed = true
			return errors.New("crash before the mark") // row A is written, but stays undone
		}
		return k.st.MarkOutboxDone(id)
	}
	k.drain()
	k.d = NewDrainer(k.st, k.fp, func(f string, a ...any) { k.logs = append(k.logs, f) }) // the daemon restarts
	k.d.now = func() time.Time { return k.now }
	k.d.SetNames(func(sid string) string { return "mlab/purdex-3f-" + strings.TrimSuffix(sid, "-sess") }, nil)
	if n := k.drain(); n != 1 {
		t.Fatalf("the restarted drainer handled %d rows, want the one undone row", n)
	}
	states := map[string]journal.TaskState{}
	for _, tk := range k.tasksFile() {
		states[tk.Key] = tk.State
	}
	if states[mkTeam+"-1"] != journal.TaskDone {
		t.Fatalf("tasks file = %v: the older row put its list back after a restart", states)
	}
}

// A team the mirror saw while its lead was not on this host is looked at again every tick, so it is caught up the moment
// its lead is here, with no new row of that team (a daemon that starts before its sessions are registered sees every lead as
// elsewhere). A team still led elsewhere stays unmirrored.
// Mutation gate: no marker for a skipped team -> never caught up (red); no re-check -> never caught up (red); a team led
// elsewhere is mirrored (red); the marker never cleared -> still set after the catch-up (red).
func TestTaskMirror_ATeamSkippedWhileItsLeadWasUnknownIsCaughtUpWithoutNewRows(t *testing.T) {
	k := newMirrorKit(t)
	k.local[mkLead] = false // a daemon that has not yet registered its sessions
	k.feed.teams["far"] = team.TeamInfo{TeamID: "far", Label: "遠方", LeadSessionID: "far-lead"}
	k.feed.set(k.task(1, "舊的進行中", taskDoing, -5*time.Hour, -5*time.Hour))
	k.feed.set(k.task(2, "舊的完成", taskCompleted, -5*time.Hour, -4*time.Hour))
	k.feed.set(team.TaskChange{TeamID: "far", Seq: 1, Subject: "別處的", Status: taskDoing, OwnerSessionID: "x", CreatedAt: k.ms(-5 * time.Hour), UpdatedAt: k.ms(-5 * time.Hour)})
	k.tick() // the first scan: every lead looks elsewhere
	k.drain()
	if len(k.tasksFile()) != 0 {
		t.Fatal("a team led elsewhere was mirrored")
	}
	k.local[mkLead] = true // the sessions are registered; no row of the team changes
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	if got := len(k.tasksFile()); got != 2 {
		t.Fatalf("the tasks file shows %d tasks, want 2", got)
	}
	if es := k.entries(); len(es) != 0 {
		t.Fatalf("the catch-up of old tasks wrote %d entries: %+v", len(es), es)
	}
	if files, _ := filepath.Glob(filepath.Join(k.fp.wt, ".purdex", "tasks", "*.md")); len(files) != 1 {
		t.Fatalf("%d tasks files, want the one of the team led here", len(files))
	}
	if v, ok, _ := k.st.JournalCursor(curSkip + mkTeam); !ok || v != "" {
		t.Fatalf("the marker of the caught-up team = %q (%v), want it cleared", v, ok)
	}
	if v, _, _ := k.st.JournalCursor(curSkip + "far"); v != "1" {
		t.Fatalf("the marker of the team still led elsewhere = %q, want it kept", v)
	}
}

// A merged report is a completion too (spec §6): a known, unfinished task gets its task_done with the closing line at once, and
// the completion that follows writes nothing more. A merged report that comes in the same tick as the task, or before the task
// is known, still ends in one task_done once the task is seen. A done-kind report only gives a closing line; a deleted task
// never gets a task_done.
// Mutation gate: no task_done from a merged report -> the first case has none (red); a done-kind report counts as merged ->
// the done-kind case gets a task_done (red); a deleted task gets one -> red; no remembered merged report -> the same-tick
// case has no task_done (red).
func TestTaskMirror_AMergedReportWritesTheTaskDoneBeforeTheCompletion(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(5, "已知的", taskDoing, time.Second, time.Second))
	k.feed.set(k.task(6, "只收到 done 報告", taskDoing, time.Second, time.Second))
	k.feed.set(k.task(7, "被刪除", taskDeleted, time.Second, 2*time.Second))
	// a later row of another team: the cursor goes past these tasks, so the rewind does not read them again and only the
	// reports can complete them
	k.feed.set(team.TaskChange{TeamID: "other", Seq: 1, Subject: "別組", Status: taskDoing, OwnerSessionID: "x", CreatedAt: k.ms(15 * time.Second), UpdatedAt: k.ms(15 * time.Second)})
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.feed.reports = []team.ReportRow{
		{RowID: 1, TeamID: mkTeam, ID: "r1", Kind: "merged", TaskSeq: 5, Summary: "已合併 #2900", CreatedAt: k.ms(20 * time.Second)},
		{RowID: 2, TeamID: mkTeam, ID: "r2", Kind: "done", TaskSeq: 6, Summary: "只是 done", CreatedAt: k.ms(21 * time.Second)},
		{RowID: 3, TeamID: mkTeam, ID: "r3", Kind: "merged", TaskSeq: 7, Summary: "刪掉的", CreatedAt: k.ms(22 * time.Second)},
	}
	// the same tick: a task that arrives together with its merged report
	k.feed.set(k.task(8, "同一輪", taskDoing, 25*time.Second, 25*time.Second))
	k.feed.reports = append(k.feed.reports, team.ReportRow{RowID: 4, TeamID: mkTeam, ID: "r4", Kind: "merged", TaskSeq: 8, Summary: "同輪已合併", CreatedAt: k.ms(26 * time.Second)})
	// a task that arrives already deleted, with a merged report in the same tick
	k.feed.set(k.task(9, "同輪就被刪除", taskDeleted, 25*time.Second, 25*time.Second))
	k.feed.reports = append(k.feed.reports, team.ReportRow{RowID: 5, TeamID: mkTeam, ID: "r5", Kind: "merged", TaskSeq: 9, Summary: "刪掉的二", CreatedAt: k.ms(26 * time.Second)})
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	e, ok := k.entry(entryID(mkTeam, 5, "done"))
	if !ok || e.Kind != journal.KindTaskDone || e.Text != "#5 已知的" || e.Detail != "已合併 #2900" {
		t.Fatalf("task_done of the merged task = %+v, %v", e, ok)
	}
	if _, ok := k.entry(entryID(mkTeam, 6, "done")); ok {
		t.Error("a done-kind report wrote a task_done")
	}
	if _, ok := k.entry(entryID(mkTeam, 7, "done")); ok {
		t.Error("a deleted task got a task_done")
	}
	if _, ok := k.entry(entryID(mkTeam, 9, "done")); ok {
		t.Error("a task deleted in the tick of its merged report got a task_done")
	}
	if e, ok := k.entry(entryID(mkTeam, 8, "done")); !ok || e.Text != "#8 同一輪" || e.Detail != "同輪已合併" {
		t.Errorf("task_done of the task that came with its merged report = %+v, %v", e, ok)
	}
	// the completions that follow write nothing more
	k.feed.set(k.task(5, "已知的", taskCompleted, time.Second, 40*time.Second))
	k.feed.set(k.task(8, "同一輪", taskCompleted, 25*time.Second, 41*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	n := 0
	for _, x := range k.entries() {
		if x.Kind == journal.KindTaskDone {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d task_done entries, want 2 (tasks 5 and 8, once each)", n)
	}
}

// A team mirrored here before whose lead goes elsewhere and comes back is caught up again: what changed while it was away is
// read, though the team already has a stored state (the catch-up is not only for a team seen the first time).
// Mutation gate: catch up only a team with no stored state -> the change made while it was away never shows (red).
func TestTaskMirror_ATeamWhoseLeadLeavesAndComesBackIsCaughtUpAgain(t *testing.T) {
	k := newMirrorKit(t)
	k.feed.set(k.task(1, "第一件", taskDoing, -5*time.Hour, -5*time.Hour))
	k.tick() // the first scan: the team is mirrored here
	k.drain()
	if len(k.tasksFile()) != 1 {
		t.Fatalf("tasks file = %+v after the first scan", k.tasksFile())
	}
	k.local[mkLead] = false
	k.feed.set(k.task(2, "離開時新增", taskDoing, -3*time.Hour, -2*time.Hour))
	// a later row of another team: the cursor goes past task 2, and the rewind (10 s) cannot reach it any more
	k.feed.set(team.TaskChange{TeamID: "other", Seq: 1, Subject: "別組", Status: taskDoing, OwnerSessionID: "x", CreatedAt: k.ms(-time.Hour), UpdatedAt: k.ms(-time.Hour)})
	k.now = k.now.Add(mirrorEvery)
	k.tick() // the cursor passes task 2 while the lead is elsewhere
	k.local[mkLead] = true
	k.now = k.now.Add(mirrorEvery)
	k.tick() // no new row of the team
	k.drain()
	if got := len(k.tasksFile()); got != 2 {
		t.Fatalf("the tasks file shows %d tasks, want 2: what changed while the lead was away was lost", got)
	}
}

// A team that was met with its lead elsewhere and is gone from the team module is forgotten, not looked at for ever.
// Mutation gate: keep the marker of a gone team -> it stays set (red).
func TestTaskMirror_ASkippedTeamThatIsGoneIsForgotten(t *testing.T) {
	k := newMirrorKit(t)
	k.feed.teams["gone"] = team.TeamInfo{TeamID: "gone", LeadSessionID: "far-lead"}
	k.feed.set(team.TaskChange{TeamID: "gone", Seq: 1, Subject: "x", Status: taskDoing, OwnerSessionID: "x", CreatedAt: k.ms(-5 * time.Hour), UpdatedAt: k.ms(-5 * time.Hour)})
	k.tick()
	if v, _, _ := k.st.JournalCursor(curSkip + "gone"); v != "1" {
		t.Fatalf("the marker of a team led elsewhere = %q, want it set", v)
	}
	delete(k.feed.teams, "gone")
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	if v, ok, _ := k.st.JournalCursor(curSkip + "gone"); !ok || v != "" {
		t.Fatalf("the marker of a gone team = %q (%v), want it cleared", v, ok)
	}
}

// A closing line whose task_done was edited away is logged and skipped: the row is done, not retried for ever, and it
// does not block the rows behind it.
// Mutation gate: treat a missing entry as a failure -> drain fails, the row stays undone (red).
func TestTaskMirror_AClosingLineForAnEditedAwayEntryIsSkipped(t *testing.T) {
	k := newMirrorKit(t)
	k.finishBaseline()
	k.feed.set(k.task(3, "寫文案", taskDoing, time.Second, time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.feed.set(k.task(3, "寫文案", taskCompleted, time.Second, 11*time.Second))
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	k.drain()
	doneID := entryID(mkTeam, 3, "done")
	files, _ := filepath.Glob(filepath.Join(k.unitDir(), "*.md"))
	for _, f := range files {
		es, err := journal.Parse(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			if e.ID != doneID {
				if err := journal.Upsert(f, e); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	k.feed.reports = append(k.feed.reports, team.ReportRow{RowID: 1, TeamID: mkTeam, ID: "r1", Kind: "merged", TaskSeq: 3, Summary: "已合併", CreatedAt: k.ms(20 * time.Second)})
	k.now = k.now.Add(mirrorEvery)
	k.tick()
	if n := k.drain(); n == 0 {
		t.Fatal("the closing-line row was not drained")
	}
	if n := k.drain(); n != 0 {
		t.Fatalf("a second drain handled %d rows: the row is retried", n)
	}
	if _, ok := k.entry(doneID); ok {
		t.Error("the edited-away entry came back")
	}
	if !strings.Contains(strings.Join(k.logs, "\n"), "not there to receive") {
		t.Errorf("no log line for the skipped closing line: %v", k.logs)
	}
}
