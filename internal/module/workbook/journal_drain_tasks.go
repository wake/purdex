package workbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/journal"
)

// writeTaskRow writes a `task` row of the team task mirror: the task entries, the closing lines that arrived after their
// entry, and the unit's tasks file. A row belongs to no workbook entry, so it is ready at once; the entries are created
// only when absent (a row replayed after a crash changes nothing, and neither does a hand edit get undone).
func (d *Drainer) writeTaskRow(it OutboxItem) error {
	var p taskPayload
	if err := json.Unmarshal([]byte(it.Payload), &p); err != nil {
		return fmt.Errorf("decode outbox row %d: %w", it.ID, err) // stays undone: a binary that can decode it may come back
	}
	reg, err := d.unitFor(it.UnitKey, UnitRef{Key: it.UnitKey, Kind: UnitTeam, TeamID: p.Team, SessionID: p.Lead, Label: p.Label})
	if err != nil {
		return err
	}
	lock := d.projectLock(reg.ProjectID)
	lock.Lock()
	defer lock.Unlock()
	slug := unitSlug(reg.Label, reg.Key)
	h := home{
		journalDir: filepath.Join(reg.WtPath, ".purdex", "journal", slug),
		tasksFile:  filepath.Join(reg.WtPath, ".purdex", "tasks", slug+".md"),
	}
	for _, ev := range p.Events {
		if err := h.putNew(journal.Entry{ID: ev.ID, Kind: journal.Kind(ev.Kind), At: time.UnixMilli(ev.At), Actor: d.actor(ev.Owner),
			Text: ev.Text, Detail: ev.Detail, Refs: ev.Refs}); err != nil {
			return err
		}
	}
	for _, f := range p.Fills {
		_, err := journal.FillDetail(journal.MonthFile(h.journalDir, time.UnixMilli(f.At)), f.ID, f.Detail)
		switch {
		case err == nil:
		case errors.Is(err, journal.ErrNotFound):
			d.logf("[workbook] journal task: entry %s is not there to receive its closing line", f.ID) // edited away; retrying cannot help
		default:
			return err
		}
	}
	return journal.WriteTasks(h.tasksFile, d.taskFileRows(p))
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
