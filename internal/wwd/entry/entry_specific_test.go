package entry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoWith makes a root with the manifest and the files (each path is made, with its directories).
func repoWith(t *testing.T, manifest string, files ...string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(manifest), 0o644))
	for _, f := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644))
	}
	return root
}

// #3813: a file under the directory of a task goes to the task whose directory is the deepest, not to the root task that also reaches it
// and sorts first by name.
const nestedManifest = `
[tasks.file-root]
run  = "pnpm vitest run {files}"
args = "files"
[tasks.file-ui]
run  = "pnpm vitest run {files}"
cwd  = "ui"
args = "files"
[tasks.file-ui-deep]
run  = "pnpm vitest run {files}"
cwd  = "ui/widgets"
args = "files"
`

func TestMatch_TheDeepestDirectoryThatHoldsTheFileWins(t *testing.T) {
	root := repoWith(t, nestedManifest, "test/a.test.ts", "ui/test/b.test.ts", "ui/widgets/c.test.ts")
	ui := filepath.Join(root, "ui")

	// standing in ui/, both the root task and the ui task reach the file; ui/'s is the more specific
	r := Match(ui, []string{"pnpm", "vitest", "run", "test/b.test.ts"})
	assert.Equal(t, "file-ui", r.Task, "ui/ holds the file, and is deeper than the root")
	assert.Equal(t, []string{"test/b.test.ts"}, r.Args)

	r = Match(filepath.Join(ui, "widgets"), []string{"pnpm", "vitest", "run", "c.test.ts"})
	assert.Equal(t, "file-ui-deep", r.Task)
	assert.Equal(t, []string{"c.test.ts"}, r.Args)

	// typed in ui/, the task of ui/ (the deeper one is not where the command stands)
	r = Match(ui, []string{"pnpm", "vitest", "run", "widgets/c.test.ts"})
	assert.Equal(t, "file-ui", r.Task)

	// standing in the root, the root's vitest runs: the root task, whatever the file
	r = Match(root, []string{"pnpm", "vitest", "run", "test/a.test.ts"})
	assert.Equal(t, "file-root", r.Task)
	r = Match(root, []string{"pnpm", "vitest", "run", "ui/test/b.test.ts"})
	assert.Equal(t, "file-root", r.Task, "a ui task is not for a command typed in the root")
}

// The depth comes before the class and the name, and the class still decides between tasks of one directory (#3711).
func TestMatch_DepthComesBeforeClassAndClassBeforeName(t *testing.T) {
	root := repoWith(t, `
[tasks.a-root-dev]
run  = "pnpm vitest run {files}"
args = "files"
[tasks.z-ui-merge]
run   = "pnpm vitest run {files}"
cwd   = "ui"
args  = "files"
class = "merge"
[tasks.y-ui-release]
run   = "pnpm vitest run {files}"
cwd   = "ui"
args  = "files"
class = "release"
[tasks.x-ui-merge]
run   = "pnpm vitest run {files}"
cwd   = "ui"
args  = "files"
class = "merge"
`, "ui/b.test.ts")
	r := Match(filepath.Join(root, "ui"), []string{"pnpm", "vitest", "run", "b.test.ts"})
	assert.Equal(t, "x-ui-merge", r.Task, "the deeper directory first (a merge task of ui/ over the dev task of the root), then the lowest class, then the first name")
}

// #3814: a flag in the task's command that narrows the run is not one the typed command may leave out.
const narrowingManifest = `
[tasks.check-affected]
run   = "pnpm check --fast --affected origin/main"
args  = "none"
class = "merge"
[tasks.check-full]
run   = "pnpm check"
args  = "none"
class = "release"
[tasks.test-changed]
run  = "go test -json -changed {packages}"
args = "packages"
`

func TestMatch_ACommandWithoutTheNarrowingFlagsIsNotTheNarrowTask(t *testing.T) {
	root := repoWith(t, narrowingManifest)

	r := Match(root, []string{"pnpm", "check"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "check-full", r.Task, "a plain check is the full one, not the affected-only one, whatever the class")

	r = Match(root, []string{"pnpm", "check", "--fast", "--affected", "origin/main"})
	assert.Equal(t, "check-affected", r.Task, "the same flags typed are the narrow task")

	r = Match(root, []string{"pnpm", "check", "--fast"})
	assert.NotEqual(t, "check-affected", r.Task, "half of the narrowing is not the narrow task")
}

// With no task that has no narrowing flag, the plain command is nobody's: it runs as typed.
func TestMatch_ANarrowTaskAloneDoesNotTakeThePlainCommand(t *testing.T) {
	root := repoWith(t, `
[entry]
mode = "rewrite"
[tasks.check-affected]
run   = "pnpm check --fast --affected origin/main"
args  = "none"
class = "merge"
`)
	r := Match(root, []string{"pnpm", "check"})
	assert.Equal(t, ActionAdhoc, r.Action)
	assert.Empty(t, r.Task)
}

// The flags that only say how to run, and the report the worker writes, may still be left out (#3711).
func TestMatch_FlagsThatOnlySayHowToRunMayBeLeftOut(t *testing.T) {
	root := repoWith(t, `
[tasks.t]
run  = "pnpm vitest run --reporter=json --outputFile={report} --maxWorkers=3 {files}"
args = "files"
[tasks.g]
run  = "go test -json -count=1 -p 3 -timeout 20m -v {packages}"
args = "packages"
`, "a.test.ts")
	assert.Equal(t, "t", Match(root, []string{"pnpm", "vitest", "run", "a.test.ts"}).Task)
	assert.Equal(t, "g", Match(root, []string{"go", "test", "./x/"}).Task)
}

// A flag the task has and the command lacks is only left out when it is known not to change what runs: any other (a mode, a filter) makes
// another command.
func TestMatch_AnUnknownFlagOfTheTaskIsNotLeftOut(t *testing.T) {
	root := repoWith(t, `
[tasks.strict]
run  = "pnpm vitest run --mode quick {files}"
args = "files"
`, "a.ts")
	r := Match(root, []string{"pnpm", "vitest", "run", "a.ts"})
	assert.NotEqual(t, "strict", r.Task)
	r = Match(root, []string{"pnpm", "vitest", "run", "--mode", "quick", "a.ts"})
	assert.Equal(t, "strict", r.Task)
}

// What a flag means is its tool's: `--run` is vitest's single run, and a filter in another tool; `-p` is the parallelism of go test and a
// package of cargo. A task of a tool that is not known to say so has no flag that may be left out.
func TestMatch_FlagsOfAnotherToolAreNotLeftOut(t *testing.T) {
	root := repoWith(t, `
[tasks.runner-filter]
run  = "make test --run smoke -p core {files}"
args = "files"
[tasks.vitest-related]
run  = "pnpm vitest related --run {files}"
args = "files"
[tasks.go-one]
run  = "go test -json -p 3 {packages}"
args = "packages"
`, "a.ts")
	assert.NotEqual(t, "runner-filter", Match(root, []string{"make", "test", "a.ts"}).Task, "--run and -p of make are filters")
	assert.Equal(t, "vitest-related", Match(root, []string{"pnpm", "vitest", "related", "a.ts"}).Task, "vitest's own --run may be left out")
	assert.Equal(t, "go-one", Match(root, []string{"go", "test", "./x/"}).Task, "go test's own -p may be left out")
}
