package fastlane

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

const testManifest = `
[tasks.go-pkg]
run = "go test -json {packages}"
cwd = "sub"
args = "packages"
local = "packages<=2"
summary = "go-json"
timeout = "5s"
locks = ["db"]

[tasks.one-file]
run = "tool {files}"
args = "files"
local = true
timeout = "5s"

[tasks.remote-only]
run = "make lint"
args = "none"
`

func task(t *testing.T, name string) *manifest.Task {
	t.Helper()
	m, err := manifest.Parse([]byte(testManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m.Tasks[name]
}

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.test.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// directNoTTY is the direct runner with an empty stdin, so a test run from a terminal does not take the terminal's process group
// (the direct runner leaves a command that reads the terminal in the caller's group).
type directNoTTY struct{}

func (directNoTTY) Run(ctx context.Context, o Options) (int, error) {
	o.Stdin = strings.NewReader("")
	return Direct{}.Run(ctx, o)
}

// testEnv is what a real run gets from these tests: the path only. The environment of a test that runs inside a wwd job carries that
// job's mark, and the direct runner ends every process that carries the mark of the run it is running, the test's own included.
func testEnv() []string { return []string{"PATH=" + os.Getenv("PATH")} }

type fakeGate struct {
	mu    sync.Mutex
	calls []Options
	run   func(ctx context.Context, o Options) int
	// skipMark: the gate never admits the command (it printed no mark); a run func may print the mark itself later.
	skipMark bool
	// waitedOut is what the gate reports through Options.WaitedOut: the wait for a slot ran past WaitMax.
	waitedOut bool
}

func (g *fakeGate) Run(ctx context.Context, o Options) (int, error) {
	g.mu.Lock()
	g.calls = append(g.calls, o)
	g.mu.Unlock()
	if o.WaitedOut != nil {
		*o.WaitedOut = g.waitedOut
	}
	// like the real gate: the admitted mark goes to the command's stderr right before the command starts
	if !g.skipMark {
		for _, kv := range o.Env {
			if nonce, ok := strings.CutPrefix(kv, AdmittedMarkEnv+"="); ok {
				io.WriteString(o.Stderr, AdmittedMark(nonce))
			}
		}
	}
	if g.run == nil {
		return 0, nil
	}
	return g.run(ctx, o), nil
}

type fakeHandle struct {
	id        string
	lease     time.Duration
	every     time.Duration
	heartbeat func() (bool, error)
	mu        sync.Mutex
	outcome   *Outcome
	finishErr error
	finishFn  func() error
}

func (h *fakeHandle) JobID() string             { return h.id }
func (h *fakeHandle) Lease() time.Duration      { return h.lease }
func (h *fakeHandle) RenewEvery() time.Duration { return h.every }
func (h *fakeHandle) Heartbeat(context.Context) (bool, error) {
	if h.heartbeat == nil {
		return false, nil
	}
	return h.heartbeat()
}
func (h *fakeHandle) Finish(_ context.Context, o Outcome) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outcome = &o
	if h.finishFn != nil {
		return h.finishFn()
	}
	return h.finishErr
}

type fakeRecorder struct {
	h      *fakeHandle
	err    error
	bodies []submit.Body
}

func (r *fakeRecorder) Register(_ context.Context, b submit.Body) (Handle, error) {
	r.bodies = append(r.bodies, b)
	if r.err != nil {
		return nil, r.err
	}
	return r.h, nil
}

func newRec() *fakeRecorder {
	return &fakeRecorder{h: &fakeHandle{id: "j_fast1", lease: time.Minute, every: time.Hour}}
}

const goEvents = `{"Action":"pass","Package":"p","Test":"TestA"}
{"Action":"fail","Package":"p","Test":"TestB"}
`

func TestRunsInTheTreeUnderALightSlotAndRecordsTheResult(t *testing.T) {
	root := tree(t)
	rec := newRec()
	g := &fakeGate{run: func(_ context.Context, o Options) int {
		o.Stdout.Write([]byte(goEvents))
		o.Stderr.Write([]byte("FAIL\n"))
		return 1
	}}
	res, err := Run(context.Background(), Request{
		Root: root, Task: task(t, "go-pkg"), Args: []string{"./a", "./b"}, Gate: g, Recorder: rec,
		Env: []string{"PATH=/bin", "WWD_JOB=j_old", "KEEP=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if len(g.calls) != 1 {
		t.Fatalf("gate calls = %d", len(g.calls))
	}
	o := g.calls[0]
	if o.Class != ClassLight {
		t.Fatalf("class = %q, want light", o.Class)
	}
	if o.Dir != filepath.Join(root, "sub") {
		t.Fatalf("dir = %q", o.Dir)
	}
	if strings.Join(o.Argv, " ") != "go test -json ./a ./b" {
		t.Fatalf("argv = %q", o.Argv)
	}
	var marks []string
	for _, kv := range o.Env {
		if strings.HasPrefix(kv, "WWD_JOB=") {
			marks = append(marks, kv)
		}
	}
	if len(marks) != 1 || marks[0] != "WWD_JOB=j_fast1" {
		t.Fatalf("job mark = %v, want exactly the new job's", marks)
	}
	if res.State != wire.StateFailed || res.Exit != 1 || res.JobID != "j_fast1" || res.Err != nil {
		t.Fatalf("result = %+v", res)
	}
	if res.Summary.Passed != 1 || res.Summary.Failed != 1 || len(res.Summary.Tail) == 0 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	out := rec.h.outcome
	if out == nil || out.State != wire.StateFailed || out.Exit == nil || *out.Exit != 1 || out.LogPath == "" {
		t.Fatalf("recorded outcome = %+v", out)
	}
	if b, _ := os.ReadFile(out.LogPath); !strings.Contains(string(b), "FAIL") {
		t.Fatalf("log = %q", b)
	}
}

func TestSuccess(t *testing.T) {
	g := &fakeGate{}
	rec := newRec()
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateSucceeded || res.Exit != 0 || rec.h.outcome == nil || *rec.h.outcome.Exit != 0 {
		t.Fatalf("result = %+v, outcome %+v", res, rec.h.outcome)
	}
}

func TestNotLocalTasksAndTooManyArgsRunNothing(t *testing.T) {
	g := &fakeGate{}
	rec := newRec()
	for name, req := range map[string]Request{
		"local=false":    {Task: task(t, "remote-only")},
		"over the count": {Task: task(t, "go-pkg"), Args: []string{"./a", "./b", "./c"}},
	} {
		req.Root, req.Gate, req.Recorder = tree(t), g, rec
		if _, err := Run(context.Background(), req); !errors.Is(err, ErrNotLocal) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(g.calls) != 0 || len(rec.bodies) != 0 {
		t.Fatalf("something ran or was recorded: %d gate calls, %d records", len(g.calls), len(rec.bodies))
	}
}

func TestInvalidArgumentsRunNothing(t *testing.T) {
	g := &fakeGate{}
	rec := newRec()
	for name, args := range map[string][]string{
		"missing file": {"nope.ts"}, "dotdot": {"../x"}, "flag": {"--config=x"}, "none": nil,
	} {
		if _, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: args, Gate: g, Recorder: rec}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(g.calls) != 0 || len(rec.bodies) != 0 {
		t.Fatal("an invalid call reached the gate or the record")
	}
}

func TestBusyLocksSendTheCallToTheQueue(t *testing.T) {
	g := &fakeGate{}
	rec := newRec()
	rec.err = ErrLocksBusy
	_, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "go-pkg"), Args: []string{"./a"}, Gate: g, Recorder: rec})
	if !errors.Is(err, ErrLocksBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(g.calls) != 0 {
		t.Fatal("the command ran although a lock was busy")
	}
}

func TestAWwdThatCannotRecordStillRunsTheCommandUnrecorded(t *testing.T) {
	g := &fakeGate{run: func(context.Context, Options) int { return 0 }}
	rec := newRec()
	rec.err = ErrUnrecorded
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "go-pkg"), Args: []string{"./a"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if len(g.calls) != 1 || res.JobID != "" || res.State != wire.StateSucceeded {
		t.Fatalf("calls %d result %+v", len(g.calls), res)
	}
}

func TestTaskFixedEnvIsSetOverTheCallersAndTheRestOfItStays(t *testing.T) {
	tk := task(t, "one-file")
	tk.Env = []string{"CI=1", "KEEP", "WWD_JOB=other", "PDX_GATE_DIR=/x", "FIXED=a=b"}
	g := &fakeGate{}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g,
		Env: []string{"PATH=/bin", "CI=0", "KEEP=yes", "SECRET=s"}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	got := strings.Join(g.calls[0].Env, "\n")
	for _, want := range []string{"CI=1", "KEEP=yes", "SECRET=s", "PATH=/bin", "FIXED=a=b"} {
		if !strings.Contains("\n"+got+"\n", "\n"+want+"\n") {
			t.Errorf("env lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"CI=0", "WWD_JOB=other", "PDX_GATE_DIR=/x"} {
		if strings.Contains("\n"+got+"\n", "\n"+bad+"\n") {
			t.Errorf("env has %q:\n%s", bad, got)
		}
	}
}

func TestWithoutARecorderTheRunIsNotRecorded(t *testing.T) {
	g := &fakeGate{}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, Env: []string{"PATH=/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.JobID != "" || res.State != wire.StateSucceeded {
		t.Fatalf("result = %+v", res)
	}
	for _, kv := range g.calls[0].Env {
		if strings.HasPrefix(kv, "WWD_JOB=") {
			t.Fatalf("a run with no record carries a job mark: %q", kv)
		}
	}
}

func TestTimeout(t *testing.T) {
	tk := task(t, "one-file")
	tk.Timeout = 150 * time.Millisecond
	rec := newRec()
	g := &fakeGate{run: func(ctx context.Context, _ Options) int { <-ctx.Done(); return 143 }}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "timeout" || res.Exit != ExitTimeout {
		t.Fatalf("result = %+v", res)
	}
	if o := rec.h.outcome; o.Reason != "timeout" || o.Exit == nil || *o.Exit != ExitTimeout {
		t.Fatalf("recorded = %+v", o)
	}
}

func TestCallerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rec := newRec()
	g := &fakeGate{run: func(ctx context.Context, _ Options) int { cancel(); <-ctx.Done(); return 130 }}
	res, err := Run(ctx, Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateCancelled {
		t.Fatalf("result = %+v", res)
	}
	if o := rec.h.outcome; o == nil || o.State != wire.StateCancelled || o.Exit != nil {
		t.Fatalf("recorded = %+v (a cancelled run has no exit)", o)
	}
}

func mark(o Options) {
	for _, kv := range o.Env {
		if nonce, ok := strings.CutPrefix(kv, AdmittedMarkEnv+"="); ok {
			io.WriteString(o.Stderr, AdmittedMark(nonce))
		}
	}
}

func TestWaitingForASlotDoesNotCountAgainstTheTaskTimeout(t *testing.T) {
	tk := task(t, "one-file")
	tk.Timeout = 100 * time.Millisecond
	rec := newRec()
	g := &fakeGate{skipMark: true, run: func(_ context.Context, o Options) int {
		time.Sleep(300 * time.Millisecond) // waiting for a slot, three times the task's time
		mark(o)                            // admitted now
		return 0
	}}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateSucceeded || res.Reason != "" {
		t.Fatalf("a wait for a slot was counted as run time: %+v", res)
	}
}

func TestTheAdmittedMarkIsNotInTheOutput(t *testing.T) {
	g := &fakeGate{run: func(_ context.Context, o Options) int { o.Stderr.Write([]byte("real stderr\n")); return 0 }}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	b, _ := os.ReadFile(res.LogPath)
	if strings.Contains(string(b), "pdx-gate-admitted") || !strings.Contains(string(b), "real stderr") {
		t.Fatalf("log = %q", b)
	}
	if g.calls[0].WaitMax != task(t, "one-file").QueueTimeout {
		t.Fatalf("WaitMax = %v, want the task's queue timeout", g.calls[0].WaitMax)
	}
}

func TestAnInstantRefusalIsInfraWhateverTheQueueTimeout(t *testing.T) {
	// a refusal for another cause (a broken gate directory, the disk floor) comes at once; with a short remaining queue timeout the
	// old reading by elapsed time took it for the wait running out
	tk := task(t, "one-file")
	tk.QueueTimeout = time.Millisecond
	rec := newRec()
	g := &fakeGate{skipMark: true, run: func(context.Context, Options) int {
		time.Sleep(5 * time.Millisecond)
		return ExitRefused
	}}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "infra" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAWaitThatOutlastsTheQueueTimeoutExpires(t *testing.T) {
	tk := task(t, "one-file")
	tk.QueueTimeout = 200 * time.Millisecond
	rec := newRec()
	g := &fakeGate{skipMark: true, waitedOut: true, run: func(context.Context, Options) int {
		return ExitRefused
	}}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateExpired || res.Reason != "queue_timeout" {
		t.Fatalf("result = %+v", res)
	}
	if o := rec.h.outcome; o.State != wire.StateExpired || o.Exit != nil {
		t.Fatalf("recorded = %+v", o)
	}
}

func TestACommandThatExits75AfterBeingAdmittedIsATestResult(t *testing.T) {
	rec := newRec()
	g := &fakeGate{run: func(context.Context, Options) int { return ExitRefused }} // the mark was printed: it ran
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err != nil || res.State != wire.StateFailed || res.Exit != 75 || rec.h.outcome.Exit == nil || *rec.h.outcome.Exit != 75 {
		t.Fatalf("result = %+v", res)
	}
}

func TestATransientFailureToRecordTheResultIsRetried(t *testing.T) {
	old := deliverBackoff
	deliverBackoff = 5 * time.Millisecond
	defer func() { deliverBackoff = old }()
	rec := newRec()
	var tries atomic.Int32
	rec.h.finishFn = func() error {
		if tries.Add(1) < 3 {
			return errors.New("wwd unreachable")
		}
		return nil
	}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: &fakeGate{}, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err != nil || tries.Load() != 3 || rec.h.outcome == nil {
		t.Fatalf("tries %d, result %+v", tries.Load(), res)
	}
}

func TestAStaleClaimDiscardsTheResultAtOnce(t *testing.T) {
	old := deliverBackoff
	deliverBackoff = 5 * time.Millisecond
	defer func() { deliverBackoff = old }()
	rec := newRec()
	var tries atomic.Int32
	rec.h.finishFn = func() error { tries.Add(1); return ErrStale }
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: &fakeGate{}, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if tries.Load() != 1 || !errors.Is(res.Err, ErrStale) {
		t.Fatalf("tries %d, err %v", tries.Load(), res.Err)
	}
}

func TestDeliveryGivesUpAfterTheBoundedPeriod(t *testing.T) {
	oldB, oldF := deliverBackoff, deliverFor
	deliverBackoff, deliverFor = 5*time.Millisecond, 60*time.Millisecond
	defer func() { deliverBackoff, deliverFor = oldB, oldF }()
	rec := newRec()
	rec.h.finishFn = func() error { return errors.New("still down") }
	start := time.Now()
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: &fakeGate{}, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", res.Err, time.Since(start))
	}
}

func TestTheLeaseIsStillRenewedWhileTheResultIsBeingDelivered(t *testing.T) {
	old := deliverBackoff
	deliverBackoff = 30 * time.Millisecond
	defer func() { deliverBackoff = old }()
	rec := newRec()
	rec.h.every, rec.h.lease = 10*time.Millisecond, time.Minute
	var beats atomic.Int32
	rec.h.heartbeat = func() (bool, error) { beats.Add(1); return false, nil }
	var tries atomic.Int32
	var beatsAtFirstTry int32
	rec.h.finishFn = func() error {
		if tries.Add(1) == 1 {
			beatsAtFirstTry = beats.Load()
			return errors.New("down")
		}
		if beats.Load() <= beatsAtFirstTry {
			return errors.New("the heartbeat stopped while the result was undelivered")
		}
		return nil
	}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: &fakeGate{}, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err != nil {
		t.Fatalf("err = %v", res.Err)
	}
}

const shellTask = `
[tasks.missing-binary]
run = "wwd-no-such-binary-xyz"
args = "none"
local = true
timeout = "5s"
`

func shellTaskNamed(t *testing.T, name string) *manifest.Task {
	t.Helper()
	m, err := manifest.Parse([]byte(shellTask))
	if err != nil {
		t.Fatal(err)
	}
	return m.Tasks[name]
}

func TestDirectRunnerAdmitsAndMarksAreStripped(t *testing.T) {
	m, err := manifest.Parse([]byte("[tasks.t]\nrun = \"echo hello\"\nargs = \"none\"\nlocal = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	tk := m.Tasks["t"]
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Gate: directNoTTY{}, Env: testEnv()})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	b, _ := os.ReadFile(res.LogPath)
	if res.State != wire.StateSucceeded || res.Err != nil || strings.TrimSpace(string(b)) != "hello" {
		t.Fatalf("result = %+v, log = %q", res, b)
	}
}

func TestDirectRunnerCommandThatCannotStartIsInfrastructure(t *testing.T) {
	rec := newRec()
	res, err := Run(context.Background(), Request{Root: tree(t), Task: shellTaskNamed(t, "missing-binary"), Gate: directNoTTY{}, Recorder: rec, Env: testEnv()})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "infra" || res.Err == nil {
		t.Fatalf("a binary that does not exist was taken for a test result: %+v", res)
	}
	if o := rec.h.outcome; o == nil || o.Exit != nil || o.Reason != "infra" {
		t.Fatalf("recorded = %+v", o)
	}
}

func TestLeaseLostWhileWaitingToDeliverNeverTriesAgain(t *testing.T) {
	old := deliverBackoff
	deliverBackoff = 200 * time.Millisecond
	defer func() { deliverBackoff = old }()
	rec := newRec()
	rec.h.every, rec.h.lease = 10*time.Millisecond, 50*time.Millisecond
	var beating atomic.Bool
	beating.Store(true)
	rec.h.heartbeat = func() (bool, error) {
		if beating.Load() {
			return false, nil
		}
		return false, errors.New("409")
	}
	var tries atomic.Int32
	rec.h.finishFn = func() error {
		tries.Add(1)
		beating.Store(false) // from now on the origin refuses the heartbeats: the lease will be lost during the pause
		return errors.New("down")
	}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: &fakeGate{}, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if tries.Load() != 1 || !errors.Is(res.Err, ErrStale) {
		t.Fatalf("tries %d err %v: a lost claim must not be answered for again", tries.Load(), res.Err)
	}
}

func TestNotStartedWinsOverATimeoutThatFiredBetweenTheMarks(t *testing.T) {
	tk := task(t, "one-file")
	tk.Timeout = 5 * time.Millisecond
	g := &fakeGate{skipMark: true, run: func(ctx context.Context, o Options) int {
		mark(o) // admitted: the task's timer starts
		time.Sleep(60 * time.Millisecond)
		for _, kv := range o.Env { // the start failed after all
			if nonce, ok := strings.CutPrefix(kv, AdmittedMarkEnv+"="); ok {
				io.WriteString(o.Stderr, NotStartedMark(nonce))
			}
		}
		return 127
	}}
	rec := newRec()
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "infra" || res.Err == nil {
		t.Fatalf("a command that did not start was reported as %+v", res)
	}
	if o := rec.h.outcome; o.Exit != nil {
		t.Fatalf("an infrastructure failure must have no exit: %+v", o)
	}
}

func TestAnImmediateRefusalIsNotAnExpiryEvenWithAShortQueueTimeout(t *testing.T) {
	tk := task(t, "one-file")
	tk.QueueTimeout = 500 * time.Millisecond
	g := &fakeGate{skipMark: true, run: func(context.Context, Options) int { return ExitRefused }}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "infra" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAFileThatIsASymlinkOutOfTheTreeIsRefused(t *testing.T) {
	root := tree(t)
	outside := filepath.Join(t.TempDir(), "secret.ts")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	g := &fakeGate{}
	for _, f := range []string{"link.ts", "linkdir/secret.ts"} {
		if _, err := Run(context.Background(), Request{Root: root, Task: task(t, "one-file"), Args: []string{f}, Gate: g}); err == nil {
			t.Errorf("%s: a path that leaves the tree was accepted", f)
		}
	}
	if len(g.calls) != 0 {
		t.Fatal("the command ran")
	}
}

func TestGateRefusalIsInfrastructureNotATestResult(t *testing.T) {
	rec := newRec()
	g := &fakeGate{skipMark: true, run: func(context.Context, Options) int { return ExitRefused }}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err == nil || res.State != wire.StateFailed || res.Reason != "infra" {
		t.Fatalf("result = %+v", res)
	}
	if o := rec.h.outcome; o.Reason != "infra" || o.Exit != nil {
		t.Fatalf("recorded = %+v (an infrastructure failure has no exit)", o)
	}
}

func TestCancelFromTheOriginStopsTheCommand(t *testing.T) {
	var beats atomic.Int32
	rec := newRec()
	rec.h.every = 20 * time.Millisecond
	rec.h.heartbeat = func() (bool, error) { return beats.Add(1) >= 2, nil }
	g := &fakeGate{run: func(ctx context.Context, _ Options) int { <-ctx.Done(); return 143 }}
	tk := task(t, "one-file")
	tk.Timeout = 10 * time.Second
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateCancelled {
		t.Fatalf("result = %+v", res)
	}
}

func TestALeaseThatCannotBeRenewedEndsTheCommandAsLost(t *testing.T) {
	rec := newRec()
	rec.h.every, rec.h.lease = 10*time.Millisecond, 60*time.Millisecond
	rec.h.heartbeat = func() (bool, error) { return false, errors.New("409") }
	g := &fakeGate{run: func(ctx context.Context, _ Options) int { <-ctx.Done(); return 143 }}
	tk := task(t, "one-file")
	tk.Timeout = 10 * time.Second
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "lost" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAClaimWwdNoLongerAcceptsEndsTheCommandAtOnce(t *testing.T) {
	rec := newRec()
	rec.h.every, rec.h.lease = 10*time.Millisecond, time.Hour // the lease alone would take an hour to run out
	rec.h.heartbeat = func() (bool, error) { return false, ErrStale }
	g := &fakeGate{run: func(ctx context.Context, _ Options) int { <-ctx.Done(); return 143 }}
	tk := task(t, "one-file")
	tk.Timeout = 10 * time.Second
	start := time.Now()
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.State != wire.StateFailed || res.Reason != "lost" || time.Since(start) > 3*time.Second {
		t.Fatalf("result %+v after %v", res, time.Since(start))
	}
}

func TestAStaleHeartbeatDoesNotCutShortTheDeliveryOfTheResult(t *testing.T) {
	rec := newRec()
	rec.h.every, rec.h.lease = 10*time.Millisecond, time.Hour
	rec.h.heartbeat = func() (bool, error) { return false, ErrStale }
	tries := 0
	rec.h.finishFn = func() error { // the first reply is lost although the result was committed
		if tries++; tries == 1 {
			time.Sleep(50 * time.Millisecond) // a heartbeat meets the terminal job meanwhile
			return errors.New("connection reset")
		}
		return nil
	}
	oldB, oldF := deliverBackoff, deliverFor
	deliverBackoff, deliverFor = time.Millisecond, time.Minute
	defer func() { deliverBackoff, deliverFor = oldB, oldF }()
	g := &fakeGate{run: func(ctx context.Context, _ Options) int { <-ctx.Done(); return 143 }}
	tk := task(t, "one-file")
	tk.Timeout = 10 * time.Second
	res, err := Run(context.Background(), Request{Root: tree(t), Task: tk, Args: []string{"a.test.ts"}, Gate: g, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err != nil || tries != 2 {
		t.Fatalf("err %v after %d tries", res.Err, tries)
	}
}

func TestAFailedRecordOfTheResultIsReported(t *testing.T) {
	rec := newRec()
	oldB, oldF := deliverBackoff, deliverFor
	deliverBackoff, deliverFor = 5*time.Millisecond, 30*time.Millisecond
	defer func() { deliverBackoff, deliverFor = oldB, oldF }()
	rec.h.finishErr = errors.New("wwd unreachable")
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: &fakeGate{}, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Cleanup()
	if res.Err == nil || !strings.Contains(res.Err.Error(), "recorded") || res.State != wire.StateSucceeded {
		t.Fatalf("result = %+v", res)
	}
}
