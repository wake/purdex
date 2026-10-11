package session

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// 13 sessions: 6 plain repos, 3 worktrees (.git file), 4 non-repo.
func benchSessions(b *testing.B, fill bool) {
	mod, _, fake := newTestModule(b)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	root := b.TempDir()
	for i := 0; i < 13; i++ {
		d := filepath.Join(root, "p"+strconv.Itoa(i), "src")
		require.NoError(b, os.MkdirAll(d, 0o755))
		base := filepath.Join(root, "p"+strconv.Itoa(i))
		switch {
		case i < 6:
			require.NoError(b, os.MkdirAll(filepath.Join(base, ".git"), 0o755))
			require.NoError(b, os.WriteFile(filepath.Join(base, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
		case i < 9:
			g := filepath.Join(root, "g"+strconv.Itoa(i))
			require.NoError(b, os.MkdirAll(g, 0o755))
			require.NoError(b, os.WriteFile(filepath.Join(g, "HEAD"), []byte("ref: refs/heads/wt\n"), 0o644))
			require.NoError(b, os.WriteFile(filepath.Join(base, ".git"), []byte("gitdir: "+g+"\n"), 0o644))
		}
		if fill {
			fake.AddSession("s"+strconv.Itoa(i), d)
		} else {
			fake.AddSession("s"+strconv.Itoa(i), "")
		}
	}
	// Warm the cache: steady state is a map read per session.
	_, err := mod.ListSessionsContext(context.Background())
	require.NoError(b, err)
	mod.branches.wait()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mod.ListSessionsContext(context.Background())
		require.NoError(b, err)
	}
}

// One full background round: resolve 13 cwds from scratch.
func BenchmarkBranchRefreshRound13(b *testing.B) {
	root := b.TempDir()
	var cwds []string
	for i := 0; i < 13; i++ {
		base := filepath.Join(root, "p"+strconv.Itoa(i))
		d := filepath.Join(base, "src")
		require.NoError(b, os.MkdirAll(filepath.Join(base, ".git"), 0o755))
		require.NoError(b, os.MkdirAll(d, 0o755))
		require.NoError(b, os.WriteFile(filepath.Join(base, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
		cwds = append(cwds, d)
	}
	c := newGitBranchCache()
	c.minInterval = 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.refresh(cwds)
		c.wait()
	}
}

func BenchmarkListSessions13WithBranch(b *testing.B)    { benchSessions(b, true) }
func BenchmarkListSessions13WithoutBranch(b *testing.B) { benchSessions(b, false) }
