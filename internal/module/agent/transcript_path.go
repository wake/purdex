package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
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

// transcriptRoot is the symlink-resolved ~/.claude/projects directory.
func transcriptRoot(home string) (string, error) {
	return filepath.EvalSymlinks(filepath.Join(home, ".claude", "projects"))
}

// openTranscript opens a path returned by resolveTranscriptPath so that a swap
// between the check and the open (the hook-supplied path is untrusted) cannot
// redirect the read outside ~/.claude/projects.
//
// root is the symlink-resolved projects directory and path the resolved file
// under it. The walk is descriptor-relative from root: every component below
// it is opened with openat(O_NOFOLLOW), so a directory swapped for a symlink
// after the check is refused at that component instead of being followed out
// of root (O_NOFOLLOW on the last component alone would not catch that).
func openTranscript(root, path string) (*os.File, error) {
	rel, ok := strings.CutPrefix(path, root+string(filepath.Separator))
	if !ok || rel == "" {
		return nil, errNoTranscript
	}
	parts := strings.Split(rel, string(filepath.Separator))
	dirFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errNoTranscript
	}
	for i, p := range parts {
		last := i == len(parts)-1
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if !last {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(dirFD, p, flags, 0)
		unix.Close(dirFD)
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return nil, errFileMissing
			}
			return nil, errNoTranscript
		}
		dirFD = next
	}
	f := os.NewFile(uintptr(dirFD), path)
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errNoTranscript
	}
	return f, nil
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
	root, err := transcriptRoot(home)
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
