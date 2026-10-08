package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

const (
	fakeTaskID    = "8f2c0f-3"
	fakeOwnerRef  = "_m1m1m1"
	fakeOwnerAddr = "mlab/_m1m1m1"
	fakeInbox     = "/tmp/cc-socks/1.sock"
)

type taskStatusCall struct {
	id  string
	req team.TaskStatusRequest
}

type taskReassignCall struct {
	id  string
	req team.ReassignTaskRequest
}

// fakeTaskDaemon speaks the routes `pdx task` calls and /api/peers/send. A
// nil answer function means the default for that route; every request is
// recorded. sendMode(n) is how the n-th send ends: "" answers send, "drop"
// closes the connection, "hold" never answers.
type fakeTaskDaemon struct {
	mu          sync.Mutex
	requests    int
	createReq   []team.CreateTaskRequest
	statusReq   []taskStatusCall
	reassignReq []taskReassignCall
	sendReq     []ipeers.SendRequest
	listQueries []url.Values
	detailPaths []string

	create   func(team.CreateTaskRequest) answer
	status   func(id string, req team.TaskStatusRequest) answer
	reassign func(id string, req team.ReassignTaskRequest) answer
	list     answer
	detail   answer
	send     answer
	sendMode func(n int) string
}

func fakeOwner() team.TaskOwner {
	return team.TaskOwner{Ref: fakeOwnerRef, Address: fakeOwnerAddr, State: "active"}
}

func fakeTask(id string, st team.TaskStatus, subject string) team.Task {
	return team.Task{ID: id, TeamID: fakeTeamID, Subject: subject, DoneWhen: []string{}, Status: st, Owner: fakeOwner(),
		Blocks: []string{}, BlockedBy: []string{}, CreatedBy: "_lead01", CreatedAt: 1, UpdatedAt: 1}
}

func (f *fakeTaskDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/api/team/tasks":
		var req team.CreateTaskRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.createReq = append(f.createReq, req)
		if f.create != nil {
			write(w, f.create(req))
			return
		}
		t := fakeTask(fakeTaskID, team.TaskPending, req.Subject)
		t.Description = req.Description
		if req.DoneWhen != nil {
			t.DoneWhen = req.DoneWhen
		}
		if req.BlockedBy != nil {
			t.BlockedBy, t.Blocked = req.BlockedBy, true
		}
		write(w, answer{status: http.StatusCreated, body: t})
	case r.Method == http.MethodGet && path == "/api/team/tasks":
		f.listQueries = append(f.listQueries, r.URL.Query())
		write(w, f.list)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/team/tasks/"):
		f.detailPaths = append(f.detailPaths, path+"?"+r.URL.RawQuery)
		write(w, f.detail)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/status"):
		var req team.TaskStatusRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/team/tasks/"), "/status")
		f.statusReq = append(f.statusReq, taskStatusCall{id, req})
		if f.status != nil {
			write(w, f.status(id, req))
			return
		}
		write(w, answer{body: fakeTask(id, req.Status, "S")})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/reassign"):
		var req team.ReassignTaskRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/team/tasks/"), "/reassign")
		f.reassignReq = append(f.reassignReq, taskReassignCall{id, req})
		if f.reassign != nil {
			write(w, f.reassign(id, req))
			return
		}
		t := fakeTask(id, team.TaskPending, "S")
		t.Owner = team.TaskOwner{Ref: "_n2n2n2", Address: "mlab/_n2n2n2", State: "active"}
		write(w, answer{body: t})
	case r.Method == http.MethodPost && path == "/api/peers/send":
		var req ipeers.SendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.sendReq = append(f.sendReq, req)
		mode := ""
		if f.sendMode != nil {
			mode = f.sendMode(len(f.sendReq))
		}
		switch mode {
		case "hold":
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
		case "drop":
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
		default:
			write(w, f.send)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeTaskDaemon) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// driveTask runs `pdx task` against d, as the lead, with a fake clock and no
// keep-alive (so a dropped connection is seen as one).
func driveTask(t *testing.T, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	return driveTeamCmdWith(t, runTaskCmd, d, leadEnv(), []daemonclient.Option{leadClockOpt(), leadNoKeepAlive()}, args...)
}

func fastBrief(t *testing.T) {
	t.Helper()
	old := briefTimeout
	briefTimeout = 100 * time.Millisecond
	t.Cleanup(func() { briefTimeout = old })
}

// The down message is spelled out here, not built from the constants.
const addDownMessage = "[pdx task 8f2c0f-3] Fix the build\n\nDo it.\n\n完成定義：\n- tests pass\n- PR open\n\n" +
	"回報：pdx report ack|progress|ready|done --task 8f2c0f-3 --summary \"…\"（見 pdx-team skill）"

var addArgs = []string{"add", "--to", fakeOwnerRef, "--subject", "Fix the build", "--brief", "Do it.",
	"--done-when", "tests pass", "--done-when", "PR open", "--blocked-by", "8f2c0f-1", "--blocked-by", "8f2c0f-2"}

// Plan T-1c: add posts the task, then sends the down message to the owner's
// address from the lead's inbox, once. A first send that dies mid-way is
// reported, not replayed: it may have arrived.
func TestTaskAdd_SendsTheDownMessageOnce(t *testing.T) {
	d := &fakeTaskDaemon{}
	code, stdout, stderr := driveTask(t, d, addArgs...)
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if stdout != "created 8f2c0f-3 (pending) → _m1m1m1: Fix the build\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if len(d.createReq) != 1 {
		t.Fatalf("create requests = %d, want 1", len(d.createReq))
	}
	got := d.createReq[0]
	if got.OriginInbox != fakeInbox || got.To != fakeOwnerRef || got.Subject != "Fix the build" || got.Description != "Do it." ||
		strings.Join(got.DoneWhen, "|") != "tests pass|PR open" || strings.Join(got.BlockedBy, "|") != "8f2c0f-1|8f2c0f-2" {
		t.Errorf("create request = %+v", got)
	}
	if len(d.sendReq) != 1 || d.sendReq[0].To != fakeOwnerAddr || d.sendReq[0].OriginInbox != fakeInbox || d.sendReq[0].Text != addDownMessage {
		t.Fatalf("send requests = %+v, want one to %s with:\n%s", d.sendReq, fakeOwnerAddr, addDownMessage)
	}

	// The first send dies after it went out; a replaying client would send again.
	fastBrief(t)
	d2 := &fakeTaskDaemon{sendMode: func(n int) string {
		if n == 1 {
			return "drop"
		}
		return ""
	}}
	code, stdout, stderr = driveTask(t, d2, addArgs...)
	if code != ExitError || len(d2.sendReq) != 1 {
		t.Fatalf("a dropped first send: code=%d sends=%d stdout=%q stderr=%q", code, len(d2.sendReq), stdout, stderr)
	}
}

// --json prints the Task the daemon made, and the message is still sent.
func TestTaskAdd_JSONPrintsTheTask(t *testing.T) {
	d := &fakeTaskDaemon{}
	code, stdout, stderr := driveTask(t, d, "add", "--to", fakeOwnerRef, "--subject", "S", "--json")
	if code != ExitOK || stderr != "" || len(d.sendReq) != 1 {
		t.Fatalf("code=%d stderr=%q sends=%d", code, stderr, len(d.sendReq))
	}
	var task team.Task
	if strings.Count(stdout, "\n") != 1 || json.Unmarshal([]byte(stdout), &task) != nil || task.ID != fakeTaskID || task.Subject != "S" {
		t.Errorf("stdout = %q", stdout)
	}
	if len(d.createReq[0].DoneWhen) != 0 || len(d.createReq[0].BlockedBy) != 0 || d.createReq[0].Description != "" {
		t.Errorf("create request = %+v, want no optional fields", d.createReq[0])
	}
}

// --brief-file is the description as is; the message drops its final newline.
func TestTaskAdd_BriefFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(f, []byte("Line one.\nLine two.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &fakeTaskDaemon{}
	if code, _, stderr := driveTask(t, d, "add", "--to", fakeOwnerRef, "--subject", "S", "--brief-file", f); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if d.createReq[0].Description != "Line one.\nLine two.\n" {
		t.Errorf("description = %q", d.createReq[0].Description)
	}
	if want := "[pdx task 8f2c0f-3] S\n\nLine one.\nLine two.\n\n回報："; !strings.HasPrefix(d.sendReq[0].Text, want) {
		t.Errorf("text = %q, want prefix %q", d.sendReq[0].Text, want)
	}
}

// A send that fails leaves the task stored: exit 1, the task JSON on stdout,
// one stderr line with the reason, the manual command and the code last.
func TestTaskAdd_SendFailures(t *testing.T) {
	fastBrief(t)
	for code, d := range map[string]*fakeTaskDaemon{
		"text_invalid":       {send: answer{status: http.StatusBadRequest, body: ipeers.APIError{Error: "text_invalid", Detail: "bad"}}},
		"not_ready":          {send: answer{status: http.StatusServiceUnavailable, body: ipeers.APIError{Error: "not_ready", Detail: "retry"}}},
		"host_unknown":       {send: answer{status: http.StatusNotFound, body: ipeers.APIError{Error: "host_unknown", Detail: "no peer host"}}},
		"unsupported":        {send: answer{status: http.StatusNotFound, body: "404 page not found"}},
		"no_answer":          {sendMode: func(int) string { return "hold" }},
		"daemon_unavailable": {sendMode: func(int) string { return "drop" }},
	} {
		exit, stdout, stderr := driveTask(t, d, "add", "--to", fakeOwnerRef, "--subject", "S")
		if exit != ExitError || len(d.sendReq) != 1 || len(d.createReq) != 1 {
			t.Errorf("%s: code=%d sends=%d creates=%d", code, exit, len(d.sendReq), len(d.createReq))
		}
		var task team.Task
		if strings.Count(stdout, "\n") != 1 || json.Unmarshal([]byte(stdout), &task) != nil || task.ID != fakeTaskID {
			t.Errorf("%s: stdout = %q, want the task JSON", code, stdout)
		}
		if strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "pdx task: ") ||
			!strings.Contains(stderr, `pdx msg send mlab/_m1m1m1 "$(pdx task show 8f2c0f-3 --message)"`) || lastToken(stderr) != code {
			t.Errorf("%s: stderr = %q", code, stderr)
		}
	}
}

// reassign sends the same message to the NEW owner; a failed send is the same
// exit 1 with the new owner's address in the manual command.
func TestTaskReassign_SendFailure(t *testing.T) {
	d := &fakeTaskDaemon{}
	code, stdout, stderr := driveTask(t, d, "reassign", fakeTaskID, "--to", "_n2n2n2")
	if code != ExitOK || stderr != "" || stdout != "reassigned 8f2c0f-3 (pending) → _n2n2n2: S\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(d.reassignReq) != 1 || d.reassignReq[0].id != fakeTaskID || d.reassignReq[0].req.To != "_n2n2n2" || d.reassignReq[0].req.OriginInbox != fakeInbox {
		t.Errorf("reassign requests = %+v", d.reassignReq)
	}
	if len(d.sendReq) != 1 || d.sendReq[0].To != "mlab/_n2n2n2" || !strings.HasPrefix(d.sendReq[0].Text, "[pdx task 8f2c0f-3] S\n\n回報：") {
		t.Errorf("send requests = %+v", d.sendReq)
	}

	fastBrief(t)
	d = &fakeTaskDaemon{send: answer{status: http.StatusServiceUnavailable, body: ipeers.APIError{Error: "not_ready", Detail: "retry"}}}
	code, stdout, stderr = driveTask(t, d, "reassign", fakeTaskID, "--to", "_n2n2n2")
	var task team.Task
	if code != ExitError || json.Unmarshal([]byte(stdout), &task) != nil || task.Owner.Ref != "_n2n2n2" ||
		!strings.Contains(stderr, `pdx msg send mlab/_n2n2n2 "$(pdx task show 8f2c0f-3 --message)"`) || lastToken(stderr) != "not_ready" {
		t.Errorf("failed send: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// Every field the daemon would refuse is exit 2 before any request.
func TestTaskAdd_FieldErrorsAreExit2BeforeAnyCall(t *testing.T) {
	big := filepath.Join(t.TempDir(), "big.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", team.MaxTaskDescriptionLen+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(t.TempDir(), "small.md")
	if err := os.WriteFile(small, []byte("do it"), 0o600); err != nil {
		t.Fatal(err)
	}
	many := []string{"add", "--to", fakeOwnerRef, "--subject", "S"}
	for i := 0; i <= team.MaxDoneWhenLines; i++ {
		many = append(many, "--done-when", "line")
	}
	base := func(extra ...string) []string {
		return append([]string{"add", "--to", fakeOwnerRef, "--subject", "S"}, extra...)
	}
	for name, args := range map[string][]string{
		"empty subject":        {"add", "--to", fakeOwnerRef, "--subject", ""},
		"no subject":           {"add", "--to", fakeOwnerRef},
		"81-rune subject":      {"add", "--to", fakeOwnerRef, "--subject", strings.Repeat("語", 81)},
		"newline in subject":   {"add", "--to", fakeOwnerRef, "--subject", "a\nb"},
		"control in subject":   {"add", "--to", fakeOwnerRef, "--subject", "a\x1bb"},
		"11 done-when lines":   many,
		"newline in done-when": base("--done-when", "a\nb"),
		"long done-when":       base("--done-when", strings.Repeat("x", 201)),
		"brief too big":        base("--brief", strings.Repeat("x", team.MaxTaskDescriptionLen+1)),
		"brief file too big":   base("--brief-file", big),
		"brief file missing":   base("--brief-file", filepath.Join(t.TempDir(), "missing.md")),
		"control in brief":     base("--brief", "a\x00b"),
		"brief and file":       base("--brief", "x", "--brief-file", small),
		"bad blocked-by":       base("--blocked-by", "xyz"),
		"blocked-by seq 0":     base("--blocked-by", "8f2c0f-0"),
		"blocked-by uppercase": base("--blocked-by", "8F2C0F-1"),
		"missing --to":         {"add", "--subject", "S"},
		"empty --to":           {"add", "--to", " ", "--subject", "S"},
		"stray argument":       base("extra"),
		"unknown flag":         base("--bogus"),
	} {
		d := &fakeTaskDaemon{}
		code, stdout, stderr := driveTask(t, d, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx task: ") || d.count() != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q requests=%d", name, code, stdout, stderr, d.count())
		}
	}
	// The fields are named.
	for flag, args := range map[string][]string{
		"--subject":    {"add", "--to", fakeOwnerRef, "--subject", ""},
		"--done-when":  base("--done-when", "a\nb"),
		"--brief":      base("--brief", strings.Repeat("x", team.MaxTaskDescriptionLen+1)),
		"--blocked-by": base("--blocked-by", "xyz"),
		"--to":         {"add", "--subject", "S"},
	} {
		if _, _, stderr := driveTask(t, &fakeTaskDaemon{}, args...); !strings.Contains(stderr, flag) {
			t.Errorf("%s: stderr = %q does not name the field", flag, stderr)
		}
	}
}

// The whole down message must fit the peers limit: checked with the longest
// possible id, before any call. (The field limits keep a valid task far
// under it, so the limit is a var here.)
func TestTaskAdd_MessageTooBigIsExit2BeforeAnyCall(t *testing.T) {
	old := taskMessageMaxBytes
	taskMessageMaxBytes = len(team.TaskDownMessage(team.Task{ID: team.TaskWorstCaseID, Subject: "S"}))
	t.Cleanup(func() { taskMessageMaxBytes = old })
	d := &fakeTaskDaemon{}
	if code, _, stderr := driveTask(t, d, "add", "--to", fakeOwnerRef, "--subject", "S"); code != ExitOK {
		t.Fatalf("a message at the limit: code=%d stderr=%q", code, stderr)
	}
	d = &fakeTaskDaemon{}
	code, stdout, stderr := driveTask(t, d, "add", "--to", fakeOwnerRef, "--subject", "S", "--brief", "x")
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "訊息太長") || d.count() != 0 {
		t.Errorf("code=%d stdout=%q stderr=%q requests=%d", code, stdout, stderr, d.count())
	}
}

// start, done and delete post the status each stands for, to the id as given.
func TestTaskStartDoneDelete_PostTheRightStatus(t *testing.T) {
	for verb, want := range map[string]team.TaskStatus{"start": team.TaskInProgress, "done": team.TaskCompleted, "delete": team.TaskDeleted} {
		d := &fakeTaskDaemon{}
		code, stdout, stderr := driveTask(t, d, verb, fakeTaskID)
		if code != ExitOK || stderr != "" || stdout != fakeTaskID+" "+string(want)+"\n" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", verb, code, stdout, stderr)
		}
		if len(d.statusReq) != 1 || d.statusReq[0].id != fakeTaskID || d.statusReq[0].req.Status != want || d.statusReq[0].req.OriginInbox != fakeInbox {
			t.Errorf("%s: status requests = %+v", verb, d.statusReq)
		}
		if len(d.sendReq) != 0 {
			t.Errorf("%s: sent a message", verb)
		}
		code, stdout, _ = driveTask(t, d, verb, fakeTaskID, "--json")
		var task team.Task
		if code != ExitOK || json.Unmarshal([]byte(stdout), &task) != nil || task.Status != want {
			t.Errorf("%s --json: code=%d stdout=%q", verb, code, stdout)
		}
	}
}

// Missing or extra arguments are exit 2 with the usage line, before any call.
func TestTask_UsageErrorsExit2(t *testing.T) {
	for name, args := range map[string][]string{
		"no subcommand":     nil,
		"unknown":           {"frobnicate"},
		"start no id":       {"start"},
		"done two ids":      {"done", "a-1", "b-2"},
		"delete empty id":   {"delete", " "},
		"show no id":        {"show"},
		"reassign no id":    {"reassign", "--to", fakeOwnerRef},
		"reassign no --to":  {"reassign", fakeTaskID},
		"ls stray":          {"ls", "extra"},
		"show json+message": {"show", fakeTaskID, "--json", "--message"},
	} {
		d := &fakeTaskDaemon{}
		code, stdout, stderr := driveTask(t, d, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx task: ") || !strings.Contains(stderr, "usage: pdx task") || d.count() != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q requests=%d", name, code, stdout, stderr, d.count())
		}
	}
}

// Team-rule refusals of the task routes are exit 13 with the code last; other
// codes are exit 1; a daemon without the route is exit 21; no inbox is exit 1.
func TestTask_RefusalsAreExit13(t *testing.T) {
	refused := []string{team.ErrNotLead, team.ErrNotYourMember, team.ErrNotMember, team.ErrTaskNotFound, team.ErrNotTaskOwner,
		team.ErrBadTaskTransition, team.ErrBlockedByUnknown, team.ErrBlockedByCycle, team.ErrOwnerNotActive}
	reply := answer{status: http.StatusConflict}
	for _, c := range refused {
		for _, args := range [][]string{{"add", "--to", fakeOwnerRef, "--subject", "S"}, {"start", fakeTaskID}, {"reassign", fakeTaskID, "--to", "x"}, {"ls"}, {"show", fakeTaskID}} {
			reply.body = team.APIError{Error: c, Detail: "the daemon says so"}
			d := &fakeTaskDaemon{
				create:   func(team.CreateTaskRequest) answer { return reply },
				status:   func(string, team.TaskStatusRequest) answer { return reply },
				reassign: func(string, team.ReassignTaskRequest) answer { return reply },
				list:     reply, detail: reply,
			}
			code, stdout, stderr := driveTask(t, d, args...)
			if code != ExitRefused || stdout != "" || lastToken(stderr) != c || !strings.HasPrefix(stderr, "pdx task: the daemon says so") || len(d.sendReq) != 0 {
				t.Errorf("%s %v: code=%d stdout=%q stderr=%q sends=%d", c, args, code, stdout, stderr, len(d.sendReq))
			}
		}
	}
	other := answer{status: http.StatusBadRequest, body: team.APIError{Error: team.ErrBadRequest, Detail: "bad"}}
	if code, _, stderr := driveTask(t, &fakeTaskDaemon{status: func(string, team.TaskStatusRequest) answer { return other }}, "done", fakeTaskID); code != ExitError || lastToken(stderr) != team.ErrBadRequest {
		t.Errorf("bad_request: code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := driveTask(t, http.NotFoundHandler(), "ls"); code != ExitUnsupported || lastToken(stderr) != "unsupported" {
		t.Errorf("plain 404: code=%d stderr=%q", code, stderr)
	}
	d := &fakeTaskDaemon{}
	code, _, stderr := driveTeamCmdWith(t, runTaskCmd, d, fakeGetenv(nil), []daemonclient.Option{leadClockOpt()}, "ls")
	if code != ExitError || !strings.Contains(stderr, "CLAUDE_CODE_MESSAGING_SOCKET") || d.count() != 0 {
		t.Errorf("no inbox: code=%d stderr=%q requests=%d", code, stderr, d.count())
	}
}

// main.go dispatches `task` to runTask and its Commands line lists it (read
// from source: main() exits, as TestDispatch_SpawnKillTeam).
func TestMainUsage_ListsTask(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "case \"task\":\n\t\trunTask(os.Args[2:])\n") {
		t.Errorf("main.go does not dispatch \"task\" to runTask")
	}
	usage := ""
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "Commands:") {
			usage = l
		}
	}
	if !strings.Contains(usage, " task,") {
		t.Errorf("the Commands line does not list task: %s", usage)
	}
}
