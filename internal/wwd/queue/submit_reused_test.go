package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func storedResult() wire.Result {
	zero := 0
	return wire.Result{Exit: &zero, Host: "mserv", Log: "j_old/log"}
}

// A submission answered from a stored result is born succeeded: cache_hit, the original log and host, nothing pinned, nothing superseded.
func TestSubmitReusedIsBornSucceeded(t *testing.T) {
	f := newFixture(t)
	older := f.submit(t) // same supersede key: a reuse must not touch it
	f.ev.reset()
	resp, err := f.q.SubmitReused(context.Background(), sub(func(s *wire.JobSubmission) { s.GitDir = f.gitDir }), storedResult())
	require.NoError(t, err)
	require.NotNil(t, resp.Result)
	assert.True(t, resp.Result.CacheHit)

	j := f.get(t, resp.ID)
	assert.Equal(t, wire.StateSucceeded, j.State)
	require.NotNil(t, j.Result)
	assert.True(t, j.Result.CacheHit)
	assert.Equal(t, "j_old/log", j.Result.Log)
	assert.Equal(t, "mserv", j.Result.Host)
	assert.NotNil(t, j.Result.FinishedAt)

	assert.Equal(t, wire.StateQueued, f.get(t, older).State, "a reuse supersedes nothing")
	assert.True(t, f.pins.has(f.gitDir, older))
	assert.False(t, f.pins.has(f.gitDir, resp.ID), "nothing runs, so nothing is pinned")
	assert.Len(t, f.ev.all(), 1, "the new job is announced once, no other job changed")
}

// Only a success with a host and a log is recorded as a reuse.
func TestSubmitReusedRefusesWhatIsNotASuccess(t *testing.T) {
	f := newFixture(t)
	one := 1
	for name, r := range map[string]wire.Result{
		"failed":  {Exit: &one, Host: "mserv", Log: "j/log"},
		"no exit": {Host: "mserv", Log: "j/log"},
		"no log":  {Exit: new(int), Host: "mserv"},
		"no host": {Exit: new(int), Log: "j/log"},
	} {
		_, err := f.q.SubmitReused(context.Background(), sub(func(s *wire.JobSubmission) { s.GitDir = f.gitDir }), r)
		assert.Error(t, err, name)
	}
	js, err := f.q.List(context.Background(), Filter{})
	require.NoError(t, err)
	assert.Empty(t, js, "nothing was stored")
}
