package queue

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func storedResult(log string) wire.Result {
	zero := 0
	return wire.Result{Exit: &zero, Host: "mserv", Log: log}
}

func (f *fixture) reuse(t *testing.T, stored wire.Result) wire.JobSubmissionResponse {
	t.Helper()
	resp, err := f.q.SubmitReused(bg, sub(func(s *wire.JobSubmission) { s.GitDir = f.gitDir }), stored)
	require.NoError(t, err)
	return resp
}

// A submission answered from a stored result is born succeeded: cache_hit, the original log and host, nothing pinned.
func TestSubmitReusedIsBornSucceeded(t *testing.T) {
	f := newFixture(t)
	resp := f.reuse(t, storedResult("j_old/log"))
	require.NotNil(t, resp.Result)
	assert.True(t, resp.Result.CacheHit)

	j := f.get(t, resp.ID)
	assert.Equal(t, wire.StateSucceeded, j.State)
	require.NotNil(t, j.Result)
	assert.True(t, j.Result.CacheHit)
	assert.Equal(t, "j_old/log", j.Result.Log)
	assert.Equal(t, "mserv", j.Result.Host)
	assert.NotNil(t, j.Result.FinishedAt)
	assert.False(t, f.pins.has(f.gitDir, resp.ID), "nothing runs, so nothing is pinned")
	assert.Len(t, f.ev.all(), 1, "the new job is announced once")
}

// The newest submission for a key is the one that counts, whether it ran or was answered from a stored result: older jobs with the
// key are superseded the same way (a queued one at once; a running one told to stop when interruptible, else it only records the newer).
func TestSubmitReusedSupersedesAQueuedJobOfTheKey(t *testing.T) {
	f := newFixture(t)
	queued := f.submit(t)
	f.ev.reset()

	resp := f.reuse(t, storedResult("j_old/log"))

	q := f.get(t, queued)
	assert.Equal(t, wire.StateSuperseded, q.State)
	require.NotNil(t, q.Result)
	require.NotNil(t, q.Result.SupersededBy)
	assert.Equal(t, resp.ID, *q.Result.SupersededBy)
	assert.Len(t, f.ev.all(), 2, "the new job and the one it changed are announced")
}

func TestSubmitReusedTellsARunningInterruptibleJobOfTheKeyToStop(t *testing.T) {
	f := newFixture(t)
	running, _ := f.running(t)
	f.seed(t, running, wire.StateRunning, "interruptible = 1")
	resp := f.reuse(t, storedResult("j_old/log"))
	assert.True(t, f.cancelRequested(t, running), "an interruptible run is told to stop")
	assert.Equal(t, resp.ID, f.supersededBy(t, running))
}

func (f *fixture) supersededBy(t *testing.T, id string) string {
	t.Helper()
	var by sql.NullString
	require.NoError(t, f.db.QueryRow(`SELECT superseded_by FROM jobs WHERE id = ?`, id).Scan(&by))
	return by.String
}

func TestSubmitReusedLetsARunningNonInterruptibleJobFinish(t *testing.T) {
	f := newFixture(t)
	running, _ := f.running(t)
	resp := f.reuse(t, storedResult("j_old/log"))
	assert.False(t, f.cancelRequested(t, running))
	assert.Equal(t, wire.StateRunning, f.get(t, running).State)
	assert.Equal(t, resp.ID, f.supersededBy(t, running), "only the newer id is recorded")
}

// The hit's log is the original job's: reading it by the new id follows the pointer, once, and only for a pointer of the right form.
func TestLogOfAReusedJobIsTheOriginalJobs(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "the original output\n"))

	resp := f.reuse(t, storedResult(id+"/log"))
	assert.Equal(t, "the original output\n", f.readLog(t, resp.ID))

	for name, pointer := range map[string]string{
		"a path":            "../" + id + "/log",
		"another file name": id + "/other",
		"itself":            "",
	} {
		other := f.reuse(t, storedResult("x/log"))
		_, err := f.db.Exec(`UPDATE jobs SET result = json_set(result, '$.log', ?) WHERE id = ?`, pointer, other.ID)
		require.NoError(t, err)
		_, _, err = f.q.OpenLog(bg, other.ID, false)
		assert.ErrorIs(t, err, ErrNotFound, name)
	}
}

// When the original log is gone (past its retention) the hit has none either; the hit does not keep it alive.
func TestLogOfAReusedJobEndsWithTheOriginals(t *testing.T) {
	f := newFixture(t)
	resp := f.reuse(t, storedResult("j_gone/log"))
	_, _, err := f.q.OpenLog(bg, resp.ID, false)
	assert.ErrorIs(t, err, ErrNotFound)
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
		_, err := f.q.SubmitReused(bg, sub(func(s *wire.JobSubmission) { s.GitDir = f.gitDir }), r)
		assert.Error(t, err, name)
	}
	js, err := f.q.List(bg, Filter{})
	require.NoError(t, err)
	assert.Empty(t, js, "nothing was stored")
}
