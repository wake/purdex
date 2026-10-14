package journal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// isolateGit keeps the user's git configuration (hooks, signing, identity) out of a test.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+k+"_NAME", "Test")
		t.Setenv("GIT_"+k+"_EMAIL", "test@example.com")
	}
}

// tgit runs git in dir and fails the test on error.
func tgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// fixture is a project (a repository with one commit on main and a bare origin) and the Git that serves it.
type fixture struct {
	root, bare string
	p          Project
	g          *Git
	pushList   []string
	logs       []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	isolateGit(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: filepath.Join(base, "repo"), bare: filepath.Join(base, "origin.git")}
	for _, d := range []string{f.root, f.bare} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tgit(t, f.bare, "init", "-q", "--bare", "-b", "main")
	tgit(t, f.root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(f.root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgit(t, f.root, "add", "README.md")
	tgit(t, f.root, "commit", "-q", "-m", "init")
	tgit(t, f.root, "remote", "add", "origin", f.bare)
	tgit(t, f.root, "push", "-q", "origin", "main")
	cacheMu.Lock()
	cache = map[string]cached{}
	cacheMu.Unlock()
	p, err := ProjectOf(context.Background(), f.root)
	if err != nil || p.CommonDir == "" {
		t.Fatalf("ProjectOf = %+v, %v", p, err)
	}
	f.p = p
	f.g = &Git{
		Dir:          filepath.Join(base, "journal"),
		PushProjects: func() ([]string, error) { return f.pushList, nil },
		Logf:         func(format string, a ...any) { f.logs = append(f.logs, fmt.Sprintf(format, a...)) },
	}
	return f
}

func (f *fixture) write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mainState is everything about the main checkout a journal must never change.
func (f *fixture) mainState(t *testing.T) string {
	t.Helper()
	return strings.Join([]string{
		tgit(t, f.root, "rev-parse", "HEAD"), tgit(t, f.root, "branch", "--show-current"),
		tgit(t, f.root, "status", "--porcelain=v1"), tgit(t, f.root, "ls-files", "-s"),
		tgit(t, f.root, "rev-parse", "main"),
	}, "\n--\n")
}

func TestGit_FirstUseCreatesAnOrphanWorktreeAndLeavesTheMainCheckoutAlone(t *testing.T) {
	f := newFixture(t)
	before := f.mainState(t)
	var dir string
	if err := f.g.With(context.Background(), f.p, func(d string) error {
		dir = d
		f.write(t, d, "roadmap.md", "# r\n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	if dir != filepath.Join(wt, ".purdex") {
		t.Fatalf("callback dir = %s, want %s/.purdex", dir, wt)
	}
	if got := tgit(t, wt, "branch", "--show-current"); got != "purdex/journal" {
		t.Fatalf("the worktree is on %q", got)
	}
	if got := tgit(t, wt, "rev-list", "--all", "--count"); got != "1" { // the main branch's one commit; the journal has none yet
		t.Fatalf("rev-list = %s", got)
	}
	if got := f.mainState(t); got != before {
		t.Fatalf("the main checkout changed:\n%s\n--- was ---\n%s", got, before)
	}
	if !strings.Contains(tgit(t, f.root, "worktree", "list"), "journal") {
		t.Fatal("the worktree is not registered with the repository")
	}
	if _, err := os.Stat(filepath.Join(f.root, ".purdex")); err == nil {
		t.Fatal("the journal files appeared in the main checkout")
	}
}

func TestGit_FirstUseAdoptsTheRemoteBranchWithItsFiles(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(filepath.Dir(f.root), "clone")
	tgit(t, filepath.Dir(f.root), "clone", "-q", f.bare, other)
	tgit(t, other, "switch", "-q", "--orphan", "purdex/journal")
	f.write(t, filepath.Join(other, ".purdex"), "roadmap.md", "# remote\n")
	tgit(t, other, "add", ".purdex")
	tgit(t, other, "commit", "-q", "-m", "remote journal")
	tgit(t, other, "push", "-q", "origin", "purdex/journal")

	if err := f.g.With(context.Background(), f.p, func(d string) error {
		b, err := os.ReadFile(filepath.Join(d, "roadmap.md"))
		if err != nil || string(b) != "# remote\n" {
			t.Errorf("roadmap.md = %q, %v", b, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	if got := tgit(t, wt, "rev-parse", "--abbrev-ref", "@{u}"); got != "origin/purdex/journal" {
		t.Fatalf("upstream = %q", got)
	}
}

func TestGit_AnUnreachableOriginCreatesNothingButNoOriginIsLocalOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tgit(t, f.root, "remote", "set-url", "origin", filepath.Join(filepath.Dir(f.root), "nowhere.git"))
	if err := f.g.With(ctx, f.p, func(string) error { return nil }); err == nil {
		t.Fatal("With succeeded with an unreachable origin")
	}
	if _, err := os.Stat(WorktreeDir(f.g.Dir, f.p)); err == nil {
		t.Fatal("a worktree was created although the remote branch could not be looked up")
	}
	tgit(t, f.root, "remote", "remove", "origin")
	if err := f.g.With(ctx, f.p, func(string) error { return nil }); err != nil {
		t.Fatalf("a repository without origin: %v", err)
	}
}

func TestGit_ProjectsWithoutGitAndGitsThatAreTooOldKeepFilesOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	plain := Project{ID: "plain", Root: t.TempDir()}
	if err := f.g.With(ctx, plain, func(d string) error { f.write(t, d, "a.md", "x\n"); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(WorktreeDir(f.g.Dir, plain), ".git")); err == nil {
		t.Fatal("a project without git got a git worktree")
	}
	f.g.version = func(context.Context) (int, int, error) { return 2, 41, nil }
	if err := f.g.With(ctx, f.p, func(d string) error { f.write(t, d, "a.md", "x\n"); return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(WorktreeDir(f.g.Dir, f.p), ".git")); err == nil {
		t.Fatal("git 2.41 got a worktree")
	}
	_ = f.g.With(ctx, f.p, func(string) error { return nil }) // asked again: still one line
	if n := len(f.logs); n != 1 {
		t.Fatalf("logged %d lines about the old git, want 1: %v", n, f.logs)
	}
}

func TestGit_WithRunsOneProjectAtATime(t *testing.T) {
	f := newFixture(t)
	if err := f.g.With(context.Background(), f.p, func(string) error { return nil }); err != nil { // the worktree exists
		t.Fatal(err)
	}
	var in, overlap atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = f.g.With(context.Background(), f.p, func(string) error {
				if in.Add(1) > 1 {
					overlap.Add(1)
				}
				time.Sleep(20 * time.Millisecond)
				in.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 {
		t.Fatal("two callbacks of one project ran at once")
	}
}

// A folder the files-only mode wrote (git was too old or unreadable) becomes the worktree's journal once git works again,
// instead of blocking the project for good (codex R1).
func TestGit_AFilesOnlyFolderIsMovedIntoTheWorktreeWhenGitWorksAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.g.version = func(context.Context) (int, int, error) { return 2, 41, nil }
	f.writeJournal(t, "a.md", "written without git\n")
	f.g.version = nil
	var seen string
	if err := f.g.With(ctx, f.p, func(d string) error {
		b, err := os.ReadFile(filepath.Join(d, "a.md"))
		seen = string(b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	if seen != "written without git\n" {
		t.Fatalf("a.md = %q", seen)
	}
	if _, err := os.Stat(wt + ".plain"); err == nil {
		t.Fatal("the moved-aside folder is still there")
	}
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatal(err)
	}
	if got := tgit(t, wt, "show", "--name-only", "--format=", "HEAD"); got != ".purdex/a.md" {
		t.Fatalf("committed files = %q", got)
	}
}

// Where the remote branch already has a file of that name, the remote's wins and the local copy is kept beside it.
func TestGit_AMovedFilesOnlyFolderNeverOverwritesTheRemoteBranchsFiles(t *testing.T) {
	f := newFixture(t)
	f.otherHostCommit(t, "a.md", "remote\n")
	f.g.version = func(context.Context) (int, int, error) { return 2, 41, nil }
	f.writeJournal(t, "a.md", "local\n")
	f.writeJournal(t, "b.md", "only local\n")
	f.g.version = nil
	if err := f.g.With(context.Background(), f.p, func(d string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(WorktreeDir(f.g.Dir, f.p), ".purdex")
	for name, want := range map[string]string{"a.md": "remote\n", "a.md.plain": "local\n", "b.md": "only local\n"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
}

// If the worktree cannot be made, the files-only folder is put back exactly as it was.
func TestGit_AFailedMigrationPutsTheFilesOnlyFolderBack(t *testing.T) {
	f := newFixture(t)
	f.g.version = func(context.Context) (int, int, error) { return 2, 41, nil }
	f.writeJournal(t, "a.md", "keep me\n")
	f.g.version = nil
	tgit(t, f.root, "remote", "set-url", "origin", filepath.Join(filepath.Dir(f.root), "nowhere.git"))
	if err := f.g.With(context.Background(), f.p, func(string) error { return nil }); err == nil {
		t.Fatal("With succeeded with an unreachable origin")
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	if b, err := os.ReadFile(filepath.Join(wt, ".purdex", "a.md")); err != nil || string(b) != "keep me\n" {
		t.Fatalf("a.md = %q, %v", b, err)
	}
	if _, err := os.Stat(wt + ".plain"); err == nil {
		t.Fatal("a moved-aside copy is left behind")
	}
}

// The local copy never takes the name of a file the worktree already has, however many such names there are.
func TestGit_AMovedFilesOnlyFolderFindsANameThatIsFree(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(filepath.Dir(f.root), "clone")
	tgit(t, filepath.Dir(f.root), "clone", "-q", f.bare, other)
	tgit(t, other, "switch", "-q", "--orphan", Branch)
	for _, n := range []string{"a.md", "a.md.plain", "a.md.plain.1"} {
		f.write(t, filepath.Join(other, ".purdex"), n, "remote "+n+"\n")
	}
	tgit(t, other, "add", ".purdex")
	tgit(t, other, "commit", "-q", "-m", "remote")
	tgit(t, other, "push", "-q", "origin", Branch)
	f.g.version = func(context.Context) (int, int, error) { return 2, 41, nil }
	f.writeJournal(t, "a.md", "local\n")
	f.g.version = nil
	if err := f.g.With(context.Background(), f.p, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(WorktreeDir(f.g.Dir, f.p), ".purdex")
	for name, want := range map[string]string{"a.md": "remote a.md\n", "a.md.plain": "remote a.md.plain\n",
		"a.md.plain.1": "remote a.md.plain.1\n", "a.md.plain.2": "local\n"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
}

// A move that stops half way is finished by the next call, not abandoned beside the worktree.
func TestGit_AMigrationThatStoppedHalfWayIsFinishedByTheNextCall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.g.version = func(context.Context) (int, int, error) { return 2, 41, nil }
	f.writeJournal(t, "a.md", "one\n")
	f.writeJournal(t, "b.md", "two\n")
	f.g.version = nil
	calls := 0
	f.g.rename = func(o, n string) error {
		calls++
		if calls == 2 { // the first file moves, the second does not
			return os.ErrPermission
		}
		return os.Rename(o, n)
	}
	if err := f.g.With(ctx, f.p, func(string) error { return nil }); err == nil {
		t.Fatal("With reported success although the move stopped")
	}
	f.g.rename = nil
	if err := f.g.With(ctx, f.p, func(string) error { return nil }); err != nil {
		t.Fatalf("the next call: %v", err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	for name, want := range map[string]string{"a.md": "one\n", "b.md": "two\n"} {
		if b, err := os.ReadFile(filepath.Join(wt, ".purdex", name)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", name, b, err)
		}
	}
	if _, err := os.Stat(wt + ".plain"); err == nil {
		t.Fatal("the moved-aside folder is still there")
	}
}

// GIT_DIR, GIT_WORK_TREE and GIT_INDEX_FILE inherited from the daemon's launch would send every journal command to
// another repository (codex R2).
func TestGit_RepositoryRedirectingEnvironmentFromTheDaemonIsIgnored(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := filepath.Join(filepath.Dir(f.root), "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, other, "init", "-q", "-b", "main")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index-elsewhere"))
	if err := f.g.With(ctx, f.p, func(d string) error { f.write(t, d, "a.md", "x\n"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.g.commitProject(ctx, f.p); err != nil {
		t.Fatal(err)
	}
	wt := WorktreeDir(f.g.Dir, f.p)
	clean := func(args ...string) string { // the test process itself carries the redirecting variables
		cmd := exec.Command("git", args...)
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") && !strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	if out := clean("-C", wt, "show", "--name-only", "--format=", "HEAD"); out != ".purdex/a.md" {
		t.Fatalf("the journal commit: %q", out)
	}
	if _, err := os.Stat(filepath.Join(other, ".git", "index-elsewhere")); err == nil {
		t.Fatal("the other repository's index was used")
	}
	if out := clean("--git-dir="+filepath.Join(other, ".git"), "rev-list", "--all", "--count"); out != "0" {
		t.Fatalf("the other repository got commits: %s", out)
	}
}

func TestGitEnv_DropsWhatRedirectsGitAndLimitsTheTransports(t *testing.T) {
	got := gitEnv([]string{"GIT_DIR=/x", "GIT_WORK_TREE=/x", "GIT_INDEX_FILE=/x", "GIT_EXEC_PATH=/x", "GIT_ALLOW_PROTOCOL=ext", "GIT_SSH_COMMAND=my-ssh", "GIT_CONFIG_GLOBAL=/g", "HOME=/h"})
	has := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		has[k] = v
	}
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_EXEC_PATH"} {
		if _, ok := has[k]; ok {
			t.Errorf("%s survived", k)
		}
	}
	if has["GIT_ALLOW_PROTOCOL"] != "file:https:http:ssh:git" {
		t.Errorf("GIT_ALLOW_PROTOCOL = %q: the daemon's own value must not widen it", has["GIT_ALLOW_PROTOCOL"])
	}
	if has["GIT_SSH_COMMAND"] != "my-ssh" || has["GIT_CONFIG_GLOBAL"] != "/g" || has["HOME"] != "/h" {
		t.Errorf("the operator's own settings were lost: %v", has)
	}
}

// Replacing the worktree's folder with another repository on a branch of the same name must not get journal commits.
func TestGit_AWorktreeReplacedByAnotherRepositoryIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.writeJournal(t, "a.md", "one\n")
	f.g.Round(ctx)
	wt := WorktreeDir(f.g.Dir, f.p)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, wt, "init", "-q", "-b", Branch)
	f.write(t, filepath.Join(wt, ".purdex"), "a.md", "an impostor\n")
	f.g.Round(ctx)
	if out, _ := gitOut(wt, "rev-list", "--all", "--count"); out != "0" {
		t.Fatalf("the other repository got %s commit(s)", out)
	}
	if countContaining(f.logs, "belongs to repository") == 0 {
		t.Fatalf("nothing logged about the replaced worktree: %v", f.logs)
	}
}

func TestGit_LogOnceForgetsKeysWhoseHourIsOver(t *testing.T) {
	g := &Git{Logf: func(string, ...any) {}}
	for i := 0; i < 500; i++ {
		g.logOnce(fmt.Sprintf("old/%d", i), "x")
	}
	g.mu.Lock()
	for k := range g.logged {
		g.logged[k] = time.Now().Add(-2 * time.Hour)
	}
	g.mu.Unlock()
	g.logOnce("fresh", "x")
	if n := len(g.logged); n > 5 {
		t.Fatalf("%d keys are kept after their hour was over", n)
	}
}

// A worktree replaced before the daemon started is not adopted: the folder is named for the repository it must belong to.
func TestGit_AWorktreeReplacedBeforeARestartIsNotAdopted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.writeJournal(t, "a.md", "one\n")
	wt := WorktreeDir(f.g.Dir, f.p)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, wt, "init", "-q", "-b", Branch)
	f.write(t, filepath.Join(wt, ".purdex"), "a.md", "an impostor\n")
	g2 := &Git{Dir: f.g.Dir, Logf: func(format string, a ...any) { f.logs = append(f.logs, fmt.Sprintf(format, a...)) }}
	g2.adopt()
	g2.Round(ctx)
	if out, _ := gitOut(wt, "rev-list", "--all", "--count"); out != "0" {
		t.Fatalf("the impostor got %s commit(s)", out)
	}
	if countContaining(f.logs, "is not the worktree of project") == 0 {
		t.Fatalf("the refusal was not logged: %v", f.logs)
	}
	// The real worktree of the same project is adopted.
	f2 := newFixture(t)
	f2.writeJournal(t, "a.md", "one\n")
	g3 := &Git{Dir: f2.g.Dir, Logf: func(string, ...any) {}}
	g3.adopt()
	if !g3.known[f2.p.ID] {
		t.Fatal("the genuine worktree was not adopted")
	}
}
