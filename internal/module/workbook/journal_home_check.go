package workbook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errUnsafeHome says a directory on the way to a unit's files is a symlink or not a directory.
var errUnsafeHome = errors.New("a directory of the unit's journal home is a symlink or not a directory")

// verify is the guard of the unit's home: .purdex, journal, the unit's folder, tasks, and the folders above the roadmap must
// each be a real directory (a missing one is fine: nothing is there to follow). The files themselves are guarded where they
// are opened (O_NOFOLLOW on the last component); this covers the ANCESTORS, which a path-based open follows. The journal
// branch can come from a remote, and a repository can hold a symlink where a folder should be, which would send a read, or
// a write of the drainer, outside the worktree. The API reads and the drainer writes both call it, before they touch a file.
//
// It checks the path at one moment: the worktree is changed only by the daemon's own git operations, so this stops what a
// repository contains, not a swap made between the check and the open.
func (h home) verify() error {
	for _, p := range []struct {
		path string
		leaf bool
	}{{h.journalDir, true}, {h.tasksFile, false}, {h.roadmapFile, false}} {
		if err := checkChain(h.wt, p.path, p.leaf); err != nil {
			return err
		}
	}
	return nil
}

// checkChain looks at every directory from root (not itself) down to target, target's last component only when leaf.
func checkChain(root, target string, leaf bool) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s is not under %s", errUnsafeHome, target, root)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if !leaf {
		parts = parts[:len(parts)-1]
	}
	p := root
	for _, part := range parts {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil // nothing below a missing folder exists
		}
		if err != nil {
			return fmt.Errorf("%w: %v", errUnsafeHome, err)
		}
		if !st.IsDir() { // Lstat: a symlink is not a directory
			return fmt.Errorf("%w: %s", errUnsafeHome, p)
		}
	}
	return nil
}
