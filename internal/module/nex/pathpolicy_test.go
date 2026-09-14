package nex

import (
	"os"
	"testing"
)

func TestComposePath(t *testing.T) {
	sep := string(os.PathListSeparator)
	launchdPath := "/usr/bin" + sep + "/bin" + sep + "/usr/sbin" + sep + "/sbin"

	notDir := func(string) bool { return false }
	allDir := func(string) bool { return true }

	tests := []struct {
		name    string
		current string
		entries []string
		isDir   func(string) bool
		want    string
	}{
		{
			name:    "prepends dirs and filters non-dirs",
			current: launchdPath,
			entries: []string{"/A", "/B", "/C"},
			isDir: func(p string) bool {
				return p != "/B"
			},
			want: "/A" + sep + "/C" + sep + launchdPath,
		},
		{
			name:    "entry already in middle of PATH is moved to front, once",
			current: "/x" + sep + "/A" + sep + "/y",
			entries: []string{"/A"},
			isDir:   allDir,
			want:    "/A" + sep + "/x" + sep + "/y",
		},
		{
			name:    "duplicate entries collapse to once",
			current: "/x",
			entries: []string{"/A", "/A", "/A"},
			isDir:   allDir,
			want:    "/A" + sep + "/x",
		},
		{
			name:    "nil entries returns current unchanged",
			current: launchdPath,
			entries: nil,
			isDir:   allDir,
			want:    launchdPath,
		},
		{
			name:    "empty entries slice returns current unchanged",
			current: launchdPath,
			entries: []string{},
			isDir:   allDir,
			want:    launchdPath,
		},
		{
			name:    "all entries filtered out returns current unchanged",
			current: launchdPath,
			entries: []string{"/A", "/B"},
			isDir:   notDir,
			want:    launchdPath,
		},
		{
			name:    "empty current with entries yields only the prefix, no trailing separator",
			current: "",
			entries: []string{"/A", "/B"},
			isDir:   allDir,
			want:    "/A" + sep + "/B",
		},
		{
			name:    "empty current and empty entries yields empty string",
			current: "",
			entries: nil,
			isDir:   allDir,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := composePath(tt.current, tt.entries, tt.isDir)
			if got != tt.want {
				t.Errorf("composePath(%q, %v) = %q, want %q", tt.current, tt.entries, got, tt.want)
			}
		})
	}
}

func TestComposePathIdempotent(t *testing.T) {
	sep := string(os.PathListSeparator)
	current := "/usr/bin" + sep + "/bin"
	entries := []string{"/A", "/B"}
	allDir := func(string) bool { return true }

	once := composePath(current, entries, allDir)
	twice := composePath(once, entries, allDir)

	if once != twice {
		t.Errorf("composePath is not idempotent: once=%q twice=%q", once, twice)
	}
}

func TestComposePathDoesNotMutateEntries(t *testing.T) {
	entries := []string{"/A", "/A", "/B"}
	allDir := func(string) bool { return true }

	_ = composePath("/x", entries, allDir)

	want := []string{"/A", "/A", "/B"}
	for i, e := range entries {
		if e != want[i] {
			t.Fatalf("composePath mutated entries: got %v, want %v", entries, want)
		}
	}
}

func TestApplyPathPolicy(t *testing.T) {
	sep := string(os.PathListSeparator)
	launchdPath := "/usr/bin" + sep + "/bin"
	allDir := func(string) bool { return true }

	t.Run("updates PATH env and reports changed", func(t *testing.T) {
		t.Setenv("PATH", launchdPath)

		final, changed := applyPathPolicy([]string{"/A", "/B"}, allDir)

		want := "/A" + sep + "/B" + sep + launchdPath
		if final != want {
			t.Errorf("final = %q, want %q", final, want)
		}
		if !changed {
			t.Error("changed = false, want true")
		}
		if got := os.Getenv("PATH"); got != want {
			t.Errorf("os.Getenv(PATH) = %q, want %q", got, want)
		}
	})

	t.Run("second call is a no-op and reports changed=false", func(t *testing.T) {
		t.Setenv("PATH", launchdPath)

		first, firstChanged := applyPathPolicy([]string{"/A", "/B"}, allDir)
		if !firstChanged {
			t.Fatal("first call: changed = false, want true")
		}

		second, secondChanged := applyPathPolicy([]string{"/A", "/B"}, allDir)
		if secondChanged {
			t.Error("second call: changed = true, want false")
		}
		if second != first {
			t.Errorf("second call: final = %q, want %q (unchanged)", second, first)
		}
		if got := os.Getenv("PATH"); got != first {
			t.Errorf("os.Getenv(PATH) = %q, want %q", got, first)
		}
	})

	t.Run("nil entries leaves PATH untouched and reports changed=false", func(t *testing.T) {
		t.Setenv("PATH", launchdPath)

		final, changed := applyPathPolicy(nil, allDir)

		if final != launchdPath {
			t.Errorf("final = %q, want %q", final, launchdPath)
		}
		if changed {
			t.Error("changed = true, want false")
		}
		if got := os.Getenv("PATH"); got != launchdPath {
			t.Errorf("os.Getenv(PATH) = %q, want %q", got, launchdPath)
		}
	})

	t.Run("entries already all present at front leaves PATH untouched", func(t *testing.T) {
		current := "/A" + sep + "/B" + sep + "/usr/bin"
		t.Setenv("PATH", current)

		final, changed := applyPathPolicy([]string{"/A", "/B"}, allDir)

		if final != current {
			t.Errorf("final = %q, want %q", final, current)
		}
		if changed {
			t.Error("changed = true, want false")
		}
	})
}
