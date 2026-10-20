package gate

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- WorkingTree: the tree of the working directory as it is now, for a record that does not need a commit (the fast lane) ----

func objectCount(t *testing.T, dir string) string { return gitIn(t, dir, "count-objects", "-v") }

// A clean working directory has HEAD's tree; a changed, a new (untracked) and a deleted file each make it another tree, and it is the
// tree SnapshotTree commits for the same state.
func TestWorkingTree_IsHeadsTreeWhenCleanAndTheRealContentWhenNot(t *testing.T) {
	dir := ignoredRepo(t)
	ctx := context.Background()
	headTree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	tree, err := WorkingTree(ctx, "", dir)
	require.NoError(t, err)
	assert.Equal(t, headTree, tree, "clean")

	writeFile(t, dir+"/a.txt", "a2 changed\n", 0o644)
	tree, err = WorkingTree(ctx, "", dir)
	require.NoError(t, err)
	assert.NotEqual(t, headTree, tree, "a changed file")
	snap, err := SnapshotTree(ctx, "", dir)
	require.NoError(t, err)
	assert.Equal(t, gitIn(t, dir, "rev-parse", snap.SHA+"^{tree}"), tree, "the same tree a snapshot commit holds")

	writeFile(t, dir+"/new.txt", "never added\n", 0o644)
	withNew, err := WorkingTree(ctx, "", dir)
	require.NoError(t, err)
	assert.NotEqual(t, tree, withNew, "an untracked file")

	require.NoError(t, os.Remove(dir+"/conf.txt"))
	withoutConf, err := WorkingTree(ctx, "", dir)
	require.NoError(t, err)
	assert.NotEqual(t, withNew, withoutConf, "a deleted file")
}

// It writes no object into the repository (the fast lane must stay out of it) and leaves the caller's index as it was.
func TestWorkingTree_WritesNothingIntoTheRepository(t *testing.T) {
	dir := ignoredRepo(t)
	writeFile(t, dir+"/a.txt", "a2 changed\n", 0o644)
	writeFile(t, dir+"/new.txt", "never added\n", 0o644)
	beforeObjects, beforeStatus := objectCount(t, dir), gitIn(t, dir, "status", "--porcelain")
	_, err := WorkingTree(context.Background(), "", dir)
	require.NoError(t, err)
	assert.Equal(t, beforeObjects, objectCount(t, dir), "no loose object written")
	assert.Equal(t, beforeStatus, gitIn(t, dir, "status", "--porcelain"), "the index is the caller's still")
}

// ---- WorkingTree gives the tree SnapshotTree commits, in every state the index and the working directory can be in (#3745 keeps this
// true while it gets faster) ----

func snapTree(t *testing.T, dir string) string {
	t.Helper()
	snap, err := SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	return gitIn(t, dir, "rev-parse", snap.SHA+"^{tree}")
}

func TestWorkingTree_EqualsTheSnapshotsTreeInEveryState(t *testing.T) {
	steps := []struct {
		name string
		do   func(t *testing.T, dir string)
	}{
		{"clean", func(t *testing.T, dir string) {}},
		{"a changed file", func(t *testing.T, dir string) { writeFile(t, dir+"/a.txt", "a2\n", 0o644) }},
		{"a new file", func(t *testing.T, dir string) { writeFile(t, dir+"/new.txt", "n\n", 0o644) }},
		{"a deleted file", func(t *testing.T, dir string) { require.NoError(t, os.Remove(dir+"/conf.txt")) }},
		{"a staged change", func(t *testing.T, dir string) {
			writeFile(t, dir+"/a.txt", "a3 staged\n", 0o644)
			gitIn(t, dir, "add", "a.txt")
		}},
		{"a staged change edited back", func(t *testing.T, dir string) { writeFile(t, dir+"/a.txt", "a1\n", 0o644) }},
		{"a staged new file then deleted", func(t *testing.T, dir string) {
			writeFile(t, dir+"/s.txt", "s\n", 0o644)
			gitIn(t, dir, "add", "s.txt")
			require.NoError(t, os.Remove(dir+"/s.txt"))
		}},
		{"an ignored tracked file changed", func(t *testing.T, dir string) { writeFile(t, dir+"/keep.log", "log2\n", 0o644) }},
		{"an assume-unchanged file changed", func(t *testing.T, dir string) {
			gitIn(t, dir, "update-index", "--assume-unchanged", "conf.txt")
			writeFile(t, dir+"/conf.txt", "c2 hidden\n", 0o644)
		}},
		{"a skip-worktree file changed", func(t *testing.T, dir string) {
			gitIn(t, dir, "update-index", "--no-assume-unchanged", "conf.txt")
			gitIn(t, dir, "update-index", "--skip-worktree", "a.txt")
			writeFile(t, dir+"/a.txt", "a4 hidden\n", 0o644)
		}},
	}
	dir := ignoredRepo(t)
	for _, s := range steps { // the steps build on each other: the states are the ones a working session passes through
		s.do(t, dir)
		got, err := WorkingTree(context.Background(), "", dir)
		require.NoError(t, err, s.name)
		assert.Equal(t, snapTree(t, dir), got, s.name)
	}
}
