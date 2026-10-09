// Package transcriptpath holds the pieces that find and safely open a Claude Code transcript under
// ~/.claude/projects: the project-directory slug, the symlink-resolved root, and the descriptor-relative open.
// It was moved verbatim out of internal/module/agent so the conversation module shares one implementation.
package transcriptpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var (
	// ErrNoTranscript: no usable transcript path (none known, or it fails the
	// containment / file-kind checks).
	ErrNoTranscript = errors.New("no_transcript")
	// ErrFileMissing: a candidate path was derived but nothing is there.
	ErrFileMissing = errors.New("file_missing")
)

// Slug mirrors how Claude Code names a project directory: every
// character outside [A-Za-z0-9] becomes '-' (per UTF-16 code unit, so an
// astral rune yields two).
func Slug(cwd string) string {
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

// Root is the symlink-resolved ~/.claude/projects directory.
func Root(home string) (string, error) {
	return filepath.EvalSymlinks(filepath.Join(home, ".claude", "projects"))
}

// Open opens a path returned by resolveTranscriptPath so that a swap
// between the check and the open (the hook-supplied path is untrusted) cannot
// redirect the read outside ~/.claude/projects.
//
// root is the symlink-resolved projects directory and path the resolved file
// under it. The walk is descriptor-relative from root: every component below
// it is opened with openat(O_NOFOLLOW), so a directory swapped for a symlink
// after the check is refused at that component instead of being followed out
// of root (O_NOFOLLOW on the last component alone would not catch that).
func Open(root, path string) (*os.File, error) {
	rel, ok := strings.CutPrefix(path, root+string(filepath.Separator))
	if !ok || rel == "" {
		return nil, ErrNoTranscript
	}
	parts := strings.Split(rel, string(filepath.Separator))
	dirFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrNoTranscript
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
				return nil, ErrFileMissing
			}
			return nil, ErrNoTranscript
		}
		dirFD = next
	}
	f := os.NewFile(uintptr(dirFD), path)
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, ErrNoTranscript
	}
	return f, nil
}
