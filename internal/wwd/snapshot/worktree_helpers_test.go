package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two helpers the worktree tests brought from internal/gate/assist_run_test.go (WWD-9 W1): gitIn verbatim; writeFileMode is gate's
// writeFile(t, path, content, mode) under another name, because this package's writeFile (serve_test.go) takes no mode.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func writeFileMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
}

// workRepo is a repo with a commit, a staged change, an unstaged change, an untracked file and an ignored one (gate's helper of the same
// name, with writeFile spelled writeFileMode).
func workRepo(t *testing.T) string {
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFileMode(t, dir+"/.gitignore", "ignored.txt\nnode_modules/\n", 0o644)
	writeFileMode(t, dir+"/a.txt", "a1\n", 0o644)
	writeFileMode(t, dir+"/sub/b.txt", "b1\n", 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	writeFileMode(t, dir+"/a.txt", "a2 staged\n", 0o644)
	gitIn(t, dir, "add", "a.txt")
	writeFileMode(t, dir+"/sub/b.txt", "b2 unstaged\n", 0o644)
	writeFileMode(t, dir+"/new.txt", "untracked\n", 0o644)
	writeFileMode(t, dir+"/ignored.txt", "ignored\n", 0o644)
	return dir
}
