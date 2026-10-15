package workbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wake/purdex/internal/journal"
)

// curTaskRow is the cursor name (+ unit key) of the newest task row whose list the unit's tasks file shows.
const curTaskRow = "taskrow:"

// writeTaskRow writes a `task` row of the team task mirror: the task entries, the closing lines that arrived after their
// entry, and the unit's tasks file. A row belongs to no workbook entry, so it is ready at once; the entries are created
// only when absent (a row replayed after a crash changes nothing, and neither does a hand edit get undone).
func (d *Drainer) writeTaskRow(it OutboxItem) (rowResult, error) {
	var p taskPayload
	if err := json.Unmarshal([]byte(it.Payload), &p); err != nil {
		return rowResult{}, fmt.Errorf("decode outbox row %d: %w", it.ID, err) // stays undone: a binary that can decode it may come back
	}
	reg, err := d.unitFor(it.UnitKey, UnitRef{Key: it.UnitKey, Kind: UnitTeam, TeamID: p.Team, SessionID: p.Lead, Label: p.Label})
	if err != nil {
		return rowResult{}, err
	}
	res := rowResult{unit: reg, what: taskWhat(p)}
	lock := d.projectLock(reg.ProjectID)
	lock.Lock()
	defer lock.Unlock()
	h := unitHome(reg)
	if err := h.verify(); err != nil {
		return rowResult{}, err // the row stays undone, retried and logged
	}
	for _, ev := range p.Events {
		if err := h.putNew(journal.Entry{ID: ev.ID, Kind: journal.Kind(ev.Kind), At: time.UnixMilli(ev.At), Actor: d.actor(ev.Owner),
			Text: ev.Text, Detail: ev.Detail, Refs: ev.Refs}); err != nil {
			return rowResult{}, err
		}
	}
	for _, f := range p.Fills {
		_, err := journal.FillDetail(journal.MonthFile(h.journalDir, time.UnixMilli(f.At)), f.ID, f.Detail)
		switch {
		case err == nil:
		case errors.Is(err, journal.ErrNotFound):
			d.logf("[workbook] journal task: entry %s is not there to receive its closing line", f.ID) // edited away; retrying cannot help
		default:
			return rowResult{}, err
		}
	}
	// A row holds the whole list as of its time, and a delayed row is retried after later ones were written (also after a
	// restart): only a row newer than the last one shown may rewrite the file, or the retry would put an older list back.
	// The newest row written is kept in the store, so the guard survives the process.
	var newest int64
	if raw, ok, err := d.st.JournalCursor(curTaskRow + it.UnitKey); err != nil {
		return rowResult{}, err
	} else if ok {
		newest, _ = strconv.ParseInt(raw, 10, 64)
	}
	if it.ID < newest {
		res.what = slicesWithout(res.what, whatTasks) // the file was not rewritten
		return res, nil
	}
	if err := journal.WriteTasks(h.tasksFile, d.taskFileRows(p)); err != nil {
		return rowResult{}, err
	}
	return res, d.st.SetJournalCursor(curTaskRow+it.UnitKey, strconv.FormatInt(it.ID, 10))
}

// slicesWithout is list without the value v.
func slicesWithout(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// taskFileRows is the mirror's task list as the tasks file shows it.
func (d *Drainer) taskFileRows(p taskPayload) []journal.Task {
	rows := make([]journal.Task, 0, len(p.Tasks))
	for _, r := range p.Tasks {
		st := journal.TaskTodo
		switch r.State {
		case taskDoing:
			st = journal.TaskDoing
		case taskCompleted:
			st = journal.TaskDone
		case taskDeleted:
			st = journal.TaskDropped
		}
		rows = append(rows, journal.Task{Key: fmt.Sprintf("%s-%d", p.Team, r.Seq), Title: r.Title, State: st,
			Owner: d.actor(r.Owner), Refs: refsOf(r.Title, r.PRs)})
	}
	return rows
}
