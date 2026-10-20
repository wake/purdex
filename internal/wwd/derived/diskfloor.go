package derived

import (
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// WorkVolume is the facts' disk that holds root: the longest mount that is root itself or a directory above it (whole path components,
// so /Users/wake/work2 is not under /Users/wake/work).
func WorkVolume(disks []wire.Disk, root string) (wire.Disk, bool) {
	var best wire.Disk
	found := false
	for _, d := range disks {
		m := strings.TrimRight(d.Mount, "/")
		if m != "" && root != d.Mount && !strings.HasPrefix(root, m+"/") {
			continue
		}
		if !found || len(d.Mount) > len(best.Mount) {
			best, found = d, true
		}
	}
	return best, found
}

// DiskLow says whether the work volume has less free space than the floor (GB): the reason a heavy run is not started (spec §3.2
// disk_low). A floor of 0 or less is none. A volume the facts do not list is not a reason, like a path the gate could not read:
// better to run than to refuse on what is not known. Exactly the floor is enough.
func DiskLow(disks []wire.Disk, workRoot string, minGB float64) bool {
	if minGB <= 0 {
		return false
	}
	d, ok := WorkVolume(disks, workRoot)
	return ok && d.FreeGB < minGB
}
