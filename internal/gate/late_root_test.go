package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The packaging gate marks the environment of its command at once, with its own pid, so a packaging gate below it can tell the
// tree has its late slot coming (#3031). A gate that is not watching gives no mark.
func TestPackaging_TheCommandGetsTheLateRootMarkAtItsStart(t *testing.T) {
	g := packagingGate(t, time.Hour)
	g.PID = func() int { return 7000 }
	dir := t.TempDir()
	fakeTool(t, dir, "echo \"$"+LateRootEnv+"\" > "+dir+"/mark")
	done := runPackaging(g, dir)
	eventually(t, "the start", func() bool { return exists(dir + "/started") })
	b, err := os.ReadFile(dir + "/mark")
	require.NoError(t, err)
	assert.Equal(t, "7000", strings.TrimSpace(string(b)))
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)

	g0 := packagingGate(t, 0) // no late slot: nothing to mark
	dir0 := t.TempDir()
	fakeTool(t, dir0, "echo \"mark=$"+LateRootEnv+"\" > "+dir0+"/mark")
	require.NoError(t, os.WriteFile(dir0+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-runPackaging(g0, dir0)).code)
	b, err = os.ReadFile(dir0 + "/mark")
	require.NoError(t, err)
	assert.Equal(t, "mark=", strings.TrimSpace(string(b)))
}

// A packaging gate below another one that will take the late slot takes none: the tree holds one slot, not two. A mark that
// names a pid that is not above it (left by another tree) is no reason to hold back.
func TestPackaging_AnInnerGateBelowTheLateRootTakesNoSlotOfItsOwn(t *testing.T) {
	g := packagingGate(t, 150*time.Millisecond)

	inner := func(mark string) (*Gate, <-chan runResult, string) {
		ig := *g
		ig.PID = func() int { return 2002 }
		ig.Procs = descendantProcs(2002, 7000) // 2002 -> 1500 -> 7000
		idir := t.TempDir()
		fakeTool(t, idir, "")
		res := runAsync(&ig, context.Background(), Options{
			Argv: []string{"npx", "electron-builder"}, Real: filepath.Join(idir, "fakepnpm"),
			Env: cleanEnv(LateRootEnv + "=" + mark),
		})
		return &ig, res, idir
	}

	// the mark names a gate above it: no slot of its own, however long it runs, and it is written down as packaging
	ig, res, idir := inner("7000")
	eventually(t, "the start", func() bool { return exists(idir + "/started") })
	time.Sleep(500 * time.Millisecond) // well past the delay
	assert.Empty(t, lightHeld(g), "the inner gate takes no late slot")
	require.NoError(t, os.WriteFile(idir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
	rec := lastDecision(t, ig)
	assert.Equal(t, "packaging", rec["outcome"])
	assert.NotContains(t, rec, "late_slot")

	// a mark of another tree: pid 8888 is not above the inner gate
	_, res, idir = inner("8888")
	eventually(t, "the late slot", func() bool { return len(lightHeld(g)) == 1 })
	require.NoError(t, os.WriteFile(idir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)

	// a mark that is not a pid, or is 1, says nothing
	for _, mark := range []string{"soon", "1", ""} {
		_, res, idir = inner(mark)
		eventually(t, "the late slot for mark "+mark, func() bool { return len(lightHeld(g)) == 1 })
		require.NoError(t, os.WriteFile(idir+"/go", nil, 0o644))
		assert.Equal(t, 0, (<-res).code)
	}
}
