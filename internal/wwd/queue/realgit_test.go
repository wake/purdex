package queue

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
)

// realPinner is the snapshot package's own Pin, Unpin and ListPins behind the queue's interface: the same three functions wwd wires in.
type realPinner struct{}

func (realPinner) Pin(ctx context.Context, dir, id, commit string) error {
	return snapshot.Pin(ctx, dir, id, commit)
}
func (realPinner) Unpin(ctx context.Context, dir, id string) error {
	return snapshot.Unpin(ctx, dir, id)
}
func (realPinner) ListPins(ctx context.Context, dir string) ([]string, error) {
	return snapshot.ListPins(ctx, dir)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// With a real repository and the real git calls: the snapshot is pinned as refs/wwd/<job id> and can be found by git; it is gone when
// the job is cancelled; a stray ref of a job that does not exist is removed by the sweep and a live job's is not; and a submission
// naming a commit the repository does not have is refused.
func TestWithARealRepositoryThePinLivesAndDiesWithTheJob(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644))
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-q", "-m", "one")
	commit := git(t, repo, "rev-parse", "HEAD")
	gitDir := filepath.Join(repo, ".git")

	f := newFixture(t)
	q, err := New(f.db, Options{Origin: "mlab", Now: f.clk.Now, Publish: f.ev.publish, Pinner: realPinner{}})
	require.NoError(t, err)
	f.q, f.gitDir = q, gitDir
	mk := func(key string) string {
		return f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.Snapshot.Commit = key, commit })
	}

	a := mk("")
	assert.Equal(t, commit, git(t, repo, "rev-parse", "refs/wwd/"+a), "git itself finds the pin")

	// a commit the repository does not have is refused, and nothing is stored
	_, err = q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.GitDir, s.Snapshot.Commit = gitDir, strings.Repeat("a", 40) }))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSnapshotRefused)
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n))
	assert.Equal(t, 1, n)

	// cancelling the job releases the snapshot
	_, err = q.Cancel(context.Background(), a)
	require.NoError(t, err)
	out := exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "refs/wwd/"+a)
	assert.Error(t, out.Run(), "the ref is gone after the job ended")

	// A pin the queue recorded for a job whose row never came (a crash between the pin and the row) is released by the sweep; the pin of a
	// live job, a ref somebody else keeps under refs/wwd/ in the same repository, and every other ref are not touched.
	live := mk("")
	f.clk.Advance(time.Hour)
	leaked := newJobID(f.clk.Now().Add(-30 * time.Minute))
	git(t, repo, "update-ref", "refs/wwd/"+leaked, commit)
	_, err = f.db.Exec(`INSERT INTO git_pins (id, git_dir, at) VALUES (?, ?, ?)`, leaked, gitDir, ms(f.clk.Now().Add(-30*time.Minute)))
	require.NoError(t, err)
	foreign := newJobID(f.clk.Now().Add(-30 * time.Minute))
	git(t, repo, "update-ref", "refs/wwd/"+foreign, commit)
	git(t, repo, "update-ref", "refs/heads/keep-me", commit)
	res, err := q.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Orphans)
	assert.Error(t, exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "refs/wwd/"+leaked).Run(), "the queue's own leaked pin is gone")
	assert.Equal(t, commit, git(t, repo, "rev-parse", "refs/wwd/"+foreign), "a ref the queue never recorded is not touched")
	assert.Equal(t, commit, git(t, repo, "rev-parse", "refs/wwd/"+live), "a live job's pin stays")
	assert.Equal(t, commit, git(t, repo, "rev-parse", "refs/heads/keep-me"), "no other ref is touched")
}
