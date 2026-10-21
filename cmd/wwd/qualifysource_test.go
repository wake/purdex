package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/qualify"
	"github.com/wake/purdex/internal/wwd/wire"
)

const qualifyManifest = `
[tasks.tsc]
run     = "echo checked"
args    = "none"
summary = "plain"
labels  = ["os:darwin", "tool:node24", "repo:purdex", "task:purdex/tsc"]
qualify = { args = [], reference = "air26" }

[tasks.spa-related]
run     = "echo {files}"
args    = "files"
summary = "plain"
labels  = ["tool:node24", "repo:purdex"]
qualify = { args = ["src/a.test.ts"], reference = "air26" }

[tasks.heavy]
run     = "echo heavy"
args    = "none"
summary = "plain"
class   = "release"
labels  = ["repo:purdex"]
qualify = { args = [], reference = "air26" }

[tasks.plain]
run     = "echo plain"
args    = "none"
summary = "plain"
`

// qualifyRepo is a real repository with the manifest committed and the file the spa-related qualify names.
func qualifyRepo(t *testing.T, manifestText string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range map[string]string{".wwd/tasks.toml": manifestText, "src/a.test.ts": "x\n"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "m"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func newQualifySource(root string) *qualifySource {
	return &qualifySource{
		repos: clones.NewSource(map[string]string{"purdex": root}),
		host: func() qualify.Host {
			return qualify.Host{Alias: "mserv", WorkerOn: true, Labels: []string{"repo:purdex"}}
		},
	}
}

// The tasks a host may qualify for are read from its prepared clones' manifests: those with a qualify that names a reference host, with the
// environment labels they need (the repo and the task's own label are not needs) and the manifest's definition hash.
func TestTheQualifySourceListsTheTasksWithAQualify(t *testing.T) {
	src := newQualifySource(qualifyRepo(t, qualifyManifest))
	tasks, err := src.Tasks()
	require.NoError(t, err)
	require.Len(t, tasks, 3, "plain has no qualify")
	byName := map[string]qualify.Task{}
	for _, tk := range tasks {
		byName[tk.Name] = tk
	}
	tsc := byName["tsc"]
	assert.Equal(t, "purdex", tsc.Repo)
	assert.Equal(t, "air26", tsc.Reference)
	assert.Equal(t, []string{}, tsc.Args, "qualify.args = [] on a task whose args are none: the whole run is the qualification")
	assert.Equal(t, []string{"os:darwin", "tool:node24"}, tsc.Needs, "neither repo:purdex nor the task's own label is a need")
	assert.Equal(t, []string{"src/a.test.ts"}, byName["spa-related"].Args)

	m, err := manifest.Parse([]byte(qualifyManifest))
	require.NoError(t, err)
	assert.NotEmpty(t, tsc.DefHash)
	assert.Equal(t, m.Tasks["tsc"].DefinitionHash(), tsc.DefHash, "the hash is the manifest's own, so the coordinator can tell the task is the same one")
	assert.Equal(t, "task:purdex/tsc", tsc.Label())
}

// What the hash covers: a change to what the task runs is another definition (a pair that ran the old one never earns the label), a change
// to another task is not; and a change to the qualify (the reference, the args) is not in the hash but in the task's identity, which the
// coordinator compares as well.
func TestTheDefinitionHashFollowsTheTasksOwnManifestFields(t *testing.T) {
	hash := func(text string) map[string]string {
		out := map[string]string{}
		tasks, err := newQualifySource(qualifyRepo(t, text)).Tasks()
		require.NoError(t, err)
		for _, tk := range tasks {
			out[tk.Name] = tk.DefHash
		}
		return out
	}
	identity := func(text string) map[string]string {
		out := map[string]string{}
		tasks, err := newQualifySource(qualifyRepo(t, text)).Tasks()
		require.NoError(t, err)
		for _, tk := range tasks {
			out[tk.Name] = tk.Identity()
		}
		return out
	}
	base := hash(qualifyManifest)
	run := hash(strings.Replace(qualifyManifest, `run     = "echo checked"`, `run     = "echo changed"`, 1))
	assert.NotEqual(t, base["tsc"], run["tsc"], "the command is part of the definition")
	assert.Equal(t, base["spa-related"], run["spa-related"], "another task's command is not")
	ref := hash(strings.Replace(qualifyManifest, `reference = "air26"`, `reference = "mini"`, 1))
	assert.Equal(t, base["tsc"], ref["tsc"], "the qualify is not in the hash's fields ...")
	baseID := identity(qualifyManifest)
	assert.NotEqual(t, baseID["tsc"], identity(strings.Replace(qualifyManifest, `reference = "air26"`, `reference = "mini"`, 1))["tsc"], "... but a new reference is another identity")
	argsID := identity(strings.Replace(qualifyManifest, `args = ["src/a.test.ts"]`, `args = ["src/b.test.ts"]`, 1))
	assert.NotEqual(t, baseID["spa-related"], argsID["spa-related"], "and so are new qualify args")
	assert.Equal(t, baseID["tsc"], argsID["tsc"], "of that task only")
}

// A clone with no manifest, one whose manifest does not parse, and a repo that is not prepared contribute nothing.
func TestTheQualifySourceSkipsWhatItCannotRead(t *testing.T) {
	none := qualifyRepo(t, qualifyManifest)
	require.NoError(t, os.RemoveAll(filepath.Join(none, ".wwd")))
	bad := qualifyRepo(t, "[tasks.x\n")
	src := &qualifySource{repos: clones.NewSource(map[string]string{"a": none, "b": bad}), host: func() qualify.Host { return qualify.Host{} }}
	tasks, err := src.Tasks()
	assert.Empty(t, tasks)
	require.Error(t, err, "a manifest that does not parse is told, not hidden as a task that is not there")
	assert.Contains(t, err.Error(), "b:")
	assert.NotContains(t, err.Error(), "a: ", "a clone with no manifest is not a fault")
}

// A manifest that is there and cannot be read (here: a directory in its place) is told as well, and the other clones' tasks still come.
func TestTheQualifySourceTellsAManifestItCannotRead(t *testing.T) {
	good := qualifyRepo(t, qualifyManifest)
	broken := qualifyRepo(t, qualifyManifest)
	require.NoError(t, os.Remove(filepath.Join(broken, ".wwd", "tasks.toml")))
	require.NoError(t, os.MkdirAll(filepath.Join(broken, ".wwd", "tasks.toml"), 0o755))
	src := &qualifySource{repos: clones.NewSource(map[string]string{"purdex": good, "other": broken}), host: func() qualify.Host { return qualify.Host{} }}
	tasks, err := src.Tasks()
	assert.Len(t, tasks, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "other:")
}

// The manifest moved between listing the task and the snapshot (the A, B, A of a person editing): the pair is refused, not run on B.
func TestPrepareRefusesATaskWhoseDefinitionMoved(t *testing.T) {
	root := qualifyRepo(t, qualifyManifest)
	src := newQualifySource(root)
	tasks, err := src.Tasks()
	require.NoError(t, err)
	var tsc qualify.Task
	for _, tk := range tasks {
		if tk.Name == "tsc" {
			tsc = tk
		}
	}
	changed := strings.Replace(qualifyManifest, `run     = "echo checked"`, `run     = "echo changed"`, 1)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(changed), 0o644))
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "B"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	_, err = src.Prepare(context.Background(), tsc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "changed its definition")
}

// Prepare takes the snapshot of the task's clone and returns the task's submission with the qualify args as its args, in the name of the
// qualification session; a task whose args are none accepts the empty qualify.args.
func TestPrepareTakesOneSnapshotWithTheQualifyArgs(t *testing.T) {
	src := newQualifySource(qualifyRepo(t, qualifyManifest))
	byName := map[string]qualify.Task{}
	all, err := src.Tasks()
	require.NoError(t, err)
	for _, tk := range all {
		byName[tk.Name] = tk
	}
	sub, err := src.Prepare(context.Background(), byName["tsc"])
	require.NoError(t, err)
	assert.Equal(t, "tsc", sub.Task)
	assert.Empty(t, sub.Args)
	assert.NotEmpty(t, sub.Snapshot.Tree)
	assert.Equal(t, wire.ClassDev, sub.Class)
	assert.Equal(t, qualify.SessionName, sub.Submitter.Session)
	assert.Equal(t, byName["tsc"].DefHash != "", sub.DefHash != "", "the submission carries the definition hash the origin keys results by")

	sub, err = src.Prepare(context.Background(), byName["heavy"])
	require.NoError(t, err)
	assert.Equal(t, wire.ClassRelease, sub.Class, "the submission carries the manifest's class; the coordinator caps the halves at merge")

	sub, err = src.Prepare(context.Background(), byName["spa-related"])
	require.NoError(t, err)
	assert.Equal(t, []string{"src/a.test.ts"}, sub.Args)

	_, err = src.Prepare(context.Background(), qualify.Task{Repo: "nope", Name: "tsc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no prepared clone")
}
