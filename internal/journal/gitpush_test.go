package journal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const publicURL = "https://github.com/wake/purdex.git"

// publicOrigin makes the origin look like github.com/wake/purdex to the allow-list while git still reaches the bare
// repository (url.<base>.insteadOf).
func (f *fixture) publicOrigin(t *testing.T) {
	t.Helper()
	tgit(t, f.root, "config", "url."+f.bare+".insteadOf", publicURL)
	tgit(t, f.root, "remote", "set-url", "origin", publicURL)
}

// journalCommit writes a commit of its own on purdex/journal in a fresh clone and pushes it, as another host would.
func (f *fixture) otherHostCommit(t *testing.T, file, text string) string {
	t.Helper()
	clone := filepath.Join(filepath.Dir(f.root), "clone-"+file)
	tgit(t, filepath.Dir(f.root), "clone", "-q", f.bare, clone)
	if tgit(t, clone, "ls-remote", "--heads", "origin", Branch) != "" {
		tgit(t, clone, "switch", "-q", "-c", Branch, "--track", "origin/"+Branch)
	} else {
		tgit(t, clone, "switch", "-q", "--orphan", Branch)
	}
	f.write(t, filepath.Join(clone, ".purdex"), file, text)
	tgit(t, clone, "add", ".purdex")
	tgit(t, clone, "commit", "-q", "-m", "other host")
	tgit(t, clone, "push", "-q", "origin", Branch)
	return tgit(t, clone, "rev-parse", "HEAD")
}

func (f *fixture) remoteHead(t *testing.T) string {
	t.Helper()
	out, _ := gitOut(f.bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+Branch+"^{commit}") // "" when there is none
	return out
}

func (f *fixture) localHead(t *testing.T) string {
	return tgit(t, WorktreeDir(f.g.Dir, f.p), "rev-parse", "HEAD")
}

func (f *fixture) writeJournal(t *testing.T, name, text string) {
	t.Helper()
	if err := f.g.With(context.Background(), f.p, func(d string) error { f.write(t, d, name, text); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestGit_NothingIsPushedUnlessTheProjectIsListed(t *testing.T) {
	f := newFixture(t)
	f.publicOrigin(t)
	ctx := context.Background()
	f.writeJournal(t, "a.md", "one\n")
	f.g.Round(ctx) // the default: an empty list
	if f.remoteHead(t) != "" {
		t.Fatal("pushed with an empty allow-list")
	}
	if got := tgit(t, WorktreeDir(f.g.Dir, f.p), "rev-list", "--count", Branch); got != "1" {
		t.Fatalf("the commit is local anyway: %s commits", got)
	}
	f.pushList = []string{"github.com/wake/somewhere-else"}
	f.g.Round(ctx)
	if f.remoteHead(t) != "" {
		t.Fatal("pushed a project that is not listed")
	}
	f.pushList = []string{"github.com/wake/purdex"}
	f.g.Round(ctx)
	if got := f.remoteHead(t); got == "" || got != f.localHead(t) {
		t.Fatalf("remote %q, local %q after listing the project", got, f.localHead(t))
	}
}

func TestGit_TheListIsReadAtEveryRoundAndAnErrorPushesNothing(t *testing.T) {
	f := newFixture(t)
	f.publicOrigin(t)
	ctx := context.Background()
	f.writeJournal(t, "a.md", "one\n")
	calls := 0
	f.g.PushProjects = func() ([]string, error) {
		calls++
		if calls == 1 {
			return nil, os.ErrPermission // an unreadable config
		}
		return []string{"github.com/wake/purdex"}, nil
	}
	f.g.Round(ctx)
	if f.remoteHead(t) != "" {
		t.Fatal("pushed while the config was unreadable")
	}
	f.g.Round(ctx)
	if calls != 2 || f.remoteHead(t) == "" {
		t.Fatalf("calls = %d, remote = %q: the second round should have read the list again and pushed", calls, f.remoteHead(t))
	}
}

func TestGit_ANewCommitIsPushedAfterAnEarlierPush(t *testing.T) {
	f := newFixture(t)
	f.publicOrigin(t)
	f.pushList = []string{"github.com/wake/purdex"}
	ctx := context.Background()
	f.writeJournal(t, "a.md", "one\n")
	f.g.Round(ctx)
	f.g.Round(ctx) // nothing new
	f.writeJournal(t, "a.md", "two\n")
	f.g.Round(ctx)
	if f.remoteHead(t) != f.localHead(t) || tgit(t, f.bare, "rev-list", "--count", Branch) != "2" {
		t.Fatalf("remote %q local %q", f.remoteHead(t), f.localHead(t))
	}
}

func TestGit_ANonFastForwardIsRebasedAndPushed(t *testing.T) {
	f := newFixture(t)
	f.publicOrigin(t)
	f.pushList = []string{"github.com/wake/purdex"}
	ctx := context.Background()
	f.writeJournal(t, "mine.md", "mine\n")
	f.g.Round(ctx)
	theirs := f.otherHostCommit(t, "theirs.md", "theirs\n") // the remote moved
	f.writeJournal(t, "mine.md", "mine, later\n")
	f.g.Round(ctx)
	wt := WorktreeDir(f.g.Dir, f.p)
	if f.remoteHead(t) != f.localHead(t) {
		t.Fatalf("remote %q != local %q", f.remoteHead(t), f.localHead(t))
	}
	if tgit(t, wt, "merge-base", "--is-ancestor", theirs, "HEAD") != "" || tgit(t, wt, "rev-list", "--merges", "--count", "HEAD") != "0" {
		t.Fatal("their commit is not under ours, or a merge commit appeared")
	}
	for _, name := range []string{"mine.md", "theirs.md"} {
		if _, err := os.Stat(filepath.Join(wt, ".purdex", name)); err != nil {
			t.Errorf("%s missing after the rebase: %v", name, err)
		}
	}
}

// A conflict aborts the rebase, keeps the local commits, never forces, and says so once.
func TestGit_ARebaseConflictAbortsKeepsLocalCommitsAndNeverForces(t *testing.T) {
	f := newFixture(t)
	f.publicOrigin(t)
	f.pushList = []string{"github.com/wake/purdex"}
	ctx := context.Background()
	f.writeJournal(t, "shared.md", "base\n")
	f.g.Round(ctx)
	theirs := f.otherHostCommit(t, "shared.md", "their line\n")
	f.writeJournal(t, "shared.md", "my line\n")
	f.g.Round(ctx)
	f.g.Round(ctx) // retried next round, logged once
	wt := WorktreeDir(f.g.Dir, f.p)
	if f.remoteHead(t) != theirs {
		t.Fatalf("remote = %q, want their commit %q untouched", f.remoteHead(t), theirs)
	}
	if !strings.Contains(tgit(t, wt, "log", "-1", "--format=%s"), "journal") || tgit(t, wt, "branch", "--show-current") != Branch {
		t.Fatal("the local branch is not as the journal left it")
	}
	if _, err := os.Stat(tgit(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge")); err == nil {
		t.Fatal("the worktree is left in the middle of a rebase")
	}
	if b, _ := os.ReadFile(filepath.Join(wt, ".purdex", "shared.md")); string(b) != "my line\n" {
		t.Fatalf("the local file = %q", b)
	}
	if n := countContaining(f.logs, "conflict"); n != 1 {
		t.Fatalf("the conflict was logged %d times in two rounds: %v", n, f.logs)
	}
	f.writeJournal(t, "other.md", "still writable\n") // writing is not blocked
}

func countContaining(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// A remote that cannot be reached costs a log line, never the commit and never the next round.
func TestGit_AnUnreachableRemoteKeepsTheCommitAndIsRetried(t *testing.T) {
	f := newFixture(t)
	f.publicOrigin(t)
	f.pushList = []string{"github.com/wake/purdex"}
	ctx := context.Background()
	f.writeJournal(t, "a.md", "one\n") // the worktree is made while the remote still answers
	nowhere := filepath.Join(filepath.Dir(f.root), "nowhere.git")
	tgit(t, f.root, "config", "--unset-all", "url."+f.bare+".insteadOf")
	tgit(t, f.root, "config", "url."+nowhere+".insteadOf", publicURL)
	f.g.Round(ctx)
	if f.localHead(t) == "" || f.remoteHead(t) != "" {
		t.Fatal("expected a local commit and no remote branch")
	}
	tgit(t, f.root, "config", "--unset-all", "url."+nowhere+".insteadOf")
	tgit(t, f.root, "config", "url."+f.bare+".insteadOf", publicURL) // the remote is back
	f.g.Round(ctx)
	if f.remoteHead(t) != f.localHead(t) {
		t.Fatal("the pending commit was not pushed once the remote came back")
	}
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func TestGit_RunCommitsOnATickAdoptsWorktreesOnDiskAndStopsWithItsContext(t *testing.T) {
	f := newFixture(t)
	f.writeJournal(t, "a.md", "one\n")
	// A restarted daemon: a new Git over the same folder, which has never seen the project.
	g2 := &Git{Dir: f.g.Dir, Interval: 20 * time.Millisecond, Logf: f.g.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g2.Run(ctx); close(done) }()
	wt := WorktreeDir(f.g.Dir, f.p)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if out, err := gitOut(wt, "rev-list", "--count", Branch); err == nil && out == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the loop did not commit the adopted worktree")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}
