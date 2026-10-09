package workbook

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// docBlocks are the two fenced ```text blocks of the prompt document: the system prompt and the re-write prompt.
func docBlocks(t *testing.T) (system, rewrite string) {
	t.Helper()
	doc, err := os.ReadFile("../../../docs/specs/2026-10-09-session-workbook-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```text\n(.*?)\n```").FindAllSubmatch(doc, -1)
	if len(m) != 2 {
		t.Fatalf("the prompt document has %d text blocks, want 2", len(m))
	}
	return string(m[0][1]), string(m[1][1])
}

// The shipped prompts are the measured ones, byte for byte. Mutation gate: change one character of a prompt → red.
func TestPrompts_EqualTheDocumentBlocks(t *testing.T) {
	system, rewrite := docBlocks(t)
	if SystemPrompt != system {
		t.Errorf("SystemPrompt differs from the document block (%d vs %d bytes)", len(SystemPrompt), len(system))
	}
	if RewritePrompt != rewrite {
		t.Errorf("RewritePrompt differs from the document block (%d vs %d bytes)", len(RewritePrompt), len(rewrite))
	}
	if PromptVersion != 1 {
		t.Errorf("PromptVersion = %d", PromptVersion)
	}
}

func TestWritePromptFiles_BytesModeAndOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workbook")
	// a stale file with the wrong mode and wrong content is replaced
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "prompt-v1.txt")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := WritePromptFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{p.System: SystemPrompt, p.Rewrite: RewritePrompt} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s: err=%v, content differs from the constant", filepath.Base(path), err)
		}
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %o, want 600", filepath.Base(path), fi.Mode().Perm())
		}
	}
	if p.System != stale || filepath.Base(p.Rewrite) != "rewrite-v1.txt" {
		t.Fatalf("paths = %+v", p)
	}
	// the dir itself is private
	if fi, _ := os.Stat(dir); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("dir is %o", fi.Mode().Perm())
	}
}
