package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/wire"
)

// endOnQueue runs one job through the queue the way a worker does (submit, claim, result) with nothing listening for its end: the
// outcome write that follows a result and failed is the same thing to the queue.
func endOnQueue(t *testing.T, h *jobHost, worker string, claimLabels []string, exit int) string {
	t.Helper()
	ctx := t.Context()
	sub, err := h.q.Submit(ctx, submissionBody(func(s *wire.JobSubmission) {
		s.Task, s.Args, s.Cacheable, s.DefHash = "go-changed", []string{"./a"}, true, "def1"
	}))
	require.NoError(t, err)
	cl, err := h.q.Claim(ctx, sub.ID, wire.ClaimRequest{Worker: worker, Labels: claimLabels, Fingerprint: map[string]string{"k": "v"}})
	require.NoError(t, err)
	state := wire.StateSucceeded
	if exit != 0 {
		state = wire.StateFailed
	}
	_, err = h.q.Result(ctx, sub.ID, wire.ResultRequest{ClaimID: cl.ClaimID, State: state, Exit: &exit})
	require.NoError(t, err)
	return sub.ID
}

// An outcome that was not recorded when the job ended is recorded by the daemon's reconcile, and so is the disagreement it makes.
func TestOutcomesThatWereNotRecordedAreRecordedLater(t *testing.T) {
	h := newJobHost(t, "mlab")
	d := New(Options{Alias: "mlab", Queue: h.q, ReferenceTasks: func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "go-changed", Reference: "air26"}}
	}})
	ref := endOnQueue(t, h, "air26", []string{"os:linux"}, 0)
	bad := endOnQueue(t, h, "mserv", []string{"os:linux", "task:purdex/go-changed"}, 1)
	assert.Empty(t, disagreementsAbout(t, h, "mserv").Items, "nothing was recorded when they ended")

	d.ReconcileOutcomes(t.Context())

	l := disagreementsAbout(t, h, "mserv")
	require.Len(t, l.Items, 1)
	assert.Equal(t, [2]string{ref, bad}, l.Items[0].Jobs)

	d.ReconcileOutcomes(t.Context()) // again: nothing is written twice
	assert.Len(t, disagreementsAbout(t, h, "mserv").Items, 1)
	// and a daemon with no queue does nothing
	New(Options{Alias: "mlab"}).ReconcileOutcomes(t.Context())
}

// A host with no standing is looked at and left alone, each time, without making anything.
func TestReconcileLeavesAHostWithNoStandingAlone(t *testing.T) {
	h := newJobHost(t, "mlab")
	d := New(Options{Alias: "mlab", Queue: h.q, ReferenceTasks: func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "go-changed", Reference: "air26"}}
	}})
	endOnQueue(t, h, "air26", []string{"os:linux"}, 0)
	endOnQueue(t, h, "mini", []string{"os:linux"}, 1) // no task label, and not the reference
	for i := 0; i < 3; i++ {
		d.ReconcileOutcomes(t.Context())
	}
	assert.Empty(t, disagreementsAbout(t, h, "mini").Items)
	assert.Empty(t, disagreementsAbout(t, h, "air26").Items)
}
