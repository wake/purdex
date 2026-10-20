package testcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

const manifestSrc = `
[tasks.one-file]
run = "tool {files}"
args = "files"
local = true

[tasks.lint]
run = "make lint"
args = "none"
class = "merge"

[tasks.go-all]
run = "go test -json ./..."
args = "none"
class = "release"
`

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repo(t *testing.T, withManifest bool) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	if withManifest {
		os.MkdirAll(filepath.Join(dir, ".wwd"), 0o755)
		os.WriteFile(filepath.Join(dir, ".wwd/tasks.toml"), []byte(manifestSrc), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "a.test.ts"), []byte("x"), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// sharedRepo is one repository with the manifest, made once: most tests only read it (a snapshot never changes the repository).
var sharedRepo string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "testcmd-repo")
	if err != nil {
		panic(err)
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		panic(err)
	}
	sharedRepo = dir
	os.MkdirAll(filepath.Join(dir, ".wwd"), 0o755)
	os.WriteFile(filepath.Join(dir, ".wwd/tasks.toml"), []byte(manifestSrc), 0o644)
	os.WriteFile(filepath.Join(dir, "a.test.ts"), []byte("x"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			panic(fmt.Sprintf("git %v: %v\n%s", args, err, out))
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type fakeAPI struct {
	mu        sync.Mutex
	infoErr   error
	submitErr error
	resp      wire.JobSubmissionResponse
	job       wire.Job
	waitErr   error
	onWait    func()
	bodies    []any
	waited    []string
	submitted int
}

func (a *fakeAPI) Info(context.Context) (wire.Info, error) {
	if a.infoErr != nil {
		return wire.Info{}, a.infoErr
	}
	return wire.Info{Alias: "mlab"}, nil
}

func (a *fakeAPI) Submit(_ context.Context, body any) (wire.JobSubmissionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.submitted++
	a.bodies = append(a.bodies, body)
	if a.submitErr != nil {
		return wire.JobSubmissionResponse{}, a.submitErr
	}
	r := a.resp
	if r.ID == "" {
		r.ID = "j_01JC"
	}
	return r, nil
}

func (a *fakeAPI) WaitJob(ctx context.Context, id string, onUpdate func(wire.Job)) (wire.Job, error) {
	a.mu.Lock()
	a.waited = append(a.waited, id)
	a.mu.Unlock()
	if a.onWait != nil {
		a.onWait()
	}
	if a.waitErr != nil {
		return wire.Job{}, a.waitErr
	}
	if onUpdate != nil {
		q := a.job
		q.State = wire.StateQueued
		onUpdate(q)
		onUpdate(a.job)
	}
	return a.job, nil
}

func doneJob(state wire.JobState, exit *int, reason string, sum *wire.Summary) wire.Job {
	now := time.Now().UTC()
	return wire.Job{ID: "j_01JC", Task: "lint", State: state, Result: &wire.Result{Exit: exit, Summary: sum, Host: "air26", Reason: reason, StartedAt: &now, FinishedAt: &now}}
}

func ip(i int) *int { return &i }

type fakeGate struct {
	calls   int
	code    int
	out     string
	waitMax time.Duration    // the WaitMax of the last call
	last    fastlane.Options // the options of the last call
	onRun   func()           // called while the command "runs" (nil: nothing)
}

func (g *fakeGate) Run(_ context.Context, o fastlane.Options) (int, error) {
	g.calls++
	if g.onRun != nil {
		g.onRun()
	}
	g.waitMax = o.WaitMax
	g.last = o
	for _, kv := range o.Env {
		if nonce, ok := strings.CutPrefix(kv, fastlane.AdmittedMarkEnv+"="); ok {
			o.Stderr.Write([]byte(fastlane.AdmittedMark(nonce)))
		}
	}
	o.Stdout.Write([]byte(g.out))
	return g.code, nil
}

func run(t *testing.T, o Options) (code int, stdout, stderr string) {
	t.Helper()
	var so, se bytes.Buffer
	o.Stdout, o.Stderr = &so, &se
	if o.Dir == "" {
		o.Dir = sharedRepo
	}
	code = Run(context.Background(), o)
	return code, so.String(), se.String()
}

func args(t *testing.T, argv ...string) Args {
	t.Helper()
	a, err := ParseArgs(argv)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseArgs(t *testing.T) {
	ok := map[string]Args{
		"spa-file a.ts b.ts":            {Task: "spa-file", TaskArgs: []string{"a.ts", "b.ts"}},
		"lint --detach --json":          {Task: "lint", Detach: true, JSON: true},
		"--class merge go-pkg ./a":      {Task: "go-pkg", TaskArgs: []string{"./a"}, Class: wire.ClassMerge},
		"go-pkg ./a --class=dev":        {Task: "go-pkg", TaskArgs: []string{"./a"}, Class: wire.ClassDev},
		"spa-file -- --weird.ts --json": {Task: "spa-file", TaskArgs: []string{"--weird.ts", "--json"}},
		"--list":                        {List: true},
		"--list --json":                 {List: true, JSON: true},
		"spa-file --bogus-flag":         {Task: "spa-file", TaskArgs: []string{"--bogus-flag"}},
	}
	for line, want := range ok {
		got, err := ParseArgs(strings.Fields(line))
		if err != nil || got.Task != want.Task || fmt.Sprint(got.TaskArgs) != fmt.Sprint(want.TaskArgs) || got.Class != want.Class ||
			got.Detach != want.Detach || got.JSON != want.JSON || got.List != want.List {
			t.Errorf("%q: got %+v err %v, want %+v", line, got, err, want)
		}
	}
	for _, line := range []string{"", "--detach", "--class", "--class urgent lint", "--list lint", "--list x", "--list --detach", "-h"} {
		if _, err := ParseArgs(strings.Fields(line)); err == nil {
			t.Errorf("%q: accepted", line)
		}
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		r    Report
		want int
	}{
		{Report{State: wire.StateSucceeded, Exit: ip(0)}, 0},
		{Report{State: wire.StateFailed, Exit: ip(2)}, 2},
		{Report{State: wire.StateFailed, Exit: ip(1)}, 1},
		{Report{State: wire.StateFailed, Exit: ip(7)}, 7},
		{Report{State: wire.StateFailed, Exit: ip(100)}, 100},
		{Report{State: wire.StateFailed, Exit: ip(123)}, 123},
		{Report{State: wire.StateFailed, Exit: ip(3)}, 1}, // the numbers pdx test uses itself are never a command's
		{Report{State: wire.StateFailed, Exit: ip(4)}, 1},
		{Report{State: wire.StateFailed, Exit: ip(5)}, 1},
		{Report{State: wire.StateFailed, Exit: ip(6)}, 1},
		{Report{State: wire.StateFailed, Exit: ip(137)}, 1},
		{Report{State: wire.StateFailed}, 1},
		{Report{State: wire.StateFailed, Reason: "lost"}, 1},
		{Report{State: wire.StateFailed, Reason: "oom"}, 1},
		{Report{State: wire.StateFailed, Reason: "timeout", Exit: ip(143)}, 124},
		{Report{State: wire.StateFailed, Exit: ip(130)}, 1}, // the command's 130 is not our "cancelled"
		{Report{State: wire.StateFailed, Exit: ip(124)}, 1},
		{Report{State: wire.StateCancelled}, 130},
		{Report{State: wire.StateSuperseded}, 130},
		{Report{State: wire.StateExpired}, 6},
	}
	for i, c := range cases {
		if got := ExitCode(c.r); got != c.want {
			t.Errorf("case %d (%s, exit %v, %q): %d, want %d", i, c.r.State, c.r.Exit, c.r.Reason, got, c.want)
		}
	}
}

func TestQueuePathSubmitsAndPrintsTheSummary(t *testing.T) {
	sum := &wire.Summary{Passed: 3, Failed: 1, Failures: []wire.Failure{{Name: "TestBad", File: "x.go", Message: "want 1 got 2"}}, Tail: []string{}}
	api := &fakeAPI{job: doneJob(wire.StateFailed, ip(1), "", sum)}
	code, out, errOut := run(t, Options{Args: args(t, "lint"), API: api})
	if code != 1 || api.submitted != 1 || len(api.waited) != 1 {
		t.Fatalf("code %d submitted %d waited %v\nout: %s\nerr: %s", code, api.submitted, api.waited, out, errOut)
	}
	for _, want := range []string{"lint  failed  j_01JC", "air26", "passed 3  failed 1", "FAIL TestBad  [x.go]", "want 1 got 2", "pdx jobs log j_01JC"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "j_01JC queued") || !strings.Contains(errOut, "failed") {
		t.Errorf("progress lines missing: %q", errOut)
	}
	b, _ := json.Marshal(api.bodies[0])
	var body map[string]any
	json.Unmarshal(b, &body)
	if body["task"] != "lint" || body["class"] != "merge" || body["git_dir"] == nil || body["git_dir"] == "" {
		t.Fatalf("submitted body = %s", b)
	}
	if sm := body["submitter"].(map[string]any); sm["host"] != "mlab" {
		t.Fatalf("submitter = %v", sm)
	}
}

// #3477: the session the caller named is the submitter's session in the job record, which is what the completion notice (spec §10) and
// the queue mark on an App tab find the conversation by; with none named it is empty, as before.
func TestQueuePathCarriesTheSessionToTheSubmitter(t *testing.T) {
	for name, want := range map[string]string{"named": "6d0b7ae9-d77a-4a0f-9a13-dbc8ab81a8cc", "none": ""} {
		api := &fakeAPI{job: doneJob(wire.StateSucceeded, ip(0), "", nil)}
		code, out, errOut := run(t, Options{Args: args(t, "lint"), API: api, Session: want})
		if code != 0 || api.submitted != 1 {
			t.Fatalf("%s: code %d submitted %d\nout: %s\nerr: %s", name, code, api.submitted, out, errOut)
		}
		b, _ := json.Marshal(api.bodies[0])
		var body struct {
			Submitter wire.Submitter `json:"submitter"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatal(err)
		}
		if body.Submitter.Session != want {
			t.Errorf("%s: submitter.session = %q, want %q (body %s)", name, body.Submitter.Session, want, b)
		}
	}
}

func TestEveryTerminalStateHasItsExitCode(t *testing.T) {
	for name, tc := range map[string]struct {
		job  wire.Job
		want int
	}{
		"succeeded": {doneJob(wire.StateSucceeded, ip(0), "", nil), 0},
		"failed 3":  {doneJob(wire.StateFailed, ip(3), "", nil), 1}, // 3 is ours (wwd unreachable): a test that exits 3 is a plain 1
		"failed 7":  {doneJob(wire.StateFailed, ip(7), "", nil), 7},
		"timeout":   {doneJob(wire.StateFailed, ip(143), "timeout", nil), 124},
		"cancelled": {doneJob(wire.StateCancelled, nil, "", nil), 130},
		"expired":   {doneJob(wire.StateExpired, nil, "queue_timeout", nil), 6},
		"lost":      {doneJob(wire.StateFailed, nil, "lost", nil), 1},
	} {
		code, _, _ := run(t, Options{Args: args(t, "lint"), API: &fakeAPI{job: tc.job}})
		if code != tc.want {
			t.Errorf("%s: exit %d, want %d", name, code, tc.want)
		}
	}
}

func TestCacheHitPrintsTheStoredResultWithoutWaiting(t *testing.T) {
	res := &wire.Result{Exit: ip(0), Host: "air26", CacheHit: true, Summary: &wire.Summary{Passed: 9, Failures: []wire.Failure{}, Tail: []string{}}}
	api := &fakeAPI{resp: wire.JobSubmissionResponse{ID: "j_hit", Result: res}}
	code, out, _ := run(t, Options{Args: args(t, "lint"), API: api})
	if code != 0 || len(api.waited) != 0 || !strings.Contains(out, "result reused") || !strings.Contains(out, "passed 9") {
		t.Fatalf("code %d waited %v\n%s", code, api.waited, out)
	}
}

func TestDetachPrintsTheIdAndDoesNotWait(t *testing.T) {
	api := &fakeAPI{}
	code, out, _ := run(t, Options{Args: args(t, "lint", "--detach"), API: api})
	if code != 0 || strings.TrimSpace(out) != "j_01JC" || len(api.waited) != 0 {
		t.Fatalf("code %d out %q waited %v", code, out, api.waited)
	}
	_, out, _ = run(t, Options{Args: args(t, "lint", "--detach", "--json"), API: &fakeAPI{}})
	var r Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.ID != "j_01JC" || r.State != wire.StateQueued {
		t.Fatalf("json %q: %+v %v", out, r, err)
	}
}

func TestJSONReport(t *testing.T) {
	api := &fakeAPI{job: doneJob(wire.StateSucceeded, ip(0), "", &wire.Summary{Passed: 2, Failures: []wire.Failure{}, Tail: []string{}})}
	code, out, errOut := run(t, Options{Args: args(t, "lint", "--json"), API: api})
	var r Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || r.State != wire.StateSucceeded || r.Summary.Passed != 2 || r.Host != "air26" {
		t.Fatalf("code %d out %q err %v", code, out, err)
	}
	if errOut != "" {
		t.Fatalf("--json must keep stderr quiet: %q", errOut)
	}
}

func TestUsageProblemsAreExit4AndNothingIsSubmitted(t *testing.T) {
	for name, tc := range map[string]struct {
		dir  string
		argv []string
	}{
		"unknown task": {"", []string{"nope"}},
		"bad file":     {"", []string{"one-file", "missing.ts"}},
		"none + arg":   {"", []string{"lint", "x"}},
		"no manifest":  {repo(t, false), []string{"lint"}},
		"not a repo":   {t.TempDir(), []string{"lint"}},
		"class raised": {"", []string{"lint", "--class", "release"}},
	} {
		api := &fakeAPI{}
		code, _, errOut := run(t, Options{Args: args(t, tc.argv...), API: api, Dir: tc.dir})
		if code != ExitUsage || api.submitted != 0 || errOut == "" {
			t.Errorf("%s: exit %d submitted %d err %q", name, code, api.submitted, errOut)
		}
	}
}

func TestWWDUnreachableIsExit3(t *testing.T) {
	unreachable := fmt.Errorf("%w: dial unix: no such file", client.ErrUnavailable)
	api := &fakeAPI{infoErr: unreachable}
	code, _, errOut := run(t, Options{Args: args(t, "lint"), API: api})
	if code != ExitNoWWD || !strings.Contains(errOut, "wwd is not running") || api.submitted != 0 {
		t.Fatalf("code %d err %q", code, errOut)
	}
	api = &fakeAPI{submitErr: unreachable}
	if code, _, _ := run(t, Options{Args: args(t, "lint"), API: api}); code != ExitNoWWD {
		t.Fatalf("submit unreachable: %d", code)
	}
	api = &fakeAPI{job: doneJob(wire.StateSucceeded, ip(0), "", nil), waitErr: unreachable}
	if code, _, _ := run(t, Options{Args: args(t, "lint"), API: api}); code != ExitNoWWD {
		t.Fatalf("wait unreachable: %d", code)
	}
}

func TestRefusalsOfTheQueueMapToExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status, want int
	}{{400, ExitUsage}, {422, ExitNoHost}, {500, ExitFailed}, {503, ExitFailed}} {
		api := &fakeAPI{submitErr: &client.StatusError{Code: tc.status, Body: "no"}}
		if code, _, errOut := run(t, Options{Args: args(t, "lint"), API: api}); code != tc.want || !strings.Contains(errOut, "refused the job") {
			t.Errorf("status %d: exit %d want %d (%q)", tc.status, code, tc.want, errOut)
		}
	}
}

func TestAnInterruptedWaitLeavesTheJobRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeAPI{waitErr: context.Canceled, onWait: cancel} // the caller is interrupted while the job is waited for
	var so, se bytes.Buffer
	code := Run(ctx, Options{Args: args(t, "lint"), API: api, Dir: sharedRepo, Stdout: &so, Stderr: &se})
	if code != ExitCancelled || !strings.Contains(se.String(), "keeps running") || !strings.Contains(se.String(), "j_01JC") {
		t.Fatalf("code %d err %q", code, se.String())
	}
}

func TestFastLaneRunsLocalTasksWithoutTheQueue(t *testing.T) {
	g := &fakeGate{code: 1, out: "boom\n"}
	api := &fakeAPI{}
	logs := t.TempDir()
	code, out, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: api, Gate: g, LogDir: logs, Env: os.Environ()})
	if code != 1 || g.calls != 1 || api.submitted != 0 {
		t.Fatalf("code %d gate calls %d submitted %d\n%s", code, g.calls, api.submitted, out)
	}
	for _, want := range []string{"one-file  failed", "this host, local fast lane", "not recorded", "log: " + logs} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	entries, _ := os.ReadDir(logs)
	if len(entries) != 1 {
		t.Fatalf("log dir = %v", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(logs, entries[0].Name(), "log")); !strings.Contains(string(b), "boom") {
		t.Fatalf("log = %q", b)
	}
}

func TestFastLaneRefusesAClassAboveTheTasksWithoutARecorder(t *testing.T) {
	g := &fakeGate{}
	code, _, errOut := run(t, Options{Args: args(t, "one-file", "a.test.ts", "--class", "release"), API: &fakeAPI{}, Gate: g, Env: os.Environ()})
	if code != ExitUsage || g.calls != 0 || !strings.Contains(errOut, "above") {
		t.Fatalf("code %d calls %d err %q", code, g.calls, errOut)
	}
}

func TestTwoFastLaneRunsInOneSecondKeepApart(t *testing.T) {
	logs := t.TempDir()
	for i := 0; i < 3; i++ {
		run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: &fakeGate{out: fmt.Sprint("run", i)}, LogDir: logs, Env: os.Environ()})
	}
	entries, _ := os.ReadDir(logs)
	if len(entries) != 3 {
		t.Fatalf("%d run directories, want 3 (runs shared a directory)", len(entries))
	}
}

func TestACommandThatExitsWithOneOfOurNumbersIsAPlain1ButItsNumberIsShown(t *testing.T) {
	for _, n := range []int{3, 4, 5, 6} {
		api := &fakeAPI{job: doneJob(wire.StateFailed, ip(n), "", nil)}
		code, out, _ := run(t, Options{Args: args(t, "lint"), API: api})
		if code != 1 || !strings.Contains(out, fmt.Sprintf("exit %d", n)) {
			t.Errorf("queue, exit %d: code %d\n%s", n, code, out)
		}
		code, out, _ = run(t, Options{Args: args(t, "one-file", "a.test.ts", "--json"), API: &fakeAPI{}, Gate: &fakeGate{code: n}, Env: os.Environ()})
		var r Report
		if err := json.Unmarshal([]byte(out), &r); err != nil || code != 1 || r.Exit == nil || *r.Exit != n {
			t.Errorf("fast lane, exit %d: code %d report %+v (%v)", n, code, r, err)
		}
	}
}

// The fast lane runs in the caller's own process: an interrupt of the caller stops the local command (the job-outlives-its-
// caller rule, D5, is the queue's); the exit is 130 and the message says the run was stopped, not that it keeps running.
func TestInterruptingAFastLaneRunStopsItAndSaysSo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &blockingGate{started: make(chan struct{})}
	var so, se bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: g, Dir: sharedRepo, Env: os.Environ(), Stdout: &so, Stderr: &se})
	}()
	<-g.started
	cancel()
	select {
	case code := <-done:
		if code != ExitCancelled || !strings.Contains(so.String(), "cancelled") || strings.Contains(se.String()+so.String(), "keeps running") {
			t.Fatalf("code %d out %q err %q", code, so.String(), se.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop")
	}
}

type blockingGate struct{ started chan struct{} }

func (g *blockingGate) Run(ctx context.Context, o fastlane.Options) (int, error) {
	for _, kv := range o.Env {
		if nonce, ok := strings.CutPrefix(kv, fastlane.AdmittedMarkEnv+"="); ok {
			o.Stderr.Write([]byte(fastlane.AdmittedMark(nonce)))
		}
	}
	close(g.started)
	<-ctx.Done()
	return 130, nil
}

func TestFastLaneSuccessIsExit0(t *testing.T) {
	code, _, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: &fakeGate{}, Env: os.Environ()})
	if code != 0 {
		t.Fatalf("code %d", code)
	}
}

func TestFastLaneBadArgumentIsExit4(t *testing.T) {
	g := &fakeGate{}
	code, _, errOut := run(t, Options{Args: args(t, "one-file", "missing.ts"), API: &fakeAPI{}, Gate: g, Env: os.Environ()})
	if code != ExitUsage || g.calls != 0 || errOut == "" {
		t.Fatalf("code %d calls %d err %q", code, g.calls, errOut)
	}
}

func TestNonLocalTasksAndDetachedRunsGoThroughTheQueue(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{job: doneJob(wire.StateSucceeded, ip(0), "", nil)}
	if code, _, _ := run(t, Options{Args: args(t, "lint"), API: api, Gate: g, Env: os.Environ()}); code != 0 || g.calls != 0 || api.submitted != 1 {
		t.Fatalf("a non-local task: code %d gate %d submitted %d", code, g.calls, api.submitted)
	}
}

func TestADetachedLocalTaskIsRefusedAndNeverQueued(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{}
	code, _, errOut := run(t, Options{Args: args(t, "one-file", "a.test.ts", "--detach"), API: api, Gate: g, Recorder: neverRecorder{t}, Env: os.Environ()})
	if code != ExitUsage || g.calls != 0 || api.submitted != 0 || !strings.Contains(errOut, "只在前景跑") {
		t.Fatalf("code %d gate %d submitted %d err %q", code, g.calls, api.submitted, errOut)
	}
}

// busyRecorder refuses the first busy calls with a lock conflict, then lets the run through (unrecorded).
type busyRecorder struct {
	busy   int
	calls  int
	onBusy func(call int) // called on each refusal (tests cancel the caller from here)
}

func (r *busyRecorder) Register(context.Context, submit.Body) (fastlane.Handle, error) {
	r.calls++
	if r.calls <= r.busy {
		if r.onBusy != nil {
			r.onBusy(r.calls)
		}
		return nil, fmt.Errorf("%w: tmux", fastlane.ErrLocksBusy)
	}
	return nil, fastlane.ErrUnrecorded
}

// slotRecorder refuses the first busy calls for want of a slot, then lets the run through (unrecorded).
type slotRecorder struct{ busy, calls int }

func (r *slotRecorder) Register(context.Context, submit.Body) (fastlane.Handle, error) {
	r.calls++
	if r.calls <= r.busy {
		return nil, fmt.Errorf("%w: no light slot: zone_danger", fastlane.ErrSlotBusy)
	}
	return nil, fastlane.ErrUnrecorded
}

func TestNoSlotMakesALocalTaskWaitHereLikeABusyLock(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{}
	rec := &slotRecorder{busy: 2}
	code, _, errOut := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: api, Gate: g, Recorder: rec, Env: os.Environ(),
		LockPoll: time.Millisecond, LockWait: time.Minute})
	if code != 0 || g.calls != 1 || api.submitted != 0 || rec.calls != 3 {
		t.Fatalf("code %d gate %d submitted %d registers %d", code, g.calls, api.submitted, rec.calls)
	}
	if strings.Count(errOut, "等待本機名額／lock") != 1 || !strings.Contains(errOut, "zone_danger") {
		t.Fatalf("the wait is told once, with the reason: %q", errOut)
	}
}

func TestNoSlotForTheWholeQueueTimeoutEndsExpired(t *testing.T) {
	g := &fakeGate{}
	rec := &slotRecorder{busy: 1 << 30}
	code, _, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: g, Recorder: rec, Env: os.Environ(),
		LockPoll: time.Millisecond, LockWait: 30 * time.Millisecond})
	if code != ExitExpired || g.calls != 0 {
		t.Fatalf("code %d gate %d", code, g.calls)
	}
}

func TestBusyLocksMakeALocalTaskWaitHereAndNeverQueueIt(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{}
	rec := &busyRecorder{busy: 2}
	code, _, errOut := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: api, Gate: g, Recorder: rec, Env: os.Environ(),
		LockPoll: time.Millisecond, LockWait: time.Minute})
	if code != 0 || g.calls != 1 || api.submitted != 0 || rec.calls != 3 {
		t.Fatalf("code %d gate %d submitted %d registers %d", code, g.calls, api.submitted, rec.calls)
	}
	if strings.Count(errOut, "等待本機名額／lock") != 1 || !strings.Contains(errOut, "tmux") {
		t.Fatalf("the wait is told once, with the keys: %q", errOut)
	}
}

func TestADeadlineShorterThanOnePollEndsAtTheDeadline(t *testing.T) {
	g := &fakeGate{}
	rec := &busyRecorder{busy: 1 << 30}
	start := time.Now()
	code, _, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: g, Recorder: rec, Env: os.Environ(),
		LockPoll: time.Hour, LockWait: 60 * time.Millisecond})
	if code != ExitExpired || g.calls != 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("code %d gate %d after %v", code, g.calls, time.Since(start))
	}
}

func TestTheLockWaitAndTheSlotWaitShareOneQueueTimeout(t *testing.T) {
	g := &fakeGate{}
	rec := &busyRecorder{busy: 2}
	code, _, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: g, Recorder: rec, Env: os.Environ(),
		LockPoll: 20 * time.Millisecond, LockWait: 5 * time.Second})
	if code != 0 || g.calls != 1 {
		t.Fatalf("code %d gate %d", code, g.calls)
	}
	if g.waitMax <= 0 || g.waitMax >= 5*time.Second {
		t.Fatalf("the gate may wait for what is left of the timeout, not for all of it: %v", g.waitMax)
	}
}

func TestALockHeldForTheWholeQueueTimeoutEndsExpiredAndNothingRunsOrQueues(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{}
	rec := &busyRecorder{busy: 1 << 30}
	code, out, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: api, Gate: g, Recorder: rec, Env: os.Environ(),
		LockPoll: time.Millisecond, LockWait: 30 * time.Millisecond})
	if code != ExitExpired || g.calls != 0 || api.submitted != 0 || !strings.Contains(out, "expired") {
		t.Fatalf("code %d gate %d submitted %d\n%s", code, g.calls, api.submitted, out)
	}
}

func TestTheCallerLeavingEndsTheWaitForALock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &fakeGate{}
	api := &fakeAPI{}
	rec := &busyRecorder{busy: 1 << 30, onBusy: func(call int) {
		if call == 2 {
			cancel()
		}
	}}
	var so, se bytes.Buffer
	code := Run(ctx, Options{Args: args(t, "one-file", "a.test.ts"), API: api, Gate: g, Recorder: rec, Env: os.Environ(), Dir: sharedRepo,
		Stdout: &so, Stderr: &se, LockPoll: 5 * time.Millisecond, LockWait: time.Minute})
	if code != ExitCancelled || g.calls != 0 || api.submitted != 0 {
		t.Fatalf("code %d gate %d submitted %d", code, g.calls, api.submitted)
	}
}

func TestAWwdThatIsNotThereStillRunsALocalTaskHere(t *testing.T) {
	g := &fakeGate{}
	api := &fakeAPI{infoErr: fmt.Errorf("%w: dial unix: no such file", client.ErrUnavailable)}
	code, _, _ := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: api, Gate: g, Recorder: neverRecorder{t}, Env: os.Environ()})
	if code != 0 || g.calls != 1 || api.submitted != 0 {
		t.Fatalf("code %d gate %d submitted %d", code, g.calls, api.submitted)
	}
	// a task that cannot take the fast lane still needs wwd
	code, _, _ = run(t, Options{Args: args(t, "lint"), API: api, Gate: g, Env: os.Environ()})
	if code != ExitNoWWD || g.calls != 1 {
		t.Fatalf("a queued task without wwd: code %d gate %d", code, g.calls)
	}
}

type neverRecorder struct{ t *testing.T }

func (r neverRecorder) Register(context.Context, submit.Body) (fastlane.Handle, error) {
	r.t.Error("a run with nothing to record against was registered")
	return nil, fastlane.ErrUnrecorded
}

func TestARepositoryWithoutACommitRunsTheLocalTaskUnrecorded(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, ".wwd"), 0o755)
	os.WriteFile(filepath.Join(dir, ".wwd/tasks.toml"), []byte(manifestSrc), 0o644)
	os.WriteFile(filepath.Join(dir, "a.test.ts"), []byte("x"), 0o644)
	g := &fakeGate{}
	code, out, errOut := run(t, Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: g, Dir: dir, Recorder: neverRecorder{t}, Env: os.Environ()})
	if code != 0 || g.calls != 1 {
		t.Fatalf("code %d gate %d\n%s\n%s", code, g.calls, out, errOut)
	}
}

func TestListShowsTheTasks(t *testing.T) {
	code, out, _ := run(t, Options{Args: args(t, "--list"), API: &fakeAPI{}})
	if code != 0 || !strings.Contains(out, "one-file") || !strings.Contains(out, "lint") || !strings.Contains(out, "local") {
		t.Fatalf("code %d\n%s", code, out)
	}
	code, out, _ = run(t, Options{Args: args(t, "--list", "--json"), API: &fakeAPI{}})
	var v struct {
		Tasks []struct{ Name, Class string } `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || code != 0 || len(v.Tasks) != 3 || v.Tasks[0].Name != "go-all" {
		t.Fatalf("code %d json %q: %v", code, out, err)
	}
	if code, _, _ := run(t, Options{Args: args(t, "--list"), API: &fakeAPI{}, Dir: repo(t, false)}); code != ExitUsage {
		t.Fatalf("a repo without a manifest: %d", code)
	}
}
