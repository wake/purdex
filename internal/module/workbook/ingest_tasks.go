package workbook

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/team"
)

// The team task mirror (workbook journal plan J-1c): every few seconds it reads the tasks and reports the team module
// publishes (team.JournalFeed) and turns what changed into outbox rows of the teams whose lead is on this host, which the
// drainer writes: a task_new / task_done entry per task, a closing line filled in when a report arrives late, and the
// unit's tasks file. Everything it stores is idempotent: a re-read row changes nothing.

const (
	mirrorEvery  = 5 * time.Second
	mirrorPage   = 200
	taskRewind   = 10 * time.Second // each tick reads from the stored cursor minus this (team.JournalFeed: not gap-free)
	deepRewind   = time.Hour        // once an hour from this far back
	deepEvery    = time.Hour
	mirrorWindow = 30 * 24 * time.Hour
	summaryRunes = 200

	curTasks   = "tasks"       // the team.TaskCursor last seen, JSON
	curReports = "reports"     // the last report rowid, decimal
	curReady   = "tasks-ready" // set when the first scan (the baseline) is finished
	curTeam    = "taskmirror:" // + team id: that team's mirror, JSON
	curSkip    = "taskskip:"   // + team id: "1" while the team was met with its lead elsewhere, "" once it is caught up or gone
)

// Task statuses of the team module.
const (
	taskCompleted = "completed"
	taskDeleted   = "deleted"
	taskPending   = "pending"
	taskDoing     = "in_progress"
)

// teamMirror is what the mirror knows of one team: enough to tell what changed, and to write its tasks file.
type teamMirror struct {
	Label string              `json:"label,omitempty"`
	Lead  string              `json:"lead,omitempty"`
	Tasks map[int]*mirrorTask `json:"tasks"`

	fresh bool // no state was stored when this tick loaded it: not saved
}

type mirrorTask struct {
	St      string `json:"st,omitempty"` // the last status seen; "" while only a closing line is known
	Title   string `json:"title,omitempty"`
	Owner   string `json:"owner,omitempty"` // the owner's session id
	PRs     []int  `json:"prs,omitempty"`
	Updated int64  `json:"upd,omitempty"`
	Sum     string `json:"sum,omitempty"`  // the closing line, from a merged / done report
	New     bool   `json:"new,omitempty"`  // its task_new is queued
	Done    bool   `json:"done,omitempty"` // its task_done is queued
	DoneAt  int64  `json:"dat,omitempty"`
	DoneSum bool   `json:"dsum,omitempty"` // the queued task_done has its closing line (or never will: a baseline task)
	Merged  bool   `json:"mg,omitempty"`   // a merged report named it: its task_done does not wait for the completion
	MergedA int64  `json:"mga,omitempty"`  // when that report was made
}

// taskPayload is a `task` outbox row.
type taskPayload struct {
	Team   string      `json:"team"`
	Label  string      `json:"label,omitempty"`
	Lead   string      `json:"lead,omitempty"` // the lead's session now: resolves the unit's project on its first write
	Events []taskEvent `json:"events,omitempty"`
	Fills  []taskFill  `json:"fills,omitempty"`
	Tasks  []taskRow   `json:"tasks"` // the whole list the tasks file shows
}

type taskEvent struct {
	ID, Kind string
	At       int64
	Owner    string // session id; the drainer writes its display name
	Text     string
	Detail   string
	Refs     []string
}

type taskFill struct {
	ID     string
	At     int64
	Detail string
}

type taskRow struct {
	Seq   int
	Title string
	State string // the team module's status
	Owner string
	PRs   []int
}

// mirrorDelta is what observing rows of one team produced.
type mirrorDelta struct {
	events  []taskEvent
	fills   []taskFill
	changed bool // the mirror changed: its state and tasks file must be stored again
}

var issueRef = regexp.MustCompile(`#(\d{3,})`)

func (tm *teamMirror) task(seq int) *mirrorTask {
	if tm.Tasks == nil {
		tm.Tasks = map[int]*mirrorTask{}
	}
	t := tm.Tasks[seq]
	if t == nil {
		t = &mirrorTask{}
		tm.Tasks[seq] = t
	}
	return t
}

func taskText(seq int, title string) string { return fmt.Sprintf("#%d %s", seq, title) }

func entryID(teamID string, seq int, suffix string) string {
	return fmt.Sprintf("task:%s:%d:%s", teamID, seq, suffix)
}

// refsOf is the issue and pull-request numbers a task names: `#1234` in its subject, `PR #1234` from its metadata.
func refsOf(title string, prs []int) []string {
	var refs []string
	for _, m := range issueRef.FindAllStringSubmatch(title, -1) {
		if r := "#" + m[1]; !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}
	for _, p := range prs {
		if r := "PR #" + strconv.Itoa(p); !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}
	return refs
}

// observeTask folds one task as the team module has it now into the mirror. A task seen for the first time queues its
// task_new, and its task_done too when it is complete already; one that becomes complete queues its task_done once. In a
// baseline (the first scan) nothing is queued: the task is only recorded, so history is not written.
func (tm *teamMirror) observeTask(teamID string, c team.TaskChange, baseline bool, d *mirrorDelta) {
	t := tm.task(c.Seq)
	if t.St != c.Status || t.Title != c.Subject || t.Owner != c.OwnerSessionID || !slices.Equal(t.PRs, c.MetadataPRs) || t.Updated != c.UpdatedAt {
		t.St, t.Title, t.Owner, t.PRs, t.Updated = c.Status, c.Subject, c.OwnerSessionID, slices.Clone(c.MetadataPRs), c.UpdatedAt
		d.changed = true
	}
	if !t.New {
		t.New, d.changed = true, true
		if !baseline {
			d.events = append(d.events, taskEvent{ID: entryID(teamID, c.Seq, "new"), Kind: string(journal.KindTaskNew), At: c.CreatedAt,
				Owner: c.OwnerSessionID, Text: taskText(c.Seq, c.Subject), Refs: refsOf(c.Subject, c.MetadataPRs)})
		}
	}
	if (c.Status == taskCompleted || t.Merged) && c.Status != taskDeleted && !t.Done {
		t.Done, t.DoneAt, d.changed = true, c.UpdatedAt, true
		t.DoneSum = t.Sum != "" || baseline
		if !baseline {
			d.events = append(d.events, taskEvent{ID: entryID(teamID, c.Seq, "done"), Kind: string(journal.KindTaskDone), At: c.UpdatedAt,
				Owner: c.OwnerSessionID, Text: taskText(c.Seq, c.Subject), Detail: t.Sum, Refs: refsOf(c.Subject, c.MetadataPRs)})
		}
	}
}

// observeReport records the closing line of a task a merged or done report names. The line goes into the task_done when it
// is queued after this; when the task_done was queued without one, it is filled in (only while the entry has none). A
// merged report is a completion too (spec §6), but it only marks the task here: the reports of a tick are read before its
// tasks, so completeMerged queues the task_done after the tasks of the tick are read (a task deleted in the same tick gets
// none), and observeTask does it for a task seen after its report. The completion that follows finds it written.
func (tm *teamMirror) observeReport(teamID string, r team.ReportRow, d *mirrorDelta) {
	if r.TaskSeq <= 0 || (r.Kind != "merged" && r.Kind != "done") {
		return
	}
	t := tm.task(r.TaskSeq)
	if r.Kind == "merged" && !t.Merged {
		t.Merged, t.MergedA, d.changed = true, r.CreatedAt, true
	}
	if sum := summaryLine(r.Summary); sum != "" && t.Sum == "" {
		t.Sum, d.changed = sum, true
		if t.Done && !t.DoneSum {
			t.DoneSum = true
			d.fills = append(d.fills, taskFill{ID: entryID(teamID, r.TaskSeq, "done"), At: t.DoneAt, Detail: sum})
		}
	}
}

// completeMerged queues the task_done of every task of the teams loaded in this tick that a merged report named and that is
// known, not finished and not deleted, from the state the tick ended with.
func (tk *tick) completeMerged() error {
	if tk.baseline {
		return nil // the first scan records, it writes no history
	}
	ids := make([]string, 0, len(tk.teams))
	for id := range tk.teams {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	deltas := map[string]*mirrorDelta{}
	for _, id := range ids {
		tm := tk.teams[id]
		seqs := make([]int, 0, len(tm.Tasks))
		for seq := range tm.Tasks {
			seqs = append(seqs, seq)
		}
		sort.Ints(seqs)
		for _, seq := range seqs {
			t := tm.Tasks[seq]
			if !t.Merged || t.Done || t.St == "" || t.St == taskDeleted {
				continue
			}
			d := deltas[id]
			if d == nil {
				d = &mirrorDelta{}
				deltas[id] = d
			}
			t.Done, t.DoneAt, t.DoneSum, d.changed = true, t.MergedA, t.Sum != "", true
			d.events = append(d.events, taskEvent{ID: entryID(id, seq, "done"), Kind: string(journal.KindTaskDone), At: t.MergedA,
				Owner: t.Owner, Text: taskText(seq, t.Title), Detail: t.Sum, Refs: refsOf(t.Title, t.PRs)})
		}
	}
	if len(deltas) == 0 {
		return nil
	}
	return tk.commit(deltas, map[string]string{})
}

// summaryLine is a report's summary as a closing line: one line, trimmed, cut to summaryRunes.
func summaryLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > summaryRunes {
		s = string([]rune(s)[:summaryRunes])
	}
	return s
}

// rows is the list the unit's tasks file shows: the open tasks and those finished (or dropped) in the last 30 days.
func (tm *teamMirror) rows(now time.Time) []taskRow {
	cutoff := now.Add(-mirrorWindow).UnixMilli()
	seqs := make([]int, 0, len(tm.Tasks))
	for s := range tm.Tasks {
		seqs = append(seqs, s)
	}
	sort.Ints(seqs)
	var out []taskRow
	for _, s := range seqs {
		t := tm.Tasks[s]
		switch {
		case t.St == "": // only a closing line is known; the task itself has not been seen
			continue
		case (t.St == taskCompleted || t.St == taskDeleted) && t.Updated < cutoff:
			continue
		}
		out = append(out, taskRow{Seq: s, Title: t.Title, State: t.St, Owner: t.Owner, PRs: t.PRs})
	}
	return out
}

// TaskMirror reads the team feed and queues the journal's task rows.
type TaskMirror struct {
	st    *Store
	feed  team.JournalFeed
	local func(leadSessionID string) bool // is the session on this host
	kick  func()                          // asks the drainer for a pass; may be nil
	logf  func(string, ...any)
	now   func() time.Time

	deepAt  time.Time
	lastLog time.Time
}

// NewTaskMirror makes the mirror. local says whether a lead's session is on this host (only those teams are mirrored).
func NewTaskMirror(st *Store, feed team.JournalFeed, local func(leadSessionID string) bool, kick func(), logf func(string, ...any)) *TaskMirror {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &TaskMirror{st: st, feed: feed, local: local, kick: kick, logf: logf, now: time.Now}
}

// Run ticks until ctx ends; a failing tick is logged at most once an hour.
func (m *TaskMirror) Run(ctx context.Context) {
	t := time.NewTicker(mirrorEvery)
	defer t.Stop()
	for {
		if err := m.Tick(ctx); err != nil && ctx.Err() == nil && m.now().Sub(m.lastLog) >= time.Hour {
			m.lastLog = m.now()
			m.logf("[workbook] journal task mirror: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick is the state of one Tick: the teams it loaded, and what the baseline decides.
type tick struct {
	m        *TaskMirror
	now      time.Time
	baseline bool
	teams    map[string]*teamMirror
	infos    map[string]*mirrorTeam
	skips    map[string]bool // teams met with their lead elsewhere, to be stored with the next page
}

// mirrorTeam is a team the tick looked up: ok when its lead is on this host (only those are mirrored).
type mirrorTeam struct {
	info  team.TeamInfo
	found bool // the team module knows the team
	ok    bool // mirrored: its lead is on this host
}

// Tick runs one pass: the reports first (a closing line is known before the task that needs it), then the tasks from the
// stored cursor rewound as the feed's contract says; each page is stored in one transaction with its cursor.
func (m *TaskMirror) Tick(ctx context.Context) error {
	now := m.now()
	ready, _, err := m.st.JournalCursor(curReady)
	if err != nil {
		return err
	}
	tk := &tick{m: m, now: now, baseline: ready == "", teams: map[string]*teamMirror{}, infos: map[string]*mirrorTeam{}, skips: map[string]bool{}}
	if err := tk.reports(ctx); err != nil {
		return err
	}
	if err := tk.tasks(ctx); err != nil {
		return err
	}
	if err := tk.completeMerged(); err != nil {
		return err
	}
	return tk.recheck(ctx)
}

func (tk *tick) lead(teamID string) (*mirrorTeam, error) {
	if x, ok := tk.infos[teamID]; ok {
		return x, nil
	}
	info, found, err := tk.m.feed.TeamInfo(teamID)
	if err != nil {
		return nil, err
	}
	x := &mirrorTeam{info: info, found: found, ok: found && info.LeadSessionID != "" && tk.m.local(info.LeadSessionID)}
	tk.infos[teamID] = x
	return x, nil
}

func (tk *tick) load(teamID string) (*teamMirror, error) {
	if tm, ok := tk.teams[teamID]; ok {
		return tm, nil
	}
	tm := &teamMirror{}
	if raw, ok, err := tk.m.st.JournalCursor(curTeam + teamID); err != nil {
		return nil, err
	} else if ok {
		if err := json.Unmarshal([]byte(raw), tm); err != nil {
			return nil, fmt.Errorf("task mirror state of team %s: %w", teamID, err)
		}
	} else {
		tm.fresh = true
	}
	tk.teams[teamID] = tm
	return tm, nil
}

// adopt catches up a team the mirror meets for the first time after the baseline. The cursors passed its tasks and
// reports while its lead was elsewhere, and the rewinds cannot reach them any more, so they are read again from the
// start. A task changed within the last hour is observed as new activity (it gets its entries, as the rewind would have
// given them); an older one is only recorded, as in the baseline, so no history is written.
func (tk *tick) adopt(ctx context.Context, teamID string, tm *teamMirror, d *mirrorDelta) error {
	var after int64
	since := tk.now.Add(-mirrorWindow).UnixMilli()
	for ctx.Err() == nil { // the reports first: a closing line is known before the task that needs it
		rows, err := tk.m.feed.ReportsAfter(after, since, mirrorPage)
		if err != nil {
			return err
		}
		for _, r := range rows {
			after = r.RowID
			if r.TeamID == teamID {
				tm.observeReport(teamID, r, d)
			}
		}
		if len(rows) < mirrorPage {
			break
		}
	}
	recent := tk.now.Add(-deepRewind).UnixMilli()
	var read team.TaskCursor
	for ctx.Err() == nil {
		rows, err := tk.m.feed.TasksChangedAfter(read, mirrorPage)
		if err != nil {
			return err
		}
		for _, c := range rows {
			if c.TeamID == teamID {
				tm.observeTask(teamID, c, c.UpdatedAt < recent, d)
			}
		}
		if len(rows) < mirrorPage {
			break
		}
		l := rows[len(rows)-1]
		read = team.TaskCursor{UpdatedAt: l.UpdatedAt, TeamID: l.TeamID, Seq: l.Seq}
	}
	return ctx.Err()
}

// skip remembers a team met while its lead is elsewhere. The cursors pass its rows, and it may have no more rows for a
// long time (a daemon that starts before its sessions are registered sees every lead as elsewhere), so recheck looks at it
// again every tick instead of waiting for a row.
func (tk *tick) skip(teamID string, x *mirrorTeam) {
	if x.found {
		tk.skips[teamID] = true
	}
}

// recheck looks again at the teams met while their lead was elsewhere: one whose lead is on this host now is caught up (it
// needs no new row of its own), one the team module does not know any more is forgotten.
func (tk *tick) recheck(ctx context.Context) error {
	if tk.baseline {
		return nil // the first scan is itself the catch-up of every team that is here
	}
	marks, err := tk.m.st.JournalCursors(curSkip)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(marks))
	for id, v := range marks {
		if v == "1" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	deltas := map[string]*mirrorDelta{}
	cursors := map[string]string{}
	for _, id := range ids {
		x, err := tk.lead(id)
		if err != nil {
			return err
		}
		switch {
		case !x.found:
			cursors[curSkip+id] = ""
		case x.ok:
			tm, err := tk.load(id)
			if err != nil {
				return err
			}
			tm.fresh = true // also a team mirrored here before: what changed while its lead was away is read again
			d := &mirrorDelta{}
			if err := tk.catchUp(ctx, id, tm, d); err != nil {
				return err
			}
			deltas[id] = d
			cursors[curSkip+id] = ""
		}
	}
	if len(cursors) == 0 {
		return nil
	}
	return tk.commit(deltas, cursors)
}

// catchUp runs adopt for a team met for the first time after the baseline (in the baseline itself the scan is the catch-up).
func (tk *tick) catchUp(ctx context.Context, teamID string, tm *teamMirror, d *mirrorDelta) error {
	if !tm.fresh {
		return nil
	}
	tm.fresh = false
	if tk.baseline {
		return nil
	}
	return tk.adopt(ctx, teamID, tm, d)
}

// reports reads the new reports and records the closing lines.
func (tk *tick) reports(ctx context.Context) error {
	raw, _, err := tk.m.st.JournalCursor(curReports)
	if err != nil {
		return err
	}
	after, _ := strconv.ParseInt(raw, 10, 64)
	var since int64
	if tk.baseline {
		since = tk.now.Add(-mirrorWindow).UnixMilli() // the first scan reads only the last 30 days
	}
	for ctx.Err() == nil {
		rows, err := tk.m.feed.ReportsAfter(after, since, mirrorPage)
		if err != nil {
			return err
		}
		deltas := map[string]*mirrorDelta{}
		for _, r := range rows {
			after = r.RowID
			x, err := tk.lead(r.TeamID)
			if err != nil {
				return err
			}
			if !x.ok {
				tk.skip(r.TeamID, x)
				continue
			}
			tm, err := tk.load(r.TeamID)
			if err != nil {
				return err
			}
			d := deltas[r.TeamID]
			if d == nil {
				d = &mirrorDelta{}
				deltas[r.TeamID] = d
			}
			if err := tk.catchUp(ctx, r.TeamID, tm, d); err != nil {
				return err
			}
			tm.observeReport(r.TeamID, r, d)
		}
		if len(rows) > 0 {
			if err := tk.commit(deltas, map[string]string{curReports: strconv.FormatInt(after, 10)}); err != nil {
				return err
			}
		}
		if len(rows) < mirrorPage {
			return nil
		}
	}
	return ctx.Err()
}

// tasks reads the changed tasks, from the stored cursor rewound (the feed is not gap-free): 10 s back every tick, an hour
// back once an hour and at the first tick after a start.
func (tk *tick) tasks(ctx context.Context) error {
	var stored team.TaskCursor
	if raw, ok, err := tk.m.st.JournalCursor(curTasks); err != nil {
		return err
	} else if ok {
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return fmt.Errorf("task mirror cursor: %w", err)
		}
	}
	back := taskRewind
	if tk.m.deepAt.IsZero() || tk.now.Sub(tk.m.deepAt) >= deepEvery {
		back = deepRewind
	}
	read := team.TaskCursor{UpdatedAt: max(0, stored.UpdatedAt-back.Milliseconds())}
	for ctx.Err() == nil {
		rows, err := tk.m.feed.TasksChangedAfter(read, mirrorPage)
		if err != nil {
			return err
		}
		deltas := map[string]*mirrorDelta{}
		for _, c := range rows {
			x, err := tk.lead(c.TeamID)
			if err != nil {
				return err
			}
			if !x.ok {
				tk.skip(c.TeamID, x)
				continue
			}
			tm, err := tk.load(c.TeamID)
			if err != nil {
				return err
			}
			d := deltas[c.TeamID]
			if d == nil {
				d = &mirrorDelta{}
				deltas[c.TeamID] = d
			}
			if err := tk.catchUp(ctx, c.TeamID, tm, d); err != nil {
				return err
			}
			tm.observeTask(c.TeamID, c, tk.baseline, d)
		}
		last := len(rows) == 0 || len(rows) < mirrorPage
		cur := map[string]string{}
		if len(rows) > 0 {
			l := rows[len(rows)-1]
			read = team.TaskCursor{UpdatedAt: l.UpdatedAt, TeamID: l.TeamID, Seq: l.Seq}
			if after(read, stored) {
				stored = read
			}
		}
		b, _ := json.Marshal(stored)
		cur[curTasks] = string(b)
		if last && tk.baseline {
			cur[curReady] = "1" // the first scan is complete; from the next tick on, changes are written as entries
		}
		if err := tk.commit(deltas, cur); err != nil {
			return err
		}
		if last {
			if back == deepRewind {
				tk.m.deepAt = tk.now
			}
			return nil
		}
	}
	return ctx.Err()
}

// after reports whether a sorts after b in the feed's order.
func after(a, b team.TaskCursor) bool {
	if a.UpdatedAt != b.UpdatedAt {
		return a.UpdatedAt > b.UpdatedAt
	}
	if a.TeamID != b.TeamID {
		return a.TeamID > b.TeamID
	}
	return a.Seq > b.Seq
}

// commit stores one page: for every team whose mirror changed or that produced entries, its outbox row and its new state,
// and the cursors, in one transaction; then asks the drainer for a pass.
func (tk *tick) commit(deltas map[string]*mirrorDelta, cursors map[string]string) error {
	page := TaskPage{Cursors: cursors}
	for id := range tk.skips {
		if _, ok := cursors[curSkip+id]; !ok {
			cursors[curSkip+id] = "1"
		}
	}
	ids := make([]string, 0, len(deltas))
	for id := range deltas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	queued := false
	for _, id := range ids {
		d, tm := deltas[id], tk.teams[id]
		if !d.changed && len(d.events) == 0 && len(d.fills) == 0 {
			continue
		}
		x := tk.infos[id]
		tm.Label, tm.Lead = x.info.Label, x.info.LeadSessionID
		b, err := json.Marshal(tm)
		if err != nil {
			return err
		}
		page.Cursors[curTeam+id] = string(b)
		page.Rows = append(page.Rows, TaskOutboxRow{UnitKey: UnitTeam + ":" + id, Payload: taskPayload{
			Team: id, Label: tm.Label, Lead: tm.Lead, Events: d.events, Fills: d.fills, Tasks: tm.rows(tk.now)}})
		queued = true
	}
	if err := tk.m.st.ApplyTaskPage(page); err != nil {
		return err
	}
	tk.skips = map[string]bool{}
	if queued && tk.m.kick != nil {
		tk.m.kick()
	}
	return nil
}
