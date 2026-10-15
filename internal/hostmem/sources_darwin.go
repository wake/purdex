//go:build darwin

package hostmem

import (
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// SystemSources are the real reads of this host. Mach is loaded once; when that fails the error is kept in MachErr and the
// reader degrades (the caller says so once).
func SystemSources() FastSources {
	m, err := NewMach()
	return FastSources{
		Mach: m, MachErr: err,
		Level: Level,
		// the compressor's swapouts: the same number as vm_stat's "Swapouts" line
		Swapouts: func() (uint64, error) { return unix.SysctlUint64("vm.compressor.swapper.swapouts_total") },
		Load1: func() (float64, error) {
			b, err := unix.SysctlRaw("vm.loadavg")
			if err != nil {
				return 0, err
			}
			if len(b) != 24 { // struct loadavg: three uint32 fixed-point values, padding, then fscale as uint64
				return 0, errors.New("vm.loadavg: unexpected size")
			}
			fscale := binary.NativeEndian.Uint64(b[16:])
			if fscale == 0 {
				return 0, errors.New("vm.loadavg: fscale is 0")
			}
			return float64(binary.NativeEndian.Uint32(b[0:])) / float64(fscale), nil
		},
		NCPU: func() (int, error) {
			n, err := unix.SysctlUint32("hw.ncpu")
			return int(n), err
		},
		PageSize: func() (uint64, error) {
			n, err := unix.SysctlUint32("vm.pagesize")
			return uint64(n), err
		},
		MemBytes: func() (uint64, error) { return unix.SysctlUint64("hw.memsize") },
		UsedMemPct: func() (float64, error) { // kern.memorystatus_level is the free share
			n, err := unix.SysctlUint32("kern.memorystatus_level")
			return float64(100 - min(n, 100)), err
		},
	}
}
