package queue

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (f *fixture) putLog(t *testing.T, id, claimID, body string) error {
	t.Helper()
	return f.q.PutLog(bg, id, claimID, strings.NewReader(body))
}

func (f *fixture) readLog(t *testing.T, id string) string {
	t.Helper()
	rc, size, err := f.q.OpenLog(bg, id, false)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.EqualValues(t, len(b), size)
	return string(b)
}

func (f *fixture) running(t *testing.T) (string, wire.ClaimResponse) {
	t.Helper()
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	return id, c
}

// The holder of a live claim uploads the log; the job learns that it has one (size and time on the origin's clock), and the result,
// posted afterwards, points at it. A second upload with the same claim replaces the first whole.
func TestLogOfALiveClaimThenResult(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)

	require.NoError(t, f.putLog(t, id, c.ClaimID, "first\n"))
	assert.Equal(t, "first\n", f.readLog(t, id))
	require.NoError(t, f.putLog(t, id, c.ClaimID, "second attempt\n"))
	assert.Equal(t, "second attempt\n", f.readLog(t, id), "a new upload replaces the old")
	var size, at int64
	require.NoError(t, f.db.QueryRow(`SELECT log_size, log_at FROM jobs WHERE id = ?`, id).Scan(&size, &at))
	assert.EqualValues(t, len("second attempt\n"), size)
	assert.Equal(t, ms(f.clk.Now()), at)

	j, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)
	assert.Equal(t, id+"/log", j.Result.Log)
}

// The log may also arrive after the result (the worker posts the result, then uploads), with the claim that posted it: the result then
// points at the log, and one event tells the App. Another claim, or none, gets the 409 and the stored log is untouched.
func TestLogAfterTheResultNeedsTheClaimThatPostedIt(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	j, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateFailed, Exit: ptr(1)})
	require.NoError(t, err)
	assert.Equal(t, "", j.Result.Log, "no log yet")
	f.ev.reset()

	require.NoError(t, f.putLog(t, id, c.ClaimID, "late log\n"))
	assert.Equal(t, id+"/log", f.get(t, id).Result.Log)
	assert.Equal(t, "late log\n", f.readLog(t, id))
	assert.Len(t, f.ev.all(), 1)

	err = f.putLog(t, id, wire.NewClaimID(), "forged\n")
	assert.Equal(t, wire.StateFailed, conflictState(t, err))
	assert.Equal(t, "late log\n", f.readLog(t, id))
}

// A stale claim_id uploads nothing and leaves nothing behind (spec 4.2: an old execution can never upload a log after its lease went to
// someone else), not even a temporary file.
func TestStaleClaimUploadsNoLog(t *testing.T) {
	f := newFixture(t)
	id, old := f.running(t)
	f.clk.Advance(61 * time.Second)
	_, err := f.q.Sweep(bg)
	require.NoError(t, err)
	fresh := f.claim(t, id, "mserv")
	require.NoError(t, f.putLog(t, id, fresh.ClaimID, "the real one\n"))

	err = f.putLog(t, id, old.ClaimID, "stale\n")
	assert.Equal(t, wire.StateClaimed, conflictState(t, err))
	assert.Equal(t, "the real one\n", f.readLog(t, id))

	err = f.putLog(t, "j_nope", old.ClaimID, "x")
	assert.ErrorIs(t, err, ErrNotFound)

	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "one log file and no temporary leftovers")
}

// The claim is checked again in the transaction that puts the log in place: one that went stale while its body was coming (the lease
// lapsed and another worker claimed the job) does not replace the log of the holder, and leaves no file behind.
func TestClaimThatGoesStaleDuringTheUploadKeepsTheHoldersLog(t *testing.T) {
	f := newFixture(t)
	id, old := f.running(t)
	var fresh wire.ClaimResponse
	f.q.beforeLogCommit = func() {
		f.clk.Advance(61 * time.Second)
		_, err := f.q.Sweep(bg)
		require.NoError(t, err)
		fresh = f.claim(t, id, "mserv")
	}
	err := f.putLog(t, id, old.ClaimID, "from the old holder\n")
	assert.Equal(t, wire.StateClaimed, conflictState(t, err))
	f.q.beforeLogCommit = nil
	_, _, err = f.q.OpenLog(bg, id, false)
	assert.ErrorIs(t, err, ErrNotFound, "nothing was stored")
	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Empty(t, entries)

	require.NoError(t, f.putLog(t, id, fresh.ClaimID, "from the holder\n"))
	assert.Equal(t, "from the holder\n", f.readLog(t, id))
}

type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, errors.New("connection reset")
	}
	r.n--
	copy(p, "partial")
	return 7, nil
}

// An upload that breaks half way keeps the log that was there and leaves no partial file.
func TestInterruptedUploadChangesNothing(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "complete\n"))

	err := f.q.PutLog(bg, id, c.ClaimID, &failingReader{n: 3})
	require.Error(t, err)
	assert.Equal(t, "complete\n", f.readLog(t, id))
	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// A log bigger than the cap is refused, not truncated and not kept.
func TestOversizeLogIsRefused(t *testing.T) {
	f := newFixture(t)
	f.q.maxLog = 16
	id, c := f.running(t)
	err := f.putLog(t, id, c.ClaimID, strings.Repeat("x", 17))
	assert.ErrorIs(t, err, ErrLogTooLarge)
	_, _, err = f.q.OpenLog(bg, id, false)
	assert.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, f.putLog(t, id, c.ClaimID, strings.Repeat("x", 16)), "exactly the cap is fine")
}

// Without a log directory the queue cannot keep logs, and says so.
func TestNoLogDir(t *testing.T) {
	f := newFixture(t)
	f.q.opt.LogDir = ""
	id, c := f.running(t)
	assert.ErrorIs(t, f.putLog(t, id, c.ClaimID, "x"), ErrNoLogDir)
}

// Peers read the logs of ordinary jobs only; a job without a log is not found.
func TestOpenLogForPeers(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	_, _, err := f.q.OpenLog(bg, id, false)
	assert.ErrorIs(t, err, ErrNotFound, "no log uploaded yet")
	require.NoError(t, f.putLog(t, id, c.ClaimID, "x"))
	rc, _, err := f.q.OpenLog(bg, id, true)
	require.NoError(t, err)
	rc.Close()

	local := f.recordLocal(t)
	require.NoError(t, f.putLog(t, local.ID, local.ClaimID, "local log"))
	_, _, err = f.q.OpenLog(bg, local.ID, true)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "local log", f.readLog(t, local.ID), "the host itself reads it")
}

// Logs are kept 3 days (the jobs 7): the sweep deletes the file, forgets the log on the job and takes the pointer out of the result.
func TestLogsAreKeptThreeDays(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "x"))
	_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)

	f.clk.Advance(3*24*time.Hour - time.Minute)
	r, err := f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 0, r.LogsRemoved)
	assert.Equal(t, id+"/log", f.get(t, id).Result.Log)

	f.clk.Advance(2 * time.Minute)
	f.ev.reset()
	r, err = f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 1, r.LogsRemoved)
	assert.Equal(t, "", f.get(t, id).Result.Log)
	_, _, err = f.q.OpenLog(bg, id, false)
	assert.ErrorIs(t, err, ErrNotFound)
	matches, _ := filepath.Glob(filepath.Join(f.q.opt.LogDir, "*"))
	assert.Empty(t, matches)
	assert.Len(t, f.ev.all(), 1, "the job changed (it has no log now)")

	r, err = f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 0, r.LogsRemoved, "and it is not done twice")
}
