package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// An outcome is written after the job's result is committed, and that write can fail (a locked database): the worker's repeat of the
// result is an echo and offers nothing again, so the outcome is found from the jobs themselves. UnrecordedOutcomes lists the ended jobs of
// the last window that have an outcome to compare and no row in task_outcomes, with what the claim said.
func TestAJobWhoseOutcomeWasNotRecordedIsFoundAgain(t *testing.T) {
	f := newFixture(t)
	f.endings() // the callback is there, and does not record anything: the write that failed
	id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	c := f.claimAs(t, id, "mserv", lblA, fpA)
	f.postResult(t, id, c.ClaimID)
	f.postResult(t, id, c.ClaimID) // the worker's repeat: an echo, nothing offered

	got, err := f.q.UnrecordedOutcomes(bg, time.Hour, 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, id, got[0].Job.ID)
	assert.Equal(t, Produced{DefHash: "def1", Labels: lblA, Fingerprint: fpA}, got[0].Produced)

	// once it is recorded it is no longer listed
	_, err = f.q.RecordOutcome(bg, outcome(id, "mserv", StandingEarned, true))
	require.NoError(t, err)
	got, err = f.q.UnrecordedOutcomes(bg, time.Hour, 50)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// Only what would have been offered when it ended is listed again: not a job that ended outside the window, not one with nothing to
// compare, not one that is still running.
func TestWhatIsListedAsUnrecordedIsWhatWouldHaveBeenOffered(t *testing.T) {
	one := 1
	for name, tc := range map[string]struct {
		sub  func(*wire.JobSubmission)
		post func(*wire.ResultRequest)
		want int
	}{
		"a success":          {want: 1},
		"a failure":          {post: func(r *wire.ResultRequest) { r.State, r.Exit = wire.StateFailed, &one }, want: 1},
		"a timeout":          {post: func(r *wire.ResultRequest) { r.State, r.Exit, r.Reason = wire.StateFailed, nil, "timeout" }},
		"not cacheable":      {sub: func(s *wire.JobSubmission) { s.Cacheable = false }},
		"an undeclared one":  {sub: func(s *wire.JobSubmission) { s.AdHoc, s.Cacheable = true, false }},
		"no definition hash": {sub: func(s *wire.JobSubmission) { s.DefHash = "" }},
	} {
		f := newFixture(t)
		edit := []func(*wire.JobSubmission){func(s *wire.JobSubmission) { s.DefHash = "def1" }}
		if tc.sub != nil {
			edit = append(edit, tc.sub)
		}
		id := f.submit(t, edit...)
		c := f.claimAs(t, id, "mserv", lblA, fpA)
		var post []func(*wire.ResultRequest)
		if tc.post != nil {
			post = append(post, tc.post)
		}
		f.postResult(t, id, c.ClaimID, post...)
		got, err := f.q.UnrecordedOutcomes(bg, time.Hour, 50)
		require.NoError(t, err)
		assert.Len(t, got, tc.want, name)
	}

	// not yet ended
	g := newFixture(t)
	id := g.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	g.claimAs(t, id, "mserv", lblA, fpA)
	got, err := g.q.UnrecordedOutcomes(bg, time.Hour, 50)
	require.NoError(t, err)
	assert.Empty(t, got, "a claimed job has no outcome yet")

	// ended before the window
	h := newFixture(t)
	hid := h.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1" })
	hc := h.claimAs(t, hid, "mserv", lblA, fpA)
	h.postResult(t, hid, hc.ClaimID)
	h.clk.Advance(2 * time.Hour)
	got, err = h.q.UnrecordedOutcomes(bg, time.Hour, 50)
	require.NoError(t, err)
	assert.Empty(t, got, "it ended two hours ago")
}

// The list is bounded and newest first, so a pile of old jobs that have no standing cannot keep the recent ones from being looked at.
func TestUnrecordedOutcomesAreBoundedNewestFirst(t *testing.T) {
	f := newFixture(t)
	var ids []string
	for i := 0; i < 5; i++ {
		id := f.submit(t, func(s *wire.JobSubmission) { s.DefHash = "def1"; s.SupersedeKey = "" })
		c := f.claimAs(t, id, "mserv", lblA, fpA)
		f.postResult(t, id, c.ClaimID)
		ids = append(ids, id)
	}
	got, err := f.q.UnrecordedOutcomes(context.Background(), time.Hour, 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, ids[4], got[0].Job.ID)
	assert.Equal(t, ids[3], got[1].Job.ID)
}
