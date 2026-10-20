package jobsmod

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The job events the daemon reads from wwd are read leniently (765): an event whose job carries started_at (a wwd on queue schema 9) is
// announced as a jobs.changed like any other, with no change to what jobs.changed says ({host, job_id, state}).
func TestEvents_AJobThatHasStartedIsAnnouncedLikeAnyOther(t *testing.T) {
	at := time.Date(2026, 10, 20, 8, 0, 3, 0, time.UTC)
	j := jobOf(t, "j_run", wire.StateRunning)
	j.StartedAt = &at
	b, err := json.Marshal(wire.JobEvent{Host: "mlab", Job: j})
	require.NoError(t, err)

	_, out, logs := started(t, &streamSource{streams: [][]wire.Event{{{Type: wire.EventJob, Data: b}}}})
	ev := next(t, out)
	assert.Equal(t, "jobs.changed", ev.Type)
	got, err := wire.Decode[wire.JobsChanged]([]byte(ev.Value))
	require.NoError(t, err)
	assert.Equal(t, wire.JobsChanged{Host: "mlab", JobID: "j_run", State: wire.StateRunning}, got)
	for _, l := range logs() {
		assert.NotContains(t, l, "does not decode", "the event was read")
	}
}
