package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	// errNoTranscript: no usable transcript path (none known, or it fails the
	// containment / file-kind checks).
	errNoTranscript = errors.New("no_transcript")
	// errFileMissing: a candidate path was derived but nothing is there.
	errFileMissing = errors.New("file_missing")
)

// transcriptSlug mirrors how Claude Code names a project directory: every
// character outside [A-Za-z0-9] becomes '-' (per UTF-16 code unit, so an
// astral rune yields two).
func transcriptSlug(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// resolveTranscriptPath picks the transcript file for a Claude Code owner and
// proves it is safe to read. The hook-reported TranscriptPath wins; otherwise
// <home>/.claude/projects/<slug(cwd)>/<session_id>.jsonl. The path comes from
// a hook (untrusted), so the order is: Stat (absent -> file_missing),
// EvalSymlinks, containment under the symlink-resolved projects root, then
// regular file with a .jsonl extension (failures -> no_transcript).
func resolveTranscriptPath(owner PaneOwner, home string) (string, error) {
	path := owner.TranscriptPath
	if path == "" {
		if owner.SessionID == "" || owner.Cwd == "" || strings.ContainsAny(owner.SessionID, `/\`) {
			return "", errNoTranscript
		}
		path = filepath.Join(home, ".claude", "projects", transcriptSlug(owner.Cwd), owner.SessionID+".jsonl")
	}
	path = filepath.Clean(path)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", errFileMissing
		}
		return "", errNoTranscript
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errNoTranscript
	}
	root, err := filepath.EvalSymlinks(filepath.Join(home, ".claude", "projects"))
	if err != nil {
		return "", errNoTranscript
	}
	if !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", errNoTranscript
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() || filepath.Ext(resolved) != ".jsonl" {
		return "", errNoTranscript
	}
	return resolved, nil
}
