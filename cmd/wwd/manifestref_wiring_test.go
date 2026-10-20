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
	"github.com/wake/purdex/internal/wwd/manifestref"
	"github.com/wake/purdex/internal/wwd/qualify"
)

// refSources: a clone whose committed manifest is qualifyManifest (HEAD stands for the ref a host would configure, e.g. origin/main) and whose
// working tree has since moved (the manifest edited, a task added, nothing committed), with a qualifySource and a refTaskSource that read it
// through a manifestref.Source.
func refSources(t *testing.T) (root, head string, qs *qualifySource, rs *refTaskSource, mr *manifestref.Source) {
	t.Helper()
	root = qualifyRepo(t, qualifyManifest)
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	head = strings.TrimSpace(string(out))
	stale := strings.Replace(qualifyManifest, `reference = "air26"`, `reference = "elsewhere"`, 1) + "\n[tasks.extra]\nrun = \"x\"\nargs = \"none\"\nsummary = \"plain\"\nqualify = { args = [], reference = \"air26\" }\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, ".wwd", "tasks.toml"), []byte(stale), 0o644))
	repos := clones.NewSource(map[string]string{"purdex": root})
	mr = manifestref.New(repos, map[string]string{"purdex": "HEAD"}, "")
	qs = newQualifySource(root)
	qs.mrefs = mr
	rs = newRefTaskSource(repos)
	rs.mrefs = mr
	return root, head, qs, rs, mr
}

func TestTheQualifySourceReadsTheRefsManifestNotTheWorkingTree(t *testing.T) {
	_, _, qs, rs, mr := refSources(t)
	_, err := qs.Tasks()
	require.Error(t, err, "nothing read at the ref yet: the working tree must not stand in for it")
	assert.Empty(t, rs.Tasks(), "the reference labels must not fall back to the working tree either")
	require.NoError(t, mr.Refresh(context.Background(), "purdex"))

	tasks, err := qs.Tasks()
	require.NoError(t, err)
	names := map[string]qualify.Task{}
	for _, tk := range tasks {
		names[tk.Name] = tk
	}
	assert.NotContains(t, names, "extra", "a task only the working tree has")
	assert.Equal(t, "air26", names["tsc"].Reference, "the committed reference, not the edited one")
	var ref []string
	for _, r := range rs.Tasks() {
		ref = append(ref, r.Task)
	}
	assert.ElementsMatch(t, []string{"tsc", "spa-related", "heavy"}, ref)
}

// The pair's snapshot is the commit the ref named when the manifest was read, so the definition hash Tasks gave is the one Prepare sees.
func TestPrepareSnapshotsTheCommitOfTheRefAndItsDefHashMatchesTasks(t *testing.T) {
	_, head, qs, _, mr := refSources(t)
	require.NoError(t, mr.Refresh(context.Background(), "purdex"))
	tasks, err := qs.Tasks()
	require.NoError(t, err)
	var tsc qualify.Task
	for _, tk := range tasks {
		if tk.Name == "tsc" {
			tsc = tk
		}
	}
	sub, err := qs.Prepare(context.Background(), tsc)
	require.NoError(t, err, "the working tree's manifest differs, but the snapshot is the ref's commit, so the hashes agree")
	assert.Equal(t, head, sub.Snapshot.Commit)
	assert.Equal(t, tsc.DefHash, sub.DefHash)
}

func TestPrepareWithoutASnapshotOfTheRefIsRefused(t *testing.T) {
	_, _, qs, _, _ := refSources(t)
	_, err := qs.Prepare(context.Background(), qualify.Task{Repo: "purdex", Name: "tsc", Args: []string{}})
	require.Error(t, err)
}
