package convfeed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/transcriptpath"
)

// ErrNotFound: no source yields a transcript for the session (a containment failure is the same answer: the path is
// never leaked).
var ErrNotFound = errors.New("convfeed: conversation not found")

const (
	// DefaultLookupDirs bounds the slug directories the last-resort lookup visits.
	DefaultLookupDirs = 2000
	// DefaultLookupTime bounds the last-resort lookup.
	DefaultLookupTime = time.Second
)

// Owner is a live pane the agent module confirmed to run the session.
type Owner struct {
	TranscriptPath string // reported by the pane's hook; may be empty
	Status         string // the pane's light: running | waiting | idle | error
	SeenAt         int64  // when the pane was last seen; the largest wins among several
}

// OwnerLookup finds the confirmed live panes of a session. An error means "could not tell", not "none".
type OwnerLookup interface {
	LiveSessions(sessionID string) ([]Owner, error)
}

// IndexLookup finds the transcript path the conversation index recorded for a session.
type IndexLookup interface {
	TranscriptPath(ctx context.Context, sessionID string) (path string, ok bool, err error)
}

// Resolver turns a session id into an open transcript.
type Resolver struct {
	Home   string // the home directory holding .claude/projects
	Owners OwnerLookup
	Index  IndexLookup

	MaxLookupDirs int           // zero: DefaultLookupDirs
	LookupTime    time.Duration // zero: DefaultLookupTime

	// afterCheck runs between a candidate's containment check and its open (tests swap a directory there).
	afterCheck func()
	// onDir runs before each directory the lookup opens (tests cancel the request mid-scan).
	onDir func()
}

// Resolve returns the current Source of the session, with its file open: the caller closes Source.Closer. The order
// (spec §8.2): the live pane's reported path, the conversation index row, then a bounded lookup of
// <slug>/<session_id>.jsonl across the projects root. A candidate that is missing or fails containment is skipped.
// An owner lookup that errors is not an error: the answer carries Status "unknown" and the search goes on.
func (r *Resolver) Resolve(ctx context.Context, sessionID string) (Source, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return Source{}, ErrNotFound
	}
	root, err := transcriptpath.Root(r.Home)
	if err != nil {
		return Source{}, ErrNotFound
	}

	var owner *Owner
	status := "ended"
	if r.Owners != nil {
		owners, oerr := r.Owners.LiveSessions(sessionID)
		switch {
		case oerr != nil:
			status = "unknown"
		case len(owners) > 0:
			o := owners[0]
			for _, c := range owners[1:] {
				if c.SeenAt >= o.SeenAt {
					o = c
				}
			}
			owner = &o
			status = o.Status
		}
	}

	finish := func(f *os.File) (Source, error) {
		identity, ierr := identityOf(f)
		if ierr != nil {
			f.Close()
			return Source{}, ErrNotFound
		}
		s := Source{File: osFile{f}, Identity: identity, Closer: f, Status: status}
		if owner != nil {
			s.Live, s.Backend = true, "terminal"
		}
		return s, nil
	}

	if owner != nil && owner.TranscriptPath != "" {
		if f := r.openCandidate(root, owner.TranscriptPath); f != nil {
			return finish(f)
		}
	}
	if r.Index != nil {
		p, ok, ierr := r.Index.TranscriptPath(ctx, sessionID)
		if ierr == nil && ok && p != "" {
			if f := r.openCandidate(root, p); f != nil {
				return finish(f)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	if f, _ := r.lookup(ctx, root, sessionID); f != nil {
		return finish(f)
	}
	if err := ctx.Err(); err != nil { // the request ended during the scan: that is not "no such conversation"
		return Source{}, err
	}
	return Source{}, ErrNotFound
}

// openCandidate checks a path that came from outside (a hook, the index): it must exist, resolve to a regular .jsonl
// file under the symlink-resolved root; then it is opened by the descriptor-relative walk, so a directory swapped for
// a symlink after the check is refused at that component. nil when the candidate is not usable.
func (r *Resolver) openCandidate(root, path string) *os.File {
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil
	}
	if !strings.HasPrefix(resolved, root+string(filepath.Separator)) || filepath.Ext(resolved) != ".jsonl" {
		return nil
	}
	if r.afterCheck != nil {
		r.afterCheck()
	}
	f, err := transcriptpath.Open(root, resolved)
	if err != nil {
		return nil
	}
	return f
}

// lookup searches <slug>/<session_id>.jsonl across the root's slug directories (depth 1 only), bounded by directory
// count, time and the context; unreadable directories are skipped. Only directories count toward the bound (files and
// sockets in the root cost nothing). It also returns how many directories it opened (tests check the bound; the
// directory order is the file system's).
func (r *Resolver) lookup(ctx context.Context, root, sessionID string) (*os.File, int) {
	maxDirs, limit := r.MaxLookupDirs, r.LookupTime
	if maxDirs <= 0 {
		maxDirs = DefaultLookupDirs
	}
	if limit <= 0 {
		limit = DefaultLookupTime
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	d, err := os.Open(root)
	if err != nil {
		return nil, 0
	}
	defer d.Close()
	visited := 0
	for {
		entries, err := d.ReadDir(256)
		for _, de := range entries {
			if !de.IsDir() {
				continue
			}
			if ctx.Err() != nil || visited >= maxDirs {
				return nil, visited
			}
			visited++
			if r.onDir != nil {
				r.onDir()
			}
			f, oerr := transcriptpath.Open(root, filepath.Join(root, de.Name(), sessionID+".jsonl"))
			if oerr == nil {
				return f, visited
			}
		}
		if err != nil { // io.EOF, or an unreadable directory: nothing more to visit
			return nil, visited
		}
	}
}

// osFile adapts an *os.File to File.
type osFile struct{ *os.File }

func (f osFile) Size() (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// identityOf is device:inode of the open file: it changes when the path now resolves to another file.
func identityOf(f *os.File) (string, error) {
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("convfeed: no inode on this platform")
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}

var _ io.Closer = (*os.File)(nil)
