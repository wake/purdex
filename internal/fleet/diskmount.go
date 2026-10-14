package fleet

import (
	"errors"
	"runtime"
)

func goos() string { return runtime.GOOS }

// DiskMount is the volume the fleet judges disk use on, for the probe script and the daemon's own reading alike (spec §5):
// Darwin's data volume (the script falls back to "/"), "/" everywhere else.
func DiskMount(goos string) string {
	if goos == "darwin" {
		return "/System/Volumes/Data"
	}
	return "/"
}

// usedPct is df's figure from statfs block counts.
func usedPct(blocks, free, avail float64) (float64, error) {
	used := blocks - free
	if blocks <= 0 || used < 0 || used+avail <= 0 {
		return 0, errors.New("empty filesystem")
	}
	return 100 * used / (used + avail), nil
}

// localDisk reads DiskMount(goos) with statfs; on Darwin a failed read of the data volume falls back to "/".
func localDisk(goos string, statfs func(path string) (float64, error)) (float64, bool) {
	if v, err := statfs(DiskMount(goos)); err == nil {
		return v, true
	}
	if goos == "darwin" {
		if v, err := statfs("/"); err == nil {
			return v, true
		}
	}
	return 0, false
}
