package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Task routes (T-1b1). The world: team uid(1) led by sid-1 (/tmp/10.sock)
// with members ma and mb; team uid(2) (the SAME 6-hex display prefix) led by
// sid-2 (/tmp/20.sock) with mx; team teamB (another prefix) led by sid-b with
// my. Display ids are <prefix>-<seq>, so team 1 and team 2 mint the same ones.
const (
	leadInbox = "/tmp/10.sock"
	maInbox   = "/tmp/ma.sock"
	mbInbox   = "/tmp/mb.sock"
	mxInbox   = "/tmp/mx.sock"
	myInbox   = "/tmp/my.sock"
	lead2     = "/tmp/20.sock"
	leadB     = "/tmp/b0.sock"
	teamB     = "abcdef12-0000-4000-8000-00000000000b"
)

type taskWorld struct {
	*fixture
	ma, mb, mx, my memberRow
	logs           []string
}

// session makes sid live in the registry at inbox for the rest of the test.
func (f *fixture) session(inbox, sid string) {
	f.t.Helper()
	ref := ipeers.RefID(sid)
	fixtureOrigins[inbox] = team.Origin{SessionID: sid, Ref: ref, PID: 60 + len(inbox), ProcStart: "p", Cwd: "/w", Address: "mlab/" + ref}
	f.t.Cleanup(func() { delete(fixtureOrigins, inbox) })
}

// taskMember is an active member of teamID on sid, reachable at inbox.
func (f *fixture) taskMember(teamID, key, sid, inbox string) memberRow {
	f.t.Helper()
	f.session(inbox, sid)
	mr := newMember(key, teamID, sid, ipeers.RefID(sid), 1)
	if err := f.m.store.InsertMember(mr); err != nil {
		f.t.Fatal(err)
	}
	return mr
}

func newTaskWorld(t *testing.T) *taskWorld {
	f, _ := newTeamFixture(t, 3)
	w := &taskWorld{fixture: f}
	f.m.logf = func(format string, a ...any) { w.logs = append(w.logs, fmt.Sprintf(format, a...)) }
	seedTeam(t, f.m.store, uid(2), "sid-2", 1)
	f.session("/tmp/b0.sock", "sid-b")
	seedTeam(t, f.m.store, teamB, "sid-b", 1)
	w.ma = f.taskMember(uid(1), "op-a", "sid-ma", maInbox)
	w.mb = f.taskMember(uid(1), "op-b", "sid-mb", mbInbox)
	w.mx = f.taskMember(uid(2), "op-x", "sid-mx", mxInbox)
	w.my = f.taskMember(teamB, "op-y", "sid-my", myInbox)
	f.session("/tmp/n.sock", "sid-n") // a live session with no role
	return w
}

// call sends the request and decodes a 2xx answer into T, else the APIError.
func call[T any](f *fixture, method, path string, body any) (int, T, team.APIError) {
	f.t.Helper()
	var out T
	code, raw := f.do(method, path, body)
	if code/100 != 2 {
		return code, out, decodeErr(f.t, raw)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatalf("decode %s %s: %v; %s", method, path, err, raw)
	}
	return code, out, team.APIError{}
}

func (f *fixture) createTask(inbox, to, subject string, edit func(*team.CreateTaskRequest)) (int, team.Task, team.APIError) {
	f.t.Helper()
	req := team.CreateTaskRequest{OriginInbox: inbox, To: to, Subject: subject}
	if edit != nil {
		edit(&req)
	}
	return call[team.Task](f, http.MethodPost, "/api/team/tasks", req)
}

// mustTask creates a task and returns it, failing on anything but 201.
func (f *fixture) mustTask(inbox, to, subject string, edit func(*team.CreateTaskRequest)) team.Task {
	f.t.Helper()
	code, tk, e := f.createTask(inbox, to, subject, edit)
	if code != http.StatusCreated {
		f.t.Fatalf("create %q: %d %+v", subject, code, e)
	}
	return tk
}

func (f *fixture) listTasks(inbox, query string) (int, team.TaskList, team.APIError) {
	f.t.Helper()
	return call[team.TaskList](f, http.MethodGet, "/api/team/tasks?origin_inbox="+url.QueryEscape(inbox)+query, "")
}

func (f *fixture) showTask(inbox, id string) (int, team.TaskDetail, team.APIError) {
	f.t.Helper()
	return call[team.TaskDetail](f, http.MethodGet, "/api/team/tasks/"+id+"?origin_inbox="+url.QueryEscape(inbox), "")
}

func (f *fixture) setStatus(inbox, id string, st team.TaskStatus) (int, team.Task, team.APIError) {
	f.t.Helper()
	return call[team.Task](f, http.MethodPost, "/api/team/tasks/"+id+"/status", team.TaskStatusRequest{OriginInbox: inbox, Status: st})
}

func (f *fixture) reassign(inbox, id, to string) (int, team.Task, team.APIError) {
	f.t.Helper()
	return call[team.Task](f, http.MethodPost, "/api/team/tasks/"+id+"/reassign", team.ReassignTaskRequest{OriginInbox: inbox, To: to})
}

func taskIDs(ts []team.Task) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

func wantErr(t *testing.T, what string, code int, e team.APIError, status int, errCode string) {
	t.Helper()
	if code != status || e.Error != errCode {
		t.Fatalf("%s: %d %+v, want %d %s", what, code, e, status, errCode)
	}
}

func taskCount(t *testing.T, w *taskWorld, teamID string) int {
	t.Helper()
	rows, err := w.m.store.ListTasks(teamID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// POST /api/team/tasks: a lead hands a task to one of its members; the answer
// is the task as the wire shows it (display id, the owner's current ref and
// address, the lead's ref, the clock in ms) and a log line names the verb.
func TestTasks_CreateAnswersTheTask(t *testing.T) {
	w := newTaskWorld(t)
	code, tk, e := w.createTask(leadInbox, "self/"+w.ma.Ref, "ship it", func(r *team.CreateTaskRequest) {
		r.Description, r.DoneWhen = "details\nmore", []string{"tests pass", "merged"}
	})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %+v", code, e)
	}
	want := team.Task{ID: "000000-1", TeamID: uid(1), Subject: "ship it", Description: "details\nmore", DoneWhen: []string{"tests pass", "merged"},
		Status: team.TaskPending, Owner: team.TaskOwner{Ref: w.ma.Ref, Address: "mlab/" + w.ma.Ref, Title: "worker", State: "active"},
		Blocks: []string{}, BlockedBy: []string{}, CreatedBy: "_abc123", CreatedAt: w.clock.Load(), UpdatedAt: w.clock.Load()}
	if !jsonEqual(tk, want) {
		t.Fatalf("task = %+v\nwant   %+v", tk, want)
	}
	if got := w.mustTask(leadInbox, w.mb.Ref, "second", nil); got.ID != "000000-2" || got.Owner.Ref != w.mb.Ref {
		t.Fatalf("second = %+v", got)
	}
	if len(w.logs) != 2 || w.logs[0] != "[team] task 000000-1 create by _abc123" {
		t.Fatalf("logs = %q", w.logs)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Every create refusal answers its code and writes no task.
func TestTasks_CreateRefusals(t *testing.T) {
	w := newTaskWorld(t)
	w.mustTask(leadInbox, w.ma.Ref, "first", nil)
	if err := w.m.store.SetMemberState("op-b", team.MemberKilled, 5); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		inbox  string
		to     string
		edit   func(*team.CreateTaskRequest)
		status int
		code   string
	}{
		{"a member is not a lead", maInbox, w.mb.Ref, nil, 409, team.ErrNotLead},
		{"a session with no role", "/tmp/n.sock", w.ma.Ref, nil, 409, team.ErrNotLead},
		{"unknown origin", "/tmp/99.sock", w.ma.Ref, nil, 400, team.ErrOriginUnknown},
		{"no such member", leadInbox, "_zzzzzz", nil, 409, team.ErrNotYourMember},
		{"another team's member", leadInbox, w.mx.Ref, nil, 409, team.ErrNotYourMember},
		{"a killed member", leadInbox, w.mb.Ref, nil, 409, team.ErrOwnerNotActive},
		{"empty subject", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.Subject = "" }, 400, team.ErrBadRequest},
		{"81-rune subject", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.Subject = strings.Repeat("x", 81) }, 400, team.ErrBadRequest},
		{"control char in subject", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.Subject = "a\x1b[2Jb" }, 400, team.ErrBadRequest},
		{"11 done_when lines", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.DoneWhen = make([]string, 11) }, 400, team.ErrBadRequest},
		{"control char in description", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.Description = "a\x1bb" }, 400, team.ErrBadRequest},
		{"malformed blocked_by", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.BlockedBy = []string{"x-1"} }, 409, team.ErrBlockedByUnknown},
		{"foreign-prefix blocked_by", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.BlockedBy = []string{"abcdef-1"} }, 409, team.ErrBlockedByUnknown},
		{"no such blocker", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.BlockedBy = []string{"000000-7"} }, 409, team.ErrBlockedByUnknown},
		{"blocked by itself", leadInbox, w.ma.Ref, func(r *team.CreateTaskRequest) { r.BlockedBy = []string{"000000-2"} }, 409, team.ErrBlockedByCycle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, e := w.createTask(c.inbox, c.to, "second", c.edit)
			wantErr(t, c.name, code, e, c.status, c.code)
			if n := taskCount(t, w, uid(1)); n != 1 {
				t.Fatalf("a refused create left %d tasks, want 1", n)
			}
		})
	}
	w.origins.setReadErr(true)
	if code, _, e := w.createTask(leadInbox, w.ma.Ref, "x", nil); code != 503 || e.Error != team.ErrNotReady {
		t.Fatalf("unreadable registry = %d %+v", code, e)
	}
}

// Finished tasks are hidden unless all=1; a lead may narrow to one member
// (by its current ref); a member sees only its own and may not filter.
func TestTasks_ListScopesAndHidesFinishedUnlessAll(t *testing.T) {
	w := newTaskWorld(t)
	a1 := w.mustTask(leadInbox, w.ma.Ref, "a1", nil)
	a2 := w.mustTask(leadInbox, w.ma.Ref, "a2", nil)
	b1 := w.mustTask(leadInbox, w.mb.Ref, "b1", nil)
	w.mustTask(lead2, w.mx.Ref, "x1", nil)
	if code, _, e := w.setStatus(leadInbox, a2.ID, team.TaskCompleted); code != 200 {
		t.Fatalf("complete %s: %d %+v", a2.ID, code, e)
	}
	list := func(inbox, q string) []string {
		code, l, e := w.listTasks(inbox, q)
		if code != 200 {
			t.Fatalf("list %s %s: %d %+v", inbox, q, code, e)
		}
		return taskIDs(l.Tasks)
	}
	if got := list(leadInbox, ""); !slices.Equal(got, []string{b1.ID, a1.ID}) { // newest change first, finished hidden
		t.Fatalf("lead list = %v", got)
	}
	if got := list(leadInbox, "&all=1"); !slices.Equal(got, []string{b1.ID, a2.ID, a1.ID}) {
		t.Fatalf("lead list all = %v", got)
	}
	if got := list(leadInbox, "&member="+url.QueryEscape("self/"+w.mb.Ref)); !slices.Equal(got, []string{b1.ID}) {
		t.Fatalf("lead list member=mb = %v", got)
	}
	if got := list(maInbox, ""); !slices.Equal(got, []string{a1.ID}) {
		t.Fatalf("ma list = %v", got)
	}
	if got := list(maInbox, "&all=1"); !slices.Equal(got, []string{a2.ID, a1.ID}) {
		t.Fatalf("ma list all = %v", got)
	}
	code, _, e := w.listTasks(leadInbox, "&member=_zzzzzz")
	wantErr(t, "unknown member filter", code, e, 409, team.ErrNotYourMember)
	code, _, e = w.listTasks(leadInbox, "&member="+w.mx.Ref)
	wantErr(t, "another team's member filter", code, e, 409, team.ErrNotYourMember)
	code, _, e = w.listTasks(maInbox, "&member="+w.ma.Ref)
	wantErr(t, "a member filtering", code, e, 409, team.ErrNotLead)
}

// A list with nothing in it is {"tasks":[]}, a task with no edges shows
// [] for blocks / blocked_by / done_when, a task with no reports shows
// "reports":[]: no null a client would have to special-case.
func TestTasks_NeverNullArrays(t *testing.T) {
	w := newTaskWorld(t)
	code, raw := w.do(http.MethodGet, "/api/team/tasks?origin_inbox="+url.QueryEscape(leadInbox), "")
	if code != 200 || strings.TrimSpace(string(raw)) != `{"tasks":[]}` {
		t.Fatalf("empty list = %d %s", code, raw)
	}
	tk := w.mustTask(leadInbox, w.ma.Ref, "a", nil)
	code, raw = w.do(http.MethodGet, "/api/team/tasks/"+tk.ID+"?origin_inbox="+url.QueryEscape(leadInbox), "")
	for _, frag := range []string{`"reports":[]`, `"blocks":[]`, `"blocked_by":[]`, `"done_when":[]`} {
		if code != 200 || !strings.Contains(string(raw), frag) {
			t.Fatalf("detail lacks %s: %d %s", frag, code, raw)
		}
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("detail has a null: %s", raw)
	}
}

// blocks and blocked are derived at view time: blocks lists the team's tasks
// that name this one, blocked is true while some blocker is neither
// completed nor deleted.
func TestTasks_BlocksAndBlockedAreDerived(t *testing.T) {
	w := newTaskWorld(t)
	t1 := w.mustTask(leadInbox, w.ma.Ref, "t1", nil)
	t2 := w.mustTask(leadInbox, w.ma.Ref, "t2", func(r *team.CreateTaskRequest) { r.BlockedBy = []string{t1.ID} })
	t3 := w.mustTask(leadInbox, w.mb.Ref, "t3", func(r *team.CreateTaskRequest) { r.BlockedBy = []string{t1.ID, t2.ID} })
	view := func(id string) team.Task {
		code, d, e := w.showTask(leadInbox, id)
		if code != 200 {
			t.Fatalf("show %s: %d %+v", id, code, e)
		}
		return d.Task
	}
	if v := view(t1.ID); v.Blocked || !slices.Equal(v.Blocks, []string{t2.ID, t3.ID}) {
		t.Fatalf("t1 = %+v", v)
	}
	if v := view(t2.ID); !v.Blocked || !slices.Equal(v.BlockedBy, []string{t1.ID}) || !slices.Equal(v.Blocks, []string{t3.ID}) {
		t.Fatalf("t2 = %+v", v)
	}
	if !view(t3.ID).Blocked {
		t.Fatal("t3 must be blocked")
	}
	w.setStatus(leadInbox, t1.ID, team.TaskCompleted)
	if view(t2.ID).Blocked || !view(t3.ID).Blocked {
		t.Fatal("completing t1 frees t2 only")
	}
	w.setStatus(leadInbox, t2.ID, team.TaskDeleted)
	if view(t3.ID).Blocked {
		t.Fatal("a deleted blocker blocks nothing")
	}
	// The member's own view carries the same derived fields.
	if code, d, _ := w.showTask(mbInbox, t3.ID); code != 200 || d.Task.Blocked || !slices.Equal(d.Task.BlockedBy, []string{t1.ID, t2.ID}) {
		t.Fatalf("mb view of t3 = %d %+v", code, d.Task)
	}
}

// GET /{id} carries the task's reports newest first, each with the
// reporting member as it is now and the task's display id.
func TestTasks_ShowCarriesTheReportsNewestFirst(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "a", nil)
	mustInsertReport(t, w.m.store, newReport(uid(1), 1, "op-a", team.ReportAck, 1, 10))
	mustInsertReport(t, w.m.store, newReport(uid(1), 1, "op-a", team.ReportReady, 2, 20))
	for _, inbox := range []string{leadInbox, maInbox} {
		code, d, e := w.showTask(inbox, tk.ID)
		if code != 200 || len(d.Reports) != 2 {
			t.Fatalf("show from %s = %d %+v %+v", inbox, code, d, e)
		}
		r := d.Reports[0]
		if r.Kind != team.ReportReady || r.PR != 12 || r.Task != tk.ID || r.Member.Ref != w.ma.Ref || r.Member.State != "active" || d.Reports[1].Kind != team.ReportAck {
			t.Fatalf("reports = %+v", d.Reports)
		}
		if d.Task.Status != team.TaskInProgress || d.Task.LastReport == nil || d.Task.LastReport.Kind != "ready" || d.Task.LastTurn != nil {
			t.Fatalf("task = %+v", d.Task)
		}
	}
}

// POST /{id}/status: a lead may make any allowed move, the owner only starts
// and completes; the table's refusals are 409 bad_task_transition, an
// unknown status is a 400. Every mutation logs one line.
func TestTasks_StatusByLeadAndOwner(t *testing.T) {
	w := newTaskWorld(t)
	t1 := w.mustTask(leadInbox, w.ma.Ref, "t1", nil)
	t2 := w.mustTask(leadInbox, w.ma.Ref, "t2", nil)
	t3 := w.mustTask(leadInbox, w.ma.Ref, "t3", nil)
	w.clock.Add(7)

	code, tk, e := w.setStatus(maInbox, t1.ID, team.TaskInProgress)
	if code != 200 || tk.Status != team.TaskInProgress || tk.UpdatedAt != w.clock.Load() || tk.CreatedAt == tk.UpdatedAt {
		t.Fatalf("owner starts: %d %+v %+v", code, tk, e)
	}
	if code, tk, _ := w.setStatus(maInbox, t1.ID, team.TaskCompleted); code != 200 || tk.Status != team.TaskCompleted {
		t.Fatalf("owner completes: %d %+v", code, tk)
	}
	code, _, e = w.setStatus(maInbox, t2.ID, team.TaskDeleted)
	wantErr(t, "owner deletes", code, e, 409, team.ErrBadTaskTransition)
	code, _, e = w.setStatus(maInbox, t2.ID, team.TaskCompleted)
	wantErr(t, "owner closes a pending task", code, e, 409, team.ErrBadTaskTransition)
	code, _, e = w.setStatus(leadInbox, t1.ID, team.TaskInProgress)
	wantErr(t, "nothing leaves completed", code, e, 409, team.ErrBadTaskTransition)
	code, _, e = w.setStatus(leadInbox, t2.ID, "bogus")
	wantErr(t, "unknown status", code, e, 400, team.ErrBadRequest)
	code, _, e = w.setStatus(leadInbox, t2.ID, team.TaskPending)
	wantErr(t, "same status", code, e, 409, team.ErrBadTaskTransition)
	if code, tk, _ := w.setStatus(leadInbox, t2.ID, team.TaskCompleted); code != 200 || tk.Status != team.TaskCompleted {
		t.Fatalf("lead closes a pending task: %d %+v", code, tk)
	}
	if code, tk, _ := w.setStatus(leadInbox, t3.ID, team.TaskDeleted); code != 200 || tk.Status != team.TaskDeleted {
		t.Fatalf("lead deletes: %d %+v", code, tk)
	}
	want := []string{"[team] task 000000-1 status:in_progress by " + w.ma.Ref, "[team] task 000000-1 status:completed by " + w.ma.Ref,
		"[team] task 000000-2 status:completed by _abc123", "[team] task 000000-3 status:deleted by _abc123"}
	if got := w.logs[3:]; !slices.Equal(got, want) { // the first three lines are the creates
		t.Fatalf("logs = %q\nwant   %q", got, want)
	}
}

// POST /{id}/reassign: the owner changes, the status goes back to pending,
// the previous owner loses it; a non-active target, a finished task and
// another team's member are refused.
func TestTasks_Reassign(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "t", nil)
	w.setStatus(maInbox, tk.ID, team.TaskInProgress)
	done := w.mustTask(leadInbox, w.ma.Ref, "done", nil)
	w.setStatus(leadInbox, done.ID, team.TaskCompleted)

	code, got, e := w.reassign(leadInbox, tk.ID, w.mb.Ref)
	if code != 200 || got.Owner.Ref != w.mb.Ref || got.Status != team.TaskPending {
		t.Fatalf("reassign = %d %+v %+v", code, got, e)
	}
	if code, _, _ := w.showTask(mbInbox, tk.ID); code != 200 {
		t.Fatalf("the new owner cannot see it: %d", code)
	}
	if line := w.logs[len(w.logs)-1]; line != "[team] task "+tk.ID+" reassign by _abc123" {
		t.Fatalf("log = %q", line)
	}
	code, _, e = w.reassign(maInbox, tk.ID, w.ma.Ref)
	wantErr(t, "a member reassigns", code, e, 409, team.ErrNotLead)
	code, _, e = w.reassign(leadInbox, tk.ID, "_zzzzzz")
	wantErr(t, "no such member", code, e, 409, team.ErrNotYourMember)
	code, _, e = w.reassign(leadInbox, tk.ID, w.mx.Ref)
	wantErr(t, "another team's member", code, e, 409, team.ErrNotYourMember)
	code, _, e = w.reassign(leadInbox, done.ID, w.mb.Ref)
	wantErr(t, "a finished task", code, e, 409, team.ErrBadTaskTransition)
	if err := w.m.store.SetMemberState("op-a", team.MemberKilled, 5); err != nil {
		t.Fatal(err)
	}
	code, _, e = w.reassign(leadInbox, tk.ID, w.ma.Ref)
	wantErr(t, "a killed target", code, e, 409, team.ErrOwnerNotActive)
}
