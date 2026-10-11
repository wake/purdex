package session

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeHead(t *testing.T, repo, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte(content), 0o644))
}

// settle lists, waits for the background refresh to land, and lists again: the
// first list only schedules the resolve, the second reads its result.
func settle(t *testing.T, mod *SessionModule) []SessionInfo {
	t.Helper()
	_, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	mod.branches.wait()
	list, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	return list
}

func TestListSessions_FillsBranchFromCwd(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	repo := t.TempDir()
	writeHead(t, repo, "ref: refs/heads/main\n")
	plain := t.TempDir()
	fake.AddSession("in-repo", repo)
	fake.AddSession("plain", plain)

	first, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	for _, s := range first {
		assert.Equal(t, "", s.Branch, "a cold cache is empty; the list never resolves inline")
	}
	list := settle(t, mod)
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
	mod.branches.minInterval = 0
	repo := t.TempDir()
	writeHead(t, repo, "ref: refs/heads/main\n")
	fake.AddSession("s", repo)

	before := settle(t, mod)
	assert.Equal(t, "main", before[0].Branch)
	writeHead(t, repo, "ref: refs/heads/dev\n")
	after := settle(t, mod)

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
	list := settle(t, mod)
	info, err := mod.GetSessionContext(context.Background(), list[0].Code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "feat/x", info.Branch)
}

func TestListSessions_BlockedResolveNeverBlocksList(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	mod.branches.resolve = func(string) string {
		calls.Add(1)
		<-release // a HEAD on a hung filesystem: never returns
		return "never"
	}
	mod.branches.timeout = 50 * time.Millisecond
	mod.branches.minInterval = 0
	fake.AddSession("s", t.TempDir())

	for i := 0; i < 5; i++ {
		start := time.Now()
		list, err := mod.ListSessionsContext(context.Background())
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 500*time.Millisecond)
		assert.Equal(t, "", list[0].Branch)
		info, err := mod.GetSessionContext(context.Background(), list[0].Code)
		require.NoError(t, err)
		assert.Equal(t, "", info.Branch)
		time.Sleep(20 * time.Millisecond)
	}
	assert.EqualValues(t, 1, calls.Load(), "a stuck cwd must not spawn a second resolver")
}

func TestBranchCache_OneInflightPerCwdAndTimeoutClears(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := newBranchCache(func(cwd string) string {
		calls.Add(1)
		<-release
		return "late"
	})
	c.timeout = 30 * time.Millisecond
	c.minInterval = 0
	// Pre-seed a value, then let the resolve time out: the stale value must go.
	c.entries["/a"] = branchEntry{val: "old"}
	for i := 0; i < 10; i++ {
		c.refresh([]string{"/a", "/a"})
	}
	require.Eventually(t, func() bool { return calls.Load() >= 1 }, time.Second, time.Millisecond)
	for i := 0; i < 5; i++ {
		c.refresh([]string{"/a"})
	}
	assert.EqualValues(t, 1, calls.Load(), "only one resolve per cwd while one is stuck")
	require.Eventually(t, func() bool { return c.get("/a") == "" }, time.Second, 5*time.Millisecond,
		"a resolve past its timeout counts as no branch")
	close(release)
	c.wait()
	assert.Equal(t, "late", c.get("/a"), "a late result is still accepted once it arrives")
	c.refresh([]string{"/a"})
	c.wait()
	assert.EqualValues(t, 2, calls.Load(), "after finishing, a new refresh may run again")
}

func TestBranchCache_PrunesVanishedCwdsAndCaps(t *testing.T) {
	c := newBranchCache(func(cwd string) string { return "b" })
	c.minInterval = 0
	c.refresh([]string{"/a", "/b"})
	c.wait()
	assert.Equal(t, "b", c.get("/a"))
	c.refresh([]string{"/b"})
	c.wait()
	c.mu.Lock()
	_, hasA := c.entries["/a"]
	c.mu.Unlock()
	assert.False(t, hasA, "cwds no longer listed are dropped")

	var many []string
	for i := 0; i < 2000; i++ {
		many = append(many, "/d/"+strconv.Itoa(i))
	}
	c.refresh(many)
	c.wait()
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	assert.LessOrEqual(t, n, branchCacheMax)
}

func TestListSessions_FifoHeadDoesNotStallRealResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mkfifo")
	}
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func(context.Context) string { return "1:1" }
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(repo, ".git", "HEAD"), 0o644))
	fake.AddSession("s", repo)

	start := time.Now()
	list := settle(t, mod)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, "", list[0].Branch)
}

func TestBranchCache_ConcurrentUse(t *testing.T) {
	c := newBranchCache(func(cwd string) string { return cwd })
	c.minInterval = 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c.refresh([]string{"/x", "/y", "/z"})
				_ = c.get("/x")
			}
		}()
	}
	wg.Wait()
	c.wait()
}
