package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/wire"
)

// An outcome is dated by the origin's clock, not by the finished_at its worker sent: a worker whose clock is a month behind would have its
// record swept at once, and one that is ahead would have it kept for ever.
func TestAnOutcomeIsDatedByTheOriginsClock(t *testing.T) {
	h := newJobHost(t, "mlab")
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	d := New(Options{Alias: "mlab", Queue: h.q, Now: func() time.Time { return now }, ReferenceTasks: func() []labels.RefTask {
		return []labels.RefTask{{Repo: "purdex", Task: "go-changed", Reference: "air26"}}
	}})
	behind := now.Add(-30 * 24 * time.Hour)
	ahead := now.Add(30 * 24 * time.Hour)
	d.NoteOutcome(t.Context(), endedJob("j_ref", "air26", 0, func(j *wire.Job) { j.Result.FinishedAt = &behind }), claimOf(false))
	d.NoteOutcome(t.Context(), endedJob("j_bad", "mserv", 1, func(j *wire.Job) { j.Result.FinishedAt = &ahead }), claimOf(true))
	l := disagreementsAbout(t, h, "mserv")
	require.Len(t, l.Items, 1)
	assert.True(t, l.Items[0].At.Equal(now), "dated %s, want the origin's %s", l.Items[0].At, now)
}
