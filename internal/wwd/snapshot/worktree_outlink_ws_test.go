package snapshot

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The target is judged as it is stored, spaces included: " ../x" is one component named " .." and stays inside, "../x " climbs out
// (the codex re-review of #3841 A: the target went through a trimming runner).
func TestWorkingTree_JudgesATargetWithSpacesAsItIs(t *testing.T) {
	dir := workRepo(t)
	require.NoError(t, os.Symlink(" ../x", dir+"/leading-space"))  // inside: the first component is " .."
	require.NoError(t, os.Symlink("../x ", dir+"/trailing-space")) // out: ".." climbs out of the repository
	tree, skipped, err := WorkingTreeSkipped(context.Background(), "", dir)
	require.NoError(t, err)
	tree = outlinkTree(t, dir, tree)
	assert.True(t, treeHas(t, dir, tree, "leading-space"))
	assert.False(t, treeHas(t, dir, tree, "trailing-space"))
	assert.Equal(t, []string{"trailing-space"}, skipped)
}
