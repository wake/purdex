//go:build unix

package journal

import (
	"os"
	"syscall"
)

// openMonthFile opens a month file for reading without following a symlink (a month file that is one is refused) and
// without waiting for a writer (a FIFO opens at once and is refused by the check on the descriptor).
func openMonthFile(path string) (monthFile, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}
