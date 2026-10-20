package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// lateCase is a worker whose clone was made BEFORE the job's base existed (the base is a commit pushed after the clone's last fetch): the
// origin repository is the clone's `origin`, as GitHub is for a real clone. #3729 / #3730.
type lateCase struct {
	materializeCase
}

func newLateCase(t *testing.T, jobID string) lateCase { return newLateCaseWith(t, jobID, nil, nil) }

// newLateCaseWith is newLateCase with a step to run on the origin before the clone is made and one after the base is committed.
func newLateCaseWith(t *testing.T, jobID string, beforeClone, afterBase func(repo string)) lateCase {
	t.Helper()
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitOut(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo+"/.gitignore", "*.log\n")
	writeFile(t, repo+"/a.txt", "a1\n")
	writeFile(t, repo+"/keep.log", "log1\n")
	gitOut(t, repo, "add", ".gitignore", "a.txt")
	gitOut(t, repo, "add", "-f", "keep.log")
	gitOut(t, repo, "commit", "-q", "-m", "one")
	if beforeClone != nil {
		beforeClone(repo)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	gitOut(t, repo, "clone", "-q", "--no-local", ".", clone) // the clone's last fetch: only "one"

	writeFile(t, repo+"/b.txt", "b1\n")
	gitOut(t, repo, "add", "b.txt")
	gitOut(t, repo, "commit", "-q", "-m", "two") // pushed after
	base := gitOut(t, repo, "rev-parse", "HEAD")
	if afterBase != nil {
		afterBase(repo)
	}
	writeFile(t, repo+"/a.txt", "a2 changed\n")
	writeFile(t, repo+"/new.txt", "new\n")
	writeFile(t, repo+"/keep.log", "log2 changed\n")
	info, err := SnapshotTree(context.Background(), "", repo)
	require.NoError(t, err)
	require.NoError(t, Pin(context.Background(), repo, jobID, info.SHA))

	claim := wire.NewClaimID()
	m := materializeCase{repo, base, info.SHA, clone, claim, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: info.SHA, Base: base}}}
	require.Error(t, exec0(clone, "cat-file", "-e", base+"^{commit}"), "the clone does not have the base yet")
	return lateCase{m}
}

// exec0 runs git in dir and returns its error.
func exec0(dir string, args ...string) error {
	_, err := gitRun(context.Background(), gitEnv(), "test", dir, smallOutput, args...)
	return err
}

// The base arrived at the origin after the clone's last fetch: the worker fetches the clone and takes the bundle, so the workspace is a
// worktree (it has a .git, and the clone has the base now), not a tar. Without the fetch the job's workspace shape would depend on whether
// manifest_ref happened to fetch a minute earlier.
func TestMaterialize_ABaseThatCameAfterTheClonesFetchIsFetchedAndTheWorkspaceIsAWorktree(t *testing.T) {
	m := newLateCase(t, "j_late")
	rec := &recorder{}
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_late", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)

	assert.Equal(t, []string{"bundle"}, rec.seen(), "a bundle, not the tar")
	checkTree(t, ws.Dir)
	_, statErr := os.Stat(filepath.Join(ws.Dir, ".git"))
	assert.NoError(t, statErr, "a worktree has a .git")
	assert.Equal(t, m.snap, gitOut(t, ws.Dir, "rev-parse", "HEAD"))
	assert.NoError(t, exec0(m.clone, "cat-file", "-e", m.base+"^{commit}"), "the clone holds the base now")
	require.NoError(t, ws.Cleanup())
}

// A clone that already has the base is not fetched: no round trip to the remote for the usual job.
func TestMaterialize_ACloneThatHasTheBaseIsNotFetched(t *testing.T) {
	var fetches int
	old := fetchClone
	fetchClone = func(ctx context.Context, clone string) error { fetches++; return old(ctx, clone) }
	t.Cleanup(func() { fetchClone = old })

	m := newMaterializeCase(t, "j_have")
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, &recorder{}), "j_have", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Zero(t, fetches, "the clone was not fetched")
	require.NoError(t, ws.Cleanup())

	// and the same count says the fetch is made when the base is missing
	late := newLateCase(t, "j_have2")
	ws, err = Materialize(context.Background(), late.in(serveRecorded(t, late.src, &recorder{}), "j_have2", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, 1, fetches, "one fetch for a base that is not there")
	require.NoError(t, ws.Cleanup())
}

// A fetch that fails (the remote is gone, nothing to reach) does not fail the job: the tar is taken as before.
func TestMaterialize_AFetchThatFailsFallsBackToATar(t *testing.T) {
	m := newLateCase(t, "j_gone")
	gitOut(t, m.clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-such-repository"))
	rec := &recorder{}
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_gone", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"tar"}, rec.seen())
	checkTree(t, ws.Dir)
}

// manifest_ref fetches the same clone every 10 minutes: when its fetch holds the lock of a remote ref at the moment a job asks for its base,
// the job's fetch fails at that ref, but git has stored the commits before it updates refs, so the base is in the clone and the workspace is a
// worktree. The job does not wait for the lock (here it is never let go) and does not depend on the fetch's own answer.
func TestMaterialize_AnotherFetchsRefLockDoesNotStopTheBaseComingIn(t *testing.T) {
	old := fetchBaseTimeout
	fetchBaseTimeout = 20 * time.Second
	t.Cleanup(func() { fetchBaseTimeout = old })
	m := newLateCase(t, "j_locked")
	lock := filepath.Join(m.clone, ".git", "refs", "remotes", "origin", "main.lock")
	require.NoError(t, os.WriteFile(lock, nil, 0o644))

	rec := &recorder{}
	start := time.Now()
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_locked", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"bundle"}, rec.seen())
	assert.Equal(t, m.snap, gitOut(t, ws.Dir, "rev-parse", "HEAD"))
	assert.Less(t, time.Since(start), 8*time.Second, "not held for the fetch's deadline")
	require.NoError(t, ws.Cleanup())
}

// git says "cannot lock ref" for a name that clashes with another ref too (the clone has `origin/foo`, the remote now has `foo/bar`), and
// that does not go away by waiting. The fetch brought the commits in all the same (main moved), so the job is not held for the deadline
// and its workspace is a worktree.
func TestMaterialize_ARefNameClashIsNotWaitedForAndTheFetchedBaseIsUsed(t *testing.T) {
	old := fetchBaseTimeout
	fetchBaseTimeout = 20 * time.Second
	t.Cleanup(func() { fetchBaseTimeout = old })
	m := newLateCaseWith(t, "j_clash",
		func(repo string) { gitOut(t, repo, "branch", "foo") },
		func(repo string) {
			gitOut(t, repo, "branch", "-D", "foo")
			gitOut(t, repo, "branch", "foo/bar")
		})
	rec := &recorder{}
	start := time.Now()
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_clash", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"bundle"}, rec.seen(), "the base came in with the fetch whose one ref update failed")
	assert.Equal(t, m.snap, gitOut(t, ws.Dir, "rev-parse", "HEAD"))
	assert.Less(t, time.Since(start), 8*time.Second, "not held for the fetch's deadline")
	require.NoError(t, ws.Cleanup())
}

// The fetch has a deadline of its own: a remote that does not answer holds a job start for that long and no longer.
func TestMaterialize_TheFetchIsBoundedAndTheTarFollows(t *testing.T) {
	old := fetchBaseTimeout
	fetchBaseTimeout = time.Nanosecond // over before git is even started
	t.Cleanup(func() { fetchBaseTimeout = old })
	m := newLateCase(t, "j_slow")
	rec := &recorder{}
	start := time.Now()
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_slow", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"tar"}, rec.seen(), "no time to fetch: the tar")
	checkTree(t, ws.Dir)
	assert.Less(t, time.Since(start), 10*time.Second)
}
