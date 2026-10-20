package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Linux disk counters, as text and a directory, with no build tag so every platform's tests can feed them fixtures.

// diskSectorBytes is the size of a sector in /proc/diskstats: always 512 bytes, whatever the device's own block size.
const diskSectorBytes = 512

// ParseDiskstats reads /proc/diskstats: one RawDiskIO per line (every device, partitions and virtual ones included; PhysicalDiskIO
// narrows them). The fields after major, minor and the name are reads completed, reads merged, sectors read, ms reading, writes
// completed, writes merged, sectors written; the rest are not used. A line with fewer fields than that is skipped.
func ParseDiskstats(text string) ([]RawDiskIO, error) {
	var out []RawDiskIO
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		var n [4]uint64
		for i, idx := range [4]int{3, 5, 7, 9} { // reads, sectors read, writes, sectors written
			v, err := strconv.ParseUint(f[idx], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("diskstats: %s: field %d: %w", f[2], idx, err)
			}
			n[i] = v
		}
		out = append(out, RawDiskIO{Device: f[2], ReadOps: n[0], ReadBytes: n[1] * diskSectorBytes, WriteOps: n[2], WriteBytes: n[3] * diskSectorBytes})
	}
	return out, nil
}

// PhysicalDiskIO keeps the devices that are physical disks: those with a device link under sysBlock (/sys/block/<name>/device). A
// partition is not listed under /sys/block at all; loop, ram, zram, device-mapper and md devices are, but have no device link, so a
// volume on a disk is not counted next to the disk. An error when sysBlock cannot be read: without it a disk cannot be told from the
// volumes on it, and guessing from the names would count a disk twice.
func PhysicalDiskIO(all []RawDiskIO, sysBlock string) ([]RawDiskIO, error) {
	if _, err := os.Stat(sysBlock); err != nil {
		return nil, err
	}
	var out []RawDiskIO
	for _, d := range all {
		if _, err := os.Stat(filepath.Join(sysBlock, d.Device, "device")); err == nil {
			out = append(out, d)
		}
	}
	return out, nil
}
