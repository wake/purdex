package facts

import "syscall"

// FreeGB is the free space an unprivileged user has on the volume that holds path, in GB (what the gate's own check reads): statfs of
// the path itself, so a work root on a volume the facts' disk list does not hold (a dedicated /mnt/wwd, /Volumes/Jobs) is measured on
// its own volume and not on the nearest listed one.
func FreeGB(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return float64(st.Bavail) * float64(st.Bsize) / (1 << 30), nil
}
