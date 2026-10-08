package teammod

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Report routes (T-1b2), on the T-1b1 world: team uid(1) with members ma and
// mb, team uid(2) with the same display prefix and member mx.

// rreq is a valid report request of kind k with just the fields k requires.
func rreq(n int, k team.ReportKind, task string) team.ReportRequest {
	r := team.ReportRequest{ID: reportID(n), Task: task, Kind: k, Summary: "summary " + string(k)}
	switch k {
	case team.ReportQuestion, team.ReportBlocked:
		r.Needs = "lead"
	case team.ReportReady:
		r.PR, r.Reviews = 12, []string{"R1=job-1"}
	case team.ReportMerged:
		r.PR, r.SHA = 12, "abcdef1"
	}
	return r
}

func (f *fixture) postReport(inbox string, req team.ReportRequest) (int, team.ReportResponse, team.APIError) {
	f.t.Helper()
	return call[team.ReportResponse](f, http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: inbox, ReportRequest: req})
}

// mustReport posts as inbox and fails on anything but 201.
func (f *fixture) mustReport(inbox string, req team.ReportRequest) team.ReportResponse {
	f.t.Helper()
	code, out, e := f.postReport(inbox, req)
	if code != http.StatusCreated {
		f.t.Fatalf("report %s: %d %+v", req.Kind, code, e)
	}
	return out
}

func reportCount(t *testing.T, w *taskWorld, teamID string) int {
	t.Helper()
	return countReports(t, w.m.store, teamID)
}

// Every kind through the route: the stored report as the wire shows it, the
// task after the kind's effect (the member's view) and the lead.
func TestReports_PostAppliesEachKindAndAnswersTheLead(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	alias, _ := w.m.selfHost()
	wantLead := team.ReportLead{Ref: "_abc123", Address: alias + "/_abc123"}

	w.clock.Add(1)
	out := w.mustReport(maInbox, rreq(1, team.ReportAck, tk.ID))
	if out.Task.Status != team.TaskInProgress || out.Task.LastReport == nil || out.Task.LastReport.Kind != "ack" {
		t.Fatalf("ack: task = %+v", out.Task)
	}
	wantReport := team.Report{ID: reportID(1), Task: tk.ID, Kind: team.ReportAck, Summary: "summary ack", CreatedAt: w.clock.Load(),
		Member: team.TaskOwner{Ref: w.ma.Ref, Address: "mlab/" + w.ma.Ref, Title: "worker", State: "active"}}
	if !jsonEqual(out.Report, wantReport) || out.Lead != wantLead {
		t.Fatalf("ack: report %+v lead %+v\nwant   %+v %+v", out.Report, out.Lead, wantReport, wantLead)
	}
	if want := "[team] report ack " + tk.ID + " by " + w.ma.Ref; w.logs[len(w.logs)-1] != want {
		t.Fatalf("last log = %q, want %q", w.logs[len(w.logs)-1], want)
	}

	for i, c := range []struct {
		kind  team.ReportKind
		check func(team.Task) bool
	}{
		{team.ReportProgress, func(t team.Task) bool { return t.Status == team.TaskInProgress }},
		{team.ReportQuestion, func(t team.Task) bool { return t.Status == team.TaskInProgress }},
		{team.ReportReady, func(t team.Task) bool { return slices.Equal(t.Metadata.PRs, []int{12}) }},
		{team.ReportMerged, func(t team.Task) bool { return slices.Equal(t.Metadata.SHAs, []string{"abcdef1"}) }},
		{team.ReportDone, func(t team.Task) bool { return t.Status == team.TaskCompleted }},
	} {
		w.clock.Add(1)
		out := w.mustReport(maInbox, rreq(2+i, c.kind, tk.ID))
		if !c.check(out.Task) || out.Task.LastReport.Kind != string(c.kind) || out.Report.Kind != c.kind {
			t.Fatalf("%s: task = %+v report = %+v", c.kind, out.Task, out.Report)
		}
	}
	if n := reportCount(t, w, uid(1)); n != 6 {
		t.Fatalf("%d reports stored, want 6", n)
	}
}

// The lead in the answer is the live registry entry; with none it is the
// recorded ref under this host's alias.
func TestReports_LeadIsTheCurrentOne(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	alias, _ := w.m.selfHost()

	w.origins.show(team.Origin{SessionID: "sid-1", Ref: "_new999", Address: "mlab/_new999", PID: 10, ProcStart: "p"})
	if out := w.mustReport(maInbox, rreq(1, team.ReportAck, tk.ID)); out.Lead != (team.ReportLead{Ref: "_new999", Address: "mlab/_new999"}) {
		t.Fatalf("live lead = %+v", out.Lead)
	}
	w.origins.hide("sid-1")
	if out := w.mustReport(maInbox, rreq(2, team.ReportProgress, tk.ID)); out.Lead != (team.ReportLead{Ref: "_abc123", Address: alias + "/_abc123"}) {
		t.Fatalf("lead not in the registry = %+v", out.Lead)
	}
}

// No task: the member's only in_progress one, never another member's. None or
// several: 400 naming the choice (the member's open tasks only).
func TestReports_DefaultTaskIsTheOnlyInProgress(t *testing.T) {
	w := newTaskWorld(t)
	t1 := w.mustTask(leadInbox, w.ma.Ref, "one", nil)
	t2 := w.mustTask(leadInbox, w.ma.Ref, "two", nil)
	t3 := w.mustTask(leadInbox, w.mb.Ref, "b's", nil)
	if code, _, e := w.setStatus(mbInbox, t3.ID, team.TaskInProgress); code != 200 {
		t.Fatalf("start b's: %d %+v", code, e)
	}

	code, _, e := w.postReport(maInbox, rreq(1, team.ReportProgress, ""))
	wantErr(t, "none in progress", code, e, 400, team.ErrBadRequest)
	if want := "no task is in progress; pass task=<id> (open: " + t1.ID + ", " + t2.ID + ")"; e.Detail != want {
		t.Fatalf("detail = %q, want %q", e.Detail, want)
	}

	if code, _, e := w.setStatus(maInbox, t2.ID, team.TaskInProgress); code != 200 {
		t.Fatalf("start two: %d %+v", code, e)
	}
	if out := w.mustReport(maInbox, rreq(2, team.ReportProgress, "")); out.Report.Task != t2.ID {
		t.Fatalf("the default task = %s, want %s (the only one in progress; b's is not ma's)", out.Report.Task, t2.ID)
	}

	if code, _, e := w.setStatus(maInbox, t1.ID, team.TaskInProgress); code != 200 {
		t.Fatalf("start one: %d %+v", code, e)
	}
	code, _, e = w.postReport(maInbox, rreq(3, team.ReportProgress, ""))
	wantErr(t, "several in progress", code, e, 400, team.ErrBadRequest)
	if want := "several tasks are in progress: " + t1.ID + ", " + t2.ID + "; pass task=<id>"; e.Detail != want {
		t.Fatalf("detail = %q, want %q", e.Detail, want)
	}
	if strings.Contains(e.Detail, t3.ID) {
		t.Fatalf("detail names another member's task: %q", e.Detail)
	}
	if n := reportCount(t, w, uid(1)); n != 1 {
		t.Fatalf("%d reports stored, want only the one that had a default task", n)
	}

	// A member with no open task at all.
	code, _, e = w.postReport(mbInbox, rreq(4, team.ReportProgress, t3.ID)) // b has one in progress: fine
	if code != http.StatusCreated {
		t.Fatalf("b: %d %+v", code, e)
	}
	if code, _, e := w.setStatus(mbInbox, t3.ID, team.TaskCompleted); code != 200 {
		t.Fatalf("finish b's: %d %+v", code, e)
	}
	code, _, e = w.postReport(mbInbox, rreq(5, team.ReportProgress, ""))
	wantErr(t, "nothing open", code, e, 400, team.ErrBadRequest)
	if !strings.Contains(e.Detail, "pass task=<id>") || strings.Contains(e.Detail, t1.ID) {
		t.Fatalf("detail = %q", e.Detail)
	}
}

// Only a member reports: a lead may report to nobody, a session with no role
// is no member, and nothing is stored.
func TestReports_LeadIsRefused(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	for _, c := range []struct {
		name, inbox string
		status      int
		code        string
	}{
		{"the lead", leadInbox, 409, team.ErrNotMember},
		{"a session with no role", "/tmp/n.sock", 409, team.ErrNotMember},
		{"an unknown origin", "/tmp/99.sock", 400, team.ErrOriginUnknown},
	} {
		code, _, e := w.postReport(c.inbox, rreq(1, team.ReportAck, tk.ID))
		wantErr(t, c.name, code, e, c.status, c.code)
	}
	if n := reportCount(t, w, uid(1)); n != 0 {
		t.Fatalf("a refused report stored %d rows", n)
	}
	if got := mustGetTask(t, w.m.store, uid(1), 1); got.Status != team.TaskPending {
		t.Fatalf("a refused report moved the task: %+v", got)
	}
}

// Whatever the caller may not report on answers exactly what a task that
// never existed answers: status, code and detail, byte for byte.
func TestReports_ForeignTaskIsNotFound(t *testing.T) {
	w := newTaskWorld(t)
	w.mustTask(leadInbox, w.ma.Ref, "a's", nil)      // 000000-1
	b := w.mustTask(leadInbox, w.mb.Ref, "b's", nil) // 000000-2
	w.mustTask(lead2, w.mx.Ref, "team two's", nil)   // 000000-1 of team 2
	w.mustTask(leadB, w.my.Ref, "team B's", nil)     // abcdef-1
	post := func(inbox, task string) (int, string) {
		code, raw := w.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: inbox, ReportRequest: rreq(1, team.ReportAck, task)})
		return code, string(raw)
	}
	wantCode, wantBody := post(maInbox, "000000-999")
	if wantCode != http.StatusConflict || !strings.Contains(wantBody, team.ErrTaskNotFound) {
		t.Fatalf("a nonexistent task = %d %s", wantCode, wantBody)
	}
	for _, c := range []struct{ name, inbox, task string }{
		{"another member's task", maInbox, b.ID},
		{"another team's id", maInbox, "abcdef-1"},
		{"a member of team 2 asking team 1's seq", mxInbox, b.ID},
		{"malformed: no number", maInbox, "abc"},
		{"malformed: leading zero", maInbox, "000000-01"},
		{"malformed: foreign prefix", maInbox, "deadbe-1"},
		{"malformed: padded", maInbox, " 000000-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if code, body := post(c.inbox, c.task); code != wantCode || body != wantBody {
				t.Fatalf("= %d %s, want exactly %d %s", code, body, wantCode, wantBody)
			}
		})
	}
	if n := reportCount(t, w, uid(1)) + reportCount(t, w, uid(2)) + reportCount(t, w, teamB); n != 0 {
		t.Fatalf("refused reports stored %d rows", n)
	}
}

// The same id and content again is a replay: 200, one row, the effect once.
func TestReports_ReplayAnswers200(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	req := rreq(1, team.ReportReady, tk.ID)

	w.clock.Add(1)
	code, first, e := w.postReport(maInbox, req)
	if code != http.StatusCreated {
		t.Fatalf("first = %d %+v", code, e)
	}
	logs := len(w.logs)
	w.clock.Add(100)
	code, again, e := w.postReport(maInbox, req)
	if code != http.StatusOK {
		t.Fatalf("replay = %d %+v, want 200", code, e)
	}
	if !jsonEqual(first, again) || again.Lead.Ref == "" || again.Lead.Address == "" {
		t.Fatalf("replay answered %+v, want the first answer %+v with the lead", again, first)
	}
	if n := reportCount(t, w, uid(1)); n != 1 {
		t.Fatalf("%d rows after a replay", n)
	}
	if len(w.logs) != logs {
		t.Fatalf("a replay logged: %q", w.logs[logs:])
	}
	if got := mustGetTask(t, w.m.store, uid(1), 1); !slices.Equal(got.Metadata.PRs, []int{12}) || got.UpdatedAt != first.Task.UpdatedAt {
		t.Fatalf("the effect ran twice: %+v", got)
	}

	// The same id with other content is a mistake, not a replay.
	other := req
	other.Summary = "something else"
	code, _, e = w.postReport(maInbox, other)
	wantErr(t, "id reused", code, e, 409, team.ErrIDConflict)
	if !strings.Contains(e.Detail, "report id") {
		t.Fatalf("detail = %q", e.Detail)
	}
	if stored, _, _ := w.m.store.GetReport(uid(1), reportID(1)); stored.Summary != req.Summary {
		t.Fatalf("a conflicting report overwrote: %+v", stored)
	}

	// Another team's report with the same id is independent.
	x := w.mustTask(lead2, w.mx.Ref, "x's", nil)
	if out := w.mustReport(mxInbox, rreq(1, team.ReportAck, x.ID)); out.Report.ID != reportID(1) {
		t.Fatalf("team 2's report = %+v", out.Report)
	}
}

func TestReports_FieldErrorsAreBadRequestAndNameTheField(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	for _, c := range []struct {
		name  string
		edit  func(*team.ReportRequest)
		field string
	}{
		{"ready without reviews", func(r *team.ReportRequest) { *r = rreq(1, team.ReportReady, tk.ID); r.Reviews = nil }, "reviews"},
		{"a short sha", func(r *team.ReportRequest) { *r = rreq(1, team.ReportMerged, tk.ID); r.SHA = "abc" }, "sha"},
		{"a 201-rune summary", func(r *team.ReportRequest) { r.Summary = strings.Repeat("x", 201) }, "summary"},
		{"a control character in the body", func(r *team.ReportRequest) { r.Body = "a\x1bb" }, "body"},
		{"question without needs", func(r *team.ReportRequest) { *r = rreq(1, team.ReportQuestion, tk.ID); r.Needs = "" }, "needs"},
		{"an unknown kind", func(r *team.ReportRequest) { r.Kind = "cheer" }, "kind"},
		{"a bad id", func(r *team.ReportRequest) { r.ID = "not-a-uuid" }, "id"},
		{"no id", func(r *team.ReportRequest) { r.ID = "" }, "id"},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := rreq(1, team.ReportAck, tk.ID)
			c.edit(&req)
			code, _, e := w.postReport(maInbox, req)
			wantErr(t, c.name, code, e, 400, team.ErrBadRequest)
			if !strings.Contains(e.Detail, c.field) {
				t.Fatalf("detail %q does not name %q", e.Detail, c.field)
			}
		})
	}
	if code, _ := w.do(http.MethodPost, "/api/team/reports", "{"); code != 400 {
		t.Fatalf("invalid JSON = %d", code)
	}
	if n := reportCount(t, w, uid(1)); n != 0 {
		t.Fatalf("refused reports stored %d rows", n)
	}
}

// reportRaces are the taskRaces plus a member that moved to another team.
func reportRaces(w *taskWorld, taskID string) map[string]func() {
	races := taskRaces(w, taskID)
	races["the member moves to another team"] = func() {
		if _, err := w.m.store.db.Exec(`UPDATE team_members SET team_id = ? WHERE spawn_op = 'op-a'`, uid(2)); err != nil {
			w.t.Fatal(err)
		}
	}
	return races
}

func raceNames() []string {
	var names []string
	for name := range reportRaces(nil, "") {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// The member's right is checked again in the transaction that writes, not
// only by the handler's lookup: each race changes the world between the two
// (the afterTaskLookup seam) and the old owner gets exactly what a task that
// never existed answers, with no report written and the task untouched.
// Mutation gate: drop the owner / active / live guard from
// InsertReportByOwner → every row red.
func TestReports_PostRacesOwnerState(t *testing.T) {
	for _, name := range raceNames() {
		for _, byDefault := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "", true: " (default task)"}[byDefault], func(t *testing.T) {
				w := newTaskWorld(t)
				tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
				if code, _, e := w.setStatus(maInbox, tk.ID, team.TaskInProgress); code != 200 {
					t.Fatalf("start: %d %+v", code, e)
				}
				before := mustGetTask(t, w.m.store, uid(1), 1)
				wantCode, wantBody := w.missing(http.MethodPost, "/api/team/reports",
					team.CreateReportRequest{OriginInbox: maInbox, ReportRequest: rreq(1, team.ReportDone, "000000-999")})

				task := tk.ID
				if byDefault {
					task = ""
				}
				race := reportRaces(w, tk.ID)[name]
				w.m.afterTaskLookup = func() { w.m.afterTaskLookup = nil; race() }
				code, raw := w.do(http.MethodPost, "/api/team/reports",
					team.CreateReportRequest{OriginInbox: maInbox, ReportRequest: rreq(1, team.ReportDone, task)})
				if byDefault && name == "the lead reassigns it to B" {
					// With no task named, the member whose only task moved away
					// simply has none in progress any more: a 400 that names no
					// foreign task.
					var e team.APIError
					if code != 400 || json.Unmarshal(raw, &e) != nil || e.Error != team.ErrBadRequest || strings.Contains(e.Detail, "000000-") {
						t.Fatalf("raced default report = %d %s, want a 400 bad_request naming no task", code, raw)
					}
				} else if code != wantCode || string(raw) != wantBody {
					t.Fatalf("raced report = %d %s, want exactly %d %s", code, raw, wantCode, wantBody)
				}
				if n := reportCount(t, w, uid(1)); n != 0 {
					t.Fatalf("the raced report was stored (%d rows)", n)
				}
				after := mustGetTask(t, w.m.store, uid(1), 1)
				if name == "the lead reassigns it to B" { // the race itself moved the task; the report added nothing
					before.OwnerKey, before.Status, before.UpdatedAt = "op-b", team.TaskPending, after.UpdatedAt
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("the raced report changed the task: %+v -> %+v", before, after)
				}
			})
		}
	}
}

// A retry is answered only to a caller that is still authorised: after the
// task moved away (or the member went), the stored report is not handed back
// with a 200. Mutation gate: look the report up before the owner check →
// every row red.
func TestReports_ReplayRacesOwnerState(t *testing.T) {
	for _, name := range raceNames() {
		t.Run(name, func(t *testing.T) {
			w := newTaskWorld(t)
			tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
			req := rreq(1, team.ReportProgress, tk.ID)
			w.mustReport(maInbox, req)
			wantCode, wantBody := w.missing(http.MethodPost, "/api/team/reports",
				team.CreateReportRequest{OriginInbox: maInbox, ReportRequest: rreq(1, team.ReportProgress, "000000-999")})

			race := reportRaces(w, tk.ID)[name]
			w.m.afterTaskLookup = func() { w.m.afterTaskLookup = nil; race() }
			code, raw := w.do(http.MethodPost, "/api/team/reports", team.CreateReportRequest{OriginInbox: maInbox, ReportRequest: req})
			if code != wantCode || string(raw) != wantBody {
				t.Fatalf("raced replay = %d %s, want exactly %d %s", code, raw, wantCode, wantBody)
			}
		})
	}
}

// The plain form of the same rule: the lead hands the task to B, then the old
// owner retries its report.
func TestReports_ReplayByTheOldOwnerAfterReassignIsNotFound(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	req := rreq(1, team.ReportAck, tk.ID)
	w.mustReport(maInbox, req)
	if code, _, e := w.reassign(leadInbox, tk.ID, w.mb.Ref); code != 200 {
		t.Fatalf("reassign: %d %+v", code, e)
	}
	code, _, e := w.postReport(maInbox, req)
	wantErr(t, "old owner's replay", code, e, 409, team.ErrTaskNotFound)
}

// With no task named, the default is the one in progress where the report is
// WRITTEN, not where the handler looked: between the two the member finished
// task 1 and started task 2, and the report belongs to task 2. Mutation gate:
// pick the default before the store call (outside its transaction) → red.
func TestReports_DefaultTaskFollowsTheMemberUntilTheWrite(t *testing.T) {
	w := newTaskWorld(t)
	t1 := w.mustTask(leadInbox, w.ma.Ref, "one", nil)
	t2 := w.mustTask(leadInbox, w.ma.Ref, "two", nil)
	if code, _, e := w.setStatus(maInbox, t1.ID, team.TaskInProgress); code != 200 {
		t.Fatalf("start one: %d %+v", code, e)
	}
	w.m.afterTaskLookup = func() {
		w.m.afterTaskLookup = nil
		if code, _, e := w.setStatus(leadInbox, t1.ID, team.TaskCompleted); code != 200 {
			t.Fatalf("finish one: %d %+v", code, e)
		}
		if code, _, e := w.setStatus(maInbox, t2.ID, team.TaskInProgress); code != 200 {
			t.Fatalf("start two: %d %+v", code, e)
		}
	}
	out := w.mustReport(maInbox, rreq(1, team.ReportReady, ""))
	if out.Report.Task != t2.ID || out.Task.ID != t2.ID || !slices.Equal(out.Task.Metadata.PRs, []int{12}) {
		t.Fatalf("the report landed on %s / %+v, want task %s", out.Report.Task, out.Task, t2.ID)
	}
	if got := mustGetTask(t, w.m.store, uid(1), 1); got.LastReportAt != 0 || len(got.Metadata.PRs) != 0 {
		t.Fatalf("the finished task 1 was reported on: %+v", got)
	}
}

// If two tasks are in progress at the write the report is refused, 400, naming
// both, and nothing is written.
func TestReports_DefaultTaskAmbiguousAtTheWriteWritesNothing(t *testing.T) {
	w := newTaskWorld(t)
	t1 := w.mustTask(leadInbox, w.ma.Ref, "one", nil)
	t2 := w.mustTask(leadInbox, w.ma.Ref, "two", nil)
	if code, _, e := w.setStatus(maInbox, t1.ID, team.TaskInProgress); code != 200 {
		t.Fatalf("start one: %d %+v", code, e)
	}
	w.m.afterTaskLookup = func() {
		w.m.afterTaskLookup = nil
		if code, _, e := w.setStatus(maInbox, t2.ID, team.TaskInProgress); code != 200 {
			t.Fatalf("start two: %d %+v", code, e)
		}
	}
	code, _, e := w.postReport(maInbox, rreq(1, team.ReportProgress, ""))
	wantErr(t, "two in progress at the write", code, e, 400, team.ErrBadRequest)
	if want := "several tasks are in progress: " + t1.ID + ", " + t2.ID + "; pass task=<id>"; e.Detail != want {
		t.Fatalf("detail = %q, want %q", e.Detail, want)
	}
	if n := reportCount(t, w, uid(1)); n != 0 {
		t.Fatalf("%d reports stored", n)
	}
}

// A report id belongs to the member that minted it: another member of the
// team using the same id neither collides nor learns that it exists.
// Mutation gate: leave member_key out of the replay lookup → red.
func TestReports_SameIDFromAnotherMemberIsIndependent(t *testing.T) {
	w := newTaskWorld(t)
	ta := w.mustTask(leadInbox, w.ma.Ref, "a's", nil)
	tb := w.mustTask(leadInbox, w.mb.Ref, "b's", nil)
	a := w.mustReport(maInbox, rreq(1, team.ReportAck, ta.ID))
	other := rreq(1, team.ReportDone, tb.ID) // the same id, other content, other member
	other.Summary = "b's own words"
	b := w.mustReport(mbInbox, other)
	if a.Report.Member.Ref != w.ma.Ref || b.Report.Member.Ref != w.mb.Ref || b.Report.Summary != "b's own words" {
		t.Fatalf("a = %+v\nb = %+v", a.Report, b.Report)
	}
	if n := reportCount(t, w, uid(1)); n != 2 {
		t.Fatalf("%d rows, want one per member", n)
	}
	if got := mustGetTask(t, w.m.store, uid(1), 1); got.Status != team.TaskInProgress {
		t.Fatalf("b's report moved a's task: %+v", got)
	}
	// Each member's retry is a replay; the same member with other content is a conflict.
	if code, _, e := w.postReport(mbInbox, other); code != http.StatusOK {
		t.Fatalf("b's retry = %d %+v", code, e)
	}
	changed := rreq(1, team.ReportAck, ta.ID)
	changed.Summary = "changed"
	code, _, e := w.postReport(maInbox, changed)
	wantErr(t, "a reuses its own id", code, e, 409, team.ErrIDConflict)
}

// With the lead missing from the registry, its address is what its request
// recorded while the ref is still the recorded one (the roster's rule), else
// <alias>/<ref>.
func TestReports_LeadNotInTheRegistryUsesTheRecordedAddress(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	alias, _ := w.m.selfHost()
	origin, _ := json.Marshal(team.Origin{SessionID: "sid-1", Ref: "_abc123", Address: "recorded/boss", PID: 10, Cwd: "/w"})
	if _, err := w.m.store.db.Exec(`UPDATE approval_requests SET origin_json = ? WHERE id = ?`, string(origin), uid(1)); err != nil {
		t.Fatal(err)
	}
	w.origins.hide("sid-1")

	if out := w.mustReport(maInbox, rreq(1, team.ReportAck, tk.ID)); out.Lead != (team.ReportLead{Ref: "_abc123", Address: "recorded/boss"}) {
		t.Fatalf("ref unchanged: lead = %+v, want the recorded address", out.Lead)
	}
	// The lead relayed: its ref moved, the recorded address is stale.
	if _, err := w.m.store.db.Exec(`UPDATE teams SET lead_ref = '_new999' WHERE id = ?`, uid(1)); err != nil {
		t.Fatal(err)
	}
	if out := w.mustReport(maInbox, rreq(2, team.ReportProgress, tk.ID)); out.Lead != (team.ReportLead{Ref: "_new999", Address: alias + "/_new999"}) {
		t.Fatalf("ref moved: lead = %+v, want %s/_new999", out.Lead, alias)
	}
}
