package snapshot

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// OutsideLinkError says that a snapshot holds a symlink that leads out of its workspace (an absolute target, one that climbs out, or a chain
// that does). It is the snapshot's content that is wrong, not the host that unpacked it: the same snapshot fails on every host, so the job
// must be rejected, not retried as the host's trouble (#3841).
type OutsideLinkError struct {
	Path, Target string // the link, relative to the workspace, and what it points at
}

func (e *OutsideLinkError) Error() string {
	return fmt.Sprintf("snapshot: symlink %q -> %q leads out of the workspace", e.Path, e.Target)
}

// FindOutsideLink walks the workspace dir (a checkout or an unpacked tar, .git left alone) and returns the first symlink that leads out
// of it, or nil.
func FindOutsideLink(dir string) (*OutsideLinkError, error) {
	links := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		links[filepath.ToSlash(rel)] = target
		return nil
	})
	if err != nil {
		return nil, err
	}
	for l, t := range links {
		if strings.HasPrefix(t, "/") || linkLeaves(links, l) {
			return &OutsideLinkError{Path: l, Target: t}, nil
		}
	}
	return nil, nil
}
