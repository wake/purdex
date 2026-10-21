package queue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-3b-1: QueuedView is the queued jobs another host's worker could take, with what the alert rules of the queue read: when each was
// submitted, how many times it was put back and counted, whether a person holds it. It reads the table.
func TestQueuedViewHasWhatTheQueueRulesRead(t *testing.T) {
	f := newFixture(t)
	distinct := func(s *wire.JobSubmission) { s.SupersedeKey = "" }
	plain := f.submit(t, distinct, func(s *wire.JobSubmission) { s.Labels = []string{"os:darwin"} })
	f.clk.Advance(time.Minute)
	held := f.submit(t, distinct)
	_, err := f.q.SetPosition(bg, held, wire.PositionHold)
	require.NoError(t, err)
	f.clk.Advance(time.Minute)
	reclaimed := f.submit(t, distinct)
	f.clk.Advance(time.Minute)
	local := f.submit(t, distinct)
	f.seed(t, local, wire.StateQueued, "local = 1")
	adhoc := f.submit(t, distinct)
	f.seed(t, adhoc, wire.StateQueued, "adhoc = 1")
	running := f.submit(t, distinct)

	// a job that was put back by a reclaim (the owner wanted the machine) is not counted
	c := f.claim(t, reclaimed, "air26")
	require.NoError(t, f.q.Release(bg, reclaimed, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}))

	// a job put back by two infrastructure releases and a lease that ran out: three attempts (the lapse is one of them)
	bounced := f.submit(t, distinct)
	for i := 0; i < 2; i++ {
		c := f.claim(t, bounced, "air26")
		require.NoError(t, f.q.Release(bg, bounced, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseInfra}))
	}
	f.claim(t, bounced, "air26")
	f.clk.Advance(61 * time.Second)
	_, err = f.q.Sweep(bg)
	require.NoError(t, err)
	putBack := f.clk.Now()
	f.clk.Advance(2 * time.Minute)
	f.claim(t, running, "air26") // claimed after the sweep, whose lapse it would otherwise be

	got, err := f.q.QueuedView(bg)
	require.NoError(t, err)
	byID := map[string]QueuedJob{}
	var order []string
	for _, j := range got {
		byID[j.ID] = j
		order = append(order, j.ID)
	}
	assert.Equal(t, []string{plain, held, reclaimed, bounced}, order, "queued, takeable by the pool, oldest first; not the running, the fast-lane, the ad-hoc")

	p := byID[plain]
	assert.Equal(t, []string{"os:darwin"}, p.Labels)
	assert.Equal(t, 0, p.Attempts)
	assert.False(t, p.Held)
	assert.True(t, p.CreatedAt.Before(byID[held].CreatedAt))

	assert.True(t, byID[held].Held, "a person holds it: it is queued and no worker takes it")
	assert.Zero(t, byID[reclaimed].Attempts, "a reclaim is not counted")

	b := byID[bounced]
	assert.Equal(t, 3, b.Attempts, "two infrastructure releases and a lapse")
	assert.WithinDuration(t, putBack, b.UpdatedAt, time.Second, "updated_at is when it was put back")
	assert.True(t, b.CreatedAt.Before(b.UpdatedAt))
}

// The counts are in the table, so a wwd that starts again sees the same (the rules do not forget a job that keeps coming back).
func TestQueuedViewSurvivesAReopenOfTheQueue(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	for i := 0; i < 3; i++ {
		c := f.claim(t, id, "air26")
		require.NoError(t, f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseInfra}))
	}
	again, err := New(f.db, Options{Origin: "mlab", Now: f.clk.Now, Publish: f.ev.publish, LogDir: t.TempDir(), Pinner: f.pins})
	require.NoError(t, err)
	got, err := again.QueuedView(bg)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 3, got[0].Attempts)
}

func TestQueuedViewOfAnEmptyQueueIsEmptyNotNil(t *testing.T) {
	f := newFixture(t)
	got, err := f.q.QueuedView(bg)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got)
}
