package report

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

func endedOf(repo string, kind runner.Kind, res run.Result) runner.Ended {
	e := ended(kind, res)
	e.Job.Repo = wire.Repo{Name: repo}
	return e
}

// A sandboxed run of any outcome can carry an Err that says its unit was not proven stopped: what it did cannot be told, so it is
// an infrastructure failure whatever the outcome says: the claim goes back, no result is sent, and the repo is held off.
func TestARunWhoseUnitWasNotProvenStoppedIsReleasedAsInfra(t *testing.T) {
	unit := errors.New("the unit could not be proven stopped")
	cases := []struct {
		name string
		kind runner.Kind
		res  run.Result
	}{
		{"exited", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 0, Err: unit}},
		{"timed out", runner.KindRan, run.Result{Outcome: run.TimedOut, ExitCode: -1, Reason: "timeout", Err: unit}},
		{"oom", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: -1, Signal: "KILL", Reason: "oom", Err: unit}},
		{"cancelled outcome", runner.KindRan, run.Result{Outcome: run.Cancelled, ExitCode: -1, Err: unit}},
		{"cancelled kind", runner.KindCancelled, run.Result{Outcome: run.Cancelled, ExitCode: -1, Err: unit}},
		{"shutdown", runner.KindShutdown, run.Result{Outcome: run.Cancelled, ExitCode: -1, Err: unit}},
	}
	for _, c := range cases {
		h := newHarness(t, Config{})
		h.fin(context.Background(), endedOf("purdex", c.kind, c.res))
		assert.Empty(t, h.s.results, c.name+": a result was sent")
		assert.Empty(t, h.s.logs, c.name+": a log was sent")
		require.Len(t, h.s.releases, 1, c.name)
		assert.Equal(t, wire.ReleaseInfra, h.s.releases[0].Reason, c.name)
		assert.Contains(t, h.s.releases[0].Detail, "not proven stopped", c.name)
		assert.True(t, h.r.Suspended("purdex"), c.name+": the repo is held off")
	}
}

// A lost job is dropped whatever it carries.
func TestALostJobWithAnErrIsStillDropped(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), endedOf("purdex", runner.KindLost, run.Result{Outcome: run.Exited, Err: errors.New("x")}))
	assert.Empty(t, h.s.calls)
	assert.False(t, h.r.Suspended("purdex"))
}

// Only the sandbox's unit says OOM (Linux). A KILL signal alone is not one: on macOS, where the reason is always empty, it is a plain
// failure.
func TestOnlyTheUnitsWordIsAnOOM(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), endedOf("purdex", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: -1, Signal: "KILL", Reason: "oom"}))
	h.fin(context.Background(), endedOf("purdex", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: -1, Signal: "KILL"}))
	h.fin(context.Background(), endedOf("purdex", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: -1, Reason: "timeout"})) // the unit's fuse
	require.Len(t, h.s.results, 3)
	for i, want := range []string{"oom", "", "timeout"} {
		got := h.s.results[i]
		assert.Equal(t, wire.StateFailed, got.State, want)
		assert.Equal(t, want, got.Reason, "result %d", i)
		assert.Nil(t, got.Exit, want)
	}
}

// An infra release holds the repo off for SuspendFor from then, by the clock; nothing else does.
func TestAnInfraReleaseSuspendsTheRepoForFiveMinutes(t *testing.T) {
	h := newHarness(t, Config{})
	assert.False(t, h.r.Suspended("purdex"))
	e := endedOf("purdex", runner.KindInfra, run.Result{})
	e.Err = errors.New("no space")
	h.fin(context.Background(), e)
	assert.True(t, h.r.Suspended("purdex"))
	assert.False(t, h.r.Suspended("aigora"), "another repo")
	h.now = h.now.Add(5*time.Minute - time.Second)
	assert.True(t, h.r.Suspended("purdex"))
	h.now = h.now.Add(time.Second)
	assert.False(t, h.r.Suspended("purdex"), "the hold ends after five minutes")
	assert.Empty(t, h.r.suspended, "an ended hold is forgotten")
}

func TestSuspendForCanBeSet(t *testing.T) {
	h := newHarness(t, Config{SuspendFor: time.Minute})
	h.fin(context.Background(), endedOf("purdex", runner.KindRan, run.Result{Outcome: run.StartFailed, Err: errors.New("pnpm: not found")}))
	assert.True(t, h.r.Suspended("purdex"))
	h.now = h.now.Add(time.Minute)
	assert.False(t, h.r.Suspended("purdex"))
}

// A release for a shutdown or a result of any kind does not hold a repo off; a release the origin will not take still does (this host
// cannot run the repo whether or not the origin knows).
func TestOnlyAnInfraFailureSuspends(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), endedOf("purdex", runner.KindShutdown, run.Result{}))
	h.fin(context.Background(), endedOf("purdex", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 1}))
	h.fin(context.Background(), endedOf("purdex", runner.KindCancelled, run.Result{}))
	h.fin(context.Background(), endedOf("purdex", runner.KindRejected, run.Result{}))
	assert.False(t, h.r.Suspended("purdex"))

	h = newHarness(t, Config{})
	h.s.errs = []error{runner.ErrNotFound}
	e := endedOf("purdex", runner.KindInfra, run.Result{})
	e.Err = errors.New("x")
	h.fin(context.Background(), e)
	assert.True(t, h.r.Suspended("purdex"), "the origin did not take the release")
}

// fakeJob is a job the worker holds.

func running(id string, stop error) (Running, *bool) {
	stopped := new(bool)
	return Running{Source: "mlab", JobID: id, Claim: claim, StopJob: func(context.Context) error {
		*stopped = true
		return stop
	}}, stopped
}

type multiSink struct {
	mu    sync.Mutex
	by    map[string]*sink
	asked []string
}

func (m *multiSink) get(source string) Sink {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked = append(m.asked, source)
	if s, ok := m.by[source]; ok {
		return s
	}
	return nil
}

// Reclaim stops every job and gives each back, as reclaim, to its own origin.
func TestReclaimStopsAndGivesEachJobBackToItsOrigin(t *testing.T) {
	mlab, air := &sink{t: t}, &sink{t: t}
	ms := &multiSink{by: map[string]*sink{"mlab": mlab, "air26": air}}
	a, stoppedA := running("j_a", nil)
	b, stoppedB := running("j_b", nil)
	b.Source = "air26"
	r := New(Config{})
	require.NoError(t, r.Reclaim(context.Background(), []Running{a, b}, ms.get))
	assert.True(t, *stoppedA && *stoppedB)
	require.Len(t, mlab.releases, 1)
	require.Len(t, air.releases, 1)
	assert.Equal(t, wire.ReleaseReclaim, mlab.releases[0].Reason)
	assert.Equal(t, claim, air.releases[0].ClaimID)
}

// A job whose stop failed is not given back (it may still be running); the others are, and the error is returned.
func TestReclaimDoesNotGiveBackAJobThatWouldNotStop(t *testing.T) {
	mlab := &sink{t: t}
	ms := &multiSink{by: map[string]*sink{"mlab": mlab}}
	a, _ := running("j_a", errors.New("still running"))
	b, _ := running("j_b", nil)
	err := New(Config{}).Reclaim(context.Background(), []Running{a, b}, ms.get)
	require.Error(t, err)
	require.Len(t, mlab.releases, 1, "only the job that stopped went back")
	assert.Equal(t, claim, mlab.releases[0].ClaimID)
}

// The origin's 409 or 404 on the release is its decision about the claim, not a failure to reclaim.
func TestReclaimTakesARefusedReleaseAsDone(t *testing.T) {
	for name, refuse := range map[string]error{"409": &runner.Conflict{State: wire.StateQueued}, "404": runner.ErrNotFound} {
		mlab := &sink{t: t, errs: []error{refuse}}
		ms := &multiSink{by: map[string]*sink{"mlab": mlab}}
		a, _ := running("j_a", nil)
		assert.NoError(t, New(Config{}).Reclaim(context.Background(), []Running{a}, ms.get), name)
	}
}

func TestReclaimWithoutASinkIsAnError(t *testing.T) {
	ms := &multiSink{by: map[string]*sink{}}
	a, stopped := running("j_a", nil)
	require.Error(t, New(Config{}).Reclaim(context.Background(), []Running{a}, ms.get))
	assert.True(t, *stopped, "it was stopped; it could not be given back")
}
