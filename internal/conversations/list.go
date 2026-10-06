// Package conversations reads Claude Code's transcripts under the projects
// root ($HOME/.claude/projects/<slug>/<session>.jsonl): it lists them and
// reads the two parts of each file the conversation index needs, the head
// (cwd, the first entrypoint, the first human prompt) and the tail (titles,
// the last entrypoint). Spec: docs/specs/2026-10-06-conversation-entity-spec.md
// §13.1, §13.5, §13.6. It imports no Nexen package.
package conversations

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// Entry is one top-level transcript.
type Entry struct {
	SessionID string // lowercase
	Path      string
	Size      int64
	MtimeMs   int64
	Inode     uint64
}

// sessionStem matches a transcript's name without ".jsonl": a UUID, in
// either case (the id is stored lowercase).
var sessionStem = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// sessionIDFromName returns the lowercase session id of a transcript file
// name "<uuid>.jsonl", and false for any other name.
func sessionIDFromName(name string) (string, bool) {
	stem, ok := strings.CutSuffix(name, ".jsonl")
	if !ok || !sessionStem.MatchString(stem) {
		return "", false
	}
	return strings.ToLower(stem), true
}

// ListRoot lists <root>/<slug>/<uuid>.jsonl. root and slug dirs are followed
// when symlinked; a transcript that is a symlink or not a regular file is
// skipped. A root that cannot be read returns err (R-4-1). A slug dir that
// cannot be read is skipped and returned in unreadable (R-4-7); so is a slug
// symlink whose target cannot be stat'ed (a dangling link, an unmounted
// volume), since nothing under it can be shown to be gone. A slug with a
// "<uuid>.jsonl" member whose lstat fails other than with "not exist"
// (EACCES, EIO, …), or with a member that vanished while the slug dir
// itself no longer stats as a directory, is returned in unreadable too,
// once, and the entries it did read are kept (addSlug). When one session
// id appears twice, the larger MtimeMs wins (the first one listed on a tie).
// Paths are under root as given, not resolved; entries are sorted by
// SessionID and unreadable in listing order.
func ListRoot(root string) (entries []Entry, unreadable []string, err error) {
	slugs, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, fmt.Errorf("conversations: list projects root: %w", err)
	}
	byID := make(map[string]Entry)
	for _, slug := range slugs {
		dir := filepath.Join(root, slug.Name())
		switch {
		case slug.IsDir():
		case slug.Type()&fs.ModeSymlink != 0:
			fi, err := os.Stat(dir)
			if err != nil {
				unreadable = append(unreadable, dir)
				continue
			}
			if !fi.IsDir() {
				continue
			}
		default:
			continue // a file in the root itself
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			unreadable = append(unreadable, dir)
			continue
		}
		if addSlug(byID, dir, files) {
			unreadable = append(unreadable, dir)
		}
	}
	entries = make([]Entry, 0, len(byID))
	for _, e := range byID {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].SessionID < entries[j].SessionID })
	return entries, unreadable, nil
}

// addSlug adds the transcripts among one slug dir's members to byID (the
// larger MtimeMs wins on a duplicate id) and reports whether the slug is
// unknown, i.e. unreadable for R-4-7: a member's lstat failed other than
// with "not exist", or a member vanished ("not exist") and dir no longer
// stats as a directory. A vanished member proves its transcript removed only
// while its slug dir is still there; when the dir itself went away after
// the listing (renamed, unmounted, removed), every member would vanish. dir
// is stat'ed once, after the loop, and only when a member vanished. The
// other members are added either way.
func addSlug(byID map[string]Entry, dir string, files []fs.DirEntry) (unknown bool) {
	vanished := false
	for _, file := range files {
		e, kind := transcriptEntry(dir, file)
		switch kind {
		case memberUnknown:
			unknown = true
		case memberVanished:
			vanished = true
		case memberTranscript:
			if old, dup := byID[e.SessionID]; !dup || e.MtimeMs > old.MtimeMs {
				byID[e.SessionID] = e
			}
		}
	}
	if vanished && !unknown {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			unknown = true
		}
	}
	return unknown
}

// memberKind is what the lstat of one slug dir member tells.
type memberKind int

const (
	// memberNotTranscript: a name that is not "<uuid>.jsonl", a symlink, a
	// dir or any other non-regular file.
	memberNotTranscript memberKind = iota
	// memberTranscript: a regular "<uuid>.jsonl" file.
	memberTranscript
	// memberVanished: the lstat of a "<uuid>.jsonl" member said it does not
	// exist: removed since the listing, unless its slug dir went away
	// (addSlug tells which).
	memberVanished
	// memberUnknown: the lstat of a "<uuid>.jsonl" member failed otherwise
	// (EACCES, EIO, …), so it may be a live transcript.
	memberUnknown
)

// transcriptEntry classifies one slug dir member, with its Entry when it is
// a transcript.
func transcriptEntry(dir string, file fs.DirEntry) (Entry, memberKind) {
	id, ok := sessionIDFromName(file.Name())
	if !ok {
		return Entry{}, memberNotTranscript
	}
	fi, err := file.Info() // lstat: a symlink is not followed
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Entry{}, memberVanished
	case err != nil:
		return Entry{}, memberUnknown
	case !fi.Mode().IsRegular():
		return Entry{}, memberNotTranscript
	}
	return entryOf(id, filepath.Join(dir, file.Name()), fi), memberTranscript
}

func entryOf(id, path string, fi fs.FileInfo) Entry {
	e := Entry{SessionID: id, Path: path, Size: fi.Size(), MtimeMs: fi.ModTime().UnixMilli()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		e.Inode = st.Ino
	}
	return e
}

// OpenTranscript opens path with O_RDONLY|O_NOFOLLOW|O_NONBLOCK and requires
// a regular file (fstat). It returns the file with its size, mtime and inode
// (and SessionID when the name is "<uuid>.jsonl"), all from the open handle,
// as Nexen's prelude.openTranscript does: O_NOFOLLOW refuses a symlink
// (ELOOP), and O_NONBLOCK lets a FIFO's open return at once so that the
// fstat refuses it instead of the open waiting for a writer.
func OpenTranscript(path string) (*os.File, Entry, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, Entry{}, fmt.Errorf("conversations: open transcript: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Entry{}, fmt.Errorf("conversations: stat transcript: %w", err)
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, Entry{}, fmt.Errorf("conversations: transcript %s is not a regular file (mode %v)", path, fi.Mode())
	}
	id, _ := sessionIDFromName(filepath.Base(path))
	return f, entryOf(id, path, fi), nil
}
