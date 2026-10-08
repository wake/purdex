package teammod

// The report routes (plan T-1b2): POST /api/team/reports (a member reports on
// its own task) and GET /api/team/reports (the lead of the team, or the
// task's owner). As with the task routes, whatever the caller may not see
// answers exactly what a task that never existed answers, and a member's
// right is checked again inside the store call that writes or reads.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handleReportCreate is POST /api/team/reports: 201 with the stored report,
// the task as it is now and the lead; 200 for a replay (same id, same
// content). A lead reports to nobody: not_member.
func (m *Module) handleReportCreate(w http.ResponseWriter, r *http.Request) {
	var req team.CreateReportRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	c, ok := m.taskCallerOf(w, req.OriginInbox)
	if !ok {
		return
	}
	if c.member == nil {
		m.notMember(w)
		return
	}
	if err := team.ValidReportID(req.ID); err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	if err := team.ValidateReport(req.ReportRequest); err != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
		return
	}
	seq, ok := m.reportTask(w, c, req.Task)
	if !ok {
		return
	}
	// The lookups above are fast fails; the right that counts is read again in
	// the transaction that stores the report.
	row, task, replay, err := m.store.InsertReportByOwner(ReportRow{TeamID: c.team.ID, TaskSeq: seq, ID: req.ID,
		MemberKey: c.ownerKey(), Kind: req.Kind, Summary: req.Summary, Needs: req.Needs, PR: req.PR, Reviews: req.Reviews,
		SHA: req.SHA, Body: req.Body, CreatedAt: m.now()})
	switch {
	case errors.Is(err, ErrReportIDReused):
		m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "report id is already used by a different report", nil)
		return
	case err != nil:
		m.taskStoreErr(w, "report "+req.ID, err)
		return
	}
	v, ok := m.newTaskView(w, c.team.ID, c.ownerKey())
	if !ok {
		return
	}
	out := team.ReportResponse{Report: v.report(row), Task: v.task(task), Lead: m.reportLead(c.team)}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	} else {
		m.logf("[team] report %s %s by %s", row.Kind, out.Report.Task, c.ref())
	}
	m.writeJSON(w, status, out)
}

// reportTask is the seq of the member's task a report is about: the one named,
// else its only in_progress task. A task that is not the member's own is the
// usual task_not_found; none or several in progress is a 400 that names the
// choice, from the member's own open tasks only.
func (m *Module) reportTask(w http.ResponseWriter, c taskCaller, id string) (int, bool) {
	if id != "" {
		row, ok := m.lookupTask(w, c, id)
		return row.Seq, ok
	}
	open, live, err := m.store.ListTasksForOwner(c.team.ID, c.ownerKey(), false)
	if err != nil {
		return 0, m.taskStorageErr(w, "open tasks of "+c.ref(), err)
	}
	if !live { // it lost its right since taskCallerOf: no member any more
		m.notMember(w)
		return 0, false
	}
	slices.SortFunc(open, func(a, b TaskRow) int { return a.Seq - b.Seq })
	var started, names []string
	var startedSeq int
	for _, t := range open {
		names = append(names, team.TaskDisplayID(t.TeamID, t.Seq))
		if t.Status == team.TaskInProgress {
			started = append(started, names[len(names)-1])
			startedSeq = t.Seq
		}
	}
	switch {
	case len(started) == 1:
		if m.afterTaskLookup != nil {
			m.afterTaskLookup()
		}
		return startedSeq, true
	case len(started) > 1:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest,
			fmt.Sprintf("several tasks are in progress: %s; pass task=<id>", strings.Join(started, ", ")), nil)
	case len(names) > 0:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest,
			fmt.Sprintf("no task is in progress; pass task=<id> (open: %s)", strings.Join(names, ", ")), nil)
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "no task is in progress and none is open; pass task=<id>", nil)
	}
	return 0, false
}

// reportLead is the lead a report is addressed to: the live registry entry of
// its session (its ref moves when the lead relays), else the ref recorded on
// the team under this host's alias.
func (m *Module) reportLead(t team.Team) team.ReportLead {
	alias, _ := m.selfHost()
	lead := team.ReportLead{Ref: t.LeadRef, Address: alias + "/" + t.LeadRef}
	if o, ok, err := m.origins.ResolveOriginBySession(t.LeadSessionID); err == nil && ok && o.Ref != "" {
		lead.Ref = o.Ref
		lead.Address = alias + "/" + o.Ref
		if o.Address != "" {
			lead.Address = o.Address
		}
	}
	return lead
}

// handleReportList is GET /api/team/reports?origin_inbox=&task=<id>&since=<ms>:
// the lead lists its team's reports (narrowed to one task by task), a member
// those of one of its own tasks (task is then required). Newest first.
func (m *Module) handleReportList(w http.ResponseWriter, r *http.Request) {
	if true { // STUB (red phase)
		http.Error(w, "stub", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query()
	c, ok := m.taskCallerOf(w, q.Get("origin_inbox"))
	if !ok {
		return
	}
	var since int64
	if s := q.Get("since"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, fmt.Sprintf("since must be a unix time in milliseconds, got %q", s), nil)
			return
		}
		since = n
	}
	id := q.Get("task")
	if id == "" && c.member != nil {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "pass task=<id>", nil)
		return
	}
	seq := 0
	if id != "" {
		row, ok := m.lookupTask(w, c, id)
		if !ok {
			return
		}
		seq = row.Seq
	}
	var rows []ReportRow
	var err error
	if c.member != nil {
		// The lookup above is a fast fail; the read that is answered checks
		// the member's right again in its own transaction.
		var held bool
		if rows, held, err = m.store.ListReportsForOwner(c.team.ID, seq, c.ownerKey(), since, 0); err == nil && !held {
			m.taskNotFound(w)
			return
		}
	} else {
		rows, err = m.store.ListReports(c.team.ID, seq, since, 0)
	}
	if err != nil {
		m.taskStorageErr(w, "list reports", err)
		return
	}
	v, ok := m.newTaskView(w, c.team.ID, c.ownerKey())
	if !ok {
		return
	}
	out := team.ReportList{Reports: make([]team.Report, 0, len(rows))}
	for _, rr := range rows {
		out.Reports = append(out.Reports, v.report(rr))
	}
	m.writeJSON(w, http.StatusOK, out)
}
