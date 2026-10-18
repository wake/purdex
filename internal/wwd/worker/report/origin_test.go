package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

func originEnded(repo, id string, err error) runner.Ended {
	e := endedOf(repo, runner.KindOrigin, run.Result{})
	e.Job.ID = id
	e.Err = err
	return e
}

// The origin's own failure (#3379): the claim goes back with reason reclaim, which counts no attempt, and the detail says it was the
// origin's, with its status and the start of its words. The repo is not held off: nothing is wrong with this host.
func TestAnOriginErrorIsReleasedAsReclaimWithTheOriginsWordsAndDoesNotSuspend(t *testing.T) {
	h := newHarness(t, Config{})
	body := strings.Repeat("the commit of this job is not pinned. ", 20)
	h.fin(context.Background(), originEnded("purdex", "j_a", &snapshot.OriginError{Status: 500, Body: body}))
	require.Len(t, h.s.releases, 1)
	assert.Equal(t, wire.ReleaseReclaim, h.s.releases[0].Reason)
	d := h.s.releases[0].Detail
	assert.True(t, strings.HasPrefix(d, "origin snapshot 500: the commit of this job is not pinned"), d)
	assert.LessOrEqual(t, len(d), len("origin snapshot 500: ")+200+len("…"), "the origin's words are clipped to 200")
	assert.False(t, h.r.Suspended("mlab", "purdex"), "the host is not held off the repo")
	assert.False(t, h.r.Suspended("mlab", "aigora"))
	assert.Empty(t, h.s.results, "no result: the job is not over")
}

func TestAnOriginBusyIsSaidToBeBusy(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), originEnded("purdex", "j_a", snapshot.ErrBusy))
	require.Len(t, h.s.releases, 1)
	assert.Equal(t, wire.ReleaseReclaim, h.s.releases[0].Reason)
	assert.Equal(t, "origin snapshot busy", h.s.releases[0].Detail)
}

// The log names the origin, the job and the repo, so the worker's operator sees what the origin said (the origin's job record does
// not keep the detail).
func TestAnOriginErrorIsLogged(t *testing.T) {
	h := newHarness(t, Config{})
	h.fin(context.Background(), originEnded("purdex", "j_a", &snapshot.OriginError{Status: 500, Body: "not pinned"}))
	joined := strings.Join(h.logs, "\n")
	for _, want := range []string{"mlab", "j_a", "purdex", "500", "not pinned"} {
		assert.Contains(t, joined, want)
	}
}

// What a job of the origin does next is held off per job: the same job is not picked again at once (a 500 that stays would be a loop
// at the poll rate), the wait doubles with every failure up to a longest, and another job of the same repo is not touched.
func TestAnOriginErrorHoldsOffThatJobAndDoublesTheWait(t *testing.T) {
	h := newHarness(t, Config{})
	oe := &snapshot.OriginError{Status: 500, Body: "x"}
	assert.False(t, h.r.HeldOff("mlab", "j_a"))
	h.fin(context.Background(), originEnded("purdex", "j_a", oe))
	assert.True(t, h.r.HeldOff("mlab", "j_a"))
	assert.False(t, h.r.HeldOff("mlab", "j_b"), "another job of the same repo")
	assert.False(t, h.r.HeldOff("evil", "j_a"), "the same id at another origin")

	h.now = h.now.Add(15*time.Second - time.Millisecond)
	assert.True(t, h.r.HeldOff("mlab", "j_a"), "15 s for the first failure")
	h.now = h.now.Add(time.Millisecond)
	assert.False(t, h.r.HeldOff("mlab", "j_a"))

	h.fin(context.Background(), originEnded("purdex", "j_a", oe)) // the second failure in a row: 30 s
	h.now = h.now.Add(30*time.Second - time.Millisecond)
	assert.True(t, h.r.HeldOff("mlab", "j_a"))
	h.now = h.now.Add(time.Millisecond)
	assert.False(t, h.r.HeldOff("mlab", "j_a"))

	for i := 0; i < 8; i++ { // 60 s, 120 s, 240 s, then the longest (5 min) however many times it fails
		h.fin(context.Background(), originEnded("purdex", "j_a", oe))
		h.now = h.now.Add(5 * time.Minute)
	}
	h.fin(context.Background(), originEnded("purdex", "j_a", oe))
	h.now = h.now.Add(5*time.Minute - time.Millisecond)
	assert.True(t, h.r.HeldOff("mlab", "j_a"))
	h.now = h.now.Add(time.Millisecond)
	assert.False(t, h.r.HeldOff("mlab", "j_a"), "five minutes at most")
}

func TestTheOriginWaitsCanBeSet(t *testing.T) {
	h := newHarness(t, Config{OriginBackoff: 2 * time.Second, OriginBackoffMax: 3 * time.Second})
	oe := &snapshot.OriginError{Status: 500}
	h.fin(context.Background(), originEnded("purdex", "j_a", oe))
	h.now = h.now.Add(2 * time.Second)
	assert.False(t, h.r.HeldOff("mlab", "j_a"))
	h.fin(context.Background(), originEnded("purdex", "j_a", oe))
	h.now = h.now.Add(3*time.Second - time.Millisecond)
	assert.True(t, h.r.HeldOff("mlab", "j_a"), "doubled to 4 s but cut to 3 s")
	h.now = h.now.Add(time.Millisecond)
	assert.False(t, h.r.HeldOff("mlab", "j_a"))
}

// A job that goes on to something else, and a wait that ran out long ago, are forgotten: the count of failures does not live for ever.
func TestAnOldOriginFailureIsForgotten(t *testing.T) {
	h := newHarness(t, Config{})
	oe := &snapshot.OriginError{Status: 500}
	h.fin(context.Background(), originEnded("purdex", "j_a", oe))
	h.fin(context.Background(), originEnded("purdex", "j_a", oe)) // 30 s now
	h.now = h.now.Add(time.Hour)
	assert.False(t, h.r.HeldOff("mlab", "j_a"))
	assert.Empty(t, h.r.originHeld, "forgotten once the wait is long over")
	h.fin(context.Background(), originEnded("purdex", "j_a", oe))
	h.now = h.now.Add(15 * time.Second)
	assert.False(t, h.r.HeldOff("mlab", "j_a"), "the first failure again: 15 s, not 60")
}

// A host-side failure still suspends the repo and does not hold a job off; the two do not mix.
func TestAnInfraFailureStillSuspendsAndDoesNotHoldTheJobOff(t *testing.T) {
	h := newHarness(t, Config{})
	e := endedOf("purdex", runner.KindInfra, run.Result{})
	e.Err = assert.AnError
	h.fin(context.Background(), e)
	require.Len(t, h.s.releases, 1)
	assert.Equal(t, wire.ReleaseInfra, h.s.releases[0].Reason)
	assert.True(t, h.r.Suspended("mlab", "purdex"))
	assert.False(t, h.r.HeldOff("mlab", "j_a"))
}
