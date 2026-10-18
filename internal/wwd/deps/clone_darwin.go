package deps

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// cloneAt clones the tree srcParent/name to dstParent/name with clonefileat(2): both parents are open directories, so neither side is a
// path that can be swapped for a link while it runs, and CLONE_NOFOLLOW keeps a link at the top a link. errNoCloneAt says it cannot be
// done here (a file system without clones, two volumes) and the caller copies another way.
func cloneAt(srcParent, dstParent *os.File, name string) error {
	err := unix.Clonefileat(int(srcParent.Fd()), name, int(dstParent.Fd()), name, unix.CLONE_NOFOLLOW)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOSYS) {
		return errNoCloneAt
	}
	return err
}
