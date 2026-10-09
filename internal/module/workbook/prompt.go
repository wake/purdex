package workbook

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// PromptVersion is stored with every entry (prompt_ver). A change to either prompt below is a new version: bump it and
// the file names, never edit a shipped version in place.
const PromptVersion = 2

// SystemPrompt (the turn prompt, with the todo rules), RewritePrompt and RefreshPrompt are the three measured blocks of
// docs/specs/2026-10-10-session-workbook-prompt-v2.md, byte for byte (a test compares them to the document's fenced
// blocks). They are embedded files because the prompts themselves contain backticks.
//
//go:embed prompts/system-v2.txt
var SystemPrompt string

//go:embed prompts/rewrite-v2.txt
var RewritePrompt string

//go:embed prompts/refresh-v2.txt
var RefreshPrompt string

// SystemPromptV1 and RewritePromptV1 are prompt_ver 1 (docs/specs/2026-10-09-session-workbook-prompt.md), kept as the
// history of what entries with prompt_ver 1 were written with; nothing sends them.
//
//go:embed prompts/system-v1.txt
var SystemPromptV1 string

//go:embed prompts/rewrite-v1.txt
var RewritePromptV1 string

// PromptFiles are where the prompts were written.
type PromptFiles struct {
	System, Rewrite, Refresh string
}

// WritePromptFiles writes the three prompts under dir (created 0700) as owner-only files, replacing a stale file. The
// process reads them by path, so a file left from another version or with another mode must not survive.
func WritePromptFiles(dir string) (PromptFiles, error) {
	// A path that is already there must be a real directory: a symlink would send the chmod and the writes below to
	// wherever it points.
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		return PromptFiles{}, fmt.Errorf("workbook prompts: %s is not a directory (a symlink is refused)", filepath.Base(dir))
	} else if err != nil && !os.IsNotExist(err) {
		return PromptFiles{}, fmt.Errorf("workbook prompts: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return PromptFiles{}, fmt.Errorf("workbook prompts: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return PromptFiles{}, fmt.Errorf("workbook prompts: %w", err)
	}
	p := PromptFiles{
		System:  filepath.Join(dir, fmt.Sprintf("prompt-v%d.txt", PromptVersion)),
		Rewrite: filepath.Join(dir, fmt.Sprintf("rewrite-v%d.txt", PromptVersion)),
		Refresh: filepath.Join(dir, fmt.Sprintf("refresh-v%d.txt", PromptVersion)),
	}
	for path, text := range map[string]string{p.System: SystemPrompt, p.Rewrite: RewritePrompt, p.Refresh: RefreshPrompt} {
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
