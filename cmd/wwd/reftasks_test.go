package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/clones"
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
