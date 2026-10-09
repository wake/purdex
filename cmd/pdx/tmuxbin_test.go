package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeTmux writes an executable `tmux` that prints one fixed line, into its own directory.
func fakeTmux(t *testing.T, line string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+line+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// #2123: a pane an ssh login (no login shell) started has PATH=/usr/bin:/bin, so the hook's `tmux` was "not found" and
// its event carried no session name. The CLI finds tmux in the usual install locations when PATH does not have it.
func TestTmuxExecutable_FallsBackToTheUsualInstallLocations(t *testing.T) {
	fallback := fakeTmux(t, "$1|from-fallback")
	t.Setenv("PATH", t.TempDir()) // nothing on it
	orig := tmuxFallbackPaths
	tmuxFallbackPaths = []string{filepath.Join(t.TempDir(), "absent", "tmux"), fallback}
	defer func() { tmuxFallbackPaths = orig }()

	if got := tmuxExecutable(); got != fallback {
		t.Fatalf("tmuxExecutable = %q, want the fallback %q", got, fallback)
	}
	id, name := queryTmuxSessionInfo()
	if id != "$1" || name != "from-fallback" {
		t.Fatalf("queryTmuxSessionInfo = (%q, %q), want the fallback tmux's answer", id, name)
	}
	if got := queryTmuxSession(); got != "$1|from-fallback" {
		t.Fatalf("queryTmuxSession = %q", got)
	}
}

// ... and a tmux that IS on PATH wins (the user's own, the one the shell would run).
func TestTmuxExecutable_PathWins(t *testing.T) {
	onPath := fakeTmux(t, "$2|from-path")
	t.Setenv("PATH", filepath.Dir(onPath))
	orig := tmuxFallbackPaths
	tmuxFallbackPaths = []string{fakeTmux(t, "$3|from-fallback")}
	defer func() { tmuxFallbackPaths = orig }()

	if got := tmuxExecutable(); got != onPath {
		t.Fatalf("tmuxExecutable = %q, want %q", got, onPath)
	}
	if _, name := queryTmuxSessionInfo(); name != "from-path" {
		t.Fatalf("name = %q", name)
	}
}

// No tmux anywhere: the bare name is returned, so the call fails exactly as before (the hook swallows it).
func TestTmuxExecutable_NothingFoundKeepsTheBareName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	orig := tmuxFallbackPaths
	tmuxFallbackPaths = []string{filepath.Join(t.TempDir(), "absent", "tmux")}
	defer func() { tmuxFallbackPaths = orig }()
	if got := tmuxExecutable(); got != "tmux" {
		t.Fatalf("tmuxExecutable = %q, want the bare name", got)
	}
	if id, name := queryTmuxSessionInfo(); id != "" || name != "" {
		t.Fatalf("(%q, %q), want empty", id, name)
	}
}
