package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A pnpm-like script whose interpreter lives in another extra_path directory is probed with the job's PATH, so it answers.
func TestToolProbeRunsWithTheJobPath(t *testing.T) {
	home := t.TempDir()
	pnpmDir, nodeDir := t.TempDir(), t.TempDir()
	writeScript(t, filepath.Join(nodeDir, "fakenode"), "#!/bin/sh\necho 10.23.0\n")
	writeScript(t, filepath.Join(pnpmDir, "pnpm"), "#!/usr/bin/env fakenode\n")
	t.Setenv("PATH", "/usr/bin:/bin") // the daemon's own PATH has no fakenode

	r := NewToolsReader(home, "/usr/bin:/bin", []string{pnpmDir, nodeDir})
	got := r.Get().Pnpm
	if got == nil || *got != "10.23.0" {
		t.Fatalf("pnpm = %v, want 10.23.0", got)
	}
}

// The probe's PATH is ToolPath: the extra directories lead, a shim directory is left out, each directory appears once.
func TestProbeEnvPathMatchesToolPath(t *testing.T) {
	home := t.TempDir()
	shims := filepath.Join(home, ".config", "pdx", "shims")
	if err := os.MkdirAll(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := []string{"/srv/a", shims, "/srv/a", "/srv/b"}
	pathEnv := "/usr/bin:" + shims + ":/srv/a"
	got := probePath(home, pathEnv, extra)
	if want := ToolPath(home, pathEnv, extra); got != want {
		t.Fatalf("probe PATH %q != ToolPath %q", got, want)
	}
	if !strings.HasPrefix(got, "/srv/a:/srv/b:") || strings.Contains(got, "shims") || strings.Count(got, "/srv/a") != 1 {
		t.Errorf("bad probe PATH %q", got)
	}
}

// An empty extra_path entry must not turn into "." (the working directory) at the head of the probe and job PATH.
func TestResolveExtraDirsSkipsEmptyEntries(t *testing.T) {
	home := t.TempDir()
	logged := 0
	got := ResolveExtraDirs(home, []string{"", "  "}, func(string, ...any) { logged++ })
	if len(got) != 0 || logged != 2 {
		t.Fatalf("got %v logged %d, want none and 2", got, logged)
	}
}
