package manifestref

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/clones"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitManifest writes the manifest into origin (a non-bare repo on branch main) and commits it; returns the commit.
func commitManifest(t *testing.T, origin, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(origin, ".wwd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, ".wwd", "tasks.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, origin, "add", "-A")
	git(t, origin, "commit", "-q", "-m", "m")
	return git(t, origin, "rev-parse", "HEAD")
}

// fixture: origin with one commit, and a prepared clone of it (the clone's working tree is deliberately left stale or dirty by the tests).
func fixture(t *testing.T) (origin, clone string, src *Source) {
	t.Helper()
	dir := t.TempDir()
	origin = filepath.Join(dir, "origin")
	clone = filepath.Join(dir, "clone")
	git(t, dir, "init", "-q", "-b", "main", origin)
	commitManifest(t, origin, "v1\n")
	git(t, dir, "clone", "-q", origin, clone)
	repos := clones.NewSource(map[string]string{"purdex": clone})
	return origin, clone, New(repos, map[string]string{"purdex": "origin/main"}, "")
}

func TestRefreshReadsTheManifestAtTheRefAndNotTheWorkingTree(t *testing.T) {
	origin, clone, src := fixture(t)
	want := git(t, origin, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(clone, ".wwd", "tasks.toml"), []byte("dirty working tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := src.Refresh(context.Background(), "purdex"); err != nil {
		t.Fatal(err)
	}
	got, ok := src.Get("purdex")
	if !ok || got.Commit != want || string(got.Manifest) != "v1\n" {
		t.Fatalf("Get = %+v, %v; want commit %s and the committed manifest", got, ok, want)
	}
}

func TestNotConfiguredRepoHasNothing(t *testing.T) {
	_, _, src := fixture(t)
	if src.Configured("other") {
		t.Fatal("other is not configured")
	}
	if _, ok := src.Get("other"); ok {
		t.Fatal("Get answered for a repo with no manifest_ref")
	}
	if !src.Configured("purdex") {
		t.Fatal("purdex is configured")
	}
	if _, ok := src.Get("purdex"); ok {
		t.Fatal("nothing was read yet: Get must say so, not answer with an empty manifest")
	}
}

func TestFetchBringsInNewCommitsAndAFailedFetchKeepsTheLastAnswer(t *testing.T) {
	origin, _, src := fixture(t)
	ctx := context.Background()
	if err := src.Refresh(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	second := commitManifest(t, origin, "v2\n")
	if got, _ := src.Get("purdex"); string(got.Manifest) != "v1\n" {
		t.Fatalf("a refresh without a fetch must not see the new commit: %+v", got)
	}
	if err := src.Fetch(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if err := src.Refresh(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := src.Get("purdex"); got.Commit != second || string(got.Manifest) != "v2\n" {
		t.Fatalf("after fetch+refresh: %+v, want %s v2", got, second)
	}
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	if err := src.Fetch(ctx, "purdex"); err == nil {
		t.Fatal("fetching from a remote that is gone must fail")
	}
	if got, ok := src.Get("purdex"); !ok || got.Commit != second {
		t.Fatalf("a failed fetch must keep the last answer: %+v %v", got, ok)
	}
}

// The remote that is fetched is the one the ref lives under (#3673 R1): upstream/main is brought up to date by fetching upstream, and a ref that
// is under no remote (a local branch, a tag) has nothing to fetch.
func TestFetchTakesTheRemoteTheRefLivesUnder(t *testing.T) {
	origin, clone, src := fixture(t)
	upstream := filepath.Join(t.TempDir(), "upstream")
	git(t, filepath.Dir(upstream), "clone", "-q", origin, upstream)
	git(t, clone, "remote", "add", "upstream", upstream)
	git(t, clone, "fetch", "-q", "upstream")
	src.refs["purdex"] = "upstream/main"
	ctx := context.Background()
	if err := src.Refresh(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	second := commitManifest(t, upstream, "from upstream\n")
	commitManifest(t, origin, "from origin\n") // origin moves too: it must not be what upstream/main shows
	if err := src.Fetch(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if err := src.Refresh(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := src.Get("purdex"); got.Commit != second || string(got.Manifest) != "from upstream\n" {
		t.Fatalf("after fetch: %+v, want upstream's %s", got, second)
	}
	src.refs["purdex"] = "refs/remotes/upstream/main" // the long spelling is under the same remote
	third := commitManifest(t, upstream, "again\n")
	if err := src.Fetch(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if err := src.Refresh(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := src.Get("purdex"); got.Commit != third {
		t.Fatalf("long spelling: %+v, want %s", got, third)
	}
	src.refs["purdex"] = "main" // a local branch: nothing to fetch, and not an error
	if err := src.Fetch(ctx, "purdex"); err != nil {
		t.Fatalf("a ref under no remote has nothing to fetch: %v", err)
	}
}

func TestAnUnresolvableRefKeepsTheLastAnswerAndReports(t *testing.T) {
	_, _, src := fixture(t)
	ctx := context.Background()
	if err := src.Refresh(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	last, _ := src.Get("purdex")
	src.refs["purdex"] = "origin/no-such-branch"
	if err := src.Refresh(ctx, "purdex"); err == nil {
		t.Fatal("a ref that does not resolve must be reported")
	}
	if got, ok := src.Get("purdex"); !ok || got.Commit != last.Commit {
		t.Fatalf("the last answer must stay: %+v %v", got, ok)
	}
}

func TestAManifestThatIsASymlinkOrMissingIsRefused(t *testing.T) {
	origin, _, src := fixture(t)
	if err := os.Remove(filepath.Join(origin, ".wwd", "tasks.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(origin, ".wwd", "tasks.toml")); err != nil {
		t.Fatal(err)
	}
	git(t, origin, "add", "-A")
	git(t, origin, "commit", "-q", "-m", "symlink")
	ctx := context.Background()
	if err := src.Fetch(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if err := src.Refresh(ctx, "purdex"); err == nil {
		t.Fatal("a manifest that is a symlink must be refused")
	}
	if _, ok := src.Get("purdex"); ok {
		t.Fatal("a refused manifest must not become the answer")
	}
}

func TestATooLargeManifestIsRefused(t *testing.T) {
	origin, _, src := fixture(t)
	commitManifest(t, origin, strings.Repeat("x", MaxBytes+1))
	ctx := context.Background()
	if err := src.Fetch(ctx, "purdex"); err != nil {
		t.Fatal(err)
	}
	if err := src.Refresh(ctx, "purdex"); err == nil {
		t.Fatal("a manifest over MaxBytes must be refused")
	}
}

func TestRunRefreshesAtOnceAndThenAfterEachFetch(t *testing.T) {
	origin, _, src := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { src.Run(ctx, 20*time.Millisecond); close(done) }()
	waitFor(t, func() bool { g, ok := src.Get("purdex"); return ok && string(g.Manifest) == "v1\n" })
	commitManifest(t, origin, "v2\n")
	waitFor(t, func() bool { g, _ := src.Get("purdex"); return string(g.Manifest) == "v2\n" })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
