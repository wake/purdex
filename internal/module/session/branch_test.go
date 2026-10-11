package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeHead(t *testing.T, repo, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte(content), 0o644))
}

func TestListSessions_FillsBranchFromCwd(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	repo := t.TempDir()
	writeHead(t, repo, "ref: refs/heads/main\n")
	plain := t.TempDir()
	fake.AddSession("in-repo", repo)
	fake.AddSession("plain", plain)

	list, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	got := map[string]string{}
	for _, s := range list {
		got[s.Name] = s.Branch
	}
	assert.Equal(t, "main", got["in-repo"])
	assert.Equal(t, "", got["plain"])
}

func TestHashSessions_ChangesWhenBranchSwitches(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	repo := t.TempDir()
	writeHead(t, repo, "ref: refs/heads/main\n")
	fake.AddSession("s", repo)

	before, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	writeHead(t, repo, "ref: refs/heads/dev\n")
	after, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "dev", after[0].Branch)
	assert.NotEqual(t, hashSessions("i", before), hashSessions("i", after),
		"switching branch must change the hash so the watcher broadcasts")
}

func TestGetSession_FillsBranch(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	repo := t.TempDir()
	writeHead(t, repo, "ref: refs/heads/feat/x\n")
	fake.AddSession("s", repo)
	list, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	info, err := mod.GetSessionContext(context.Background(), list[0].Code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "feat/x", info.Branch)
}
