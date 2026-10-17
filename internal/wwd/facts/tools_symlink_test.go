package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A pdx shim reached through a symlink is still a shim (R2): a PATH directory
// that links to the shim directory, or a tool that links to a shim, must not be
// run.
func TestShimsBehindSymlinksAreNeverRun(t *testing.T) {
	root := t.TempDir()
	shims := filepath.Join(root, "home", ".config", "pdx", "shims")
	shimGo := exe(t, shims, "go")
	shimNode := exe(t, shims, "node")

	dirLink := filepath.Join(root, "linked-bin")
	if err := os.Symlink(shims, dirLink); err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shimNode, filepath.Join(toolDir, "node")); err != nil {
		t.Fatal(err)
	}
	realPnpm := exe(t, filepath.Join(root, "real"), "pnpm")

	// the fake answers for every path a shim could be reached by
	run := &fakeRun{out: map[string]string{
		shimGo: "go version go1.0.0 x/y", filepath.Join(dirLink, "go"): "go version go1.0.0 x/y",
		shimNode: "v1.0.0", filepath.Join(toolDir, "node"): "v1.0.0",
		realPnpm: "10.23.0",
	}}
	r := &ToolsReader{
		Run: run.run, Now: func() time.Time { return t0 }, TTL: time.Minute,
		PathDirs: []string{dirLink, toolDir, filepath.Join(root, "real")},
	}
	got := r.Get()
	if got.Go != nil || got.Node != nil {
		t.Errorf("a shim was used: go=%v node=%v", got.Go, got.Node)
	}
	if got.Pnpm == nil || *got.Pnpm != "10.23.0" {
		t.Errorf("the real pnpm was not found: %v", got.Pnpm)
	}
	for _, c := range run.calls {
		if strings.Contains(c, "linked-bin") || strings.Contains(c, "tools") || strings.Contains(c, "shims") {
			t.Errorf("ran %s", c)
		}
	}
}
