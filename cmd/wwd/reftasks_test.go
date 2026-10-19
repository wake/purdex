package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/labels"
)

const refManifest = `
[tasks.tsc]
run     = "pnpm tsc -b"
cwd     = "spa"
args    = "none"
labels  = ["os:darwin", "tool:node24"]
qualify = { args = [], reference = "air26" }

[tasks.plain]
run    = "pnpm lint"
args   = "none"
labels = ["os:darwin"]
`

func refRepo(t *testing.T, manifestText string) (*clones.Source, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".wwd"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".wwd", "tasks.toml")
	if manifestText != "" {
		if err := os.WriteFile(file, []byte(manifestText), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return clones.NewSource(map[string]string{"purdex": root}), file
}

func TestRefTaskSourceListsOnlyTasksThatNameAReference(t *testing.T) {
	src, _ := refRepo(t, refManifest)
	got := newRefTaskSource(src).Tasks()
	if len(got) != 1 || got[0].Repo != "purdex" || got[0].Task != "tsc" || got[0].Reference != "air26" ||
		!slices.Equal(got[0].Needs, []string{"os:darwin", "tool:node24"}) {
		t.Fatalf("%+v", got)
	}
}

func TestRefTaskSourceRereadsAChangedManifestAndDropsABrokenOne(t *testing.T) {
	src, file := refRepo(t, refManifest)
	s := newRefTaskSource(src)
	if len(s.Tasks()) != 1 {
		t.Fatal("first read")
	}
	if err := os.WriteFile(file, []byte("this is not [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if got := s.Tasks(); len(got) != 0 {
		t.Fatalf("a manifest that does not parse still contributed: %+v", got)
	}
}

func TestRefTaskSourceNoManifestIsEmptyNotNil(t *testing.T) {
	src, _ := refRepo(t, "")
	if got := newRefTaskSource(src).Tasks(); got == nil || len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}

// The labels a real manifest gives a task (its repo, its own earned label, an environment) are not all needs: the repo and the task's
// own label are not conditions of the host, a foreign label still is (and keeps the task from being held, labels.ReferenceLabels).
func TestEnvironmentNeedsDropsTheRepoAndTheTasksOwnLabel(t *testing.T) {
	got := environmentNeeds("purdex", "tsc", []string{"repo:purdex", "task:purdex/tsc", "tool:node24", "os:darwin", "task:purdex/other", "host:air26", "repo:mlab"})
	want := []string{"tool:node24", "os:darwin", "task:purdex/other", "host:air26", "repo:mlab"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	if got := environmentNeeds("purdex", "tsc", nil); got == nil || len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}

// End to end with the shape of purdex's own manifest: an A-class task carries its repo and its own task label and no os:darwin, a Mac
// task carries os:darwin and the repo. The reference host holds each of them once it has the environment they name.
func TestARealManifestShapeIsHeldByItsReference(t *testing.T) {
	const m = `
[tasks.spa-related]
run     = "pnpm vitest related --run {files}"
args    = "files"
labels  = ["os:darwin", "repo:purdex"]
qualify = { args = ["a.test.ts"], reference = "air26" }

[tasks.tsc]
run     = "pnpm tsc -b"
args    = "none"
labels  = ["repo:purdex", "task:purdex/tsc", "tool:node24"]
qualify = { args = [], reference = "air26" }
`
	src, _ := refRepo(t, m)
	tasks := newRefTaskSource(src).Tasks()
	have := []string{"os:darwin", "arch:arm64", "repo:purdex", "tool:node24"}
	if got := labels.ReferenceLabels("air26", have, tasks); !slices.Equal(got, []string{"task:purdex/spa-related", "task:purdex/tsc"}) {
		t.Fatalf("the reference does not hold its tasks: %v (%+v)", got, tasks)
	}
	if got := labels.ReferenceLabels("air26", []string{"os:darwin", "repo:purdex"}, tasks); !slices.Equal(got, []string{"task:purdex/spa-related"}) {
		t.Fatalf("without the tool: %v", got)
	}
}

// A same-size rewrite with the mtime put back still takes the old capability away: the cache goes by content, not by metadata.
func TestRefTaskSourceFollowsContentNotMetadata(t *testing.T) {
	src, file := refRepo(t, refManifest)
	s := newRefTaskSource(src)
	if len(s.Tasks()) != 1 {
		t.Fatal("first read")
	}
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	same := strings.Replace(refManifest, `reference = "air26"`, `reference = "air27"`, 1) // one character different, same size
	if len(same) != len(refManifest) {
		t.Fatal("test setup: sizes differ")
	}
	if err := os.WriteFile(file, []byte(same), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	got := s.Tasks()
	if len(got) != 1 || got[0].Reference != "air27" {
		t.Fatalf("a stale entry survived: %+v", got)
	}
}

// One clone configured under two names gives each name its own tasks.
func TestRefTaskSourceTwoNamesOneClone(t *testing.T) {
	one, _ := refRepo(t, refManifest)
	root, _ := one.Path("purdex")
	src := clones.NewSource(map[string]string{"alpha": root, "beta": root})
	got := newRefTaskSource(src).Tasks()
	if len(got) != 2 || got[0].Repo != "alpha" || got[1].Repo != "beta" {
		t.Fatalf("%+v", got)
	}
}

// A tasks.toml that is a symlink out of the clone, or larger than the bound, is not read.
func TestRefTaskSourceRefusesSymlinkOutAndOversize(t *testing.T) {
	src, file := refRepo(t, refManifest)
	outside := filepath.Join(t.TempDir(), "outside.toml")
	if err := os.WriteFile(outside, []byte(refManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if got := newRefTaskSource(src).Tasks(); len(got) != 0 {
		t.Fatalf("read through a symlink out of the clone: %+v", got)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	big := refManifest + "\n# " + strings.Repeat("x", maxManifestBytes) + "\n"
	if err := os.WriteFile(file, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newRefTaskSource(src).Tasks(); len(got) != 0 {
		t.Fatalf("read an oversize manifest: %+v", got)
	}
}
