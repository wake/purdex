package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// realTmp is t.TempDir() with its own symlinks resolved (macOS keeps /var -> /private/var), so expected names are
// comparable with what canonPath returns.
func realTmp(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return real
}

// #2576: a dangling config symlink whose target holds ".." after another symlink. The ".." is the parent of what the symlink
// points at, not of the symlink's own directory, so it may not be folded before the symlink is expanded.
// Mutation gate: fold ".." lexically first (filepath.Clean on the target) → red.
func TestCanonPath_DotDotAfterASymlinkInADanglingTarget(t *testing.T) {
	tmp := realTmp(t)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "real", "sub"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join("real", "sub"), filepath.Join(tmp, "alias")))
	cfg := filepath.Join(tmp, "config.toml")
	require.NoError(t, os.Symlink(filepath.Join("alias", "..", "target.toml"), cfg)) // target.toml does not exist yet

	// alias -> real/sub, so alias/.. is real/ and the file would be created at real/target.toml.
	require.Equal(t, filepath.Join(tmp, "real", "target.toml"), canonPath(cfg))
}

// #2576: a path through a dangling DIRECTORY symlink names where the directory would be created. Mutation gate: only follow
// a symlink in the last position → red.
func TestCanonPath_ThroughADanglingDirectorySymlink(t *testing.T) {
	tmp := realTmp(t)
	require.NoError(t, os.Symlink("missing-dir", filepath.Join(tmp, "dangling-dir")))
	require.Equal(t, filepath.Join(tmp, "missing-dir", "config.toml"), canonPath(filepath.Join(tmp, "dangling-dir", "config.toml")))
	// also with deeper not-yet-existing components behind it
	require.Equal(t, filepath.Join(tmp, "missing-dir", "a", "b", "config.toml"), canonPath(filepath.Join(tmp, "dangling-dir", "a", "b", "config.toml")))
}

// The point of it all: every name of one config file gives the same canonical path, hence the same <path>.lock / <path>.run.
func TestCanonPath_AllNamesOfOneConfigAgree(t *testing.T) {
	tmp := realTmp(t)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "real", "sub"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join("real", "sub"), filepath.Join(tmp, "alias")))
	require.NoError(t, os.Symlink(filepath.Join("alias", "..", "target.toml"), filepath.Join(tmp, "config.toml")))
	require.NoError(t, os.Symlink("missing-dir", filepath.Join(tmp, "dangling-dir")))
	require.NoError(t, os.Symlink(filepath.Join("dangling-dir", "config.toml"), filepath.Join(tmp, "via-dir.toml")))

	want := filepath.Join(tmp, "real", "target.toml")
	require.Equal(t, want, canonPath(filepath.Join(tmp, "config.toml")))
	require.Equal(t, want, canonPath(filepath.Join(tmp, "alias", "..", "target.toml")))
	require.Equal(t, want, canonPath(filepath.Join(tmp, "real", "target.toml")))
	require.Equal(t, filepath.Join(tmp, "missing-dir", "config.toml"), canonPath(filepath.Join(tmp, "via-dir.toml")))
}

// A symlink loop ends (ELOOP-like cap) instead of spinning, and still answers with an absolute name (the write that follows
// fails on its own). Mutation gate: remove the cap → the call never returns → red.
func TestCanonPath_ASymlinkLoopTerminates(t *testing.T) {
	tmp := realTmp(t)
	require.NoError(t, os.Symlink("b", filepath.Join(tmp, "a")))
	require.NoError(t, os.Symlink("a", filepath.Join(tmp, "b")))
	require.NoError(t, os.Symlink("self", filepath.Join(tmp, "self")))

	for _, p := range []string{
		filepath.Join(tmp, "a", "config.toml"),
		filepath.Join(tmp, "self"),
		filepath.Join(tmp, "self", "x", "config.toml"),
	} {
		done := make(chan string, 1)
		go func() { done <- canonPath(p) }()
		select {
		case got := <-done:
			require.True(t, filepath.IsAbs(got), "%s -> %q", p, got)
		case <-time.After(5 * time.Second):
			t.Fatalf("canonPath(%s) did not return: a symlink loop is not capped", p)
		}
	}
}

// What was right stays right: an existing file, a path not yet there under an existing directory, a relative name, a plain
// ".." in a tail that does not exist, and a file reached through an ordinary symlinked directory.
func TestCanonPath_OrdinaryPathsAreUnchanged(t *testing.T) {
	tmp := realTmp(t)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "dir"), 0o755))
	file := filepath.Join(tmp, "dir", "config.toml")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	require.NoError(t, os.Symlink("dir", filepath.Join(tmp, "link")))

	require.Equal(t, file, canonPath(file))
	require.Equal(t, file, canonPath(filepath.Join(tmp, "link", "config.toml")))
	require.Equal(t, filepath.Join(tmp, "dir", "new.toml"), canonPath(filepath.Join(tmp, "dir", "new.toml")))
	require.Equal(t, filepath.Join(tmp, "dir", "later", "x.toml"), canonPath(filepath.Join(tmp, "dir", "later", "x.toml")))
	require.Equal(t, filepath.Join(tmp, "dir", "x.toml"), canonPath(filepath.Join(tmp, "dir", "nothere", "..", "x.toml")))
	require.Equal(t, file, canonPath(filepath.Join(tmp, "dir", ".", "config.toml")))
	require.Equal(t, filepath.Join(tmp, "dir"), canonPath(filepath.Join(tmp, "link")+string(filepath.Separator)))

	// a relative name is made absolute against the working directory
	wd, err := os.Getwd()
	require.NoError(t, err)
	realWd, err := filepath.EvalSymlinks(wd)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(realWd, "rel-config.toml"), canonPath("rel-config.toml"))

	// the default location: <home>/.config/pdx/config.toml with nothing there yet
	home := realTmp(t)
	require.Equal(t, filepath.Join(home, ".config", "pdx", "config.toml"), canonPath(filepath.Join(home, ".config", "pdx", "config.toml")))
}
