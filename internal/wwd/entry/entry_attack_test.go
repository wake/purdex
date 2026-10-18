package entry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeManifest makes a repo root (with .git when git is true) holding the manifest src.
func writeManifest(t *testing.T, dir, src string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".wwd", "tasks.toml"), []byte(src), 0o644))
}

// A flag the task has, and the value that goes with it, are left out together or given together: a bare number in the command is
// not the value of a flag that is not there.
func TestMatch_AFlagAndItsValueGoTogether(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `
[entry]
mode = "deny"
[tasks.go-all]
run   = "go test -json -p 3 -timeout 30m ./..."
args  = "none"
class = "release"
`)
	assert.Equal(t, ActionRewrite, Match(root, []string{"go", "test", "./..."}).Action, "every flag left out")
	assert.Equal(t, ActionRewrite, Match(root, []string{"go", "test", "-p", "3", "./..."}).Action, "one flag with its value")
	assert.Equal(t, ActionRewrite, Match(root, []string{"go", "test", "-json", "-p", "3", "-timeout", "30m", "./..."}).Action, "all of them")
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "-timeout", "30m", "-p", "3", "./..."}).Action, "the task's order; another order is not matched")
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "3", "30m", "./..."}).Action, "bare words are not the flags' values")
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "3", "./..."}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test", "-p", "4", "./..."}).Action, "the flag with another value")
	assert.Equal(t, ActionDeny, Match(root, []string{"go", "test"}).Action, "the task's trailing ./... is part of it")
}

// A flag that carries its value with = has no value word: the plain word after it is the task's own.
func TestMatch_AFlagWithEqualsTakesNoValueWord(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "[entry]\nmode = \"deny\"\n[tasks.t]\nrun = \"make test --mode=fast onlyfast\"\nargs = \"none\"\n")
	assert.Equal(t, ActionRewrite, Match(root, []string{"make", "test", "onlyfast"}).Action, "the flag left out, its neighbour kept")
	assert.Equal(t, ActionRewrite, Match(root, []string{"make", "test", "--mode=fast", "onlyfast"}).Action)
	assert.Equal(t, ActionDeny, Match(root, []string{"make", "test"}).Action, "the plain word is part of the task")
}

// The manifest is the repository's: the search stops at the first repository root, so a project inside another repository that has
// a manifest does not borrow it, and a repository without one has none.
func TestMatch_TheManifestIsTheRepositorys(t *testing.T) {
	outer := t.TempDir()
	writeManifest(t, outer, "[entry]\nmode = \"deny\"\n[tasks.lint]\nrun = \"make lint\"\nargs = \"none\"\n")
	require.NoError(t, os.MkdirAll(filepath.Join(outer, ".git"), 0o755))
	inner := filepath.Join(outer, "vendored", "project")
	require.NoError(t, os.MkdirAll(filepath.Join(inner, ".git"), 0o755)) // another repository inside, with no manifest
	r := Match(inner, []string{"make", "lint"})
	assert.Equal(t, ActionNone, r.Action)
	assert.Contains(t, r.Reason, "no manifest")
	assert.False(t, r.Broken)

	linked := filepath.Join(outer, "wt") // a linked worktree: .git is a file
	require.NoError(t, os.MkdirAll(linked, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /x\n"), 0o644))
	assert.Equal(t, ActionNone, Match(linked, []string{"make", "lint"}).Action, "its own root, no manifest in it")

	plain := filepath.Join(outer, "sub") // a plain subdirectory of the repository with the manifest
	require.NoError(t, os.MkdirAll(plain, 0o755))
	assert.Equal(t, ActionRewrite, Match(plain, []string{"make", "lint"}).Action)

	writeManifest(t, linked, "[tasks.lint]\nrun = \"make lint\"\nargs = \"none\"\n") // a repository root that has its own
	assert.Equal(t, ActionRewrite, Match(linked, []string{"make", "lint"}).Action)
}

// A file that is a symbolic link is not accepted: the snapshot a queued job runs on holds the link, not what it points at.
func TestMatch_ASymbolicLinkIsNotAFile(t *testing.T) {
	root := repo(t, "deny")
	outside := filepath.Join(t.TempDir(), "secret.test.ts")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "spa", "src", "out.test.ts")))                                       // out of the repository
	require.NoError(t, os.Symlink(filepath.Join(root, "spa", "src", "a.test.ts"), filepath.Join(root, "spa", "src", "in.test.ts"))) // inside
	require.NoError(t, os.Symlink(filepath.Join(root, "internal"), filepath.Join(root, "spa", "linkdir")))                          // a directory link
	spa := filepath.Join(root, "spa")
	for name, f := range map[string]string{"out": "src/out.test.ts", "in": "src/in.test.ts"} {
		assert.Equal(t, ActionDeny, Match(spa, []string{"pnpm", "vitest", "run", f}).Action, name)
	}
	assert.Equal(t, ActionDeny, Match(spa, []string{"pnpm", "vitest", "run", "linkdir/foo/x.go"}).Action, "through a linked directory")
	assert.Equal(t, ActionRewrite, Match(spa, []string{"pnpm", "vitest", "run", "src/a.test.ts"}).Action, "a plain file")
}

// The task's directory itself may not be a link either: a snapshot holds the link, not what it points at.
func TestMatch_ALinkedTaskDirectoryIsNotMatched(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "[entry]\nmode = \"deny\"\n[tasks.spa-file]\nrun = \"pnpm vitest run {files}\"\ncwd = \"spa\"\nargs = \"files\"\n[tasks.spa-all]\nrun = \"pnpm vitest run\"\ncwd = \"spa\"\nargs = \"none\"\n")
	elsewhere := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(elsewhere, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(elsewhere, "src", "a.test.ts"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(root, "spa")))
	spa := filepath.Join(root, "spa")
	assert.Equal(t, ActionDeny, Match(spa, []string{"pnpm", "vitest", "run", "src/a.test.ts"}).Action, "a task directory that is a link out of the repository")
	assert.Equal(t, ActionDeny, Match(spa, []string{"pnpm", "vitest", "run"}).Action, "and a task that takes no files in it")

	inside := t.TempDir()
	writeManifest(t, inside, "[entry]\nmode = \"deny\"\n[tasks.spa-all]\nrun = \"pnpm vitest run\"\ncwd = \"spa\"\nargs = \"none\"\n")
	require.NoError(t, os.MkdirAll(filepath.Join(inside, "real"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(inside, "real"), filepath.Join(inside, "spa")))
	assert.Equal(t, ActionDeny, Match(filepath.Join(inside, "spa"), []string{"pnpm", "vitest", "run"}).Action, "a link to another place in the repository")
}

// A value that starts with a dot belongs to its flag, except at the very end of the task, where a pattern like ./... is the task's
// own word.
func TestMatch_ADottedValueBelongsToItsFlag(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "[entry]\nmode = \"deny\"\n[tasks.t]\nrun = \"vitest run --config ./vitest.config.ts --silent\"\nargs = \"none\"\n")
	assert.Equal(t, ActionRewrite, Match(root, []string{"vitest", "run"}).Action, "the flag left out with its value")
	assert.Equal(t, ActionRewrite, Match(root, []string{"vitest", "run", "--config", "./vitest.config.ts", "--silent"}).Action)
	assert.Equal(t, ActionRewrite, Match(root, []string{"vitest", "run", "--config", "./vitest.config.ts"}).Action, "without --silent")
	assert.Equal(t, ActionDeny, Match(root, []string{"vitest", "run", "--config", "./other.ts"}).Action, "the same flag with another value")
}

// A runner with options in front of the tool is still the tool, also the options this package does not know and a package manager
// whose own options come before exec.
func TestNormalize_UnknownAndLeadingOptions(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"pnpm", "--filter", "spa", "exec", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"pnpm", "-C", "spa", "test"}, []string{"pnpm", "test"}},
		{[]string{"pnpm", "-r", "exec", "vitest"}, []string{"vitest"}},
		{[]string{"pnpm", "exec", "--weird", "x", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"npx", "--weird", "x", "tsc", "-b"}, []string{"tsc", "-b"}},
		{[]string{"npx", "--weird", "x", "foo"}, []string{"x", "foo"}},                            // nothing recognisable: left as it is, not a family command
		{[]string{"npx", "vitest", "run", "go", "test"}, []string{"vitest", "run", "go", "test"}}, // the tool is where it stands: its arguments are not searched
	} {
		assert.Equal(t, c.want, normalize(c.in), c.in)
	}
	assert.False(t, isFamily(normalize([]string{"npx", "--weird", "x", "foo"})))
	assert.True(t, isFamily(normalize([]string{"pnpm", "exec", "--weird", "x", "vitest", "run"})))
}

// A runner with options in front of the tool is still the tool: pnpm exec's `--`, its directory options, npx's options with values.
func TestNormalize_RunnerOptions(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"pnpm", "exec", "--", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"pnpm", "exec", "-r", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"pnpm", "exec", "--filter", "spa", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"pnpm", "exec", "-F", "spa", "--", "vitest"}, []string{"vitest"}},
		{[]string{"pnpm", "exec", "--dir=spa", "vitest"}, []string{"vitest"}},
		{[]string{"npx", "--workspace", "spa", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"npx", "--yes", "--", "vitest", "run"}, []string{"vitest", "run"}},
		{[]string{"npx", "--prefix", "spa", "vitest"}, []string{"vitest"}},
		{[]string{"pnpm", "exec"}, nil},
		{[]string{"npx", "--", "-p", "x"}, []string{"-p", "x"}}, // after -- everything is the command
	} {
		assert.Equal(t, c.want, normalize(c.in), c.in)
	}
}

// A manifest that is there but does not read is not "no manifest": the answer is still none (the hook fails open) but it is marked
// broken, so the hook can say so instead of staying quiet.
func TestMatch_ABrokenManifestIsMarked(t *testing.T) {
	bad := t.TempDir()
	writeManifest(t, bad, "[tasks.x\n")
	r := Match(bad, []string{"go", "test", "./..."})
	assert.Equal(t, ActionNone, r.Action)
	assert.True(t, r.Broken)
	assert.Contains(t, r.Reason, "manifest")

	r = Match(bad, []string{"ls"})
	assert.False(t, r.Broken, "a command that is not a test or build one never looks")
}
