package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const claim = "0123456789abcdef0123456789abcdef"

// sink records what it is sent and validates every request against the wire contract. errs is a script: the error each next call
// of any kind returns (nil: success); when it runs out, calls succeed.
type sink struct {
	mu       sync.Mutex
	errs     []error
	always   error
	onResult func() // runs when a result is taken (a test ends the lease's context here)
	calls    []string
	results  []wire.ResultRequest
	releases []wire.ReleaseRequest
	logs     []string
	t        *testing.T
}

func (s *sink) next(call string) error {
	s.calls = append(s.calls, call)
	if s.always != nil {
		return s.always
	}
	if len(s.errs) == 0 {
		return nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
}

func (s *sink) Result(_ context.Context, id string, req wire.ResultRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NoError(s.t, req.Validate(), "a result that the origin would refuse")
	if err := s.next("result"); err != nil {
		return err
	}
	s.results = append(s.results, req)
	if s.onResult != nil {
		s.onResult()
	}
	return nil
}

func (s *sink) PutLog(_ context.Context, id, claimID string, body io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := io.ReadAll(body)
	if err := s.next("log"); err != nil {
		return err
	}
	assert.Equal(s.t, claim, claimID)
	s.logs = append(s.logs, string(b))
	return nil
}

func (s *sink) Release(_ context.Context, id string, req wire.ReleaseRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NoError(s.t, req.Validate(), "a release that the origin would refuse")
	if err := s.next("release"); err != nil {
		return err
	}
	s.releases = append(s.releases, req)
	return nil
}

// harness is a Reporter with a fake clock whose waits advance it, and the lines it logs.
type harness struct {
	r     *Reporter
	s     *sink
	now   time.Time
	waits []time.Duration
	logs  []string
	fin   runner.Finish
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	h := &harness{s: &sink{t: t}, now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	cfg.Now = func() time.Time { return h.now }
	cfg.Wait = func(_ context.Context, d time.Duration) error { // does not look at ctx: the retry loop's own check is what is tested
		h.waits = append(h.waits, d)
		h.now = h.now.Add(d)
		return nil
	}
	cfg.Logf = func(f string, a ...any) { h.logs = append(h.logs, fmt.Sprintf(f, a...)) }
	h.r = New(cfg)
	h.fin = h.r.Finish(func(string) Sink { return h.s })
	return h
}

func ended(kind runner.Kind, res run.Result) runner.Ended {
	return runner.Ended{Kind: kind, Source: "mlab", Job: wire.Job{ID: "j_a", Task: "lint"}, ClaimID: claim, Run: res}
}

func logFile(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log")
	require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
	return p
}

func TestARunThatSucceededPostsTheResultThenTheLog(t *testing.T) {
	h := newHarness(t, Config{})
	start := h.now.Add(-time.Minute)
	e := ended(runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 0, StartedAt: start, FinishedAt: h.now, LogPath: logFile(t, "ok\n"), Tail: []byte("ok\n")})
	h.fin(context.Background(), e)
	require.Len(t, h.s.results, 1)
	got := h.s.results[0]
	assert.Equal(t, wire.StateSucceeded, got.State)
	require.NotNil(t, got.Exit)
	assert.Equal(t, 0, *got.Exit)
	assert.Equal(t, claim, got.ClaimID)
	assert.Equal(t, start, *got.StartedAt)
	assert.NotNil(t, got.Summary)
	assert.Empty(t, got.Reason)
	assert.Equal(t, []string{"ok\n"}, h.s.logs)
	assert.Equal(t, []string{"result", "log"}, h.s.calls, "the result first, then the log")
}

func TestAFailedRunCarriesItsExitCodeAndASignalDoesNot(t *testing.T) {
	cases := []struct {
		name   string
		res    run.Result
		exit   *int
		reason string
	}{
		{"exit 3", run.Result{Outcome: run.Exited, ExitCode: 3}, intp(3), ""},
		{"killed by a signal", run.Result{Outcome: run.Exited, ExitCode: -1, Signal: "KILL"}, nil, ""},
		{"its own timeout", run.Result{Outcome: run.TimedOut, ExitCode: -1}, nil, "timeout"},
	}
	for _, c := range cases {
		h := newHarness(t, Config{})
		h.fin(context.Background(), ended(runner.KindRan, c.res))
		require.Len(t, h.s.results, 1, c.name)
		got := h.s.results[0]
		assert.Equal(t, wire.StateFailed, got.State, c.name)
		assert.Equal(t, c.exit, got.Exit, c.name)
		assert.Equal(t, c.reason, got.Reason, c.name)
	}
}

// A run whose log could not be kept, or that never started, is not a result of the job: the claim goes back as infra, with why.
func TestARunThatCannotBeReportedAsAResultIsReleasedAsInfra(t *testing.T) {
	for name, res := range map[string]run.Result{
		"capture error": {Outcome: run.Exited, CaptureErr: errors.New("disk full")},
		"start failed":  {Outcome: run.StartFailed, ExitCode: -1, Err: errors.New("pnpm: not found")},
	} {
		h := newHarness(t, Config{})
		h.fin(context.Background(), ended(runner.KindRan, res))
		assert.Empty(t, h.s.results, name)
		require.Len(t, h.s.releases, 1, name)
		assert.Equal(t, wire.ReleaseInfra, h.s.releases[0].Reason, name)
		assert.NotEmpty(t, h.s.releases[0].Detail, name)
	}
}

func TestACancelledRunPostsCancelled(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), ended(runner.KindCancelled, run.Result{Outcome: run.Cancelled, ExitCode: -1}))
	require.Len(t, h.s.results, 1)
	assert.Equal(t, wire.StateCancelled, h.s.results[0].State)
	assert.Nil(t, h.s.results[0].Exit)
}

// A rejected job is failed with no exit, the reason as the one line of the tail and of the log, and no reason field (a rejection is not
// one of oom, timeout or infra).
func TestARejectedJobFailsWithTheReasonAsTheTailAndTheLog(t *testing.T) {
	h := newHarness(t, Config{})
	e := ended(runner.KindRejected, run.Result{})
	e.Err = &runner.Rejected{Reason: `task_unknown: "x" is not a task of the snapshot's manifest`}
	h.fin(context.Background(), e)
	require.Len(t, h.s.results, 1)
	got := h.s.results[0]
	assert.Equal(t, wire.StateFailed, got.State)
	assert.Nil(t, got.Exit)
	assert.Empty(t, got.Reason)
	assert.Equal(t, []string{`task_unknown: "x" is not a task of the snapshot's manifest`}, got.Summary.Tail)
	assert.Empty(t, got.Summary.Failures)
	assert.Equal(t, []string{`task_unknown: "x" is not a task of the snapshot's manifest` + "\n"}, h.s.logs)
}

func TestALostJobSendsNothing(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), ended(runner.KindLost, run.Result{Outcome: run.Exited, ExitCode: 0}))
	assert.Empty(t, h.s.calls)
}

func TestShutdownAndInfraAreReleases(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), ended(runner.KindShutdown, run.Result{}))
	e := ended(runner.KindInfra, run.Result{})
	e.Err = errors.New("the workspace could not be made:\n  no space left")
	h.fin(context.Background(), e)
	require.Len(t, h.s.releases, 2)
	assert.Equal(t, wire.ReleaseReclaim, h.s.releases[0].Reason)
	assert.Equal(t, wire.ReleaseInfra, h.s.releases[1].Reason)
	assert.Equal(t, "the workspace could not be made: no space left", h.s.releases[1].Detail, "one line")
}

// A delivery that fails is tried again with a pause that doubles up to the longest, and stops when it succeeds.
func TestADeliveryIsRetriedWithADoublingPause(t *testing.T) {
	h := newHarness(t, Config{Backoff: time.Second, MaxBackoff: 4 * time.Second})
	h.s.errs = []error{errors.New("down"), errors.New("down"), errors.New("down"), errors.New("down"), nil}
	h.fin(context.Background(), ended(runner.KindCancelled, run.Result{}))
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}, h.waits)
	assert.Len(t, h.s.results, 1)
}

// The origin's refusal is final: a 409 means the claim is no longer live, a 404 that the job is gone. Neither is sent again, and the
// log line says which, so a late result that was refused can be told from one that was lost.
func TestARefusalEndsTheDeliveryAndSaysWhich(t *testing.T) {
	h := newHarness(t, Config{})
	h.s.errs = []error{&runner.Conflict{State: wire.StateQueued}}
	h.fin(context.Background(), ended(runner.KindCancelled, run.Result{}))
	assert.Equal(t, []string{"result"}, h.s.calls)
	require.Len(t, h.logs, 1)
	assert.Contains(t, h.logs[0], "409")
	assert.Contains(t, h.logs[0], "queued")
	assert.Contains(t, h.logs[0], "no longer live")

	h = newHarness(t, Config{})
	h.s.errs = []error{runner.ErrNotFound}
	h.fin(context.Background(), ended(runner.KindCancelled, run.Result{}))
	assert.Equal(t, []string{"result"}, h.s.calls)
	require.Len(t, h.logs, 1)
	assert.Contains(t, h.logs[0], "404")
}

// A result the origin refused (409: the claim is not live, 404: the job is gone) is not followed by the log: the log goes with the
// claim, and a claim that is not live may not upload.
func TestNoLogFollowsARefusedResult(t *testing.T) {
	for name, refuse := range map[string]error{"409": &runner.Conflict{State: wire.StateFailed}, "404": runner.ErrNotFound} {
		h := newHarness(t, Config{})
		h.s.errs = []error{refuse}
		h.fin(context.Background(), ended(runner.KindRan, run.Result{Outcome: run.Exited, LogPath: logFile(t, "x")}))
		assert.Equal(t, []string{"result"}, h.s.calls, name)

		h = newHarness(t, Config{})
		h.s.errs = []error{refuse}
		e := ended(runner.KindRejected, run.Result{})
		e.Err = &runner.Rejected{Reason: "task_unknown"}
		h.fin(context.Background(), e)
		assert.Equal(t, []string{"result"}, h.s.calls, name+" (rejected)")
	}
}

// A result the origin did not take (still down) is not followed by the log.
func TestNoLogFollowsAResultThatWasNotTaken(t *testing.T) {
	h := newHarness(t, Config{})
	h.s.always = errors.New("down")
	h.fin(context.Background(), ended(runner.KindRan, run.Result{Outcome: run.Exited, LogPath: logFile(t, "x")}))
	for _, c := range h.s.calls {
		assert.Equal(t, "result", c, "only the result is tried while it is not taken")
	}
	assert.Empty(t, h.s.logs)
}

// Ten minutes of trying is the end of it, by the clock; the pauses are what moves it.
func TestADeliveryIsGivenUpAfterTenMinutes(t *testing.T) {
	h := newHarness(t, Config{Backoff: time.Second, MaxBackoff: 30 * time.Second})
	h.s.always = errors.New("down")
	h.fin(context.Background(), ended(runner.KindCancelled, run.Result{}))
	assert.LessOrEqual(t, h.now.Sub(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)), RetryFor)
	assert.Greater(t, h.now.Sub(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)), RetryFor-31*time.Second)
	require.NotEmpty(t, h.logs)
	assert.Contains(t, h.logs[len(h.logs)-1], "given up")
}

func TestACancelledContextStopsTheDelivery(t *testing.T) {
	h := newHarness(t, Config{})
	h.s.always = errors.New("down")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.fin(ctx, ended(runner.KindCancelled, run.Result{}))
	assert.Equal(t, []string{"result"}, h.s.calls, "one try, then it stops")
	assert.Empty(t, h.waits)
}

func TestAnUnknownOriginIsLoggedAndDropped(t *testing.T) {
	h := newHarness(t, Config{})
	fin := h.r.Finish(func(string) Sink { return nil })
	fin(context.Background(), ended(runner.KindRan, run.Result{}))
	require.Len(t, h.logs, 1)
	assert.Contains(t, h.logs[0], "no sink")
}

// A log that is gone does not undo a result that was taken.
func TestAMissingLogFileIsNotAFailure(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), ended(runner.KindRan, run.Result{Outcome: run.Exited, LogPath: filepath.Join(t.TempDir(), "gone")}))
	assert.Len(t, h.s.results, 1)
	assert.Equal(t, []string{"result"}, h.s.calls)
}

// The summary is built from what the run left in its output directory, by the task's kind.
func TestTheSummaryIsBuiltFromTheFilesTheRunLeft(t *testing.T) {
	dir := t.TempDir()
	stdout := filepath.Join(dir, "stdout")
	events := `{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0.1}
{"Action":"run","Package":"p","Test":"TestB"}
{"Action":"fail","Package":"p","Test":"TestB","Elapsed":0.1}
`
	require.NoError(t, os.WriteFile(stdout, []byte(events), 0o600))
	h := newHarness(t, Config{})
	e := ended(runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 1, StdoutPath: stdout, Tail: []byte("FAIL\n")})
	e.Plan = runner.Plan{Summary: "go-json", OutDir: dir}
	e.Root = dir
	h.fin(context.Background(), e)
	require.Len(t, h.s.results, 1)
	s := h.s.results[0].Summary
	require.NotNil(t, s)
	assert.Equal(t, 1, s.Passed)
	assert.Equal(t, 1, s.Failed)
	assert.Equal(t, []string{"FAIL"}, s.Tail)
}

func TestAReportThatCannotBeReadLeavesZeroCountsAndTheTail(t *testing.T) {
	h := newHarness(t, Config{})
	e := ended(runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 1, Tail: []byte("boom\n")})
	e.Plan = runner.Plan{Summary: "vitest-json", OutDir: t.TempDir()} // no report.json in it
	h.fin(context.Background(), e)
	require.Len(t, h.s.results, 1)
	s := h.s.results[0].Summary
	assert.Zero(t, s.Passed+s.Failed)
	assert.Equal(t, []string{"boom"}, s.Tail)
	assert.NotNil(t, s.Failures)
	assert.True(t, strings.Contains(strings.Join(h.logs, "\n"), "summary"), "the unreadable report is logged")
}

// The result makes the job final, the next heartbeat is refused and the lease's context ends: the log that follows is retried under
// its own, so a transient failure of the upload does not lose it.
func TestTheLogIsRetriedAfterTheLeasesContextEnds(t *testing.T) {
	h := newHarness(t, Config{})
	ctx, endLease := context.WithCancel(context.Background())
	h.s.onResult = endLease
	h.s.errs = []error{nil, errors.New("down"), errors.New("down"), nil} // the result; the log twice transient, then taken
	h.fin(ctx, ended(runner.KindRan, run.Result{Outcome: run.Exited, LogPath: logFile(t, "line\n")}))
	assert.Equal(t, []string{"result", "log", "log", "log"}, h.s.calls)
	assert.Equal(t, []string{"line\n"}, h.s.logs)
}

// A 409 on the log itself is the origin saying the claim is not live: it stops there.
func TestARefusedLogIsNotSentAgain(t *testing.T) {
	h := newHarness(t, Config{})
	h.s.errs = []error{nil, &runner.Conflict{State: wire.StateFailed}}
	h.fin(context.Background(), ended(runner.KindRan, run.Result{Outcome: run.Exited, LogPath: logFile(t, "x")}))
	assert.Equal(t, []string{"result", "log"}, h.s.calls)
	require.NotEmpty(t, h.logs)
	assert.Contains(t, h.logs[len(h.logs)-1], "409")
}

// The report and stdout files are the task's: a link (to anything the worker can read) and a pipe (which would block the worker) are
// not read, and the summary has its zero counts and the tail.
func TestSummaryInputsThatAreNotRegularFilesAreNotRead(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(secret, []byte(`{"numPassedTests":99,"numFailedTests":99}`), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(dir, "report.json")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "junit.xml"), 0o600))
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "stdout"), 0o600))
	h := newHarness(t, Config{})
	e := ended(runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 1, StdoutPath: filepath.Join(dir, "stdout"), Tail: []byte("t\n")})
	e.Plan = runner.Plan{Summary: "vitest-json", OutDir: dir}
	done := make(chan struct{})
	go func() { h.fin(context.Background(), e); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a pipe in the output directory blocked the reporter")
	}
	require.Len(t, h.s.results, 1)
	s := h.s.results[0].Summary
	assert.Zero(t, s.Passed+s.Failed, "the linked file was read")
}
