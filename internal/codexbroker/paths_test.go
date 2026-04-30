package codexbroker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPaths_DefaultRoots checks the default path resolution for the plugin
// data root and the socket-glob roots.
func TestPaths_DefaultRoots(t *testing.T) {
	t.Setenv("PDX_CODEX_STATE_ROOT", "")
	t.Setenv("PDX_CODEX_SOCKET_ROOTS", "")

	got, err := PluginDataRoot()
	if err != nil {
		t.Fatalf("PluginDataRoot: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude", "plugins", "data", "codex-openai-codex", "state")
	if got != want {
		t.Errorf("PluginDataRoot = %q, want %q", got, want)
	}

	roots, err := SocketGlobRoots()
	if err != nil {
		t.Fatalf("SocketGlobRoots: %v", err)
	}
	if len(roots) == 0 {
		t.Fatalf("SocketGlobRoots returned empty")
	}
	// Default should include $TMPDIR (always exists at runtime).
	tmp := os.TempDir()
	found := false
	for _, r := range roots {
		if r == tmp {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("SocketGlobRoots = %v, expected to contain TMPDIR=%q", roots, tmp)
	}
	// On macOS the per-user /var/folders/*/T glob root should also be in the list.
	if runtime.GOOS == "darwin" {
		hasVarFolders := false
		for _, r := range roots {
			if strings.HasPrefix(r, "/var/folders") || strings.HasPrefix(r, "/private/var/folders") {
				hasVarFolders = true
				break
			}
		}
		if !hasVarFolders {
			t.Errorf("SocketGlobRoots on darwin = %v, expected /var/folders entry", roots)
		}
	}
}

// TestPaths_EnvOverride checks PDX_CODEX_STATE_ROOT and PDX_CODEX_SOCKET_ROOTS
// take priority over defaults.
func TestPaths_EnvOverride(t *testing.T) {
	t.Setenv("PDX_CODEX_STATE_ROOT", "/tmp/test-state")
	root, err := PluginDataRoot()
	if err != nil {
		t.Fatalf("PluginDataRoot: %v", err)
	}
	if root != "/tmp/test-state" {
		t.Errorf("PluginDataRoot under override = %q, want %q", root, "/tmp/test-state")
	}

	t.Setenv("PDX_CODEX_SOCKET_ROOTS", "/tmp/a:/tmp/b")
	roots, err := SocketGlobRoots()
	if err != nil {
		t.Fatalf("SocketGlobRoots: %v", err)
	}
	want := []string{"/tmp/a", "/tmp/b"}
	if len(roots) != len(want) || roots[0] != want[0] || roots[1] != want[1] {
		t.Errorf("SocketGlobRoots under override = %v, want %v", roots, want)
	}
}

// TestPaths_DedupesSocketRoots ensures the default resolution does not return
// duplicate roots when TMPDIR happens to coincide with one of the platform
// roots.
func TestPaths_DedupesSocketRoots(t *testing.T) {
	t.Setenv("PDX_CODEX_SOCKET_ROOTS", "")
	t.Setenv("TMPDIR", "/tmp")
	roots, err := SocketGlobRoots()
	if err != nil {
		t.Fatalf("SocketGlobRoots: %v", err)
	}
	seen := make(map[string]int)
	for _, r := range roots {
		seen[r]++
	}
	for r, n := range seen {
		if n > 1 {
			t.Errorf("root %q appears %d times in %v", r, n, roots)
		}
	}
}
