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
