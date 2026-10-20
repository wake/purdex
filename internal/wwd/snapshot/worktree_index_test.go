package snapshot

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
)

// ---- the snapshot starts from a copy of the caller's index (WWD-0 M-S1): faster, and a file git tracks stays in it ----

// ignoredRepo is a repo whose .gitignore covers *.log, with keep.log tracked anyway (git add -f) and committed.
func ignoredRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFileMode(t, dir+"/.gitignore", "*.log\n", 0o644)
	writeFileMode(t, dir+"/a.txt", "a1\n", 0o644)
	writeFileMode(t, dir+"/keep.log", "log1\n", 0o644)
	writeFileMode(t, dir+"/conf.txt", "c1\n", 0o644)
	gitIn(t, dir, "add", ".gitignore", "a.txt", "conf.txt")
	gitIn(t, dir, "add", "-f", "keep.log")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func snapFiles(t *testing.T, dir string) (info SnapshotInfo, files []string) {
	t.Helper()
	info, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	return info, strings.Fields(gitIn(t, dir, "ls-tree", "-r", "--name-only", info.SHA))
}

// A tracked file that the ignore rules match is in the snapshot with the content it has now; when it is deleted the snapshot has no
// file; an ignored file that is not tracked stays out. (Before: the empty temporary index never saw the tracked ones, and
// `add -A` skips what is ignored, so the snapshot lost keep.log.)
func TestSnapshot_ATrackedFileTheIgnoreRulesMatchIsKept(t *testing.T) {
	dir := ignoredRepo(t)
	writeFileMode(t, dir+"/keep.log", "log2 changed\n", 0o644)
	writeFileMode(t, dir+"/other.log", "never tracked\n", 0o644)
	info, files := snapFiles(t, dir)
	assert.ElementsMatch(t, []string{".gitignore", "a.txt", "conf.txt", "keep.log"}, files, "keep.log stays, other.log (ignored, untracked) is out")
	assert.Equal(t, "log2 changed", gitIn(t, dir, "show", info.SHA+":keep.log"))

	require.NoError(t, os.Remove(dir+"/keep.log"))
	info, files = snapFiles(t, dir)
	assert.ElementsMatch(t, []string{".gitignore", "a.txt", "conf.txt"}, files, "deleted in the tree: gone from the snapshot")
}

// git's "racily clean" rule: an entry whose file time is not older than the index file's might have been changed after the index was
// written without its stat information showing it, so git compares the content. The copy of the index must keep the original's file
// time, or a same-size edit made in the same second as the last index write (an agent that edits a file right after a commit or a
// `git add`) reads as unchanged from the copy and the snapshot holds the old content.
func TestSnapshot_ASameSizeEditInTheSecondOfTheIndexWriteIsInTheSnapshot(t *testing.T) {
	dir := ignoredRepo(t)
	stamp := time.Now().Truncate(time.Second).Add(-time.Hour)
	// the file as committed, with a fixed time; the index entry records it
	require.NoError(t, os.Chtimes(dir+"/a.txt", stamp, stamp))
	gitIn(t, dir, "update-index", "--refresh")
	// the same-size edit that leaves the file's size and time as they were: only the content differs
	writeFileMode(t, dir+"/a.txt", "a2\n", 0o644)
	require.NoError(t, os.Chtimes(dir+"/a.txt", stamp, stamp))
	// the index file is as old as the entry: the entry is racily clean for git, which then reads the content
	require.NoError(t, os.Chtimes(dir+"/.git/index", stamp, stamp))

	info, _ := snapFiles(t, dir)
	assert.Equal(t, "a2", gitIn(t, dir, "show", info.SHA+":a.txt"), "the edit made in the second of the index write is in the snapshot")
}

// skip-worktree and assume-unchanged entries are copied with their flags, and `add -A` does not look at a flagged entry: the snapshot
// would hold the old content. The flags are cleared on the copy (the caller's index keeps them), so the working tree wins.
func TestSnapshot_FlaggedEntriesFollowTheWorkingTree(t *testing.T) {
	dir := ignoredRepo(t)
	gitIn(t, dir, "update-index", "--skip-worktree", "conf.txt")
	gitIn(t, dir, "update-index", "--assume-unchanged", "a.txt")
	writeFileMode(t, dir+"/conf.txt", "c2 changed\n", 0o644)
	writeFileMode(t, dir+"/a.txt", "a2 changed\n", 0o644)
	before := gitIn(t, dir, "ls-files", "-v")
	info, _ := snapFiles(t, dir)
	assert.Equal(t, "c2 changed", gitIn(t, dir, "show", info.SHA+":conf.txt"), "skip-worktree")
	assert.Equal(t, "a2 changed", gitIn(t, dir, "show", info.SHA+":a.txt"), "assume-unchanged")
	assert.Equal(t, before, gitIn(t, dir, "ls-files", "-v"), "the caller's flags are as they were")

	require.NoError(t, os.Remove(dir+"/conf.txt"))
	_, files := snapFiles(t, dir)
	assert.NotContains(t, files, "conf.txt", "a flagged file that is gone from the tree is gone from the snapshot")
}

// With split index the real index points at a shared index in the git directory: the copy reads it from there, and the snapshot writes
// nothing into the caller's git directory (no new sharedindex.*, the index file itself unchanged).
func TestSnapshot_SplitIndexIsReadAndTheGitDirectoryIsNotWrittenTo(t *testing.T) {
	dir := ignoredRepo(t)
	gitIn(t, dir, "config", "core.splitIndex", "true")
	gitIn(t, dir, "update-index", "--split-index")
	writeFileMode(t, dir+"/a.txt", "a2 changed\n", 0o644)
	writeFileMode(t, dir+"/new.txt", "new\n", 0o644)
	writeFileMode(t, dir+"/keep.log", "log2 changed\n", 0o644)
	gitdir := filepath.Join(dir, ".git")
	list := func() string {
		var names []string
		ents, err := os.ReadDir(gitdir)
		require.NoError(t, err)
		for _, e := range ents {
			names = append(names, e.Name())
		}
		return strings.Join(names, ",")
	}
	indexBefore, err := os.ReadFile(filepath.Join(gitdir, "index"))
	require.NoError(t, err)
	namesBefore := list()
	require.Contains(t, namesBefore, "sharedindex.", "the repo really is split")

	info, files := snapFiles(t, dir)
	assert.ElementsMatch(t, []string{".gitignore", "a.txt", "conf.txt", "keep.log", "new.txt"}, files)
	assert.Equal(t, "log2 changed", gitIn(t, dir, "show", info.SHA+":keep.log"))
	indexAfter, err := os.ReadFile(filepath.Join(gitdir, "index"))
	require.NoError(t, err)
	assert.Equal(t, indexBefore, indexAfter, "the caller's index file is not touched")
	assert.Equal(t, namesBefore, list(), "and no shared index was written beside it")
}

// A linked worktree keeps its index under the main repo's .git/worktrees/<name>/: the copy is of that one.
func TestSnapshot_ALinkedWorktreeUsesItsOwnIndex(t *testing.T) {
	main := ignoredRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, main, "worktree", "add", "-q", "-b", "side", wt)
	if r, err := filepath.EvalSymlinks(wt); err == nil {
		wt = r
	}
	writeFileMode(t, wt+"/keep.log", "log in the worktree\n", 0o644)
	writeFileMode(t, wt+"/a.txt", "a in the worktree\n", 0o644)
	gitIn(t, wt, "add", "a.txt") // staged in this worktree's index only
	writeFileMode(t, wt+"/a.txt", "a in the worktree, later\n", 0o644)
	info, files := snapFiles(t, wt)
	assert.ElementsMatch(t, []string{".gitignore", "a.txt", "conf.txt", "keep.log"}, files)
	assert.Equal(t, "log in the worktree", gitIn(t, wt, "show", info.SHA+":keep.log"))
	assert.Equal(t, "a in the worktree, later", gitIn(t, wt, "show", info.SHA+":a.txt"))
	assert.Equal(t, "c1", gitIn(t, main, "show", info.SHA+":conf.txt"))
}

// The snapshot has the same tree as one made from an empty index for a repo with nothing special (the speed does not change what is in it).
func TestSnapshot_APlainRepoGivesTheSameTreeAsBefore(t *testing.T) {
	dir := workRepo(t)
	info, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	tmp := t.TempDir()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "empty-index")}
	gitInEnv(t, dir, env, "add", "-A")
	wantTree := gitInEnv(t, dir, env, "write-tree")
	assert.Equal(t, wantTree, gitIn(t, dir, "rev-parse", info.SHA+"^{tree}"))
}

// A snapshot is of the repository that holds dir, whatever repository variables the process carries (a git hook that runs the gate has
// GIT_DIR and GIT_INDEX_FILE): another repository is not read, written to, or made the parent of the commit.
func TestSnapshot_InheritedGitRepositoryVariablesAreIgnored(t *testing.T) {
	dir := ignoredRepo(t)
	writeFileMode(t, dir+"/a.txt", "a changed here\n", 0o644)
	other := ignoredRepo(t)
	writeFileMode(t, other+"/a.txt", "a changed in the other repository\n", 0o644)
	otherIndex, err := os.ReadFile(other + "/.git/index")
	require.NoError(t, err)
	otherObjects := gitIn(t, other, "count-objects", "-v")
	otherHead := gitIn(t, other, "rev-parse", "HEAD")

	set := map[string]string{
		"GIT_DIR": other + "/.git", "GIT_WORK_TREE": other, "GIT_INDEX_FILE": filepath.Join(t.TempDir(), "bogus-index"),
		"GIT_OBJECT_DIRECTORY": other + "/.git/objects", "GIT_COMMON_DIR": other + "/.git",
	}
	old := map[string]*string{}
	for k, v := range set {
		if cur, ok := os.LookupEnv(k); ok {
			c := cur
			old[k] = &c
		} else {
			old[k] = nil
		}
		require.NoError(t, os.Setenv(k, v))
	}
	info, err := SnapshotTree(context.Background(), "", dir)
	for k, was := range old { // before anything below runs git: the helpers must see the real environment
		if was == nil {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, *was)
		}
	}
	require.NoError(t, err)

	assert.Equal(t, dir, info.Root)
	assert.Equal(t, "a changed here", gitIn(t, dir, "show", info.SHA+":a.txt"))
	assert.Equal(t, gitIn(t, dir, "rev-parse", "HEAD"), gitIn(t, dir, "rev-parse", info.SHA+"^"), "its parent is this repository's HEAD")
	assert.Equal(t, otherHead, gitIn(t, other, "rev-parse", "HEAD"))
	assert.Equal(t, otherObjects, gitIn(t, other, "count-objects", "-v"), "nothing was written to the other repository")
	afterIndex, err := os.ReadFile(other + "/.git/index")
	require.NoError(t, err)
	assert.Equal(t, otherIndex, afterIndex)
}

// Configuration that arrives in the environment (GIT_CONFIG_COUNT / KEY_n / VALUE_n, GIT_CONFIG_PARAMETERS) is ignored too: here it would
// make git leave a new file out of the snapshot (core.excludesFile), and the same way it could name a command to run.
func TestSnapshot_InheritedGitConfigVariablesAreIgnored(t *testing.T) {
	dir := ignoredRepo(t)
	writeFileMode(t, dir+"/u.txt", "untracked\n", 0o644)
	excludes := filepath.Join(t.TempDir(), "excludes")
	writeFileMode(t, excludes, "u.txt\n", 0o644)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.excludesFile")
	t.Setenv("GIT_CONFIG_VALUE_0", excludes)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.excludesFile="+excludes+"'")
	info, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	t.Setenv("GIT_CONFIG_COUNT", "") // the helper below must see an ordinary git
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	assert.Equal(t, "untracked", gitIn(t, dir, "show", info.SHA+":u.txt"), "the injected exclude file did not apply")
}

func gitInEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}
