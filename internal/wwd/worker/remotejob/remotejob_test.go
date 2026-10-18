package remotejob

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const tasks = `
[tasks.spa-file]
run     = "pnpm vitest run --reporter=json --outputFile={report} {files}"
cwd     = "spa"
args    = "files"
summary = "vitest-json"
env     = ["PATH", "CI=1"]

[tasks.go-pkg]
run     = "go test -json {packages}"
args    = "packages"
class   = "merge"
timeout = "10m"
summary = "go-json"

[tasks.lint]
run     = "make lint"
args    = "none"
class   = "release"
timeout = "20m"

[tasks.scratch]
run     = "sh run.sh"
args    = "pass"
local   = true
`

// snapshot makes a snapshot tree (the files of the tree, slash paths) with the manifest in it.
func snapshot(t *testing.T, manifest string, files ...string) string {
	t.Helper()
	root := t.TempDir()
	put := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if manifest != "" {
		put(".wwd/tasks.toml", manifest)
	}
	for _, f := range files {
		put(f, "x")
	}
	return root
}

func jobOf(task string, class wire.JobClass, args ...string) wire.Job {
	return wire.Job{ID: "j_a", Task: task, Class: class, Args: args}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var r *Rejection
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a *Rejection", err)
	}
	return r.Code
}

func TestFilesTaskRendersWithTheFilesAndTheOutputInOut(t *testing.T) {
	root := snapshot(t, tasks, "spa/src/a.test.ts", "spa/src/b.test.ts")
	out := t.TempDir()
	p, err := Prepare(jobOf("spa-file", wire.ClassDev, "src/a.test.ts", "src/b.test.ts"), root, out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pnpm", "vitest", "run", "--reporter=json", "--outputFile=" + filepath.Join(out, "report.json"), "src/a.test.ts", "src/b.test.ts"}
	if !slices.Equal(p.Argv, want) {
		t.Fatalf("argv = %q, want %q", p.Argv, want)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if p.Dir != filepath.Join(realRoot, "spa") {
		t.Fatalf("dir = %q", p.Dir)
	}
	if !slices.Equal(p.Env, []string{"PATH", "CI=1"}) || p.Task.Name != "spa-file" || p.Class != wire.ClassDev {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPackagesAndNoneTasks(t *testing.T) {
	root := snapshot(t, tasks)
	p, err := Prepare(jobOf("go-pkg", wire.ClassMerge, "./internal/wwd/...", "./cmd/pdx"), root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"go", "test", "-json", "./internal/wwd/...", "./cmd/pdx"}; !slices.Equal(p.Argv, want) {
		t.Fatalf("argv = %q", p.Argv)
	}
	p, err = Prepare(jobOf("lint", wire.ClassRelease), root, t.TempDir())
	if err != nil || !slices.Equal(p.Argv, []string{"make", "lint"}) {
		t.Fatalf("lint: %+v, %v", p, err)
	}
}

// Whatever the origin sent that does not fit the snapshot's manifest is a rejection with a fixed code, and nothing is rendered.
func TestRejections(t *testing.T) {
	root := snapshot(t, tasks, "spa/ok.test.ts", "top.txt")
	cases := []struct {
		name string
		job  wire.Job
		want string
	}{
		{"unknown task", jobOf("nope", wire.ClassDev), CodeTaskUnknown},
		{"local-only task", jobOf("scratch", wire.ClassDev, "a"), CodeLocalOnly},
		{"class above the task's", jobOf("go-pkg", wire.ClassRelease, "./x"), CodeClassAbove},
		{"class that is none", jobOf("lint", "bogus"), CodeClassAbove},
		{"ad-hoc job", func() wire.Job { j := jobOf("lint", wire.ClassDev); j.AdHoc = true; return j }(), CodeNotRemote},
		{"fast-lane job", func() wire.Job { j := jobOf("lint", wire.ClassDev); j.Local = true; return j }(), CodeNotRemote},
		{"no file", jobOf("spa-file", wire.ClassDev), CodeArgsInvalid},
		{"file not in the snapshot", jobOf("spa-file", wire.ClassDev, "missing.ts"), CodeArgsInvalid},
		{"dotdot", jobOf("spa-file", wire.ClassDev, "../top.txt"), CodeArgsInvalid},
		{"absolute path", jobOf("spa-file", wire.ClassDev, "/etc/passwd"), CodeArgsInvalid},
		{"option as a file name", jobOf("spa-file", wire.ClassDev, "--config=evil"), CodeArgsInvalid},
		{"no package", jobOf("go-pkg", wire.ClassDev), CodeArgsInvalid},
		{"package that is an option", jobOf("go-pkg", wire.ClassDev, "-exec=evil"), CodeArgsInvalid},
		{"package with a shell word", jobOf("go-pkg", wire.ClassDev, "./x;rm"), CodeArgsInvalid},
		{"arguments to a none task", jobOf("lint", wire.ClassDev, "-v"), CodeArgsInvalid},
	}
	for _, c := range cases {
		p, err := Prepare(c.job, root, t.TempDir())
		if p != nil {
			t.Errorf("%s: a plan came with a rejection: %+v", c.name, p)
		}
		if got := code(t, err); got != c.want {
			t.Errorf("%s: code %s, want %s (%v)", c.name, got, c.want, err)
		}
	}
}

func TestManifestMissingOrBroken(t *testing.T) {
	_, err := Prepare(jobOf("lint", wire.ClassDev), snapshot(t, ""), t.TempDir())
	if c := code(t, err); c != CodeManifestMissing {
		t.Errorf("no manifest: %s", c)
	}
	_, err = Prepare(jobOf("lint", wire.ClassDev), snapshot(t, "this is [not toml"), t.TempDir())
	if c := code(t, err); c != CodeManifestInvalid {
		t.Errorf("broken manifest: %s", c)
	}
	_, err = Prepare(jobOf("lint", wire.ClassDev), snapshot(t, "[tasks.lint]\nrun = \"a | b\"\nargs = \"none\"\n"), t.TempDir())
	if c := code(t, err); c != CodeManifestInvalid {
		t.Errorf("a manifest with a shell operator: %s", c)
	}
}

// The origin's job decides nothing about what runs but the task name and its arguments: the command and the timeout are the
// snapshot's.
func TestTheTimeoutIsTheSnapshotTasksAndTheJobCanOnlyShortenIt(t *testing.T) {
	root := snapshot(t, tasks)
	for _, c := range []struct {
		jobS int
		want time.Duration
	}{{0, 20 * time.Minute}, {60, time.Minute}, {99999, 20 * time.Minute}} {
		j := jobOf("lint", wire.ClassDev)
		j.TimeoutS = c.jobS
		p, err := Prepare(j, root, t.TempDir())
		if err != nil || p.Timeout != c.want {
			t.Errorf("job timeout %ds: %v, %v (want %v)", c.jobS, p, err, c.want)
		}
	}
}

// A link out of the snapshot is no file, no manifest and no working directory of it (the snapshot comes from another host).
func TestLinksOutOfTheSnapshotAreRefused(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.ts"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(outside, "dir"), 0o755)

	root := snapshot(t, tasks)
	os.MkdirAll(filepath.Join(root, "spa"), 0o755)
	if err := os.Symlink(filepath.Join(outside, "secret.ts"), filepath.Join(root, "spa", "link.ts")); err != nil {
		t.Fatal(err)
	}
	_, err := Prepare(jobOf("spa-file", wire.ClassDev, "link.ts"), root, t.TempDir())
	if c := code(t, err); c != CodeArgsInvalid {
		t.Errorf("a file that is a link out: %s", c)
	}

	// the task's cwd is a link out
	root2 := snapshot(t, tasks)
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(root2, "spa")); err != nil {
		t.Fatal(err)
	}
	_, err = Prepare(jobOf("lint", wire.ClassRelease), root2, t.TempDir()) // lint has no cwd: fine
	if err != nil {
		t.Errorf("a task without cwd: %v", err)
	}
	os.WriteFile(filepath.Join(outside, "dir", "a.ts"), []byte("x"), 0o644)
	_, err = Prepare(jobOf("spa-file", wire.ClassDev, "a.ts"), root2, t.TempDir())
	if c := code(t, err); c != CodeCwdOutside {
		t.Errorf("a cwd that is a link out: %s", c)
	}

	// the manifest itself is a link out
	root3 := t.TempDir()
	os.MkdirAll(filepath.Join(root3, ".wwd"), 0o755)
	os.WriteFile(filepath.Join(outside, "tasks.toml"), []byte(tasks), 0o644)
	if err := os.Symlink(filepath.Join(outside, "tasks.toml"), filepath.Join(root3, ".wwd", "tasks.toml")); err != nil {
		t.Fatal(err)
	}
	_, err = Prepare(jobOf("lint", wire.ClassRelease), root3, t.TempDir())
	if c := code(t, err); c != CodeManifestMissing {
		t.Errorf("a manifest that is a link out: %s", c)
	}
}

func TestACwdThatIsNotThereIsARejection(t *testing.T) {
	root := snapshot(t, tasks) // no spa/ directory
	_, err := Prepare(jobOf("spa-file", wire.ClassDev, "a.ts"), root, t.TempDir())
	if c := code(t, err); c != CodeCwdOutside {
		t.Errorf("cwd missing: %s", c)
	}
}

func TestReasonIsTheCodeThenTheDetail(t *testing.T) {
	r := &Rejection{Code: CodeTaskUnknown, Detail: `"x" is not a task`}
	if r.Reason() != `task_unknown: "x" is not a task` || r.Error() != r.Reason() {
		t.Fatalf("reason %q", r.Reason())
	}
	if (&Rejection{Code: CodeNotRemote}).Reason() != CodeNotRemote {
		t.Fatal("a rejection without detail is its code")
	}
	if strings.Contains(r.Reason(), "\n") {
		t.Fatal("the reason is one line")
	}
}
