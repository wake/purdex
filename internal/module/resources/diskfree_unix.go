//go:build unix

package resourcesmod

import "syscall"

// statFreeBytes is the space available to an unprivileged user on the volume of path.
func statFreeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
