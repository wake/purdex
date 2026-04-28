package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// mustWrite creates a file (and any missing parent directories) with the given
// string content. Used by search_engine tests and search_handler tests.
func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rawRoot is a small helper to build a SearchRoot pointing at an absolute path
// for unit tests; production code never produces roots this way (capabilities
// only).
func rawRoot(abs string) SearchRoot {
	return SearchRoot{Kind: rawAbsoluteTestOnly, Absolute: abs}
}

func TestSearchEngine_BasenameMatchInRoots(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "foo.go"), "ok")
	mustWrite(t, filepath.Join(dir, "b", "bar.go"), "no")
	req := SearchRequest{
		Mode:   "basename",
		Query:  SearchQuery{Basename: "foo.go"},
		Roots:  []SearchRoot{rawRoot(dir)},
		Limits: SearchLimits{MaxResults: 10},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d (%+v)", len(resp.Matches), resp.Matches)
	}
}

func TestSearchEngine_ExcludeDirsPrunesSubtree(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "x", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	req := SearchRequest{
		Mode:    "basename",
		Query:   SearchQuery{Basename: "foo.go"},
		Roots:   []SearchRoot{rawRoot(dir)},
		Filters: SearchFilters{ExcludeDirs: []string{"src"}}, // user adds src; mandatory still apply
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// node_modules pruned (mandatory) + src pruned (user) → 0
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches, got %+v", resp.Matches)
	}
}

func TestSearchEngine_MandatoryExcludesAlwaysApply(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "node_modules", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	// 攻擊 review #10：client 傳 [] 不能關掉 mandatory excludes
	req := SearchRequest{
		Mode:    "basename",
		Query:   SearchQuery{Basename: "foo.go"},
		Roots:   []SearchRoot{rawRoot(dir)},
		Filters: SearchFilters{ExcludeDirs: []string{}},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match (node_modules pruned by mandatory), got %+v", resp.Matches)
	}
}

func TestSearchEngine_MandatoryBasenameGlobsAlwaysApply(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pnpm-lock.lock"), "")
	mustWrite(t, filepath.Join(dir, "kept.lock"), "") // both should be skipped
	mustWrite(t, filepath.Join(dir, "ok.txt"), "")
	req := SearchRequest{
		Mode:  "basename",
		Query: SearchQuery{Basename: "kept.lock"},
		Roots: []SearchRoot{rawRoot(dir)},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches (*.lock blocked by mandatory glob), got %+v", resp.Matches)
	}
}

func TestSearchEngine_RespectGitignoreDefaultTrue(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "build/\n")
	mustWrite(t, filepath.Join(dir, "build", "foo.go"), "skip")
	mustWrite(t, filepath.Join(dir, "src", "foo.go"), "ok")
	// 攻擊 review #10：respectGitignore omitted → default true
	req := SearchRequest{
		Mode:    "basename",
		Query:   SearchQuery{Basename: "foo.go"},
		Roots:   []SearchRoot{rawRoot(dir)},
		Filters: SearchFilters{}, // RespectGitignore is *bool nil → treated as true
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match (build pruned by gitignore), got %+v", resp.Matches)
	}
}

func TestSearchEngine_GitignoreCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	mustWrite(t, filepath.Join(dir, "ignored.go"), "now we look")
	off := false
	req := SearchRequest{
		Mode:    "basename",
		Query:   SearchQuery{Basename: "ignored.go"},
		Roots:   []SearchRoot{rawRoot(dir)},
		Filters: SearchFilters{RespectGitignore: &off},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("ignored.go should be returned when respectGitignore=false: %+v", resp.Matches)
	}
}

func TestSearchEngine_GitignoreFiltersBasenameMatch(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	mustWrite(t, filepath.Join(dir, "ignored.go"), "skip")
	mustWrite(t, filepath.Join(dir, "kept.go"), "ok")
	req := SearchRequest{
		Mode:  "basename",
		Query: SearchQuery{Basename: "ignored.go"},
		Roots: []SearchRoot{rawRoot(dir)},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("ignored.go should not appear when respectGitignore default true: %+v", resp.Matches)
	}
}

func TestSearchEngine_GitignoreParseFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Unbalanced character class — go-gitignore silently swallows the bad
	// regex; our preflight catches it as ErrGitignoreParse.
	mustWrite(t, filepath.Join(dir, ".gitignore"), "[invalid-bracket\n")
	req := SearchRequest{
		Mode:  "basename",
		Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{rawRoot(dir)},
	}
	_, err := Search(context.Background(), req)
	// 攻擊 review #11：parse failure 不 fail-open — engine returns ErrGitignoreParse;
	// handler maps to 4xx in 5.1b.
	if err == nil {
		t.Fatal("expected gitignore parse error to bubble (no fail-open)")
	}
	if !errors.Is(err, ErrGitignoreParse) {
		t.Errorf("expected ErrGitignoreParse, got %v", err)
	}
}

func TestSearchEngine_TimeoutReturnsPartial(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		mustWrite(t, filepath.Join(dir, fmt.Sprintf("d%03d", i), "foo.go"), "x")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
	defer cancel()
	// Sleep so the deadline definitely passes before walking starts.
	time.Sleep(2 * time.Millisecond)
	req := SearchRequest{
		Mode:  "basename",
		Query: SearchQuery{Basename: "foo.go"},
		Roots: []SearchRoot{rawRoot(dir)},
	}
	resp, err := Search(ctx, req)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected err: %v", err)
	}
	if !resp.Partial {
		t.Errorf("expected partial=true on timeout, matches=%d", len(resp.Matches))
	}
}

func TestSearchEngine_MaxResultsCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		mustWrite(t, filepath.Join(dir, fmt.Sprintf("d%02d", i), "foo.go"), "x")
	}
	req := SearchRequest{
		Mode:   "basename",
		Query:  SearchQuery{Basename: "foo.go"},
		Roots:  []SearchRoot{rawRoot(dir)},
		Limits: SearchLimits{MaxResults: 5},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 5 {
		t.Errorf("expected 5 matches (capped), got %d", len(resp.Matches))
	}
	if !resp.Partial {
		t.Errorf("expected partial=true when result cap hit")
	}
}

func TestSearchEngine_MaxDepthBoundary(t *testing.T) {
	dir := t.TempDir()
	// foo.go directly under root → depth 0; under a/ → depth 1; under a/b/ → 2.
	mustWrite(t, filepath.Join(dir, "foo.go"), "0")
	mustWrite(t, filepath.Join(dir, "a", "foo.go"), "1")
	mustWrite(t, filepath.Join(dir, "a", "b", "foo.go"), "2")
	mustWrite(t, filepath.Join(dir, "a", "b", "c", "foo.go"), "3")
	req := SearchRequest{
		Mode:   "basename",
		Query:  SearchQuery{Basename: "foo.go"},
		Roots:  []SearchRoot{rawRoot(dir)},
		Limits: SearchLimits{MaxResults: 100, MaxDepth: 2},
	}
	resp, err := Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Files at depth 0, 1, 2 should be included; depth 3 excluded.
	if len(resp.Matches) != 3 {
		t.Errorf("expected 3 matches with maxDepth=2, got %d (%+v)", len(resp.Matches), resp.Matches)
	}
}

func TestSearchEngine_RejectsUnknownMode(t *testing.T) {
	_, err := Search(context.Background(), SearchRequest{
		Mode:  "fuzzy",
		Query: SearchQuery{Basename: "x"},
		Roots: []SearchRoot{rawRoot(t.TempDir())},
	})
	if err == nil {
		t.Fatal("expected error for unsupported mode")
	}
}

func TestSearchEngine_RequiresBasenameQuery(t *testing.T) {
	_, err := Search(context.Background(), SearchRequest{
		Mode:  "basename",
		Roots: []SearchRoot{rawRoot(t.TempDir())},
	})
	if err == nil {
		t.Fatal("expected error when basename empty")
	}
}

func TestSearchEngine_SymlinkLoopDoesNotInfiniteWalk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	mustWrite(t, filepath.Join(a, "x.go"), "ok")
	// loop inside a/ that points back to a/. WalkDir does not follow symlinks
	// so this should not cause infinite descent.
	if err := os.Symlink(a, filepath.Join(a, "loop")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		req := SearchRequest{
			Mode:   "basename",
			Query:  SearchQuery{Basename: "x.go"},
			Roots:  []SearchRoot{rawRoot(dir)},
			Limits: SearchLimits{MaxDepth: 6},
		}
		_, _ = Search(context.Background(), req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("symlink loop caused hang")
	}
}
