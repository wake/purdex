package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/transcriptpath"
)

var (
	// errNoTranscript: no usable transcript path (none known, or it fails the
	// containment / file-kind checks).
	errNoTranscript = transcriptpath.ErrNoTranscript
	// errFileMissing: a candidate path was derived but nothing is there.
	errFileMissing = transcriptpath.ErrFileMissing
)

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
		path = filepath.Join(home, ".claude", "projects", transcriptpath.Slug(owner.Cwd), owner.SessionID+".jsonl")
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
	root, err := transcriptpath.Root(home)
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
