package gitbranch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, p, s string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(s), 0o644))
}

func TestBranch(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "repo/.git/HEAD"), "ref: refs/heads/feat/x\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "repo/a/b"), 0o755))
	assert.Equal(t, "feat/x", Branch(filepath.Join(root, "repo"), root))
	assert.Equal(t, "feat/x", Branch(filepath.Join(root, "repo/a/b"), root), "nested dir")

	write(t, filepath.Join(root, "det/.git/HEAD"), "abc1234def5678abc1234def5678abc1234def56\n")
	assert.Equal(t, "@abc1234", Branch(filepath.Join(root, "det"), root))

	// worktree: .git file with relative gitdir
	write(t, filepath.Join(root, "main/.git/worktrees/w/HEAD"), "ref: refs/heads/wt\n")
	write(t, filepath.Join(root, "wt/.git"), "gitdir: ../main/.git/worktrees/w\n")
	assert.Equal(t, "wt", Branch(filepath.Join(root, "wt"), root))
	// absolute gitdir
	write(t, filepath.Join(root, "wt2/.git"), "gitdir: "+filepath.Join(root, "main/.git/worktrees/w")+"\n")
	assert.Equal(t, "wt", Branch(filepath.Join(root, "wt2"), root))

	// not a repo / empty / unreadable
	require.NoError(t, os.MkdirAll(filepath.Join(root, "plain"), 0o755))
	assert.Equal(t, "", Branch(filepath.Join(root, "plain"), root))
	assert.Equal(t, "", Branch("", root))
	assert.Equal(t, "", Branch("relative/path", root))
	write(t, filepath.Join(root, "bad/.git/HEAD"), "garbage")
	assert.Equal(t, "", Branch(filepath.Join(root, "bad"), root))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nohead/.git"), 0o755))
	assert.Equal(t, "", Branch(filepath.Join(root, "nohead"), root))
	write(t, filepath.Join(root, "badptr/.git"), "nonsense")
	assert.Equal(t, "", Branch(filepath.Join(root, "badptr"), root))
}

func TestStopAtBoundary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git/HEAD"), "ref: refs/heads/outer\n")
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "p"), 0o755))
	assert.Equal(t, "", Branch(filepath.Join(home, "p"), home), "never looks above stopAt")
	assert.Equal(t, "outer", Branch(filepath.Join(home, "p"), root))
}

func TestResolverRereadsHeadAndRediscovers(t *testing.T) {
	root := t.TempDir()
	r := NewResolver(root)
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	assert.Equal(t, "", r.Branch(repo))
	head := filepath.Join(repo, ".git/HEAD")
	write(t, head, "ref: refs/heads/main\n")
	assert.Equal(t, "main", r.Branch(repo))
	write(t, head, "ref: refs/heads/dev\n")
	assert.Equal(t, "dev", r.Branch(repo), "HEAD content is re-read")
	require.NoError(t, os.RemoveAll(filepath.Join(repo, ".git")))
	assert.Equal(t, "", r.Branch(repo), "stale cached location is rediscovered")
	write(t, filepath.Join(repo, ".git/HEAD"), "ref: refs/heads/again\n")
	assert.Equal(t, "again", r.Branch(repo))
}

func BenchmarkResolver13(b *testing.B) {
	root := b.TempDir()
	var dirs []string
	for i := 0; i < 13; i++ {
		n := string(rune('a' + i))
		d := filepath.Join(root, "d", n, "src", "deep")
		require.NoError(b, os.MkdirAll(d, 0o755))
		switch {
		case i < 6:
			write(b, filepath.Join(root, "d", n, ".git/HEAD"), "ref: refs/heads/main\n")
		case i < 9:
			write(b, filepath.Join(root, "g", n, "HEAD"), "ref: refs/heads/wt\n")
			write(b, filepath.Join(root, "d", n, ".git"), "gitdir: "+filepath.Join(root, "g", n)+"\n")
		}
		dirs = append(dirs, d)
	}
	r := NewResolver(root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, d := range dirs {
			r.Branch(d)
		}
	}
}
