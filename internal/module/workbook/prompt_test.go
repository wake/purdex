package workbook

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// docBlocks are the fenced ```text blocks of a prompt document, in order.
func docBlocks(t *testing.T, file string, want int) []string {
	t.Helper()
	doc, err := os.ReadFile("../../../docs/specs/" + file)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```text\n(.*?)\n```").FindAllSubmatch(doc, -1)
	if len(m) != want {
		t.Fatalf("%s has %d text blocks, want %d", file, len(m), want)
	}
	out := make([]string, len(m))
	for i := range m {
		out[i] = string(m[i][1])
	}
	return out
}

// The shipped prompts are the measured ones, byte for byte. Mutation gate: change one character of a prompt → red.
func TestPrompts_EqualTheDocumentBlocks(t *testing.T) {
	b := docBlocks(t, "2026-10-10-session-workbook-prompt-v2.md", 3)
	for name, pair := range map[string][2]string{"SystemPrompt": {SystemPrompt, b[0]}, "RewritePrompt": {RewritePrompt, b[1]}, "RefreshPrompt": {RefreshPrompt, b[2]}} {
		if pair[0] != pair[1] {
			t.Errorf("%s differs from the document block (%d vs %d bytes)", name, len(pair[0]), len(pair[1]))
		}
	}
	if PromptVersion != 2 {
		t.Errorf("PromptVersion = %d", PromptVersion)
	}
}

// The v1 prompts stay what they were: the history of what prompt_ver 1 entries were written with.
func TestPromptsV1_EqualTheirDocumentBlocks(t *testing.T) {
	b := docBlocks(t, "2026-10-09-session-workbook-prompt.md", 2)
	if SystemPromptV1 != b[0] || RewritePromptV1 != b[1] {
		t.Error("a v1 prompt differs from its document")
	}
}

// A workbook path that is a symlink is refused, and its target is left exactly as it was.
// Mutation gate: drop the Lstat check → red.
func TestWritePromptFiles_RefusesASymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "workbook")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePromptFiles(link); err == nil {
		t.Fatal("a symlinked directory was accepted")
	}
	fi, _ := os.Stat(target)
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("the target's mode changed to %o", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Errorf("something was written into the target: %d entries", len(ents))
	}
}

func TestWritePromptFiles_BytesModeAndOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workbook")
	// a stale file with the wrong mode and wrong content is replaced
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "prompt-v2.txt")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := WritePromptFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{p.System: SystemPrompt, p.Rewrite: RewritePrompt, p.Refresh: RefreshPrompt} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s: err=%v, content differs from the constant", filepath.Base(path), err)
		}
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %o, want 600", filepath.Base(path), fi.Mode().Perm())
		}
	}
	if filepath.Base(p.System) != "prompt-v2.txt" || filepath.Base(p.Rewrite) != "rewrite-v2.txt" || filepath.Base(p.Refresh) != "refresh-v2.txt" {
		t.Fatalf("paths = %+v", p)
	}
	// the dir itself is private
	if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("dir is %o", fi.Mode().Perm())
	}
}
