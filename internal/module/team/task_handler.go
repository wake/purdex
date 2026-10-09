package teammod

// The task routes (plan T-1b1): POST/GET /api/team/tasks, GET
// /api/team/tasks/{id}, POST /api/team/tasks/{id}/status and /reassign. A lead
// sees and changes every task of its team; a member only its own. What a
// caller may not see answers exactly what a task that never existed answers
// (task_not_found, one detail text), so existence never leaks.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/wake/purdex/internal/team"
)

// taskNotFoundDetail is the one text every task_not_found carries.
const taskNotFoundDetail = "no such task"

// taskRefusals maps the store's task errors to their 409 codes.
var taskRefusals = []struct {
	err  error
	code string
}{
	{ErrTaskNotFound, team.ErrTaskNotFound},
	{ErrBadTaskTransition, team.ErrBadTaskTransition},
	{ErrBlockedByUnknown, team.ErrBlockedByUnknown},
	{ErrBlockedByCycle, team.ErrBlockedByCycle},
	{ErrOwnerNotActive, team.ErrOwnerNotActive},
}

// taskCaller is the session behind a task request: a live team's lead
// (member nil) or one of its active members.
type taskCaller struct {
	team   team.Team
	member *memberRow
}

// ownerKey is the member key the store must check, "" for a lead.
func (c taskCaller) ownerKey() string {
	if c.member != nil {
		return c.member.SpawnOp
	}
	return ""
}

// ref is the caller's current ref, for the log.
func (c taskCaller) ref() string {
	if c.member != nil {
		return c.member.Ref
	}
	return c.team.LeadRef
}

// taskCallerOf resolves a lead-or-member route's caller: the team its
// session leads, else the live team it is an active member of, else 409
// not_member. Lead-only routes use callerTeam (not_lead) instead.
func (m *Module) taskCallerOf(w http.ResponseWriter, inbox string) (taskCaller, bool) {
	origin, ok := m.callerOrigin(w, inbox)
	if !ok {
		return taskCaller{}, false
	}
	t, lead, err := m.store.LiveTeamByLead(origin.SessionID)
	if err != nil {
		return taskCaller{}, m.taskStorageErr(w, "team of "+origin.SessionID, err)
	}
	if lead {
		return taskCaller{team: t}, true
	}
	mr, t, member, err := m.store.ActiveMemberInLiveTeam(origin.SessionID)
	if err != nil {
		return taskCaller{}, m.taskStorageErr(w, "member "+origin.SessionID, err)
	}
	if !member {
		m.notMember(w)
		return taskCaller{}, false
	}
	return taskCaller{team: t, member: &mr}, true
}

// notMember is the one answer for a session that is not (or no longer) a
// live team's lead or active member.
func (m *Module) notMember(w http.ResponseWriter) {
	m.writeErr(w, http.StatusConflict, team.ErrNotMember, "this session neither leads a live team nor is an active member of one", nil)
}

// taskStorageErr logs err and answers 500; it always returns false.
func (m *Module) taskStorageErr(w http.ResponseWriter, what string, err error) bool {
	m.logf("[team] task %s: %v", what, err)
	m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	return false
}

// taskStoreErr answers a store error: a task refusal as its 409, else 500.
func (m *Module) taskStoreErr(w http.ResponseWriter, what string, err error) {
	for _, r := range taskRefusals {
		if errors.Is(err, r.err) {
			detail := err.Error()
			if r.code == team.ErrTaskNotFound {
				detail = taskNotFoundDetail
			}
			m.writeErr(w, http.StatusConflict, r.code, detail, nil)
			return
		}
	}
	m.taskStorageErr(w, what, err)
}

// targetMember is the member of t that target names (matchMember), or an
// error written: 503 / 500 / 409 not_your_member.
func (m *Module) targetMember(w http.ResponseWriter, t team.Team, target string) (memberRow, bool) {
	mr, found, err := m.matchMember(t, target)
	switch {
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
	case err != nil:
		return memberRow{}, m.taskStorageErr(w, fmt.Sprintf("member %q", target), err)
	case !found:
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, fmt.Sprintf("%q is no member of team %s", target, t.ID), nil)
	default:
		return mr, true
	}
	return memberRow{}, false
}

// taskNotFound is the one answer for every task the caller may not see.
func (m *Module) taskNotFound(w http.ResponseWriter) {
	m.writeErr(w, http.StatusConflict, team.ErrTaskNotFound, taskNotFoundDetail, nil)
}

// lookupTask is the task id names in the caller's scope: looked up with the
// caller's team id, and for a member only if it owns it. Anything else,
// whatever the reason, is the same task_not_found.
func (m *Module) lookupTask(w http.ResponseWriter, c taskCaller, id string) (TaskRow, bool) {
	notFound := func() (TaskRow, bool) {
		m.taskNotFound(w)
		return TaskRow{}, false
	}
	seq, ok := team.ParseTaskID(id, c.team.ID)
	if !ok {
		return notFound()
	}
	row, found, err := m.store.GetTask(c.team.ID, seq)
	if err != nil {
		return TaskRow{}, m.taskStorageErr(w, "get "+id, err)
	}
	if !found || (c.member != nil && row.OwnerKey != c.member.SpawnOp) {
		return notFound()
	}
	if m.afterTaskLookup != nil {
		m.afterTaskLookup()
	}
	return row, true
}

// handleTaskCreate is POST /api/team/tasks (lead): a new pending task for
// one of the lead's active members.
func (m *Module) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	var req team.CreateTaskRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	t, ok := m.callerTeam(w, req.OriginInbox)
	if !ok {
		return
	}
	for _, err := range []error{team.ValidTaskSubject(req.Subject), team.ValidTaskDescription(req.Description), team.ValidDoneWhen(req.DoneWhen)} {
		if err != nil {
			m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
			return
		}
	}
	owner, ok := m.targetMember(w, t, req.To)
	if !ok {
		return
	}
	blockedBy := make([]int, 0, len(req.BlockedBy))
	for _, id := range req.BlockedBy {
		seq, ok := team.ParseTaskID(id, t.ID)
		if !ok {
			m.writeErr(w, http.StatusConflict, team.ErrBlockedByUnknown, fmt.Sprintf("blocked_by %q is no task of this team", id), nil)
			return
		}
		blockedBy = append(blockedBy, seq)
	}
	now := m.now()
	row, err := m.store.CreateTask(TaskRow{TeamID: t.ID, Subject: req.Subject, Description: req.Description, DoneWhen: req.DoneWhen,
		OwnerKey: owner.SpawnOp, BlockedBy: blockedBy, CreatedByRef: t.LeadRef, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		m.taskStoreErr(w, "create", err)
		return
	}
	m.respondTask(w, http.StatusCreated, taskCaller{team: t}, row, "create")
}

// handleTaskList is GET /api/team/tasks?origin_inbox=&member=<ref>&all=1: a
// lead lists its team's tasks (narrowed to one member by member), a member
// its own. mine=1 is a member's only: a lead is answered not_member (T-2).
// Finished tasks are hidden unless all=1.
func (m *Module) handleTaskList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if m.forwardAsRemoteMember(w, q.Get("origin_inbox"), http.MethodGet, "/api/team/tasks", q, nil, true, "") {
		return
	}
	c, ok := m.taskCallerOf(w, q.Get("origin_inbox"))
	if !ok {
		return
	}
	m.listTasksAs(w, c, q)
}

// listTasksAs is the task list for the caller c (see createReportAs): its own query decides member / mine / all.
func (m *Module) listTasksAs(w http.ResponseWriter, c taskCaller, q url.Values) {
	owner := ""
	switch target := q.Get("member"); {
	case q.Get("mine") == "1" && c.member == nil:
		// `pdx task mine` is a member's: a lead asking is not one (T-2).
		m.notMember(w)
		return
	case c.member != nil && target != "":
		m.writeErr(w, http.StatusConflict, team.ErrNotLead, "only a lead filters by member", nil)
		return
	case c.member != nil:
		owner = c.member.SpawnOp
	case target != "":
		mr, ok := m.targetMember(w, c.team, target)
		if !ok {
			return
		}
		owner = mr.SpawnOp
	}
	if m.afterTaskLookup != nil {
		m.afterTaskLookup()
	}
	all := q.Get("all") == "1" || q.Get("all") == "true"
	var rows []TaskRow
	var err error
	if c.member != nil {
		// The member's right is checked again in the read; a member that lost
		// it meanwhile is no member, as taskCallerOf would have answered.
		var live bool
		if rows, live, err = m.store.ListTasksForOwner(c.team.ID, owner, all); err == nil && !live {
			m.notMember(w)
			return
		}
	} else {
		rows, err = m.store.ListTasks(c.team.ID, owner, all)
	}
	if err != nil {
		m.taskStorageErr(w, "list", err)
		return
	}
	v, ok := m.newTaskView(w, c.team.ID, c.ownerKey())
	if !ok {
		return
	}
	out := team.TaskList{Tasks: make([]team.Task, 0, len(rows))}
	for _, row := range rows {
		out.Tasks = append(out.Tasks, v.task(row))
	}
	m.writeJSON(w, http.StatusOK, out)
}

// handleTaskGet is GET /api/team/tasks/{id}?origin_inbox=: the task and its
// reports, newest first.
func (m *Module) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	c, ok := m.taskCallerOf(w, r.URL.Query().Get("origin_inbox"))
	if !ok {
		return
	}
	row, ok := m.lookupTask(w, c, r.PathValue("id"))
	if !ok {
		return
	}
	// The lookup above is a fast fail; the read that is answered is this one,
	// which checks a member's right again in the transaction that reads the
	// task and its reports.
	row, reports, found, err := m.store.GetTaskDetail(c.team.ID, row.Seq, c.ownerKey())
	if err != nil {
		m.taskStorageErr(w, "detail of "+r.PathValue("id"), err)
		return
	}
	if !found {
		m.taskNotFound(w)
		return
	}
	v, ok := m.newTaskView(w, c.team.ID, c.ownerKey())
	if !ok {
		return
	}
	out := team.TaskDetail{Task: v.task(row), Reports: make([]team.Report, 0, len(reports))}
	for _, rr := range reports {
		out.Reports = append(out.Reports, v.report(rr))
	}
	m.writeJSON(w, http.StatusOK, out)
}

// handleTaskStatus is POST /api/team/tasks/{id}/status: the lead makes any
// move the table allows, the owner starts and completes (the table says
// which).
func (m *Module) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	var req team.TaskStatusRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if m.forwardAsRemoteMember(w, req.OriginInbox, http.MethodPost, "/api/team/tasks/"+url.PathEscape(r.PathValue("id"))+"/status", nil, map[string]any{"status": req.Status}, false, "look at pdx task mine before changing it again") {
		return
	}
	c, ok := m.taskCallerOf(w, req.OriginInbox)
	if !ok {
		return
	}
	m.setTaskStatusAs(w, c, r.PathValue("id"), req)
}

// setTaskStatusAs is the status change of task id for the caller c (see createReportAs).
func (m *Module) setTaskStatusAs(w http.ResponseWriter, c taskCaller, id string, req team.TaskStatusRequest) {
	if !slices.Contains([]team.TaskStatus{team.TaskPending, team.TaskInProgress, team.TaskCompleted, team.TaskDeleted}, req.Status) {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, fmt.Sprintf("status must be pending, in_progress, completed or deleted, got %q", string(req.Status)), nil)
		return
	}
	row, ok := m.lookupTask(w, c, id)
	if !ok {
		return
	}
	// A member's change re-checks its right inside the write (the lookup
	// above is a fast fail only); a lead's needs only the team scope.
	var updated TaskRow
	var err error
	if c.member != nil {
		updated, err = m.store.SetTaskStatusByOwner(c.team.ID, row.Seq, c.ownerKey(), req.Status, m.now())
	} else {
		updated, err = m.store.SetTaskStatus(c.team.ID, row.Seq, req.Status, team.TaskByLead, m.now())
	}
	if err != nil {
		m.taskStoreErr(w, "status of "+id, err)
		return
	}
	m.respondTask(w, http.StatusOK, c, updated, "status:"+string(req.Status))
}

// handleTaskReassign is POST /api/team/tasks/{id}/reassign (lead): the task
// goes to another active member and back to pending.
func (m *Module) handleTaskReassign(w http.ResponseWriter, r *http.Request) {
	var req team.ReassignTaskRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	t, ok := m.callerTeam(w, req.OriginInbox)
	if !ok {
		return
	}
	c := taskCaller{team: t}
	row, ok := m.lookupTask(w, c, r.PathValue("id"))
	if !ok {
		return
	}
	to, ok := m.targetMember(w, t, req.To)
	if !ok {
		return
	}
	updated, err := m.store.ReassignTask(t.ID, row.Seq, to.SpawnOp, m.now())
	if err != nil {
		m.taskStoreErr(w, "reassign "+r.PathValue("id"), err)
		return
	}
	m.respondTask(w, http.StatusOK, c, updated, "reassign")
}

// respondTask logs one line for a successful mutation and answers the task.
func (m *Module) respondTask(w http.ResponseWriter, status int, c taskCaller, row TaskRow, verb string) {
	m.rosterChanged() // a created, started, finished, deleted or reassigned task changes a member's current task (T-3b)
	v, ok := m.newTaskView(w, c.team.ID, c.ownerKey())
	if !ok {
		return
	}
	tk := v.task(row)
	m.logf("[team] task %s %s by %s", tk.ID, verb, c.ref())
	m.writeJSON(w, status, tk)
}

// taskView builds the wire views of one team's tasks and reports. It reads
// the team's members and tasks once, so a list costs two queries, not two
// per task.
type taskView struct {
	m         *Module
	teamID    string
	viewerKey string               // a member caller's key, "" for a lead
	members   map[string]memberRow // by spawn op (the member key)
	tasks     map[int]TaskRow
	blocks    map[int][]int // seq -> seqs of the tasks that name it in blocked_by
	owners    map[string]team.TaskOwner
}

// newTaskView reads the team once. viewerKey is the member key of a member
// caller ("" for a lead): that view names, in blocks and blocked_by, only
// the tasks the member owns; the flag blocked still counts every blocker.
func (m *Module) newTaskView(w http.ResponseWriter, teamID, viewerKey string) (*taskView, bool) {
	members, err := m.store.MembersOf(teamID)
	if err != nil {
		return nil, m.taskStorageErr(w, "members of "+teamID, err)
	}
	all, err := m.store.ListTasks(teamID, "", true)
	if err != nil {
		return nil, m.taskStorageErr(w, "tasks of "+teamID, err)
	}
	v := &taskView{m: m, teamID: teamID, viewerKey: viewerKey, members: map[string]memberRow{}, tasks: map[int]TaskRow{}, blocks: map[int][]int{}, owners: map[string]team.TaskOwner{}}
	for _, mr := range members {
		v.members[mr.SpawnOp] = mr
	}
	for _, t := range all {
		v.tasks[t.Seq] = t
	}
	for _, t := range all {
		for _, b := range t.BlockedBy {
			v.blocks[b] = append(v.blocks[b], t.Seq)
		}
	}
	for _, s := range v.blocks {
		slices.Sort(s)
	}
	return v, true
}

// owner is the member key's row as the wire shows it, in any state; a row
// that no longer exists (it should not happen) is a bare "gone".
func (v *taskView) owner(key string) team.TaskOwner {
	if o, ok := v.owners[key]; ok {
		return o
	}
	o := team.TaskOwner{State: string(team.MemberGone)}
	if mr, ok := v.members[key]; ok {
		mv := v.m.memberView(mr)
		o = team.TaskOwner{Ref: mv.Ref, Address: mv.Address, Title: mv.Title, State: string(mv.State)}
	}
	v.owners[key] = o
	return o
}

// displayIDs names the tasks of seqs the viewer may see: all for a lead, only
// its own for a member (another member's task id is not its to know).
func (v *taskView) displayIDs(seqs []int) []string {
	out := make([]string, 0, len(seqs))
	for _, s := range seqs {
		if t, ok := v.tasks[s]; v.viewerKey != "" && (!ok || t.OwnerKey != v.viewerKey) {
			continue
		}
		out = append(out, team.TaskDisplayID(v.teamID, s))
	}
	return out
}

func (v *taskView) task(r TaskRow) team.Task {
	tk := team.Task{ID: team.TaskDisplayID(r.TeamID, r.Seq), TeamID: r.TeamID, Subject: r.Subject, Description: r.Description,
		DoneWhen: r.DoneWhen, Status: r.Status, Owner: v.owner(r.OwnerKey), Blocks: v.displayIDs(v.blocks[r.Seq]),
		BlockedBy: v.displayIDs(r.BlockedBy), CreatedBy: r.CreatedByRef, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Metadata: r.Metadata}
	for _, b := range r.BlockedBy {
		if bt, ok := v.tasks[b]; ok && (bt.Status == team.TaskPending || bt.Status == team.TaskInProgress) {
			tk.Blocked = true
		}
	}
	if r.LastReportAt != 0 {
		tk.LastReport = &team.TaskReportStamp{Kind: r.LastReportKind, Summary: r.LastReportSummary, At: r.LastReportAt}
	}
	if r.LastTurnAt != 0 {
		tk.LastTurn = &team.TaskTurnStamp{Summary: r.LastTurnSummary, At: r.LastTurnAt}
	}
	return tk
}

func (v *taskView) report(r ReportRow) team.Report {
	return team.Report{ID: r.ID, Task: team.TaskDisplayID(r.TeamID, r.TaskSeq), Kind: r.Kind, Summary: r.Summary, Needs: r.Needs,
		PR: r.PR, Reviews: r.Reviews, SHA: r.SHA, Body: r.Body, Member: v.owner(r.MemberKey), CreatedAt: r.CreatedAt}
}
