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
	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "and the old one is not kept beside it")
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

	// a claim that is stale from the start is turned away before its body is read
	spy := &spyReader{}
	err = f.q.PutLog(bg, id, old.ClaimID, spy)
	assert.Equal(t, wire.StateClaimed, conflictState(t, err))
	assert.False(t, spy.read, "the body of a stale upload is not read")

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

// When the transaction that records an upload fails, the log that was there comes back: the new file is not left in its place, and no
// backup is left beside it (3b-3b R1). The failure is made by taking away the column the update writes.
func TestFailedCommitRestoresTheOldLog(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "old and good\n"))

	f.q.beforeLogCommit = func() {
		_, err := f.db.Exec(`ALTER TABLE jobs RENAME COLUMN log_at TO log_at_gone`)
		require.NoError(t, err)
	}
	err := f.putLog(t, id, c.ClaimID, "new but never recorded\n")
	require.Error(t, err)
	f.q.beforeLogCommit = nil
	_, err = f.db.Exec(`ALTER TABLE jobs RENAME COLUMN log_at_gone TO log_at`)
	require.NoError(t, err)

	assert.Equal(t, "old and good\n", f.readLog(t, id))
	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the log itself")

	// and a first upload that fails leaves no file at all
	id2, c2 := f.running(t)
	f.q.beforeLogCommit = func() {
		_, err := f.db.Exec(`ALTER TABLE jobs RENAME COLUMN log_at TO log_at_gone`)
		require.NoError(t, err)
	}
	require.Error(t, f.putLog(t, id2, c2.ClaimID, "never recorded\n"))
	f.q.beforeLogCommit = nil
	_, err = f.db.Exec(`ALTER TABLE jobs RENAME COLUMN log_at_gone TO log_at`)
	require.NoError(t, err)
	_, _, err = f.q.OpenLog(bg, id2, false)
	assert.ErrorIs(t, err, ErrNotFound)
	entries, err = os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// A job deleted at the end of its 7 days takes its log with it, even a log uploaded late enough to be younger than 3 days; and upload
// leftovers a crash left (a temporary file, a backup) are cleared once they are an hour old (3b-3b R1).
func TestDeletingAJobDeletesItsLogAndLeftoversAreCleared(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)
	f.clk.Advance(6*24*time.Hour + 23*time.Hour) // nearly 7 days after it ended
	require.NoError(t, f.putLog(t, id, c.ClaimID, "very late\n"))

	stale := filepath.Join(f.q.opt.LogDir, ".upload-crashed")
	fresh := filepath.Join(f.q.opt.LogDir, ".upload-in-flight")
	require.NoError(t, os.WriteFile(stale, []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(fresh, []byte("x"), 0o600))
	old := f.clk.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))
	require.NoError(t, os.Chtimes(fresh, f.clk.Now(), f.clk.Now()))
	_, err = f.q.Sweep(bg)
	require.NoError(t, err)
	_, err = os.Stat(stale)
	assert.ErrorIs(t, err, os.ErrNotExist, "an old leftover goes")
	_, err = os.Stat(fresh)
	assert.NoError(t, err, "an upload that may be in flight stays")
	_ = os.Remove(fresh)

	f.clk.Advance(2 * time.Hour) // the job is now past 7 days; its log is not past 3
	r, err := f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Deleted)
	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the log went with the job")
}

// A file a process died after putting in place, for a job whose row never learned of it, is removed with the job at its 7 days: the
// file is found by the job's id, not by what the row says (3b-3b R2).
func TestDeletingAJobRemovesALogFileItsRowDoesNotKnow(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.q.logPath(id), []byte("written, never recorded"), 0o600))

	f.clk.Advance(7*24*time.Hour + time.Minute)
	r, err := f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Deleted)
	entries, err := os.ReadDir(f.q.opt.LogDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// The log of a job is deleted only after the change that forgets it has committed: when that change fails, the file is still there and
// the job still points at it (3b-3b R2). The failure is made by taking away the column the change writes.
func TestFailedForgetKeepsTheLogFile(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "x"))
	f.clk.Advance(3*24*time.Hour + time.Minute)
	// the lease would lapse first; keep the job alive so only the log is due
	_, err := f.q.Heartbeat(bg, id, c.ClaimID)
	require.NoError(t, err)

	_, err = f.db.Exec(`ALTER TABLE jobs RENAME COLUMN log_size TO log_size_gone`)
	require.NoError(t, err)
	_, err = f.q.Sweep(bg)
	require.Error(t, err)
	_, err = f.db.Exec(`ALTER TABLE jobs RENAME COLUMN log_size_gone TO log_size`)
	require.NoError(t, err)

	assert.Equal(t, "x", f.readLog(t, id), "the file and the pointer to it are both still there")
}

// An upload recorded between the sweep's forgetting of an old log and its removal of the file is not lost: the file at that path is the
// new one, and the sweep leaves it (3b-3b critic).
func TestSweepDoesNotRemoveALogUploadedWhileItWasForgetting(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "old\n"))
	_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)
	f.clk.Advance(3*24*time.Hour + time.Minute)

	f.q.afterLogForget = func() { require.NoError(t, f.putLog(t, id, c.ClaimID, "new\n")) }
	r, err := f.q.Sweep(bg)
	require.NoError(t, err)
	f.q.afterLogForget = nil
	assert.Equal(t, 1, r.LogsRemoved, "the old log was forgotten")
	assert.Equal(t, "new\n", f.readLog(t, id), "and the new one is still there")
	assert.Equal(t, id+"/log", f.get(t, id).Result.Log)
}

// The lock itself: while the sweep has decided which files to remove and not yet removed them, an upload cannot be put in place and
// recorded; it waits, and when the sweep is done its file is the one that stays (3b-3b re-review). Without the lock on either side the
// upload goes through during the sweep's window and the sweep then removes its file.
func TestAnUploadWaitsForTheSweepsFileRemoval(t *testing.T) {
	f := newFixture(t)
	id, c := f.running(t)
	require.NoError(t, f.putLog(t, id, c.ClaimID, "old\n"))
	_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)
	f.clk.Advance(3*24*time.Hour + time.Minute)

	done := make(chan struct{})
	var perr error
	f.q.inLogRemoval = func() {
		go func() {
			perr = f.putLog(t, id, c.ClaimID, "new\n")
			close(done)
		}()
		select {
		case <-done:
			t.Error("the upload got in while the sweep held its lock")
		case <-time.After(300 * time.Millisecond):
		}
	}
	_, err = f.q.Sweep(bg)
	require.NoError(t, err)
	f.q.inLogRemoval = nil
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the upload never finished")
	}
	require.NoError(t, perr)
	assert.Equal(t, "new\n", f.readLog(t, id))
	assert.Equal(t, id+"/log", f.get(t, id).Result.Log)
}

type spyReader struct{ read bool }

func (r *spyReader) Read([]byte) (int, error) { r.read = true; return 0, io.EOF }

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
