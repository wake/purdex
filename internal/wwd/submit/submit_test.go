package submit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

const testManifest = `
[tasks.spa-file]
run = "pnpm vitest run {files}"
cwd = "spa"
args = "files"
local = true

[tasks.go-changed]
run = "go test -json {packages}"
args = "packages"
class = "merge"
labels = ["os:darwin"]
locks = ["db"]
cacheable = true
interruptible = true
timeout = "10m"
queue_timeout = "1h"
summary = "go-json"

[tasks.lint]
run = "make lint"
args = "none"
class = "merge"
`

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, ".wwd/tasks.toml"), testManifest)
	write(t, filepath.Join(dir, "spa/src/a.test.ts"), "x\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	gitT(t, dir, "remote", "add", "origin", "git@example.com:org/widget.git")
	return dir
}

func TestPrepareFillsTheSubmission(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, "spa/src/new.test.ts"), "untracked\n") // the snapshot has files that are not committed
	p, err := Prepare(context.Background(), Request{
		Dir: dir, Task: "go-changed", Args: []string{"./internal/a"},
		Submitter: wire.Submitter{Session: "s", Ref: "_abc123", Host: "mlab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := p.Body
	if b.Task != "go-changed" || b.Class != wire.ClassMerge || !b.Cacheable || !b.Interruptible || b.TimeoutS != 600 || b.QueueTimeoutS != 3600 {
		t.Fatalf("manifest values not copied: %+v", b)
	}
	if len(b.Labels) != 1 || b.Labels[0] != "os:darwin" || len(b.Locks) != 1 || b.Locks[0] != "db" {
		t.Fatalf("labels/locks = %v %v", b.Labels, b.Locks)
	}
	if b.Repo.Name != "widget" || b.Repo.Remote != "git@example.com:org/widget.git" || len(b.Repo.Root) != 40 {
		t.Fatalf("repo = %+v", b.Repo)
	}
	if b.Snapshot.Commit == "" || b.Snapshot.Tree == "" || b.Snapshot.Base != gitT(t, dir, "rev-parse", "HEAD") || b.Snapshot.RelCwd != "." {
		t.Fatalf("snapshot = %+v", b.Snapshot)
	}
	if b.GitDir != filepath.Join(dir, ".git") {
		t.Fatalf("git_dir = %q", b.GitDir)
	}
	if !strings.HasSuffix(b.SupersedeKey, ":go-changed") || b.Submitter.Ref != "_abc123" {
		t.Fatalf("key/submitter = %q %+v", b.SupersedeKey, b.Submitter)
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(b.DefHash) != 64 || b.DefHash != p.Task.DefinitionHash() || b.NoCache {
		t.Fatalf("def_hash = %q no_cache = %v", b.DefHash, b.NoCache)
	}
	again, err := Prepare(context.Background(), Request{Dir: dir, Task: "go-changed", Args: []string{"./internal/a"}, NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Body.NoCache || again.Body.DefHash != b.DefHash {
		t.Fatalf("--no-cache: %+v", again.Body)
	}
}

func TestPrepareGitDirOfAWorktreeIsTheCommonDirectory(t *testing.T) {
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	p, err := Prepare(context.Background(), Request{Dir: wt, Task: "lint"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.GitDir != filepath.Join(dir, ".git") {
		t.Fatalf("git_dir = %q, want the main repo's .git", p.Body.GitDir)
	}
	if p.Body.Repo.Name != "widget" {
		t.Fatalf("repo name = %q", p.Body.Repo.Name)
	}
}

func TestPrepareIgnoresInheritedGitEnvironment(t *testing.T) {
	dir := newRepo(t)
	other := newRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "lint"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.GitDir != filepath.Join(dir, ".git") || p.Root != dir {
		t.Fatalf("followed the inherited repository: %q %q", p.Body.GitDir, p.Root)
	}
}

func TestPrepareUsageErrors(t *testing.T) {
	dir := newRepo(t)
	cases := map[string]Request{
		"unknown task":     {Dir: dir, Task: "nope"},
		"missing files":    {Dir: dir, Task: "spa-file"},
		"file not in tree": {Dir: dir, Task: "spa-file", Args: []string{"src/missing.test.ts"}},
		"dotdot file":      {Dir: dir, Task: "spa-file", Args: []string{"../x"}},
		"args for none":    {Dir: dir, Task: "lint", Args: []string{"x"}},
		"bad package":      {Dir: dir, Task: "go-changed", Args: []string{"-run=x"}},
		"class raised":     {Dir: dir, Task: "lint", Class: wire.ClassRelease},
		"bad class":        {Dir: dir, Task: "lint", Class: "urgent"},
		"not a repo":       {Dir: t.TempDir(), Task: "lint"},
	}
	for name, req := range cases {
		_, err := Prepare(context.Background(), req)
		if err == nil || !IsUsage(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}

func TestPrepareRefusesAShallowClone(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, "more.txt"), "m\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "second")
	clone := filepath.Join(t.TempDir(), "shallow")
	gitT(t, t.TempDir(), "clone", "-q", "--depth", "1", "file://"+dir, clone)
	if _, err := Prepare(context.Background(), Request{Dir: clone, Task: "lint"}); err == nil || !IsUsage(err) {
		t.Fatalf("err = %v, want a usage error for a shallow clone", err)
	}
}

func TestPrepareUsesTheManifestOfTheSnapshot(t *testing.T) {
	dir := newRepo(t)
	// an uncommitted edit is in the snapshot, so its values (not the committed ones) are submitted
	write(t, filepath.Join(dir, ".wwd/tasks.toml"), strings.Replace(testManifest, `timeout = "10m"`, `timeout = "45m"`, 1))
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "go-changed", Args: []string{"./a"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.TimeoutS != 45*60 {
		t.Fatalf("timeout_s = %d, want the snapshot's 2700", p.Body.TimeoutS)
	}
}

func TestPrepareRefusesALocalOnlyTask(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, ".wwd/tasks.toml"), testManifest+"\n[tasks.vet]\nrun = \"go vet\"\nargs = \"pass\"\nlocal = true\n")
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "vet", Args: []string{"./a"}}); err == nil || !IsUsage(err) {
		t.Fatalf("err = %v, want a usage error for a pass task", err)
	}
}

func TestPrepareRefusesWhenTheDirectoryIsSwappedBeforeTheBaseQuery(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	beforeGit = func(args []string) {
		for _, a2 := range args {
			if a2 == "merge-base" {
				os.Remove(link)
				os.Symlink(b, link)
				return
			}
		}
	}
	t.Cleanup(func() { beforeGit = nil })
	if _, err := Prepare(context.Background(), Request{Dir: link, Task: "lint"}); err == nil {
		t.Fatal("a directory swapped before the base query was accepted")
	}
}

func TestPrepareLocalOnlyAnUnbornBranchIsNoCommit(t *testing.T) {
	// a branch without a commit is ErrNoCommit
	unborn, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, unborn, "init", "-q", "-b", "main")
	write(t, filepath.Join(unborn, ".wwd/tasks.toml"), testManifest)
	_, err = Prepare(context.Background(), Request{Dir: unborn, Task: "spa-file", Args: []string{"src/a.test.ts"}, Local: true})
	if !errors.Is(err, ErrNoCommit) || !IsUsage(err) {
		t.Fatalf("unborn: %v", err)
	}
	// a repository with commits whose HEAD git cannot resolve (damaged objects, say) is not "no commit"
	dir := newRepo(t)
	fake := filepath.Join(t.TempDir(), "git")
	write(t, fake, "#!/bin/sh\ncase \"$*\" in *'HEAD^{commit}'*) echo 'fatal: bad object' >&2; exit 128;; esac\nexec git \"$@\"\n")
	if err := os.Chmod(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/a.test.ts"}, Local: true, Git: fake})
	if err == nil || errors.Is(err, ErrNoCommit) || IsUsage(err) {
		t.Fatalf("a HEAD that cannot be resolved: %v", err)
	}
}

func TestPrepareLocalNamesHeadAndRecordsTheWorkingTreeOfADirtyDirectory(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, "spa/src/new.test.ts"), "untracked\n")
	before := gitT(t, dir, "count-objects", "-v")
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/new.test.ts"}, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	head := gitT(t, dir, "rev-parse", "HEAD")
	headTree := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	if p.Body.Snapshot.Commit != head {
		t.Fatalf("snapshot commit = %q, want HEAD %s", p.Body.Snapshot.Commit, head)
	}
	if p.Body.Snapshot.Tree == "" || p.Body.Snapshot.Tree == headTree {
		t.Fatalf("snapshot tree = %q: the directory has an untracked file, so it is not HEAD's tree %s (#3735)", p.Body.Snapshot.Tree, headTree)
	}
	if p.Note != "" {
		t.Fatalf("note = %q", p.Note)
	}
	if gitT(t, dir, "count-objects", "-v") != before {
		t.Fatal("a local preparation wrote objects into the repository")
	}
	if p.Body.GitDir != filepath.Join(dir, ".git") {
		t.Fatalf("git_dir = %q", p.Body.GitDir)
	}
	if p.Body.DefHash != "" || p.Body.NoCache {
		t.Fatalf("a fast-lane run is never answered from a stored result: def_hash %q no_cache %v", p.Body.DefHash, p.Body.NoCache)
	}
}

func TestPrepareLocalRecordsHeadsTreeWhenTheDirectoryIsClean(t *testing.T) {
	dir := newRepo(t)
	if out := gitT(t, dir, "status", "--porcelain"); out != "" {
		t.Fatalf("the fixture is not clean: %s", out)
	}
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/a.test.ts"}, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := gitT(t, dir, "rev-parse", "HEAD^{tree}"); p.Body.Snapshot.Tree != want {
		t.Fatalf("tree = %q, want HEAD's %s", p.Body.Snapshot.Tree, want)
	}
}

// A working tree that cannot be taken is not HEAD's tree: the run goes on with no tree recorded, and the note says why.
func TestPrepareLocalRecordsNoTreeWhenTheWorkingTreeCannotBeTaken(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, "spa/src/new.test.ts"), "untracked\n")
	fake := filepath.Join(t.TempDir(), "git")
	write(t, fake, "#!/bin/sh\ncase \"$*\" in *write-tree*) echo 'fatal: disk full' >&2; exit 128;; esac\nexec git \"$@\"\n")
	if err := os.Chmod(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/new.test.ts"}, Local: true, Git: fake})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.Snapshot.Tree != "" || !strings.Contains(p.Note, "disk full") {
		t.Fatalf("tree %q note %q: want no tree and the reason", p.Body.Snapshot.Tree, p.Note)
	}
	if p.Body.Snapshot.Commit == "" {
		t.Fatal("the record still names HEAD")
	}
	if err := p.Body.Validate(); err != nil {
		t.Fatalf("a record without a tree must still validate: %v", err)
	}
}

func TestPrepareLocalReadsTheManifestFromTheWorkingTreeAndNeverCaches(t *testing.T) {
	dir := newRepo(t)
	// an edit that is not committed, and a task the committed manifest does not have
	write(t, filepath.Join(dir, ".wwd/tasks.toml"), testManifest+"\n[tasks.vet]\nrun = \"go vet\"\nargs = \"pass\"\nlocal = true\ncacheable = true\n")
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "vet", Args: []string{"./a", "-x"}, Local: true})
	if err != nil {
		t.Fatalf("a local-only pass task must be preparable locally: %v", err)
	}
	if p.Body.Cacheable {
		t.Fatal("a local run has no snapshot tree to key a result by: it must not be cacheable")
	}
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "vet", Args: []string{"./a"}}); err == nil || !IsUsage(err) {
		t.Fatalf("the same pass task must still be refused for the queue: %v", err)
	}
}

func TestPrepareLocalChecksFilesInTheWorkingTree(t *testing.T) {
	dir := newRepo(t)
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/gone.test.ts"}, Local: true}); err == nil || !IsUsage(err) {
		t.Fatalf("err = %v", err)
	}
	// a tracked file deleted from the working tree is not there to run
	if err := os.Remove(filepath.Join(dir, "spa/src/a.test.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/a.test.ts"}, Local: true}); err == nil || !IsUsage(err) {
		t.Fatalf("a file deleted in the tree was accepted: %v", err)
	}
}

func TestTreeFileExistsDoesNotFollowLinksOutOfTheTree(t *testing.T) {
	root := newRepo(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.ts"), "x")
	if err := os.Symlink(filepath.Join(outside, "secret.ts"), filepath.Join(root, "file-link.ts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("spa/src/a.test.ts", filepath.Join(root, "inside-link.ts")); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{
		"spa/src/a.test.ts":            true,
		"inside-link.ts":               true, // a link that stays inside the tree is fine
		"file-link.ts":                 false,
		"dir-link/secret.ts":           false,
		"spa":                          false, // a directory is not a file
		"spa/src/missing.ts":           false,
		"../" + filepath.Base(outside): false,
	} {
		if got := TreeFileExists(root, rel); got != want {
			t.Errorf("TreeFileExists(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestPrepareNoManifestIsUsage(t *testing.T) {
	dir := newRepo(t)
	if err := os.Remove(filepath.Join(dir, ".wwd/tasks.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), Request{Dir: dir, Task: "lint"}); err == nil || !IsUsage(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareClassMayBeLowered(t *testing.T) {
	dir := newRepo(t)
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "lint", Class: wire.ClassDev})
	if err != nil || p.Body.Class != wire.ClassDev {
		t.Fatalf("class = %v, err %v", p.Body.Class, err)
	}
}

func TestPrepareFilesAreCheckedAgainstTheSnapshot(t *testing.T) {
	dir := newRepo(t)
	// an untracked file is in the snapshot, so it is accepted
	write(t, filepath.Join(dir, "spa/src/new.test.ts"), "untracked\n")
	p, err := Prepare(context.Background(), Request{Dir: dir, Task: "spa-file", Args: []string{"src/new.test.ts", "src/a.test.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Body.Snapshot.RelCwd != "spa" {
		t.Fatalf("rel_cwd = %q, want the task's cwd", p.Body.Snapshot.RelCwd)
	}
}
