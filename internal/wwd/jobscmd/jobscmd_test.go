package jobscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

type fakeAPI struct {
	earned wire.EarnedLabelList
	// earnedUnknown: the wwd answered 404 for the earned-labels route (it predates it)
	earnedUnknown bool
	jobs          []wire.Job
	query         client.JobsQuery
	jobErr        error
	logBytes      []byte
	logErr        error
	cancelErr     error
	cancelled     []string
	moved         []string
	moveJob       wire.Job
	moveErr       error
	waitJob       wire.Job
	info          wire.Info
	pool          wire.Pool
	worker        wire.WorkerSettings
	set           *wire.WorkerSettings
	started       *wire.Exception
	stopped       string
	startedOn     string
	stoppedOn     string
	labelCalls    []string // "quarantine <label>" and "release <label>", in order
	qualified     []string
	qualifyStatus wire.QualifyStatus
	usage         wire.UsageResponse
	usageQ        client.UsageQuery
	err           error // returned by every call when set (wwd unreachable)
}

func (f *fakeAPI) Usage(_ context.Context, q client.UsageQuery) (wire.UsageResponse, error) {
	f.usageQ = q
	return f.usage, f.err
}

func (f *fakeAPI) Info(context.Context) (wire.Info, error) { return f.info, f.err }
func (f *fakeAPI) Pool(context.Context) (wire.Pool, error) { return f.pool, f.err }
func (f *fakeAPI) Jobs(_ context.Context, q client.JobsQuery) ([]wire.Job, error) {
	f.query = q
	return append([]wire.Job(nil), f.jobs...), f.err
}
func (f *fakeAPI) Job(_ context.Context, id string) (wire.Job, error) {
	if f.jobErr != nil || f.err != nil {
		return wire.Job{}, firstErr(f.jobErr, f.err)
	}
	for _, j := range f.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return wire.Job{}, &client.StatusError{Code: 404, Body: "no such job"}
}
func (f *fakeAPI) Log(context.Context, string) ([]byte, error) {
	return f.logBytes, firstErr(f.logErr, f.err)
}
func (f *fakeAPI) Cancel(_ context.Context, id string) error {
	f.cancelled = append(f.cancelled, id)
	return firstErr(f.cancelErr, f.err)
}
func (f *fakeAPI) SetPosition(_ context.Context, id string, p wire.Position) (wire.Job, error) {
	f.moved = append(f.moved, id+" "+string(p))
	j := f.moveJob
	if p != wire.PositionNormal {
		j.Manual = p
	}
	return j, firstErr(f.moveErr, f.err)
}
func (f *fakeAPI) WaitJob(context.Context, string, func(wire.Job)) (wire.Job, error) {
	return f.waitJob, f.err
}
func (f *fakeAPI) Worker(context.Context) (wire.WorkerSettings, error) { return f.worker, f.err }
func (f *fakeAPI) SetWorker(_ context.Context, s wire.WorkerSettings) (wire.WorkerSettings, error) {
	f.set = &s
	return s, f.err
}
func (f *fakeAPI) StartException(_ context.Context, e wire.Exception) error {
	f.started = &e
	return f.err
}
func (f *fakeAPI) StopException(_ context.Context, name string) error {
	f.stopped = name
	return f.err
}
func (f *fakeAPI) StartExceptionOn(_ context.Context, host string, e wire.Exception) error {
	f.startedOn = host
	f.started = &e
	return f.err
}
func (f *fakeAPI) StopExceptionOn(_ context.Context, host, name string) error {
	f.stoppedOn = host
	f.stopped = name
	return f.err
}

func (f *fakeAPI) EarnedLabels(context.Context) (wire.EarnedLabelList, bool, error) {
	return f.earned, !f.earnedUnknown, f.err
}

func (f *fakeAPI) QuarantineLabel(_ context.Context, label string) error {
	f.labelCalls = append(f.labelCalls, "quarantine "+label)
	return f.err
}
func (f *fakeAPI) ReleaseLabel(_ context.Context, label string) error {
	f.labelCalls = append(f.labelCalls, "release "+label)
	return f.err
}
func (f *fakeAPI) Qualify(_ context.Context, task string) (wire.QualifyStarted, error) {
	f.qualified = append(f.qualified, task)
	if f.err != nil {
		return wire.QualifyStarted{}, f.err
	}
	return wire.QualifyStarted{Label: "task:" + task, Candidate: "j_cand", Reference: "j_ref"}, nil
}
func (f *fakeAPI) QualifyStatus(context.Context) (wire.QualifyStatus, error) {
	return f.qualifyStatus, f.err
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func job(id string, state wire.JobState, ageMin int) wire.Job {
	return wire.Job{ID: id, Task: "lint", State: state, Class: wire.ClassMerge, Origin: "mlab",
		CreatedAt: t0.Add(-time.Duration(ageMin) * time.Minute), Submitter: wire.Submitter{Session: "s1"}}
}

func doneWith(j wire.Job, exit int, host string) wire.Job {
	e := exit
	j.Result = &wire.Result{Exit: &e, Host: host, Summary: &wire.Summary{Passed: 4, Failures: []wire.Failure{}, Tail: []string{}}}
	return j
}

func runJobs(f *fakeAPI, args ...string) (int, string, string) {
	var so, se bytes.Buffer
	code := Jobs(context.Background(), args, Env{API: f, Session: "s1", Now: func() time.Time { return t0 }, Stdout: &so, Stderr: &se})
	return code, so.String(), se.String()
}

func runWWD(f *fakeAPI, args ...string) (int, string, string) {
	var so, se bytes.Buffer
	code := WWD(context.Background(), args, Env{API: f, Now: func() time.Time { return t0 }, Stdout: &so, Stderr: &se})
	return code, so.String(), se.String()
}

func TestLsListsThisSessionsJobsNewestFirst(t *testing.T) {
	f := &fakeAPI{jobs: []wire.Job{job("j_old", wire.StateSucceeded, 90), job("j_new", wire.StateRunning, 2)}}
	f.jobs[0] = doneWith(f.jobs[0], 0, "air26")
	code, out, _ := runJobs(f)
	if code != 0 || f.query.Session != "s1" || f.query.State != "" {
		t.Fatalf("code %d query %+v", code, f.query)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") || !strings.HasPrefix(lines[1], "j_new") || !strings.HasPrefix(lines[2], "j_old") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(lines[1], "running") || !strings.Contains(lines[1], "2m") || !strings.Contains(lines[2], "air26") || !strings.Contains(lines[2], "1h") {
		t.Fatalf("columns:\n%s", out)
	}
}

func TestLsOptions(t *testing.T) {
	f := &fakeAPI{}
	if code, out, _ := runJobs(f, "ls", "--all", "--state", "queued"); code != 0 || f.query.Session != "" || f.query.State != wire.StateQueued || !strings.Contains(out, "no jobs") {
		t.Fatalf("code %d query %+v out %q", code, f.query, out)
	}
	for i := 0; i < 25; i++ {
		f.jobs = append(f.jobs, job(fmt.Sprintf("j_%02d", i), wire.StateQueued, i))
	}
	_, out, _ := runJobs(f, "--limit", "5")
	if strings.Count(out, "j_") != 5 || !strings.Contains(out, "and 20 more") {
		t.Fatalf("limit:\n%s", out)
	}
	_, out, _ = runJobs(f)
	if strings.Count(out, "j_") != 20 {
		t.Fatalf("the default limit is 20:\n%s", out)
	}
	_, out, _ = runJobs(f, "--json", "--limit", "2")
	var got []wire.Job
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 {
		t.Fatalf("json %q: %v", out, err)
	}
}

func TestLsRefusesBadInput(t *testing.T) {
	for _, args := range [][]string{{"ls", "--state", "bogus"}, {"--limit", "0"}, {"--limit", "x"}, {"--nope"}, {"ls", "extra"}, {"--state"}} {
		if code, _, errOut := runJobs(&fakeAPI{}, args...); code != ExitUsage || errOut == "" {
			t.Errorf("%v: code %d err %q", args, code, errOut)
		}
	}
	if code, _, _ := runJobs(&fakeAPI{}, "frobnicate"); code != ExitUsage {
		t.Fatalf("an unknown subcommand: %d", code)
	}
}

func TestShow(t *testing.T) {
	j := doneWith(job("j_1", wire.StateSucceeded, 5), 0, "air26")
	f := &fakeAPI{jobs: []wire.Job{j}}
	code, out, _ := runJobs(f, "show", "j_1")
	for _, want := range []string{"lint  succeeded  j_1", "air26", "passed 4", "class merge", "from mlab (session s1)", "5m ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	_, out, _ = runJobs(f, "show", "j_1", "--json")
	var got wire.Job
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ID != "j_1" {
		t.Fatalf("json %q: %v", out, err)
	}
	if code, _, errOut := runJobs(f, "show", "j_nope"); code != ExitUsage || !strings.Contains(errOut, "not found") {
		t.Fatalf("an unknown job: %d %q", code, errOut)
	}
	if code, _, _ := runJobs(f, "show"); code != ExitUsage {
		t.Fatalf("no id: %d", code)
	}
	q := job("j_q", wire.StateQueued, 1)
	q.Claim = &wire.Claim{Worker: "air26", Attempt: 2}
	f.jobs = append(f.jobs, q)
	if _, out, _ := runJobs(f, "show", "j_q"); !strings.Contains(out, "claimed by air26 (attempt 2)") {
		t.Fatalf("a claimed job:\n%s", out)
	}
}

func TestLog(t *testing.T) {
	f := &fakeAPI{logBytes: []byte("line1\nline2\n")}
	if code, out, _ := runJobs(f, "log", "j_1"); code != 0 || out != "line1\nline2\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	f.logErr = &client.StatusError{Code: 404, Body: "no log"}
	if code, _, errOut := runJobs(f, "log", "j_1"); code != ExitFailed || !strings.Contains(errOut, "no log stored") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestOutputFromJobsAndOtherHostsNeverDrivesTheTerminal(t *testing.T) {
	evil := job("j_evil", wire.StateRunning, 1)
	evil.Task = "lint\x1b[2J"
	evil.Origin = "mlab\r"
	evil.Submitter.Session = "s\x1b]0;x\x07"
	evil.Claim = &wire.Claim{Worker: "air26\x1b[31m", Attempt: 1}
	f := &fakeAPI{jobs: []wire.Job{evil}, logBytes: []byte("ok\x1b[2J\rfake PASS\x07\nnext\n"),
		info:   wire.Info{Alias: "ml\x1bab", Version: "1\x1b", Proto: 1, Labels: []string{"os:darwin\x1b[0m"}},
		worker: wire.WorkerSettings{OwnerRule: "yield\x1b"},
		pool:   wire.Pool{Peers: []wire.Peer{{Info: wire.Info{Alias: "peer\x1b[2J"}, Reachable: true}}}}
	check := func(name, out string) {
		t.Helper()
		for _, bad := range []string{"\x1b", "\r", "\x07"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: %q reached the output: %q", name, bad, out)
			}
		}
	}
	_, out, _ := runJobs(f, "ls", "--all")
	check("ls", out)
	_, out, _ = runJobs(f, "show", "j_evil")
	check("show", out)
	_, out, _ = runJobs(f, "log", "j_evil")
	check("log", out)
	if !strings.Contains(out, `\x1b[2J`) || !strings.Contains(out, "next") {
		t.Errorf("log should show the escapes and keep its lines: %q", out)
	}
	_, out, _ = runWWD(f, "status")
	check("status", out)
	_, out, _ = runWWD(f, "pool")
	check("pool", out)
	// --raw is the opt-in for the bytes as they are; --json is data for a program
	if _, out, _ := runJobs(f, "log", "j_evil", "--raw"); out != string(f.logBytes) {
		t.Errorf("--raw changed the bytes: %q", out)
	}
	if _, out, _ := runJobs(f, "ls", "--all", "--json"); !strings.Contains(out, `\u001b`) || strings.ContainsRune(out, '\x1b') {
		t.Errorf("json: %q", out)
	}
}

func TestCancel(t *testing.T) {
	f := &fakeAPI{waitJob: job("j_1", wire.StateCancelled, 1)}
	code, out, _ := runJobs(f, "cancel", "j_1")
	if code != 0 || len(f.cancelled) != 1 || !strings.Contains(out, "cancel requested") {
		t.Fatalf("code %d cancelled %v out %q", code, f.cancelled, out)
	}
	code, out, _ = runJobs(f, "cancel", "--wait", "j_1")
	if code != 0 || !strings.Contains(out, "j_1: cancelled") {
		t.Fatalf("--wait: code %d out %q", code, out)
	}
	f.cancelErr = &client.StatusError{Code: 409, Body: "already ended"}
	if code, _, errOut := runJobs(f, "cancel", "j_1"); code != ExitFailed || !strings.Contains(errOut, "already ended") {
		t.Fatalf("a refusal: %d %q", code, errOut)
	}
}

func TestMove(t *testing.T) {
	f := &fakeAPI{}
	for _, p := range []string{"front", "back", "hold", "normal"} {
		code, out, _ := runJobs(f, "move", "j_1", p)
		if code != 0 || !strings.Contains(out, "j_1: "+p) {
			t.Errorf("move %s: code %d out %q", p, code, out)
		}
	}
	if fmt.Sprint(f.moved) != "[j_1 front j_1 back j_1 hold j_1 normal]" {
		t.Errorf("moved %v", f.moved)
	}
	f.moved = nil
	for _, args := range [][]string{{"move"}, {"move", "j_1"}, {"move", "j_1", "sideways"}, {"move", "j_1", "front", "extra"}} {
		if code, _, errOut := runJobs(f, args...); code != ExitUsage || errOut == "" {
			t.Errorf("%v: code %d %q", args, code, errOut)
		}
	}
	if len(f.moved) != 0 {
		t.Errorf("a bad call reached wwd: %v", f.moved)
	}
	f.moveErr = &client.StatusError{Code: 409, Body: `{"state":"running"}`}
	if code, _, errOut := runJobs(f, "move", "j_1", "front"); code != ExitFailed || errOut == "" {
		t.Fatalf("a refusal: %d %q", code, errOut)
	}
}

func TestUnreachableWWDIsExit3Everywhere(t *testing.T) {
	f := &fakeAPI{err: fmt.Errorf("%w: dial", client.ErrUnavailable)}
	for _, args := range [][]string{{"ls"}, {"show", "j_1"}, {"log", "j_1"}, {"cancel", "j_1"}, {"move", "j_1", "front"}} {
		if code, _, errOut := runJobs(f, args...); code != ExitNoWWD || !strings.Contains(errOut, "wwd is not running") {
			t.Errorf("jobs %v: %d %q", args, code, errOut)
		}
	}
	for _, args := range [][]string{{"status"}, {"pool"}, {"usage"}, {"worker", "on"}, {"exception", "stop", "x"}} {
		if code, _, _ := runWWD(f, args...); code != ExitNoWWD {
			t.Errorf("wwd %v: %d", args, code)
		}
	}
}

func TestWWDStatusAndPool(t *testing.T) {
	f := &fakeAPI{
		info:   wire.Info{Alias: "mlab", Version: "1.2.3", Proto: 1, Platform: "darwin", Arch: "arm64", Labels: []string{"os:darwin", "arch:arm64"}},
		worker: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, OwnerRule: "yield"},
		pool: wire.Pool{Peers: []wire.Peer{
			{Info: wire.Info{Alias: "mlab", Worker: wire.InfoWorker{Enabled: true}}, Reachable: true, Facts: &wire.Facts{
				At: t0.Add(-5 * time.Second), Derived: wire.Derived{Zone: wire.ZoneWarning, Headroom: wire.Slots{Heavy: 0, Light: 2}},
				Worker: wire.FactsWorker{Enabled: true, Running: wire.Slots{Heavy: 1}}}},
			{Info: wire.Info{Alias: "air19"}, Reachable: false},
		}},
	}
	code, out, _ := runWWD(f)
	for _, want := range []string{"wwd mlab  1.2.3  (darwin/arm64, proto 1)", "worker on  slots heavy 1 light 2  owner rule: yield", "labels os:darwin arch:arm64"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("status lacks %q (code %d):\n%s", want, code, out)
		}
	}
	_, out, _ = runWWD(f, "status", "--json")
	var st struct {
		Info   wire.Info
		Worker wire.WorkerSettings
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || st.Info.Alias != "mlab" || !st.Worker.Enabled {
		t.Fatalf("status json %q: %v", out, err)
	}
	_, out, _ = runWWD(f, "pool")
	if !strings.Contains(out, "warning") || !strings.Contains(out, "0/2") || !strings.Contains(out, "1/0") || !strings.Contains(out, "5s") {
		t.Fatalf("pool:\n%s", out)
	}
	if !strings.Contains(out, "air19") || !strings.Contains(out, "NO") {
		t.Fatalf("an unreachable host must be marked:\n%s", out)
	}
}

func TestWorkerSwitchKeepsWhatItWasNotAskedToChange(t *testing.T) {
	f := &fakeAPI{worker: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, OwnerRule: "yield"}}
	if code, out, _ := runWWD(f, "worker", "off"); code != 0 || f.set == nil || f.set.Enabled || f.set.Slots.Heavy != 1 || f.set.Slots.Light != 2 || f.set.OwnerRule != "yield" || !strings.Contains(out, "worker off") {
		t.Fatalf("off: code %d set %+v out %q", code, f.set, out)
	}
	if code, _, _ := runWWD(f, "worker", "on", "--heavy", "2", "--light=0"); code != 0 || !f.set.Enabled || f.set.Slots.Heavy != 2 || f.set.Slots.Light != 0 {
		t.Fatalf("on with slots: code %d set %+v", code, f.set)
	}
	f.set = nil
	for _, args := range [][]string{{"worker"}, {"worker", "maybe"}, {"worker", "on", "--heavy", "-1"}, {"worker", "on", "--heavy", "x"}, {"worker", "on", "--bogus", "1"}} {
		if code, _, _ := runWWD(f, args...); code != ExitUsage || f.set != nil {
			t.Errorf("%v: code %d set %+v", args, code, f.set)
		}
	}
}

func TestWorkerSetsTheDiskFloorAndShowsIt(t *testing.T) {
	floor := 10.0
	f := &fakeAPI{worker: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: &floor}}
	if code, out, _ := runWWD(f, "worker", "on", "--disk-min-gb", "25.5"); code != 0 || f.set == nil || f.set.DiskMinGB == nil || *f.set.DiskMinGB != 25.5 ||
		f.set.Slots.Heavy != 1 || !strings.Contains(out, "disk floor 25.5 GB") {
		t.Fatalf("set: code %d set %+v out %q", code, f.set, out)
	}
	if code, out, _ := runWWD(f, "worker", "on", "--disk-min-gb=0"); code != 0 || *f.set.DiskMinGB != 0 || !strings.Contains(out, "disk floor none") {
		t.Fatalf("0 is none: code %d set %+v out %q", code, f.set, out)
	}
	// a switch that does not name the floor puts back the one it read
	f.set = nil
	if code, _, _ := runWWD(f, "worker", "off"); code != 0 || f.set == nil || f.set.DiskMinGB == nil || *f.set.DiskMinGB != 10 {
		t.Fatalf("not named: code %d set %+v", code, f.set)
	}
	f.set = nil
	for _, args := range [][]string{{"worker", "on", "--disk-min-gb", "-1"}, {"worker", "on", "--disk-min-gb", "x"}, {"worker", "on", "--disk-min-gb", "NaN"}} {
		if code, _, _ := runWWD(f, args...); code != ExitUsage || f.set != nil {
			t.Errorf("%v: code %d set %+v", args, code, f.set)
		}
	}
}

func TestStatusShowsTheDiskFloor(t *testing.T) {
	floor := 12.0
	f := &fakeAPI{info: wire.Info{Alias: "mlab", Version: "1.2.3", Platform: "darwin", Arch: "arm64", Proto: 1},
		worker: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: &floor}}
	if code, out, _ := runWWD(f, "status"); code != 0 || !strings.Contains(out, "disk floor 12 GB") {
		t.Fatalf("status: code %d out %q", code, out)
	}
}

func TestExceptions(t *testing.T) {
	f := &fakeAPI{}
	if code, _, _ := runWWD(f, "exception", "start", "app-dev-package", "--until", "30m"); code != 0 || f.started == nil ||
		f.started.Until == nil || !f.started.Until.Equal(t0.Add(30*time.Minute)) || f.started.PID != nil || len(f.started.Holds) != 2 {
		t.Fatalf("until: %+v", f.started)
	}
	if code, _, _ := runWWD(f, "exception", "start", "vite", "--pid", "4242", "--holds", "heavy"); code != 0 || f.started.PID == nil || *f.started.PID != 4242 || len(f.started.Holds) != 1 || f.started.Holds[0] != wire.HoldHeavy {
		t.Fatalf("pid: %+v", f.started)
	}
	at := t0.Add(2 * time.Hour).Format(time.RFC3339)
	if code, _, _ := runWWD(f, "exception", "start", "x", "--until", at); code != 0 || f.started.Until == nil || !f.started.Until.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("rfc3339: %+v", f.started)
	}
	f.started = nil
	for _, args := range [][]string{
		{"exception", "start", "x"},                                        // neither until nor pid
		{"exception", "start", "x", "--until", "1h", "--pid", "5"},         // both
		{"exception", "start", "x", "--until", "-5m"},                      // in the past
		{"exception", "start", "x", "--until", "tomorrow"},                 // not a time
		{"exception", "start", "x", "--pid", "0"},                          // not a pid
		{"exception", "start", "x", "--pid", "5", "--holds", "everything"}, // not a hold
		{"exception", "start"},
		{"exception", "frob", "x"},
		{"exception"},
	} {
		if code, _, _ := runWWD(f, args...); code != ExitUsage || f.started != nil {
			t.Errorf("%v: code %d started %+v", args, code, f.started)
		}
	}
	if code, out, _ := runWWD(f, "exception", "stop", "vite"); code != 0 || f.stopped != "vite" || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: code %d stopped %q out %q", code, f.stopped, out)
	}
}

func TestHelpAndUnknown(t *testing.T) {
	if code, out, _ := runJobs(&fakeAPI{}, "help"); code != 0 || !strings.Contains(out, "usage: pdx jobs") {
		t.Fatalf("jobs help: %d %q", code, out)
	}
	if code, out, _ := runWWD(&fakeAPI{}, "help"); code != 0 || !strings.Contains(out, "usage: pdx wwd") {
		t.Fatalf("wwd help: %d %q", code, out)
	}
	if code, _, _ := runWWD(&fakeAPI{}, "frobnicate"); code != ExitUsage {
		t.Fatalf("unknown wwd subcommand: %d", code)
	}
}
