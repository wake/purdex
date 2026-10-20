package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/entry"
	"github.com/wake/purdex/internal/wwd/wire"
)

func entryRepo(t *testing.T) string {
	t.Helper()
	// a queued task is only rewritten when some host can take it: these tests are about the rules of the manifest, so one can
	withPool(t, wire.Pool{Peers: []wire.Peer{peerOf("mlab", true, true)}}, nil)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".wwd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte("[entry]\nmode = \"deny\"\n[tasks.lint]\nrun = \"make lint\"\nargs = \"none\"\n"), 0o644))
	return root
}

func callEntry(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := entryMain(args, &out, &errb)
	return code, out.String(), errb.String()
}

// `pdx entry match --json --cwd D -- argv…` prints the entry's answer for one command as a JSON object, and exits 0 whatever the
// answer (the hook reads the answer, not the exit code).
func TestEntryMatch_PrintsTheAnswerAsJSON(t *testing.T) {
	root := entryRepo(t)
	code, out, errs := callEntry(t, "match", "--json", "--cwd", root, "--", "make", "lint")
	require.Equal(t, 0, code, errs)
	var r entry.Result
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, entry.ActionRewrite, r.Action)
	assert.Equal(t, "lint", r.Task)

	code, out, _ = callEntry(t, "match", "--json", "--cwd", root, "--", "go", "test", "./...")
	require.Equal(t, 0, code)
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, entry.ActionDeny, r.Action)

	code, out, _ = callEntry(t, "match", "--json", "--cwd", root, "--", "ls")
	require.Equal(t, 0, code)
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, entry.ActionNone, r.Action)
}

// The command's own flags belong to it: everything after the `--` is the command, even words that look like ours.
func TestEntryMatch_TheCommandFollowsTheDashes(t *testing.T) {
	root := entryRepo(t)
	code, out, _ := callEntry(t, "match", "--json", "--cwd", root, "--", "go", "test", "--json", "--cwd", "x")
	require.Equal(t, 0, code)
	var r entry.Result
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, entry.ActionDeny, r.Action, "a go test with flags of its own is still a go test")
}

func TestEntryMatch_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no subcommand":      {},
		"unknown subcommand": {"nope"},
		"no command":         {"match", "--json", "--"},
		"no dashes":          {"match", "--json", "make", "lint"},
		"no json":            {"match", "--", "make", "lint"},
		"bad cwd":            {"match", "--json", "--cwd", "/nonexistent-dir-for-entry-test", "--", "make", "lint"},
	} {
		code, out, errs := callEntry(t, args...)
		assert.Equal(t, 2, code, name)
		assert.Empty(t, out, name)
		assert.NotEmpty(t, errs, name)
	}
}

// A relative --cwd is read from the process's own directory, and the manifest is still found above it.
func TestEntryMatch_ARelativeCwd(t *testing.T) {
	root := entryRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spa", "src"), 0o755))
	t.Chdir(root)
	for _, cwd := range []string{"spa", "spa/src", "./spa", "."} {
		code, out, errs := callEntry(t, "match", "--json", "--cwd", cwd, "--", "make", "lint")
		require.Equal(t, 0, code, errs)
		var r entry.Result
		require.NoError(t, json.Unmarshal([]byte(out), &r))
		assert.Equal(t, entry.ActionRewrite, r.Action, cwd)
	}
	t.Chdir(filepath.Join(root, "spa"))
	code, out, _ := callEntry(t, "match", "--json", "--cwd", "src", "--", "make", "lint")
	require.Equal(t, 0, code)
	var r entry.Result
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, entry.ActionRewrite, r.Action, "from below the root, with a relative path")
}

// The cwd defaults to the process's own.
func TestEntryMatch_DefaultCwd(t *testing.T) {
	root := entryRepo(t)
	t.Chdir(root)
	code, out, _ := callEntry(t, "match", "--json", "--", "make", "lint")
	require.Equal(t, 0, code)
	var r entry.Result
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, "lint", r.Task)
}
