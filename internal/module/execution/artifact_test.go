package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gitInitRepo creates a git repo with one committed file and returns the repo
// path and the HEAD commit sha (the head_at_start diff base).
func gitInitRepo(t *testing.T) (repo, head string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	head = run("rev-parse", "HEAD")
	return repo, head
}

func TestGitDiff_ModifiedFile(t *testing.T) {
	repo, head := gitInitRepo(t)
	// Replace three lines with four different lines: 4 additions, 3 deletions.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"),
		[]byte("1\n2\n3\n4\n"), 0o644))

	meta, err := GitDiff(context.Background(), repo, head)
	require.NoError(t, err)
	require.Equal(t, 1, meta.Files)
	require.Equal(t, 4, meta.Add)
	require.Equal(t, 3, meta.Del)
}

func TestGitDiff_CleanTree(t *testing.T) {
	repo, head := gitInitRepo(t)
	meta, err := GitDiff(context.Background(), repo, head)
	require.NoError(t, err)
	require.Equal(t, 0, meta.Files)
	require.Equal(t, 0, meta.Add)
	require.Equal(t, 0, meta.Del)
}

func TestGitDiff_UntrackedNewFileCounted(t *testing.T) {
	repo, head := gitInitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x\ny\n"), 0o644))

	meta, err := GitDiff(context.Background(), repo, head)
	require.NoError(t, err)
	require.Equal(t, 1, meta.Files)
	require.Equal(t, 2, meta.Add)
	require.Equal(t, 0, meta.Del)
}

func TestDiffPointer_DaemonScoped(t *testing.T) {
	got := DiffPointer("dmn_1", "exc_9")
	require.Equal(t, "pdx://dmn_1/execution/exc_9/diff", got)
}

func TestTranscriptPointer_DaemonScoped(t *testing.T) {
	got := TranscriptPointer("dmn_1", "exc_9")
	require.Equal(t, "pdx://dmn_1/execution/exc_9/transcript", got)
}

func TestBuildDiffArtifact_PointerFirstNoBlob(t *testing.T) {
	repo, head := gitInitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("1\n2\n3\n4\n"), 0o644))

	art, err := BuildDiffArtifact(context.Background(), "dmn_1", "exc_9", repo, head)
	require.NoError(t, err)
	require.Equal(t, "diff", art.Kind)
	require.Equal(t, "pdx://dmn_1/execution/exc_9/diff", art.Pointer)
	// meta is a summary only — never an inlined blob.
	require.Equal(t, 1, art.Meta["files"])
	require.Equal(t, 4, art.Meta["add"])
	require.Equal(t, 3, art.Meta["del"])
	require.NotContains(t, art.Meta, "diff")
	require.NotContains(t, art.Meta, "patch")
	require.NotContains(t, art.Meta, "content")
}
