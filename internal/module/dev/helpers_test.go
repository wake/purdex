package dev

import (
	"os"
	"path/filepath"
	"testing"
)

// Test helpers shared by the build and download tests.

// writeThrowawayModule creates a module that builds at ./cmd/pdx and
// returns its root. Shared by build and download tests.
func writeThrowawayModule(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}
