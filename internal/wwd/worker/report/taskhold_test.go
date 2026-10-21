package report

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// ---- a command that is not on this host's PATH holds off its task, not the repo (#3908 b) ----

func notFound() error { return fmt.Errorf("run: %q not found in PATH: %w", "claude", exec.ErrNotFound) }

// endedTask is a job of task in repo whose run ended with res.
func endedTask(repo, task string, kind runner.Kind, res run.Result) runner.Ended {
	e := endedOf(repo, kind, res)
	e.Job.Task = task
	return e
}

func startFailed(err error) run.Result {
	return run.Result{Outcome: run.StartFailed, ExitCode: -1, Err: err}
}

func TestAMissingCommandHoldsOffItsTaskNotTheRepo(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), endedTask("purdex", "mod-hooks", runner.KindRan, startFailed(notFound())))

	require.Len(t, h.s.releases, 1)
	assert.Equal(t, wire.ReleaseInfra, h.s.releases[0].Reason, "the reason stays infra: the origin counts the attempt")
	assert.True(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"))
	assert.False(t, h.r.TaskHeld("mlab", "purdex", "go-changed"), "another task of the repo")
	assert.False(t, h.r.Suspended("mlab", "purdex"), "the repo is not held off")
}

func TestOtherStartFailuresAndOtherInfraStillHoldTheWholeRepo(t *testing.T) {
	cases := map[string]runner.Ended{
		"the workspace is missing":  endedTask("purdex", "mod-hooks", runner.KindRan, startFailed(errors.New("mkdir /x: permission denied"))),
		"a start failure, no error": endedTask("purdex", "mod-hooks", runner.KindRan, run.Result{Outcome: run.StartFailed, ExitCode: -1}),
		"an infra ending": func() runner.Ended {
			e := endedTask("purdex", "mod-hooks", runner.KindInfra, run.Result{})
			e.Err = errors.New("no space")
			return e
		}(),
		"a unit not proven stopped": endedTask("purdex", "mod-hooks", runner.KindRan, run.Result{Outcome: run.Exited, Err: errors.New("unit")}),
		"a not-found that ran":      endedTask("purdex", "mod-hooks", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 127, Err: notFound()}),
	}
	for name, e := range cases {
		h := newHarness(t, Config{})
		h.fin(context.Background(), e)
		assert.True(t, h.r.Suspended("mlab", "purdex"), name+": the repo is held off")
		assert.False(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"), name+": no task hold")
	}
}

func TestTheTaskHoldDoublesToItsCeilingAndEnds(t *testing.T) {
	h := newHarness(t, Config{})
	fail := func() {
		h.fin(context.Background(), endedTask("purdex", "mod-hooks", runner.KindRan, startFailed(notFound())))
	}

	fail() // 15 s
	h.now = h.now.Add(15*time.Second - time.Second)
	assert.True(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"))
	h.now = h.now.Add(time.Second)
	assert.False(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"), "the first wait is 15 seconds")

	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		fail()
		h.now = h.now.Add(w - time.Second)
		assert.True(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"), "failure %d: held for %v", i+2, w)
		h.now = h.now.Add(time.Second)
		assert.False(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"), "failure %d: over after %v", i+2, w)
	}
}

func TestARunOfTheTaskEndsItsRunOfFailures(t *testing.T) {
	h := newHarness(t, Config{})
	fail := func() {
		h.fin(context.Background(), endedTask("purdex", "mod-hooks", runner.KindRan, startFailed(notFound())))
	}
	fail()
	h.now = h.now.Add(16 * time.Second)
	fail() // 30 s
	h.now = h.now.Add(31 * time.Second)

	h.fin(context.Background(), endedTask("purdex", "mod-hooks", runner.KindRan, run.Result{Outcome: run.Exited, ExitCode: 0, StartedAt: h.now, FinishedAt: h.now}))
	assert.Empty(t, h.r.taskHeld, "the command was found: the run of failures is over")

	fail()
	h.now = h.now.Add(15 * time.Second)
	assert.False(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"), "back at the first wait")
}

func TestTheTaskHoldIsPerOriginRepoAndTask(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), endedTask("purdex", "mod-hooks", runner.KindRan, startFailed(notFound())))
	assert.True(t, h.r.TaskHeld("mlab", "purdex", "mod-hooks"))
	assert.False(t, h.r.TaskHeld("air26", "purdex", "mod-hooks"), "another origin")
	assert.False(t, h.r.TaskHeld("mlab", "aigora", "mod-hooks"), "another repo with a task of the same name")
}

// The hold begins and ends with one line each, naming the origin, repo, task and when the task may be taken again.
func TestTheTaskHoldIsLoggedWhenItBeginsAndWhenItEnds(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), endedTask("purdex", "mod-hooks", runner.KindRan, startFailed(notFound())))
	until := h.now.Add(15 * time.Second).Format(time.RFC3339)
	var begin []string
	for _, l := range h.logs {
		if strings.Contains(l, "mod-hooks") {
			begin = append(begin, l)
		}
	}
	require.Len(t, begin, 1)
	for _, w := range []string{"mlab", "purdex", "mod-hooks", until} {
		assert.Contains(t, begin[0], w)
	}

	h.now = h.now.Add(10 * time.Second)
	h.r.TaskHeld("mlab", "purdex", "mod-hooks")
	h.now = h.now.Add(10 * time.Second)
	h.r.TaskHeld("mlab", "purdex", "mod-hooks")
	h.r.TaskHeld("mlab", "purdex", "mod-hooks") // asked every round: one line only
	var all []string
	for _, l := range h.logs {
		if strings.Contains(l, "mod-hooks") {
			all = append(all, l)
		}
	}
	require.Len(t, all, 2, "one line when it begins, one when it ends: %v", all)
	assert.Contains(t, all[1], "over")
	assert.Contains(t, all[1], "purdex")
}
