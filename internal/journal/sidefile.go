package journal

import (
	"errors"
	"os"
)

// MaxSideFileBytes bounds tasks.md and roadmap.md: files of the journal branch, which can come from a remote, that the
// reader takes whole.
const MaxSideFileBytes = 1 << 20

// ReadFileBounded reads one of those side files with the guard ReadUnit gives a month file: the file is opened once, checked
// to be a regular file on the descriptor (a symlink is not followed, a FIFO is not waited for) and read through a limit. A file
// that is not there is os.ErrNotExist; one that is refused, over the limit or unreadable is another error.
func ReadFileBounded(path string, limit int64) (string, error) {
	text, reason := readMonth(path, limit, limit)
	if reason == "" {
		return text, nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return "", os.ErrNotExist
	}
	return "", errors.New(reason)
}
