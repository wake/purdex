package workbook

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// PromptVersion is stored with every entry (prompt_ver). A change to either prompt below is a new version: bump it and
// the file names, never edit a shipped version in place.
const PromptVersion = 1

// SystemPrompt and RewritePrompt are the measured prompts of docs/specs/2026-10-09-session-workbook-prompt.md, byte for
// byte (a test compares them to the document's fenced blocks). They are embedded files because the prompt itself
// contains backticks.
//
//go:embed prompts/system-v1.txt
var SystemPrompt string

//go:embed prompts/rewrite-v1.txt
var RewritePrompt string

// PromptFiles are where the prompts were written for `claude --system-prompt-file`.
type PromptFiles struct {
	System, Rewrite string
}

// WritePromptFiles writes both prompts under dir (created 0700) as owner-only files, replacing a stale file. The
// process reads them by path, so a file left from another version or with another mode must not survive.
func WritePromptFiles(dir string) (PromptFiles, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return PromptFiles{}, fmt.Errorf("workbook prompts: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return PromptFiles{}, fmt.Errorf("workbook prompts: %w", err)
	}
	p := PromptFiles{
		System:  filepath.Join(dir, fmt.Sprintf("prompt-v%d.txt", PromptVersion)),
		Rewrite: filepath.Join(dir, fmt.Sprintf("rewrite-v%d.txt", PromptVersion)),
	}
	for path, text := range map[string]string{p.System: SystemPrompt, p.Rewrite: RewritePrompt} {
		if err := writeOwnerOnly(path, text); err != nil {
			return PromptFiles{}, err
		}
	}
	return p, nil
}

// writeOwnerOnly replaces path with text, mode 0600, through a temp file in the same directory so a reader never sees
// half a prompt.
func writeOwnerOnly(path, text string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prompt-*")
	if err != nil {
		return fmt.Errorf("workbook prompts: %w", err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("workbook prompts: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.WriteString(text); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("workbook prompts: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("workbook prompts: %w", err)
	}
	return nil
}
