package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolDirsPutsExtraInFrontWithoutDuplicates(t *testing.T) {
	home := t.TempDir()
	extra := []string{"/srv/node/bin", "/opt/x"}
	dirs := ToolDirs(home, "/usr/bin:/opt/x", extra)
	if len(dirs) < 3 || dirs[0] != "/srv/node/bin" || dirs[1] != "/opt/x" {
		t.Fatalf("extra must lead the list: %v", dirs)
	}
	n := 0
	for _, d := range dirs {
		if d == "/opt/x" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("/opt/x listed %d times in %v", n, dirs)
	}
	for _, d := range DefaultToolDirs(home, "/usr/bin:/opt/x") {
		if !contains(dirs, d) {
			t.Errorf("default dir %s missing from %v", d, dirs)
		}
	}
}

func TestToolDirsWithoutExtraIsTheDefault(t *testing.T) {
	home := t.TempDir()
	if got, want := ToolDirs(home, "/usr/bin", nil), DefaultToolDirs(home, "/usr/bin"); strings.Join(got, ":") != strings.Join(want, ":") {
		t.Errorf("%v != %v", got, want)
	}
}

func TestResolveExtraDirsExpandsHomeAndSkipsMissing(t *testing.T) {
	home := t.TempDir()
	good := filepath.Join(home, "tc", "bin")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	got := ResolveExtraDirs(home, []string{"~/tc/bin", filepath.Join(home, "nope"), file}, logf)
	if len(got) != 1 || got[0] != good {
		t.Fatalf("got %v, want [%s]", got, good)
	}
	if len(logged) != 2 || !strings.Contains(logged[0], "nope") || !strings.Contains(logged[1], "afile") {
		t.Errorf("each skipped entry is logged once: %v", logged)
	}
}

// What the facts report must be what a job finds: the reader and the job PATH are built from the one list.
func TestToolsReaderSearchesTheSameListAsAJobPath(t *testing.T) {
	home := t.TempDir()
	extra := []string{"/srv/node/bin"}
	r := NewToolsReader(home, "/usr/bin", extra)
	if got, want := strings.Join(r.PathDirs, ":"), ToolPath(home, "/usr/bin", extra); got != want {
		t.Errorf("reader %q != job PATH %q", got, want)
	}
}
