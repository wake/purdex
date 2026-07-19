package execution

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// This file implements M0 pointer-first artifact capture (spec §6): a diff taken
// relative to head_at_start, summarised as {files, add, del} with a daemon-scoped
// opaque pointer. The blob is never inlined — meta is a summary only; the full
// diff is retrieved later via the deeplink. Diff capture is provider-agnostic
// (claude/codex identical): it only shells out to git.

// Artifact is the Purdex-side pointer-first artifact reference (m0-contract §5).
// It mirrors the wire shape the dispatch report layer marshals; execution stays
// free of any dispatch import (the adapter maps this onto the report Artifact).
type Artifact struct {
	Kind    string
	Pointer string
	Meta    map[string]any
}

// DiffMeta is the diff summary carried in an artifact's meta (spec §6.1). It is
// intentionally a summary, never the patch text.
type DiffMeta struct {
	Files int
	Add   int
	Del   int
}

// DiffPointer builds the daemon-scoped opaque diff ref (spec §6.1). Ploom stores
// it verbatim and never parses it; the full diff is fetched via the deeplink.
func DiffPointer(daemonID, executionID string) string {
	return fmt.Sprintf("pdx://%s/execution/%s/diff", daemonID, executionID)
}

// TranscriptPointer builds the daemon-scoped opaque transcript ref (spec §6). It
// points at the already-recorded session transcript, not a re-uploaded blob.
func TranscriptPointer(daemonID, executionID string) string {
	return fmt.Sprintf("pdx://%s/execution/%s/transcript", daemonID, executionID)
}

// GitDiff computes the diff summary of repoPath's current working tree relative
// to headAtStart (spec §6). It counts both tracked changes (git diff --numstat)
// and untracked new files (each counted as additions), so agent-created files
// are reflected. Binary files contribute to the file count but not line counts.
func GitDiff(ctx context.Context, repoPath, headAtStart string) (DiffMeta, error) {
	var meta DiffMeta

	// (1) tracked changes vs the base commit (staged + unstaged).
	out, err := gitStdout(ctx, repoPath, "diff", "--numstat", headAtStart)
	if err != nil {
		return DiffMeta{}, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		meta.Files++
		meta.Add += parseNumstat(fields[0])
		meta.Del += parseNumstat(fields[1])
	}

	// (2) untracked new files, counted as additions.
	others, err := gitStdout(ctx, repoPath, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return DiffMeta{}, err
	}
	for _, name := range strings.Split(others, "\x00") {
		if name == "" {
			continue
		}
		meta.Files++
		meta.Add += countLines(ctx, repoPath, name)
	}

	return meta, nil
}

// BuildDiffArtifact captures the diff relative to headAtStart and wraps it as a
// pointer-first diff artifact (spec §6). meta is the {files,add,del} summary; the
// pointer is daemon-scoped and opaque.
func BuildDiffArtifact(ctx context.Context, daemonID, executionID, repoPath, headAtStart string) (Artifact, error) {
	meta, err := GitDiff(ctx, repoPath, headAtStart)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		Kind:    "diff",
		Pointer: DiffPointer(daemonID, executionID),
		Meta: map[string]any{
			"files": meta.Files,
			"add":   meta.Add,
			"del":   meta.Del,
		},
	}, nil
}

// TranscriptArtifact wraps the already-recorded session transcript as a
// pointer-first artifact (spec §6). It carries no meta blob — only the pointer.
func TranscriptArtifact(daemonID, executionID string) Artifact {
	return Artifact{Kind: "transcript", Pointer: TranscriptPointer(daemonID, executionID)}
}

// gitStdout runs a git subcommand in repoPath and returns stdout. On failure the
// stderr output is folded into the error for diagnosis.
func gitStdout(ctx context.Context, repoPath string, args ...string) (string, error) {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// parseNumstat parses one numstat count. "-" (binary file) yields 0.
func parseNumstat(s string) int {
	if s == "-" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// countLines counts the newline-terminated lines of an untracked file. It is a
// best-effort read: an error contributes 0 additions (the file still counts
// toward Files). ctx is unused (a plain file read) but kept for symmetry.
func countLines(_ context.Context, repoPath, name string) int {
	data, err := os.ReadFile(filepath.Join(repoPath, name))
	if err != nil {
		return 0
	}
	return bytes.Count(data, []byte("\n"))
}
