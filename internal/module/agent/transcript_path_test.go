package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptSlug(t *testing.T) {
	// Fixtures are real ~/.claude/projects directory names and the cwd they
	// came from (dot, hyphen runs, worktree dirs).
	cases := map[string]string{
		"/Users/wake/Workspace/.system":                                               "-Users-wake-Workspace--system",
		"/Users/wake/Workspace/tangency/csp-plugin/.claude/worktrees/hear3-migration": "-Users-wake-Workspace-tangency-csp-plugin--claude-worktrees-hear3-migration",
		"/Users/wake/Workspace/wake/purdex":                                           "-Users-wake-Workspace-wake-purdex",
		"/tmp/a_b c":                                                                  "-tmp-a-b-c",
		"/tmp/中文":                                                                     "-tmp---",
	}
	for cwd, want := range cases {
		if got := transcriptSlug(cwd); got != want {
			t.Errorf("slug(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func projectsDir(t *testing.T, home string) string {
	t.Helper()
	d := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTranscriptPath(t *testing.T) {
	home := t.TempDir()
	proj := projectsDir(t, home)
	real, _ := filepath.EvalSymlinks(proj)

	// provenance path wins
	prov := filepath.Join(proj, "-x", "prov.jsonl")
	writeFile(t, prov)
	got, err := resolveTranscriptPath(PaneOwner{TranscriptPath: prov, SessionID: "other", Cwd: "/x"}, home)
	if err != nil || got != filepath.Join(real, "-x", "prov.jsonl") {
		t.Fatalf("got %q, %v", got, err)
	}

	// slug fallback
	fb := filepath.Join(proj, "-Users-me-app", "sid1.jsonl")
	writeFile(t, fb)
	got, err = resolveTranscriptPath(PaneOwner{SessionID: "sid1", Cwd: "/Users/me/app"}, home)
	if err != nil || got != filepath.Join(real, "-Users-me-app", "sid1.jsonl") {
		t.Fatalf("got %q, %v", got, err)
	}

	// neither
	if _, err = resolveTranscriptPath(PaneOwner{}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("err = %v", err)
	}
	// session id without cwd cannot be slugged
	if _, err = resolveTranscriptPath(PaneOwner{SessionID: "s"}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("err = %v", err)
	}
	// missing file inside projects
	if _, err = resolveTranscriptPath(PaneOwner{TranscriptPath: filepath.Join(proj, "nope.jsonl")}, home); !errors.Is(err, errFileMissing) {
		t.Fatalf("err = %v", err)
	}
	// slug that does not exist
	if _, err = resolveTranscriptPath(PaneOwner{SessionID: "zz", Cwd: "/no/where"}, home); !errors.Is(err, errFileMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveTranscriptPathRejectsEscapes(t *testing.T) {
	home := t.TempDir()
	proj := projectsDir(t, home)
	outside := t.TempDir()

	// outside projects entirely
	out := filepath.Join(outside, "secret.jsonl")
	writeFile(t, out)
	if _, err := resolveTranscriptPath(PaneOwner{TranscriptPath: out}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("outside: %v", err)
	}
	// symlink inside projects pointing out
	link := filepath.Join(proj, "link.jsonl")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTranscriptPath(PaneOwner{TranscriptPath: link}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("symlink: %v", err)
	}
	// ".." traversal
	if _, err := resolveTranscriptPath(PaneOwner{TranscriptPath: filepath.Join(proj, "..", "..", "x", "..", filepath.Base(outside), "secret.jsonl")}, home); err == nil {
		t.Fatal("traversal accepted")
	}
	// sibling dir sharing the projects prefix
	sib := filepath.Join(home, ".claude", "projects-evil", "a.jsonl")
	writeFile(t, sib)
	if _, err := resolveTranscriptPath(PaneOwner{TranscriptPath: sib}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("prefix sibling: %v", err)
	}
	// non-jsonl inside projects
	txt := filepath.Join(proj, "a.txt")
	writeFile(t, txt)
	if _, err := resolveTranscriptPath(PaneOwner{TranscriptPath: txt}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("ext: %v", err)
	}
	// directory named *.jsonl
	dir := filepath.Join(proj, "d.jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTranscriptPath(PaneOwner{TranscriptPath: dir}, home); !errors.Is(err, errNoTranscript) {
		t.Fatalf("dir: %v", err)
	}
	// a session id that tries to climb out
	if _, err := resolveTranscriptPath(PaneOwner{SessionID: "../../../../" + filepath.Base(outside) + "/secret", Cwd: "/c"}, home); err == nil {
		t.Fatal("session id traversal accepted")
	}
}
