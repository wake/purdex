package journal

import (
	"errors"
	"os"
)

// afterLstatHook runs between the check and the open of openLstatChecked; a test puts a race there.
var afterLstatHook func()

// openLstatChecked opens path for reading only if it is a regular file that Lstat reports without following a symlink, and
// only if what was opened is that same file (so a path swapped between the check and the open is refused). It is how a
// platform without O_NOFOLLOW opens a month file; it needs no build tag, so the rule is tested everywhere.
func openLstatChecked(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if afterLstatHook != nil {
		afterLstatHook()
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		if err == nil {
			err = errors.New("changed while opening")
		}
		return nil, err
	}
	return f, nil
}
