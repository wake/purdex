package journal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func resetCache() {
	cacheMu.Lock()
	cache = map[string]cached{}
	cacheMu.Unlock()
}

func TestProjectOfWorktreesShareID(t *testing.T) {
	resetCache()
	main := t.TempDir()
	git(t, main, "init", "-q", "-b", "main")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", "-b", "side", wt)
	sub := filepath.Join(main, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err1 := ProjectOf(context.Background(), main)
	b, err2 := ProjectOf(context.Background(), wt)
	c, err3 := ProjectOf(context.Background(), sub)
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatal(err1, err2, err3)
	}
	if a.ID != b.ID || a.ID != c.ID || len(a.ID) != 16 {
		t.Fatalf("ids differ: %v %v %v", a, b, c)
	}
	if a.CommonDir == "" || b.Root != a.Root {
		t.Fatalf("root/common dir: %v %v", a, b)
	}
	if real, _ := filepath.EvalSymlinks(main); a.Root != real {
		t.Fatalf("root %q want %q", a.Root, real)
	}
}

func TestProjectOfNonRepo(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	p, err := ProjectOf(context.Background(), dir)
	if err != nil || p.CommonDir != "" || p.Root != dir || p.ID != hashID(dir) {
		t.Fatalf("%v %v", p, err)
	}
	other, _ := ProjectOf(context.Background(), t.TempDir())
	if other.ID == p.ID {
		t.Fatal("two non-repo dirs share an ID")
	}
	if _, err := ProjectOf(context.Background(), ""); err == nil {
		t.Fatal("empty cwd accepted")
	}
}

func TestProjectOfTimeoutNotCached(t *testing.T) {
	resetCache()
	old := runGit
	defer func() { runGit = old }()
	runGit = func(ctx context.Context, cwd string) (string, error) { <-ctx.Done(); return "", ctx.Err() }
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p, err := ProjectOf(ctx, "/some/where")
	if err == nil || p.Root != "/some/where" || time.Since(start) > time.Second {
		t.Fatalf("%v %v", p, err)
	}
	runGit = func(ctx context.Context, cwd string) (string, error) { return "/r/.git", nil }
	p, err = ProjectOf(context.Background(), "/some/where")
	if err != nil || p.Root != "/r" {
		t.Fatalf("timeout was cached: %v %v", p, err)
	}
}

func TestProjectOfCacheExpires(t *testing.T) {
	resetCache()
	oldRun, oldNow := runGit, now
	defer func() { runGit, now = oldRun, oldNow }()
	calls := 0
	runGit = func(ctx context.Context, cwd string) (string, error) { calls++; return "/r/.git", nil }
	base := time.Now()
	now = func() time.Time { return base }
	_, _ = ProjectOf(context.Background(), "/c")
	_, _ = ProjectOf(context.Background(), "/c")
	now = func() time.Time { return base.Add(cacheTTL + time.Second) }
	_, _ = ProjectOf(context.Background(), "/c")
	if calls != 2 {
		t.Fatalf("git ran %d times, want 2", calls)
	}
}
