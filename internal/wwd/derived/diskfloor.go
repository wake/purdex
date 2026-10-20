package derived

import (
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// WorkVolume is the facts' disk that holds root: the longest mount that is root itself or a directory above it (whole path components,
// so /Users/wake/work2 is not under /Users/wake/work). It is the advisor's view of the work volume (its disk low alerts); the worker's disk
// floor reads the work root's own volume (facts.FreeGB), which the facts' disk list may not hold.
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

// DiskLow says whether the work volume's free space (GB) is below the floor: the reason a heavy run is not started (spec §3.2
// disk_low). A floor of 0 or less is none. Exactly the floor is enough, as in the gate.
func DiskLow(freeGB, minGB float64) bool { return minGB > 0 && freeGB < minGB }
