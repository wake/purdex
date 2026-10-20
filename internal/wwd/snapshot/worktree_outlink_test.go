package snapshot

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- untracked symlinks that point out of the repository are not part of a snapshot (#3841) ----

// snapTree is the tree of a real snapshot commit (WorkingTree writes its objects to a quarantine, so its tree cannot be listed) and
// is checked to be the tree WorkingTreeSkipped gives.
func outlinkTree(t *testing.T, dir, workTree string) string {
	t.Helper()
	snap, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	tree := gitIn(t, dir, "rev-parse", snap.SHA+"^{tree}")
	require.Equal(t, workTree, tree)
	return tree
}

func treeHas(t *testing.T, dir, tree, name string) bool {
	t.Helper()
	return gitIn(t, dir, "ls-tree", "--name-only", tree, "--", name) != ""
}

func TestWorkingTree_SkipsUntrackedSymlinksLeavingTheRepo(t *testing.T) {
	dir := workRepo(t)
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, dir+"/abs-link"))                   // absolute target
	require.NoError(t, os.Symlink("../../../elsewhere", dir+"/sub/dotdot"))    // climbs out
	require.NoError(t, os.Symlink("sub/../../x", dir+"/dotdot-in-the-middle")) // climbs out after a detour
	require.NoError(t, os.Symlink("a.txt", dir+"/inside-link"))                // stays inside
	require.NoError(t, os.Symlink("sub/b.txt", dir+"/inside-link2"))           // stays inside
	tree, skipped, err := WorkingTreeSkipped(context.Background(), "", dir)
	require.NoError(t, err)
	tree = outlinkTree(t, dir, tree)
	for _, gone := range []string{"abs-link", "sub/dotdot", "dotdot-in-the-middle"} {
		assert.False(t, treeHas(t, dir, tree, gone), gone)
	}
	for _, kept := range []string{"inside-link", "inside-link2"} {
		assert.True(t, treeHas(t, dir, tree, kept), kept)
	}
	assert.ElementsMatch(t, []string{"abs-link", "sub/dotdot", "dotdot-in-the-middle"}, skipped)
	// the plain entry point gives the same tree
	same, err := WorkingTree(context.Background(), "", dir)
	require.NoError(t, err)
	assert.Equal(t, tree, same)
}

// A link that stays inside by its own text but goes out through another link is out too.
func TestWorkingTree_SkipsALinkThatLeavesThroughAnotherLink(t *testing.T) {
	dir := workRepo(t)
	require.NoError(t, os.Symlink(t.TempDir(), dir+"/hop"))
	require.NoError(t, os.Symlink("hop/file", dir+"/via-hop"))
	tree, skipped, err := WorkingTreeSkipped(context.Background(), "", dir)
	require.NoError(t, err)
	tree = outlinkTree(t, dir, tree)
	assert.False(t, treeHas(t, dir, tree, "via-hop"))
	assert.ElementsMatch(t, []string{"hop", "via-hop"}, skipped)
}

// A tracked link is the repository's own: it stays whatever it points at.
func TestWorkingTree_KeepsTrackedSymlinkPointingOut(t *testing.T) {
	dir := workRepo(t)
	require.NoError(t, os.Symlink("/etc", dir+"/tracked-out"))
	gitIn(t, dir, "add", "tracked-out")
	gitIn(t, dir, "commit", "-q", "-m", "link")
	tree, skipped, err := WorkingTreeSkipped(context.Background(), "", dir)
	require.NoError(t, err)
	tree = outlinkTree(t, dir, tree)
	assert.True(t, treeHas(t, dir, tree, "tracked-out"))
	assert.Empty(t, skipped)
}

func TestSnapshotTree_SkipsAndReportsOutsideLinks(t *testing.T) {
	dir := workRepo(t)
	require.NoError(t, os.Symlink(t.TempDir(), dir+"/node_modules2"))
	snap, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"node_modules2"}, snap.Skipped)
	assert.False(t, treeHas(t, dir, gitIn(t, dir, "rev-parse", snap.SHA+"^{tree}"), "node_modules2"))
}

// The repository's own .gitignore keeps a root node_modules symlink out: `node_modules/` (with a slash) matches directories only.
func TestRepoGitignoreIgnoresRootNodeModulesSymlink(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err)
	root := strings.TrimSpace(string(top))
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	b, err := os.ReadFile(root + "/.gitignore")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dir+"/.gitignore", b, 0o644))
	require.NoError(t, os.Symlink("/nonexistent", dir+"/node_modules"))
	out, err := exec.Command("git", "-C", dir, "check-ignore", "node_modules").CombinedOutput()
	assert.NoError(t, err, string(out))
}
