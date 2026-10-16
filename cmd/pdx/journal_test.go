package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/journal"
)

func journalCfg(t *testing.T, dataDir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("data_dir = \""+dataDir+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runJournalT(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runJournalCmd(context.Background(), args, func() (string, error) { return cwd, nil }, &out, &errb)
	return code, out.String(), errb.String()
}

func TestJournalPath_PrintsTheWorktreesPurdexFolderOfTheCurrentProject(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cfg := journalCfg(t, dataDir)
	p, err := journal.ProjectOf(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(journal.WorktreeDir(filepath.Join(dataDir, "journal"), p), ".purdex")

	// No worktree yet: an error that says why, and nothing on stdout.
	if code, out, errs := runJournalT(t, repo, "path", "--config", cfg); code != 1 || out != "" || !strings.Contains(errs, "no journal") {
		t.Fatalf("before the first write: code %d, stdout %q, stderr %q", code, out, errs)
	}
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	// The project root and a folder deep inside it name the same worktree.
	for _, cwd := range []string{repo, sub} {
		if code, out, errs := runJournalT(t, cwd, "path", "--config", cfg); code != 0 || out != want+"\n" || errs != "" {
			t.Fatalf("cwd %s: code %d, stdout %q, stderr %q, want %q", cwd, code, out, errs, want)
		}
	}
}

func TestJournalPath_UsageAndErrors(t *testing.T) {
	if code, _, errs := runJournalT(t, t.TempDir()); code != 2 || !strings.Contains(errs, "usage: pdx journal path") {
		t.Fatalf("no subcommand: code %d, %q", code, errs)
	}
	if code, _, errs := runJournalT(t, t.TempDir(), "nope"); code != 2 || !strings.Contains(errs, "usage: pdx journal path") {
		t.Fatalf("unknown subcommand: code %d, %q", code, errs)
	}
	if code, _, _ := runJournalT(t, t.TempDir(), "path", "--config", filepath.Join(t.TempDir(), "x", "config.toml"), "extra"); code != 2 {
		t.Fatalf("extra argument: code %d", code)
	}
}
