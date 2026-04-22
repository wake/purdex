package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNormalizeExecutablePath_PreservesSymlinkInvocationPath ensures the
// symlink path is returned as-is. Claude Code 2.1.113+ ships as a symlink
// (~/.local/bin/claude) pointing to a versioned Mach-O binary
// (~/.local/share/claude/versions/<X.Y.Z>). Resolving the symlink would make
// basename the version string instead of "claude", breaking agent Identify.
func TestNormalizeExecutablePath_PreservesSymlinkInvocationPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "versioned-target")
	if err := os.WriteFile(target, []byte{}, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "invocation-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := normalizeExecutablePath(link)
	if err != nil {
		t.Fatalf("normalizeExecutablePath: %v", err)
	}
	if got != link {
		t.Fatalf("normalizeExecutablePath(%q) = %q, want %q (symlink preserved)", link, got, link)
	}
}
