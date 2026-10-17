package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// fakePinner stands in for the git side: refs/wwd/<job id> per git directory, with failures that tests switch on.
type fakePinner struct {
	mu      sync.Mutex
	refs    map[string]map[string]string // git dir → job id → commit
	pinErr  error
	unErr   error
	listErr map[string]error
	calls   []string
}

func newFakePinner() *fakePinner {
	return &fakePinner{refs: map[string]map[string]string{}, listErr: map[string]error{}}
}

func (p *fakePinner) Pin(_ context.Context, dir, id, commit string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "pin "+id)
	if p.pinErr != nil {
		return p.pinErr
	}
	if p.refs[dir] == nil {
		p.refs[dir] = map[string]string{}
	}
	p.refs[dir][id] = commit
	return nil
}

func (p *fakePinner) Unpin(_ context.Context, dir, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "unpin "+id)
	if p.unErr != nil {
		return p.unErr
	}
	delete(p.refs[dir], id)
	return nil
}

func (p *fakePinner) ListPins(_ context.Context, dir string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.listErr[dir]; err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil { // like the real one: a directory that is not there cannot be listed
		return nil, err
	}
	var ids []string
	for id := range p.refs[dir] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (p *fakePinner) has(dir, id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.refs[dir][id]
	return ok
}

func (p *fakePinner) put(dir, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs[dir] == nil {
		p.refs[dir] = map[string]string{}
	}
	p.refs[dir][id] = "c0ffee"
}

func (f *fixture) pinned(t *testing.T, id string) bool {
	t.Helper()
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT pinned FROM jobs WHERE id = ?`, id).Scan(&v))
	return v == 1
}

// A submission pins its snapshot in the repo's git directory before the job exists, under refs/wwd/<job id>, and the row says so.
func TestSubmitPinsTheSnapshotInTheGitDir(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	assert.True(t, f.pins.has(f.gitDir, id), "refs/wwd/<id> points at the snapshot in the submission's git dir")
	assert.Equal(t, "c0ffee", f.pins.refs[f.gitDir][id], "the snapshot commit")
	assert.True(t, f.pinned(t, id))
}

// A snapshot that cannot be pinned (the commit is not there, the git dir is not one) refuses the submission: a job whose snapshot cannot
// be fetched must not be queued. Nothing is stored or announced, and the error says so (the client turns it into exit 4).
func TestSubmitIsRefusedWhenTheSnapshotCannotBePinned(t *testing.T) {
	f := newFixture(t)
	f.pins.pinErr = errors.New("snapshot: the git dir does not have the commit")
	_, err := f.q.Submit(context.Background(), sub())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSnapshotRefused)
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n))
	assert.Zero(t, n)
	assert.Empty(t, f.ev.all())
}

// If the row cannot be stored after the pin was made, the pin is taken back (it would hold the snapshot for a job that does not
// exist), but never the pin of a job that does: an insert that failed because the id exists must not unpin that job.
func TestAFailedInsertUnpinsOnlyWhatItPinned(t *testing.T) {
	f := newFixture(t)
	f.q.newI = func() string { return "j_FIXED" }
	first := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	require.Equal(t, "j_FIXED", first)
	_, err := f.q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.SupersedeKey, s.GitDir = "", f.gitDir }))
	require.Error(t, err, "the id exists")
	assert.True(t, f.pins.has(f.gitDir, "j_FIXED"), "the existing job's pin is still there")
	assert.True(t, f.pinned(t, "j_FIXED"))
}

// The pin goes when the job reaches a terminal state, and not before: cancelled, superseded and expired all end a queued job; a job a
// worker holds keeps its pin until it ends.
func TestThePinIsReleasedWhenTheJobEnds(t *testing.T) {
	f := newFixture(t)
	a := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := f.q.Cancel(context.Background(), a)
	require.NoError(t, err)
	assert.False(t, f.pins.has(f.gitDir, a), "cancelled")
	assert.False(t, f.pinned(t, a))

	old := f.submit(t)
	newer := f.submit(t)
	assert.False(t, f.pins.has(f.gitDir, old), "superseded")
	assert.True(t, f.pins.has(f.gitDir, newer), "the newer job keeps its pin")

	held := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	f.seed(t, held, wire.StateRunning, "")
	_, err = f.q.Cancel(context.Background(), held)
	require.NoError(t, err)
	assert.True(t, f.pins.has(f.gitDir, held), "a held job is still running: its snapshot is still needed")
	assert.True(t, f.pinned(t, held))
}

// An Unpin that fails leaves the row marked, and the next sweep does it: the state change is not undone or blocked by git.
func TestAFailedUnpinIsRetriedBySweep(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	f.pins.unErr = errors.New("git is busy")
	_, err := f.q.Cancel(context.Background(), id)
	require.NoError(t, err, "the cancel stands")
	assert.Equal(t, wire.StateCancelled, f.get(t, id).State)
	assert.True(t, f.pinned(t, id), "still marked")
	assert.True(t, f.pins.has(f.gitDir, id))

	f.pins.unErr = nil
	res, err := f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Unpinned)
	assert.False(t, f.pins.has(f.gitDir, id))
	assert.False(t, f.pinned(t, id))
}

// A git directory that no longer exists holds no ref: the mark is cleared without asking git, so the row can age out.
func TestAMissingGitDirIsTreatedAsUnpinned(t *testing.T) {
	f := newFixture(t)
	gone := f.gitDir + "-gone"
	id := f.submit(t, func(s *wire.JobSubmission) { s.GitDir = gone })
	_, err := f.q.Cancel(context.Background(), id)
	require.NoError(t, err)
	assert.False(t, f.pinned(t, id))
	assert.NotContains(t, f.pins.calls, "unpin "+id, "git was not asked about a directory that is gone")

	// and the sweep does not try to list it: a directory that is gone is not an error every time the sweep runs
	_, err = f.q.Sweep(context.Background())
	assert.NoError(t, err)
}

// Orphan refs: a ref under refs/wwd/ whose job is gone or has ended is removed; a live job's is kept; a young id (the pin made
// moments ago, before its row was written) is kept whatever the table says. Only job ids are ever unpinned.
func TestSweepRemovesOrphanRefsButNotLiveOrYoungOnes(t *testing.T) {
	f := newFixture(t)
	live := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	ended := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := f.q.Cancel(context.Background(), ended)
	require.NoError(t, err)
	f.pins.put(f.gitDir, ended) // a ref that survived the end of its job

	f.clk.Advance(time.Hour)
	oldOrphan := newJobID(f.clk.Now().Add(-30 * time.Minute)) // a job that is not in the table, made half an hour ago
	youngOrphan := newJobID(f.clk.Now().Add(-1 * time.Minute))
	f.pins.put(f.gitDir, oldOrphan)
	f.pins.put(f.gitDir, youngOrphan)

	res, err := f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.True(t, f.pins.has(f.gitDir, live), "a live job's pin stays")
	assert.False(t, f.pins.has(f.gitDir, ended), "the ref of an ended job goes")
	assert.False(t, f.pins.has(f.gitDir, oldOrphan), "an orphan goes")
	assert.True(t, f.pins.has(f.gitDir, youngOrphan), "a young id may be a pin whose row is not written yet")
	assert.Equal(t, 2, res.Orphans)
}

// The pin was made, the row could not be written and the pin could not be taken back (or the process died between the two): a ref is
// left in a repository no job row names any more. The sweep still finds it, because the directory was written down before the pin was
// made. Mutation gate: the directories read from the jobs table alone → this ref is never looked for.
func TestSweepFindsARefLeftByASubmissionThatFailedAfterPinning(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.q.afterTicket = func() { f.q.afterTicket = nil; cancel() } // the commit fails after the pin was made
	f.pins.unErr = errors.New("git is busy")                     // and so does taking the pin back
	_, err := f.q.Submit(ctx, sub(func(s *wire.JobSubmission) { s.GitDir = f.gitDir }))
	require.Error(t, err)
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n))
	require.Zero(t, n, "no row names this repository")
	ids, _ := f.pins.ListPins(context.Background(), f.gitDir)
	require.Len(t, ids, 1, "but the ref is there")

	f.pins.unErr = nil
	f.clk.Advance(time.Hour)
	res, err := f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Orphans)
	ids, _ = f.pins.ListPins(context.Background(), f.gitDir)
	assert.Empty(t, ids)
}

// The list of git directories does not grow for ever, and forgets nothing that matters. Each case is its own directory:
//   - gone from the disk → forgotten at once;
//   - no job, no ref, used moments ago → kept (it may be used again), and forgotten once a day has passed;
//   - no job but a ref it could not remove (git refused), or a young ref → kept however old the entry is;
//   - a live job → kept.
//
// Mutation gates: the missing-directory prune; the day condition; the "refs left" condition; the "job" condition.
func TestSweepForgetsGitDirsThatAreGoneOrEmpty(t *testing.T) {
	f := newFixture(t)
	has := func(dir string) bool {
		var n int
		require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM git_dirs WHERE dir = ?`, dir).Scan(&n))
		return n == 1
	}
	seen := func(dir string, at time.Time) {
		_, err := f.db.Exec(`UPDATE git_dirs SET seen_at = ? WHERE dir = ?`, ms(at), dir)
		require.NoError(t, err)
	}
	mk := func(key string) string {
		dir := t.TempDir()
		f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.GitDir = key, dir })
		return dir
	}
	gone, empty, stuckRef, youngRef, live := mk("g"), mk("e"), mk("s"), mk("y"), mk("l")
	// every dir but "live" has only ended jobs, all released and deleted: nothing but the list names them
	_, err := f.db.Exec(`DELETE FROM jobs WHERE git_dir != ?`, live)
	require.NoError(t, err)
	f.pins.refs = map[string]map[string]string{}
	require.NoError(t, os.RemoveAll(gone))

	old := f.clk.Now().Add(-25 * time.Hour)
	for _, d := range []string{stuckRef, youngRef, live} {
		seen(d, old)
	}
	f.pins.put(stuckRef, newJobID(f.clk.Now().Add(-30*time.Minute))) // an orphan git refuses to remove
	f.pins.unErr = errors.New("git is busy")
	f.pins.put(youngRef, newJobID(f.clk.Now().Add(-1*time.Minute))) // a pin made a minute ago

	_, err = f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.False(t, has(gone), "gone from the disk")
	assert.True(t, has(empty), "no job, no ref, but used moments ago")
	assert.True(t, has(stuckRef), "a ref it could not remove: it must be looked at again")
	assert.True(t, has(youngRef), "a young ref")
	assert.True(t, has(live), "a live job")

	f.pins.unErr = nil
	f.clk.Advance(25 * time.Hour)
	seen(stuckRef, f.clk.Now().Add(-25*time.Hour))
	_, err = f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.False(t, has(empty), "a day without a job or a ref")
	assert.False(t, has(stuckRef), "its ref is removed now, and the day has passed")
	assert.True(t, has(live), "a live job keeps its directory")
}

// A directory used again is not forgotten for being old: its use is written down each time.
func TestUsingAGitDirAgainRefreshesItsEntry(t *testing.T) {
	f := newFixture(t)
	f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	f.clk.Advance(30 * time.Hour)
	f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	var seenAt int64
	require.NoError(t, f.db.QueryRow(`SELECT seen_at FROM git_dirs WHERE dir = ?`, f.gitDir).Scan(&seenAt))
	assert.Equal(t, ms(f.clk.Now()), seenAt)
}

// One git directory that cannot be listed does not stop the sweep of the others, and is reported.
func TestSweepGoesOnWhenOneGitDirCannotBeListed(t *testing.T) {
	f := newFixture(t)
	other := t.TempDir()
	f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "a" })
	f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.GitDir = "b", other })
	f.clk.Advance(time.Hour)
	f.pins.put(f.gitDir, newJobID(f.clk.Now().Add(-30*time.Minute)))
	f.pins.listErr[other] = fmt.Errorf("not a git directory")

	res, err := f.q.Sweep(context.Background())
	require.Error(t, err, "the failure is reported")
	assert.Equal(t, 1, res.Orphans, "the readable directory was still swept")
}
