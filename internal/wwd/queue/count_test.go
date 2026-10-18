package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// CountQueued is facts.queue.queued (spec §13.2): the jobs waiting on this host, and no others.
func TestCountQueuedCountsOnlyQueuedJobs(t *testing.T) {
	f := newFixture(t)
	n, err := f.q.CountQueued(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, n)
	mk := func(key string) string {
		f.clk.Advance(time.Second)
		return f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = key })
	}
	a, b, c := mk("k1"), mk("k2"), mk("k3")
	n, _ = f.q.CountQueued(context.Background())
	require.Equal(t, 3, n)
	f.seed(t, b, wire.StateRunning, "")
	f.seed(t, c, wire.StateSucceeded, "")
	n, err = f.q.CountQueued(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "a running and a finished job are not waiting (job %s is)", a)
}
