package workbook

// What the journal tells the App after a write (the journal.changed event): the drainer reports, for every outbox row it has
// written and marked, which unit it wrote to and which of the unit's resources it touched. One event goes out per resource,
// after the row is done, so a row that failed, or whose mark failed and will be written again, announces nothing until it is.

// The resources a write can change; the App re-reads only the ones named.
const (
	whatEntries = "entries"
	whatTasks   = "tasks"
	whatRoadmap = "roadmap"
)

// rowResult is what writing one outbox row changed: the unit it wrote to and the resources it touched.
type rowResult struct {
	unit UnitRow
	what []string
}

// SetNotify sets what the drainer calls for each resource a done row changed (nil: nothing). Call it before Run.
func (d *Drainer) SetNotify(fn func(JournalChange)) { d.notify = fn }

// announce reports a done row to the notification.
func (d *Drainer) announce(r rowResult) {
	if d.notify == nil {
		return
	}
	for _, w := range r.what {
		d.notify(JournalChange{UnitKey: r.unit.Key, ProjectID: r.unit.ProjectID, What: w})
	}
}

// turnWhat is what a turn row wrote (see writeTurn): entries when a solo todo became a task entry or an ok entry wrote its
// exec line, the tasks file when a solo conversation's todos changed.
func turnWhat(solo bool, ent Entry, p turnPayload) []string {
	var what []string
	if solo {
		for _, t := range p.Todos {
			if t.State == TodoOpen || t.State == TodoDone {
				what = append(what, whatEntries)
				break
			}
		}
	}
	if len(what) == 0 && ent.Kind == KindTurn && ent.State == StateOK && ent.Entry != "" {
		what = append(what, whatEntries)
	}
	if solo && len(p.Todos) > 0 {
		what = append(what, whatTasks)
	}
	return what
}

// journalWhat is what a journal row wrote (see writeJournal): entries for events and confirm ticks, the roadmap for statuses.
func journalWhat(p journalPayload) []string {
	var what []string
	if len(p.Events) > 0 || len(p.Ticks) > 0 {
		what = append(what, whatEntries)
	}
	if len(p.Roadmap) > 0 {
		what = append(what, whatRoadmap)
	}
	return what
}

// taskWhat is what a task row wrote (see writeTaskRow): the tasks file every time, entries when it carries events or
// closing lines.
func taskWhat(p taskPayload) []string {
	what := []string{}
	if len(p.Events) > 0 || len(p.Fills) > 0 {
		what = append(what, whatEntries)
	}
	return append(what, whatTasks)
}
