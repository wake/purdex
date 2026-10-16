package journal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const entryLine = "<!-- j {\"id\":\"%s\"} -->\n"

func TestGit_ACommitLandsOnTheJournalBranchWithTheJournalIdentityAndLeavesMainAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.mainState(t)
	if err := f.g.With(ctx, f.p, func(d string) error {
		f.write(t, d, "a.md", "## 2026-10-14\n"+strings.Replace(entryLine, "%s", "e1", 1)+strings.Replace(entryLine, "%s", "e2", 1))
		f.write(t, d, "roadmap.md", "# r\n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatal(err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	if got := tgit(t, wt, "log", "-1", "--format=%s|%an <%ae>|%cn <%ce>"); got != "journal: 2 entries|Purdex journal <journal@purdex.local>|Purdex journal <journal@purdex.local>" {
		t.Fatalf("commit = %q", got)
	}
	if got := tgit(t, wt, "show", "--name-only", "--format=", "HEAD"); got != ".purdex/a.md\n.purdex/roadmap.md" {
		t.Fatalf("files = %q", got)
	}
	if got := f.mainState(t); got != before {
		t.Fatalf("the main checkout changed:\n%s\n--- was ---\n%s", got, before)
	}
	// A second round with no change commits nothing.
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatal(err)
	}
	if got := tgit(t, wt, "rev-list", "--count", Branch); got != "1" {
		t.Fatalf("%s commits after an idle round, want 1", got)
	}
	// A change that adds no entry line is "update".
	if err := f.g.With(ctx, f.p, func(d string) error { f.write(t, d, "roadmap.md", "# r2\n"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatal(err)
	}
	if got := tgit(t, wt, "log", "-1", "--format=%s"); got != "journal: update" {
		t.Fatalf("subject = %q", got)
	}
}

func TestGit_OnlyTheJournalFolderIsCommittedAndTheProjectsHooksDoNotRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hook := filepath.Join(f.root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.g.With(ctx, f.p, func(d string) error {
		f.write(t, d, "a.md", "x\n")
		f.write(t, filepath.Dir(d), "stray.txt", "not the journal\n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatalf("commit with a failing project hook: %v", err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	if got := tgit(t, wt, "show", "--name-only", "--format=", "HEAD"); got != ".purdex/a.md" {
		t.Fatalf("files = %q", got)
	}
}

func TestGit_ASecondCheckoutOfTheBranchIsRefusedByGit(t *testing.T) {
	f := newFixture(t)
	if err := f.g.With(context.Background(), f.p, func(d string) error { f.write(t, d, "a.md", "a\n"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.g.commitProject(context.Background(), f.p); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(f.root), "other")
	out, err := exec.Command("git", "-C", f.root, "worktree", "add", other, "purdex/journal").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "already") {
		t.Fatalf("a second checkout of the journal branch was allowed: %v %s", err, out)
	}
}

func TestGit_AWorktreeWhoseFolderIsGoneIsPrunedAndReattached(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.g.With(ctx, f.p, func(d string) error { f.write(t, d, "a.md", "kept\n"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(WorktreeDir(f.g.Dir, f.p)); err != nil {
		t.Fatal(err)
	}
	if err := f.g.With(ctx, f.p, func(d string) error {
		if b, err := os.ReadFile(filepath.Join(d, "a.md")); err != nil || string(b) != "kept\n" {
			t.Errorf("a.md = %q, %v", b, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
