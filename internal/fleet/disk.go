//go:build unix

package fleet

import "golang.org/x/sys/unix"

// LocalDisk is the used percentage of this host's DiskMount volume, read as df does: used / (used + available). On Darwin
// it falls back to "/" when the data volume cannot be read.
func LocalDisk() (usedPct float64, ok bool) { return localDisk(goos(), statfsUsed) }

func statfsUsed(path string) (float64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return usedPct(float64(st.Blocks), float64(st.Bfree), float64(st.Bavail))
}
