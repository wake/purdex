package entry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const manifestSrc = `
[entry]
mode = "%MODE%"

[tasks.spa-file]
run  = "pnpm vitest run --reporter=json --outputFile={report} {files}"
cwd  = "spa"
args = "files"

[tasks.spa-full]
run  = "pnpm vitest run --reporter=json --outputFile={report}"
cwd  = "spa"
args = "none"

[tasks.tsc-force]
run  = "pnpm tsc -b --force"
cwd  = "spa"
args = "none"

[tasks.go-pkg]
run  = "go test -json -count=1 {packages}"
args = "packages"

[tasks.lint]
run  = "make lint"
args = "none"

[tasks.phpunit]
run  = "vendor/bin/phpunit {files}"
args = "files"
`

// repo makes a repository root with the manifest (mode substituted) and the directories and files the tests refer to.
func repo(t *testing.T, mode string) string {
	t.Helper()
	root := t.TempDir()
	src := []byte(replaceMode(manifestSrc, mode))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), src, 0o644))
	for _, d := range []string{"spa/src/lib", "internal/foo", "tests"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	for _, f := range []string{"spa/src/a.test.ts", "spa/src/lib/b.test.ts", "tests/ExampleTest.php"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644))
	}
	return root
}

func replaceMode(s, mode string) string {
	if mode == "" { // no [entry] table at all
		return strings.TrimPrefix(s, "\n[entry]\nmode = \"%MODE%\"\n")
	}
	return strings.ReplaceAll(s, "%MODE%", mode)
}

func TestNormalize(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"npx", "vitest", "run", "a"}, []string{"vitest", "run", "a"}},
		{[]string{"npx", "-y", "vitest"}, []string{"vitest"}},
		{[]string{"npx", "--package", "vitest", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"npx", "-p", "vitest", "-y", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"npx", "--package=vitest", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"npx", "-c", "vitest run"}, []string{"npx", "-c", "vitest run"}},
		{[]string{"pnpm", "exec", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"pnpm", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"pnpm", "tsc", "-b"}, []string{"tsc", "-b"}},
		{[]string{"yarn", "vitest"}, []string{"vitest"}},
		{[]string{"bunx", "vitest"}, []string{"vitest"}},
		{[]string{"./node_modules/.bin/vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"../node_modules/.bin/tsc", "-b"}, []string{"tsc", "-b"}},
		{[]string{"vendor/bin/phpunit", "--filter", "X"}, []string{"phpunit", "--filter", "X"}},
		{[]string{"/repo/vendor/bin/pest"}, []string{"pest"}},
		{[]string{"pnpm", "run", "build"}, []string{"pnpm", "build"}},
		{[]string{"npm", "run", "test"}, []string{"npm", "test"}},
		{[]string{"pnpm", "test"}, []string{"pnpm", "test"}},
		{[]string{"go", "test", "./..."}, []string{"go", "test", "./..."}},
		{[]string{"ls", "-la"}, []string{"ls", "-la"}},
		{nil, nil},
	} {
		assert.Equal(t, c.want, normalize(c.in), c.in)
	}
}

func TestFamily(t *testing.T) {
	yes := [][]string{
		{"go", "test", "./..."}, {"go", "build", "./..."}, {"go", "vet", "./x"},
		{"vitest", "run"}, {"jest"}, {"tsc", "-b"}, {"phpunit"}, {"pest", "--parallel"},
		{"swift", "build"}, {"swift", "test"}, {"xcodebuild", "-scheme", "x"},
		{"pnpm", "build"}, {"pnpm", "test"}, {"pnpm", "check"}, {"npm", "test"}, {"yarn", "build"}, {"bun", "test"},
		{"make", "lint"}, {"make", "test"}, {"composer", "test"},
	}
	for _, c := range yes {
		assert.True(t, isFamily(c), c)
	}
	no := [][]string{
		{"go", "version"}, {"go", "mod", "tidy"}, {"swift", "package"}, {"pnpm", "install"}, {"pnpm", "dev"}, {"make", "clean"},
		{"composer", "install"}, {"ls"}, {"git", "status"}, {"echo", "go", "test"}, nil,
	}
	for _, c := range no {
		assert.False(t, isFamily(c), c)
	}
}

func TestMatch_RewritesADeclaredShape(t *testing.T) {
	root := repo(t, "advise")
	spa := filepath.Join(root, "spa")
	for name, c := range map[string]struct {
		cwd  string
		argv []string
		task string
		args []string
	}{
		"files, via pnpm":           {spa, []string{"pnpm", "vitest", "run", "src/a.test.ts"}, "spa-file", []string{"src/a.test.ts"}},
		"files, via npx":            {spa, []string{"npx", "vitest", "run", "src/a.test.ts", "src/lib/b.test.ts"}, "spa-file", []string{"src/a.test.ts", "src/lib/b.test.ts"}},
		"files, by path":            {spa, []string{"./node_modules/.bin/vitest", "run", "src/a.test.ts"}, "spa-file", []string{"src/a.test.ts"}},
		"the task's own flag":       {spa, []string{"pnpm", "vitest", "run", "--reporter=json", "src/a.test.ts"}, "spa-file", []string{"src/a.test.ts"}},
		"files from a deeper dir":   {filepath.Join(spa, "src"), []string{"pnpm", "vitest", "run", "lib/b.test.ts"}, "spa-file", []string{"src/lib/b.test.ts"}},
		"no arguments":              {spa, []string{"pnpm", "vitest", "run"}, "spa-full", nil},
		"tsc":                       {spa, []string{"npx", "tsc", "-b", "--force"}, "tsc-force", nil},
		"go packages":               {root, []string{"go", "test", "./internal/foo/"}, "go-pkg", []string{"./internal/foo/"}},
		"go all":                    {root, []string{"go", "test", "./..."}, "go-pkg", []string{"./..."}},
		"go packages from a subdir": {filepath.Join(root, "internal"), []string{"go", "test", "./foo/"}, "go-pkg", []string{"./internal/foo/"}},
		"make":                      {root, []string{"make", "lint"}, "lint", nil},
		"phpunit by path":           {root, []string{"vendor/bin/phpunit", "tests/ExampleTest.php"}, "phpunit", []string{"tests/ExampleTest.php"}},
	} {
		r := Match(c.cwd, c.argv)
		assert.Equal(t, ActionRewrite, r.Action, name)
		assert.Equal(t, c.task, r.Task, name)
		assert.Equal(t, c.args, r.Args, name)
	}
}

// A shape that is not a task's — a flag the task does not have, files where it takes none, the wrong directory — is not rewritten:
// it falls to the manifest's mode, with the task that is nearest to use instead.
func TestMatch_NoMatchFallsToTheMode(t *testing.T) {
	spa := func(root string) string { return filepath.Join(root, "spa") }
	cases := map[string]struct {
		cwd  func(string) string
		argv []string
		hint string
	}{
		"a flag the task lacks":     {spa, []string{"pnpm", "vitest", "run", "-t", "name", "src/a.test.ts"}, "spa-file"},
		"files for a no-files task": {spa, []string{"pnpm", "tsc", "-b", "--force", "x"}, "tsc-force"},
		"a flag no task has":        {spa, []string{"pnpm", "vitest", "run", "--watch"}, "spa-file"}, // two tasks are as near: the first name
		"the wrong directory":       {func(r string) string { return r }, []string{"pnpm", "vitest", "run", "src/a.test.ts"}, "spa-file"},
		"another go command":        {func(r string) string { return r }, []string{"go", "vet", "./..."}, "go-pkg"},
		"a go flag":                 {func(r string) string { return r }, []string{"go", "test", "-cover", "./internal/foo/"}, "go-pkg"}, // -v, -count, -p… only say how to run, and are ignored (#S2-b0b)
		"a package script":          {func(r string) string { return r }, []string{"pnpm", "test"}, ""},
	}
	for name, c := range cases {
		for _, mode := range []string{"advise", "deny", "rewrite", ""} {
			root := repo(t, mode)
			r := Match(c.cwd(root), c.argv)
			wantAction := Action(mode)
			switch mode {
			case "":
				wantAction = ActionAdvise // an absent [entry] is advise
			case "rewrite":
				wantAction = ActionAdhoc // an undeclared command runs as an ad-hoc job
			}
			assert.Equal(t, wantAction, r.Action, name+" / "+mode)
			assert.Empty(t, r.Task, name)
			assert.Equal(t, c.hint, r.Hint, name+" / "+mode)
			if mode == "" {
				assert.Equal(t, "advise", r.Mode, "no [entry] table is advise, and it says so")
			}
			if mode == "rewrite" {
				assert.Equal(t, "rewrite", r.Mode)
				assert.Empty(t, r.Reason, "nothing is held back: the command runs as ad-hoc")
			}
			assert.False(t, r.Queued, "only a declared task goes through the queue; an ad-hoc job is never queued (spec 8.3)")
		}
	}
}

// The repository's own manifest, as the hook will meet it: what a developer types most is the development task, not the merge one
// of the same shape.
func TestMatch_ThePurdexManifest(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	r := Match(root, []string{"go", "test", "./internal/wwd/entry/"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "go-pkg", r.Task)
	assert.Equal(t, []string{"./internal/wwd/entry/"}, r.Args)
	r = Match(filepath.Join(root, "spa"), []string{"npx", "vitest", "run", "src/lib/fleet/parse.test.ts"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "spa-file", r.Task)
	assert.Equal(t, []string{"src/lib/fleet/parse.test.ts"}, r.Args)
	r = Match(filepath.Join(root, "spa"), []string{"pnpm", "tsc", "-b"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "tsc", r.Task)
}

// Two tasks of one shape (a development one and a merge one): a command typed by hand is the lowest class, then the first name.
func TestMatch_SameShapeTakesTheLowestClass(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(`
[tasks.a-merge]
run   = "go test -json {packages}"
args  = "packages"
class = "merge"
[tasks.b-release]
run   = "go test -json {packages}"
args  = "packages"
class = "release"
[tasks.z-dev]
run   = "go test -json {packages}"
args  = "packages"
[tasks.y-dev]
run   = "go test -json {packages}"
args  = "packages"
`), 0o644))
	r := Match(root, []string{"go", "test", "./..."})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "y-dev", r.Task, "dev before merge and release, then the first name")
}

// A flag whose value is an output file of the worker's is the task's alone: a caller who types it is not running the task.
func TestMatch_OutputFlagsAreNotTheCallers(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(`
[entry]
mode = "deny"
[tasks.go-junit]
run  = "go test -junitfile {junit} {packages}"
args = "packages"
[tasks.vt]
run  = "vitest run --outputFile={report} {files}"
args = "files"
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.test.ts"), []byte("x"), 0o644))
	assert.Equal(t, ActionRewrite, Match(root, []string{"go", "test", "./pkg"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "-junitfile", "out.xml", "./pkg"}).Action)
	assert.Equal(t, ActionRewrite, Match(root, []string{"vitest", "run", "a.test.ts"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"vitest", "run", "--outputFile=out.json", "a.test.ts"}).Action)

	// a task that passes its arguments on would otherwise take the output flag as one of them
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(`
[entry]
mode = "deny"
[tasks.vt-pass]
run   = "vitest run --outputFile={report}"
args  = "pass"
local = true
[tasks.go-pass]
run   = "go test -junitfile {junit}"
args  = "pass"
local = true
`), 0o644))
	assert.Equal(t, ActionRewrite, Match(root, []string{"vitest", "run", "-t", "x"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"vitest", "run", "--outputFile=out.json"}).Action)
	assert.Equal(t, ActionRewrite, Match(root, []string{"go", "test", "-run", "X"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "-junitfile", "out.xml"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "-junitfile=out.xml"}).Action)
	// Go reads --flag as -flag: the double-dash spelling of the worker's own output flag is refused as well (#3711 R2)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "--junitfile", "out.xml"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "--junitfile=out.xml"}).Action)
}

// A task that takes no arguments is the task of its directory: the same command from elsewhere is not it.
func TestMatch_TheDirectoryDecidesForATaskWithoutArguments(t *testing.T) {
	root := repo(t, "advise")
	assert.Equal(t, ActionAdvise, Match(root, []string{"npx", "tsc", "-b", "--force"}).Action, "from the repo root, not from spa")
	assert.Equal(t, ActionRewrite, Match(filepath.Join(root, "spa"), []string{"npx", "tsc", "-b", "--force"}).Action)
	assert.Equal(t, ActionRewrite, Match(filepath.Join(root, "spa", "src"), []string{"npx", "tsc", "-b", "--force"}).Action, "from below it")
}

// A flag the task has, given another value, is another command; for a task that passes its arguments on it would otherwise be a
// second value after the first.
func TestMatch_ASameFlagWithAnotherValueIsNotTheTask(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte("[entry]\nmode = \"deny\"\n[tasks.go-pass]\nrun = \"go test -count=1\"\nargs = \"pass\"\nlocal = true\n"), 0o644))
	r := Match(root, []string{"go", "test", "-count=1", "-run", "X"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, []string{"-run", "X"}, r.Args)
	r = Match(root, []string{"go", "test", "-run", "X"})
	assert.Equal(t, ActionRewrite, r.Action, "the task's flag left out")
	// -count only says how to run (S2-b0b): another value is replaced by the task's, the command is still the task
	r = Match(root, []string{"go", "test", "-count=2", "-run", "X"})
	assert.Equal(t, ActionRewrite, r.Action, "-count=2 is replaced by the task's -count=1")
	// a flag that picks what runs is different: the same flag with another value is not the task
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte("[entry]\nmode = \"deny\"\n[tasks.go-tagged]\nrun = \"go test -tags a\"\nargs = \"pass\"\nlocal = true\n"), 0o644))
	assert.Equal(t, ActionRewrite, Match(root, []string{"go", "test", "-tags", "a", "-run", "X"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "-tags", "b", "-run", "X"}).Action, "-tags b is not the task's -tags a")
}

// A call that is not a test or build command, no manifest, or a manifest that does not parse: left alone.
func TestMatch_LeavesWhatIsNotItsOwn(t *testing.T) {
	root := repo(t, "deny")
	assert.Equal(t, ActionNone, Match(root, []string{"ls", "-la"}).Action)
	assert.Equal(t, ActionNone, Match(root, []string{"go", "version"}).Action)
	assert.Equal(t, ActionNone, Match(root, nil).Action)

	bare := t.TempDir()
	r := Match(bare, []string{"go", "test", "./..."})
	assert.Equal(t, ActionNone, r.Action)
	assert.Contains(t, r.Reason, "no manifest")

	bad := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(bad, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bad, ".wwd", "tasks.toml"), []byte("[tasks.x\n"), 0o644))
	r = Match(bad, []string{"go", "test", "./..."})
	assert.Equal(t, ActionNone, r.Action)
	assert.Contains(t, r.Reason, "manifest")
}

// The manifest is found from a subdirectory.
func TestMatch_FindsTheManifestAbove(t *testing.T) {
	root := repo(t, "advise")
	r := Match(filepath.Join(root, "internal", "foo"), []string{"make", "lint"})
	assert.Equal(t, ActionRewrite, r.Action)
	assert.Equal(t, "lint", r.Task)
}

// A file that is not there, or outside the task's directory, or that starts like an option, is not a match.
func TestMatch_ArgumentsAreChecked(t *testing.T) {
	root := repo(t, "deny")
	spa := filepath.Join(root, "spa")
	for name, argv := range map[string][]string{
		"a missing file":    {"pnpm", "vitest", "run", "src/nope.test.ts"},
		"outside the task":  {"pnpm", "vitest", "run", "../tests/ExampleTest.php"},
		"an absolute path":  {"pnpm", "vitest", "run", filepath.Join(root, "tests", "ExampleTest.php")},
		"an option as file": {"pnpm", "vitest", "run", "--", "src/a.test.ts"},
		"an empty file":     {"pnpm", "vitest", "run", ""},
	} {
		r := Match(spa, argv)
		assert.Equal(t, ActionDeny, r.Action, name)
		assert.Empty(t, r.Task, name)
	}
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", ""}).Action, "an empty package pattern")
	r := Match(spa, []string{"pnpm", "vitest", "run", filepath.Join(spa, "src", "a.test.ts")})
	assert.Equal(t, ActionRewrite, r.Action, "an absolute path inside the task's directory is fine")
	assert.Equal(t, []string{"src/a.test.ts"}, r.Args)
}
