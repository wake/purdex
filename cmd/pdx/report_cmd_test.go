package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

const (
	fakeReportID  = "11111111-2222-4333-8444-555555555555"
	fakeLeadRef   = "_lead01"
	fakeLeadAddr  = "mlab/_lead01"
	reportUpAck   = "[report ack 8f2c0f-3] starting now"
	reportOtherID = "99999999-2222-4333-8444-555555555555"
)

// fakeReportDaemon speaks POST/GET /api/team/reports and /api/peers/send.
// The first POST of an id answers 201, a later one 200 (a replay); post
// overrides the answer.
type fakeReportDaemon struct {
	mu       sync.Mutex
	requests int
	posts    []team.CreateReportRequest
	queries  []url.Values
	sendReq  []ipeers.SendRequest
	seen     map[string]bool

	post     func(team.CreateReportRequest) *answer
	list     answer
	sendCode int
}

func (f *fakeReportDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/team/reports":
		var req team.CreateReportRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.posts = append(f.posts, req)
		if f.post != nil {
			if a := f.post(req); a != nil {
				write(w, *a)
				return
			}
		}
		if f.seen == nil {
			f.seen = map[string]bool{}
		}
		st := http.StatusCreated
		if f.seen[req.ID] {
			st = http.StatusOK
		}
		f.seen[req.ID] = true
		task := req.Task
		if task == "" {
			task = fakeTaskID
		}
		rep := team.Report{ID: req.ID, Task: task, Kind: req.Kind, Summary: req.Summary, Needs: req.Needs, PR: req.PR,
			Reviews: req.Reviews, SHA: req.SHA, Body: req.Body, Member: fakeOwner(), CreatedAt: 1}
		write(w, answer{status: st, body: team.ReportResponse{Report: rep,
			Task: fakeTask(task, team.TaskInProgress, "S"), Lead: team.ReportLead{Ref: fakeLeadRef, Address: fakeLeadAddr}}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/team/reports":
		f.queries = append(f.queries, r.URL.Query())
		if r.URL.Query().Get("task") == "" { // the real handler: a member must pass task
			write(w, answer{status: http.StatusBadRequest, body: team.APIError{Error: team.ErrBadRequest, Detail: "pass task=<id>"}})
			return
		}
		write(w, f.list)
	case r.Method == http.MethodPost && r.URL.Path == "/api/peers/send":
		var req ipeers.SendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.sendReq = append(f.sendReq, req)
		if f.sendCode != 0 {
			write(w, answer{status: f.sendCode, body: team.APIError{Error: "peer_unreachable", Detail: "lead gone"}})
			return
		}
		write(w, answer{body: map[string]any{"ok": true}})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeReportDaemon) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func driveReport(t *testing.T, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runReportCmd(context.Background(), append(append([]string{}, args...), "--config", cfgPath), leadEnv(), &stdout, &stderr,
		leadClockOpt(), leadNoKeepAlive())
	return code, stdout.String(), stderr.String()
}

func fixedReportID(t *testing.T, id string) {
	t.Helper()
	old := reportNewID
	reportNewID = func() string { return id }
	t.Cleanup(func() { reportNewID = old })
}

// Plan T-1d: each kind's required fields are checked before any call, the
// field named (exit 2); a field on a kind that does not take it too.
func TestReport_PerKindRequiredFieldsExit2(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"ack no summary":      {[]string{"ack"}, "summary"},
		"question no needs":   {[]string{"question", "--summary", "s"}, "needs"},
		"blocked bad needs":   {[]string{"blocked", "--summary", "s", "--needs", "x"}, "needs"},
		"ready no pr":         {[]string{"ready", "--summary", "s", "--reviews", "R1=j"}, "pr"},
		"ready no reviews":    {[]string{"ready", "--summary", "s", "--pr", "5"}, "reviews"},
		"ready bad review":    {[]string{"ready", "--summary", "s", "--pr", "5", "--reviews", "R1"}, "reviews[0]"},
		"merged no sha":       {[]string{"merged", "--summary", "s", "--pr", "5"}, "sha"},
		"merged bad sha":      {[]string{"merged", "--summary", "s", "--pr", "5", "--sha", "zz"}, "sha"},
		"done with pr":        {[]string{"done", "--summary", "s", "--pr", "5"}, "pr"},
		"progress with needs": {[]string{"progress", "--summary", "s", "--needs", "lead"}, "needs"},
		"bad task id":         {[]string{"ack", "--summary", "s", "--task", "nope"}, "--task"},
		"bad report id":       {[]string{"ack", "--summary", "s", "--id", "not-a-uuid"}, "--id"},
		"file and text":       {[]string{"ack", "--summary", "s", "--file", "x", "--text", "y"}, "--file"},
		"unknown kind":        {[]string{"bogus", "--summary", "s"}, "bogus"},
	} {
		d := &fakeReportDaemon{}
		code, stdout, stderr := driveReport(t, d, c.args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx report: ") || !strings.Contains(stderr, c.want) || d.count() != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q requests=%d", name, code, stdout, stderr, d.count())
		}
	}
}

// The report is posted with the id the CLI minted, then the lead gets the up
// message, once, from the member's inbox.
func TestReport_SendsTheUpMessage(t *testing.T) {
	fixedReportID(t, fakeReportID)
	d := &fakeReportDaemon{}
	code, stdout, stderr := driveReport(t, d, "ack", "--task", fakeTaskID, "--summary", "starting now")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if stdout != "reported ack 8f2c0f-3 (in_progress) → _lead01: starting now\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if len(d.posts) != 1 || d.posts[0].ID != fakeReportID || d.posts[0].OriginInbox != fakeInbox || d.posts[0].Task != fakeTaskID || d.posts[0].Kind != team.ReportAck {
		t.Fatalf("posts = %+v", d.posts)
	}
	if len(d.sendReq) != 1 || d.sendReq[0].To != fakeLeadAddr || d.sendReq[0].OriginInbox != fakeInbox || d.sendReq[0].Text != reportUpAck {
		t.Fatalf("send requests = %+v, want one to %s with %q", d.sendReq, fakeLeadAddr, reportUpAck)
	}

	// A ready report carries its fields and the body from --text.
	d = &fakeReportDaemon{}
	code, _, stderr = driveReport(t, d, "ready", "--summary", "PR up", "--pr", "123", "--reviews", "R1=job-a", "--reviews", "R2=job-b", "--text", "findings: none")
	want := "[report ready 8f2c0f-3] PR up\npr: #123\nreviews: R1=job-a R2=job-b\n\nfindings: none"
	if code != ExitOK || stderr != "" || len(d.sendReq) != 1 || d.sendReq[0].Text != want {
		t.Fatalf("ready: code=%d stderr=%q sends=%+v want %q", code, stderr, d.sendReq, want)
	}
	if d.posts[0].Task != "" {
		t.Errorf("no --task must post an empty task (the daemon picks), got %q", d.posts[0].Task)
	}
}

// A failed send (refused, daemon unavailable) leaves the report stored: exit
// 1, the answer's JSON on stdout, the manual command on stderr. `--id <rid>`
// repeats the command: the daemon replays (nothing new stored) and the CLI
// sends again.
func TestReport_SendFailures(t *testing.T) {
	fixedReportID(t, fakeReportID)
	d := &fakeReportDaemon{sendCode: http.StatusConflict}
	code, stdout, stderr := driveReport(t, d, "ack", "--task", fakeTaskID, "--summary", "starting now")
	if code != ExitError {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var resp team.ReportResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil || resp.Report.ID != fakeReportID {
		t.Errorf("stdout is not the answer's JSON: %q (%v)", stdout, err)
	}
	cmd := resendCommand(t, stderr)
	if !strings.Contains(cmd, "pdx msg send") || !strings.Contains(cmd, "'"+fakeLeadAddr+"'") ||
		!strings.Contains(cmd, "pdx report show") || !strings.Contains(cmd, "--task '"+fakeTaskID+"'") || !strings.Contains(cmd, fakeReportID) || !strings.Contains(cmd, "--message") {
		t.Errorf("manual command = %q", cmd)
	}
	if err := shellSyntaxOK(cmd); err != nil {
		t.Errorf("manual command does not parse: %v: %q", err, cmd)
	}

	// The retry keeps the id: the daemon answers 200 (a replay), the CLI sends.
	d.sendCode = 0
	d2 := &fakeReportDaemon{seen: map[string]bool{fakeReportID: true}}
	fixedReportID(t, reportOtherID) // a minted id would show up as a different one
	code, _, stderr = driveReport(t, d2, "ack", "--task", fakeTaskID, "--summary", "starting now", "--id", fakeReportID)
	if code != ExitOK || stderr != "" || len(d2.posts) != 1 || d2.posts[0].ID != fakeReportID || len(d2.sendReq) != 1 {
		t.Fatalf("retry: code=%d stderr=%q posts=%+v sends=%d", code, stderr, d2.posts, len(d2.sendReq))
	}

	// No lead address in the answer: stored, not sent, the placeholder.
	d3 := &fakeReportDaemon{post: func(req team.CreateReportRequest) *answer {
		return &answer{status: http.StatusCreated, body: team.ReportResponse{
			Report: team.Report{ID: req.ID, Task: fakeTaskID, Kind: req.Kind, Summary: req.Summary}, Task: fakeTask(fakeTaskID, team.TaskInProgress, "S")}}
	}}
	code, stdout, stderr = driveReport(t, d3, "ack", "--summary", "s")
	if code != ExitError || stdout == "" || !strings.Contains(stderr, "<ADDRESS>") || len(d3.sendReq) != 0 {
		t.Errorf("no address: code=%d stdout=%q stderr=%q sends=%d", code, stdout, stderr, len(d3.sendReq))
	}
}

// Refusals: a lead (not_member) and the other team codes are exit 13 with the
// code last; the daemon's 400 naming the ambiguous default task is exit 1.
func TestReport_LeadIsRefused(t *testing.T) {
	for _, c := range []string{team.ErrNotMember, team.ErrTaskNotFound} {
		d := &fakeReportDaemon{post: func(team.CreateReportRequest) *answer {
			return &answer{status: http.StatusConflict, body: team.APIError{Error: c, Detail: "no"}}
		}}
		code, stdout, stderr := driveReport(t, d, "ack", "--summary", "s")
		if code != ExitRefused || stdout != "" || lastToken(stderr) != c || len(d.sendReq) != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q sends=%d", c, code, stdout, stderr, len(d.sendReq))
		}
	}
}

func TestReport_DefaultTaskAskedWhenAmbiguous(t *testing.T) {
	d := &fakeReportDaemon{post: func(team.CreateReportRequest) *answer {
		return &answer{status: http.StatusBadRequest, body: team.APIError{Error: team.ErrBadRequest, Detail: "two tasks in progress: 8f2c0f-3, 8f2c0f-4; say --task"}}
	}}
	code, _, stderr := driveReport(t, d, "progress", "--summary", "s")
	if code != ExitError || !strings.Contains(stderr, "say --task") || len(d.sendReq) != 0 {
		t.Errorf("code=%d stderr=%q sends=%d", code, stderr, len(d.sendReq))
	}
}

func fakeReports() team.ReportList {
	return team.ReportList{Reports: []team.Report{
		{ID: fakeReportID, Task: fakeTaskID, Kind: team.ReportReady, Summary: "PR up", PR: 7, Reviews: []string{"R1=j"}, Member: fakeOwner(), CreatedAt: 1},
		{ID: reportOtherID, Task: fakeTaskID, Kind: team.ReportAck, Summary: "esc\x1b[31m", Member: fakeOwner(), CreatedAt: 1},
	}}
}

func TestReportLs_TableQueryAndJSON(t *testing.T) {
	fakeTaskClock(t)
	d := &fakeReportDaemon{list: answer{body: fakeReports()}}
	code, stdout, stderr := driveReport(t, d, "ls", "--task", fakeTaskID, "--since", "2h")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	q := d.queries[0]
	if q.Get("task") != fakeTaskID || q.Get("origin_inbox") != fakeInbox {
		t.Errorf("query = %v", q)
	}
	if since := q.Get("since"); since == "" || since == "0" {
		t.Errorf("--since not sent: %v", q)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 || strings.Fields(lines[0])[0] != "ID" || !strings.Contains(lines[1], "ready") || !strings.Contains(lines[1], "PR up") {
		t.Errorf("table = %q", stdout)
	}
	if strings.ContainsRune(stdout, 0x1b) {
		t.Errorf("a control character reached the terminal: %q", stdout)
	}

	code, stdout, _ = driveReport(t, &fakeReportDaemon{list: answer{body: fakeReports()}}, "ls", "--json", "--task", fakeTaskID)
	var back team.ReportList
	if code != ExitOK || json.Unmarshal([]byte(stdout), &back) != nil || len(back.Reports) != 2 || strings.Count(stdout, "\n") != 1 {
		t.Errorf("--json: code=%d stdout=%q", code, stdout)
	}
	for _, bad := range [][]string{{"ls", "--since", "-5m"}, {"ls", "--since", "soon"}, {"ls", "--task", "nope"}, {"ls", "extra"}} {
		d := &fakeReportDaemon{}
		if code, _, stderr := driveReport(t, d, bad...); code != ExitUsage || d.count() != 0 {
			t.Errorf("%v: code=%d stderr=%q requests=%d", bad, code, stderr, d.count())
		}
	}
	// since is "now - dur": with a fixed clock the value is exact.
	old := reportNow
	reportNow = func() time.Time { return time.UnixMilli(10_000_000) }
	t.Cleanup(func() { reportNow = old })
	d = &fakeReportDaemon{list: answer{body: fakeReports()}}
	driveReport(t, d, "ls", "--since", "1m", "--task", fakeTaskID)
	if got := d.queries[0].Get("since"); got != "9940000" {
		t.Errorf("since = %q, want 9940000", got)
	}
}

func TestReportShow_MessageAndNotFound(t *testing.T) {
	d := &fakeReportDaemon{list: answer{body: fakeReports()}}
	code, stdout, stderr := driveReport(t, d, "show", fakeReportID, "--task", fakeTaskID, "--message")
	want := "[report ready 8f2c0f-3] PR up\npr: #7\nreviews: R1=j\n"
	if code != ExitOK || stderr != "" || stdout != want {
		t.Errorf("--message: code=%d stdout=%q stderr=%q want %q", code, stdout, stderr, want)
	}
	code, stdout, _ = driveReport(t, d, "show", fakeReportID, "--task", fakeTaskID, "--json")
	var r team.Report
	if code != ExitOK || json.Unmarshal([]byte(stdout), &r) != nil || r.ID != fakeReportID {
		t.Errorf("--json: code=%d stdout=%q", code, stdout)
	}
	code, stdout, stderr = driveReport(t, d, "show", "aaaaaaaa-2222-4333-8444-555555555555", "--task", fakeTaskID)
	if code != ExitError || stdout != "" || lastToken(stderr) != "report_not_found" {
		t.Errorf("unknown id: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, bad := range [][]string{{"show"}, {"show", "x"}, {"show", fakeReportID, "--json", "--message"}, {"show", fakeReportID, "--task", "nope"}} {
		d := &fakeReportDaemon{}
		if code, _, _ := driveReport(t, d, bad...); code != ExitUsage || d.count() != 0 {
			t.Errorf("%v: code=%d requests=%d", bad, code, d.count())
		}
	}
}

func TestMainUsage_ListsReport(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "case \"report\":\n\t\trunReport(os.Args[2:])\n") || !strings.Contains(src, " report,") {
		t.Errorf("main.go does not dispatch or list report")
	}
}

// A member's list and show need --task (the daemon refuses without it): the
// failure is the daemon's 400, exit 1, naming the flag; with the task the
// manual command's show works.
func TestReportLsShow_MemberWithoutTaskIsTheDaemons400(t *testing.T) {
	for _, args := range [][]string{{"ls"}, {"show", fakeReportID}} {
		d := &fakeReportDaemon{list: answer{body: fakeReports()}}
		code, stdout, stderr := driveReport(t, d, args...)
		if code != ExitError || stdout != "" || !strings.Contains(stderr, "pass task=<id>") {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}
