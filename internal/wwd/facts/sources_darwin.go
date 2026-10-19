//go:build darwin

package facts

import (
	"encoding/binary"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/hostmem"
	"github.com/wake/purdex/internal/wwd/wire"
)

// SystemSources are the real reads of this macOS host: sysctl, Mach (through
// hostmem, no cgo) and statfs. Nothing forks. When Mach cannot be loaded the
// CPU and memory reads return the error and Sample reports it.
func SystemSources() Sources { return SystemSourcesWith(SourceOptions{}) }

// SystemSourcesWith is SystemSources with options. When the owner frameworks
// cannot be loaded the owner stays null rather than failing the sample.
func SystemSourcesWith(opt SourceOptions) Sources {
	mach, machErr := hostmem.NewMach()
	var owner func(ProcSnapshot, ProcContext) *wire.OwnerState
	if opt.Owner != nil {
		if osrc, err := DarwinOwnerSources(); err == nil {
			cfg := *opt.Owner
			if cfg.UID == 0 {
				cfg.UID = os.Getuid() // wwd runs as the owner
			}
			owner = func(s ProcSnapshot, c ProcContext) *wire.OwnerState {
				cur := cfg
				if opt.OwnerMinutes != nil {
					cur.IdleMinutes = opt.OwnerMinutes()
				}
				return BuildOwner(cur, osrc, s, c)
			}
		}
	}
	var procs func(time.Time) (ProcSnapshot, ProcContext, error)
	if pr, err := NewProcReader(DefaultSessionsDir()); err == nil {
		procs = pr.Snapshot
	} else {
		procs = func(time.Time) (ProcSnapshot, ProcContext, error) { return ProcSnapshot{}, ProcContext{}, err }
	}
	return Sources{
		Procs: procs,
		Owner: owner,
		Tools: systemTools(opt.ToolDirs),
		Now:   time.Now,
		CPU: func() (RawCPU, error) {
			if mach == nil {
				return RawCPU{}, machErr
			}
			l1, l5, err := loadavg()
			if err != nil {
				return RawCPU{}, err
			}
			n, err := unix.SysctlUint32("hw.ncpu")
			if err != nil {
				return RawCPU{}, err
			}
			busy, total, err := mach.CPUTicks()
			if err != nil {
				return RawCPU{}, err
			}
			return RawCPU{Load1: l1, Load5: l5, NCPU: int(n), Busy: busy, Total: total}, nil
		},
		Mem: func() (RawMem, error) {
			if mach == nil {
				return RawMem{}, machErr
			}
			total, err := unix.SysctlUint64("hw.memsize")
			if err != nil {
				return RawMem{}, err
			}
			ps, err := unix.SysctlUint32("vm.pagesize")
			if err != nil {
				return RawMem{}, err
			}
			vm, err := mach.VM()
			if err != nil {
				return RawMem{}, err
			}
			m := RawMem{TotalBytes: total, PageSize: uint64(ps), Free: vm.Free, Inactive: vm.Inactive, Pageouts: vm.Pageouts,
				Decompressions: vm.Decompressions, HaveDecompressions: true}
			if lvl, err := hostmem.Level(); err == nil {
				m.Level = lvl
			}
			if so, err := unix.SysctlUint64("vm.compressor.swapper.swapouts_total"); err == nil {
				m.Swapouts = so
			} else {
				m.NoSwapouts = true // the Mach pageouts are still good
			}
			if b, err := swapUsedBytes(); err == nil {
				m.SwapUsedBytes = &b
			}
			if b, err := unix.SysctlUint64("vm.compressor_bytes_used"); err == nil {
				m.CompressorBytes = &b
			}
			return m, nil
		},
		Disks: statfsMounts,
	}
}

// loadavg reads struct loadavg: three uint32 fixed-point values, padding, then
// fscale as uint64.
func loadavg() (l1, l5 float64, err error) {
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, 0, err
	}
	if len(b) != 24 {
		return 0, 0, errors.New("vm.loadavg: unexpected size")
	}
	fscale := binary.NativeEndian.Uint64(b[16:])
	if fscale == 0 {
		return 0, 0, errors.New("vm.loadavg: fscale is 0")
	}
	return float64(binary.NativeEndian.Uint32(b[0:])) / float64(fscale),
		float64(binary.NativeEndian.Uint32(b[4:])) / float64(fscale), nil
}

// swapUsedBytes reads struct xsw_usage: total, avail, used (uint64 each), then
// pagesize and the encrypted flag.
func swapUsedBytes() (uint64, error) {
	b, err := unix.SysctlRaw("vm.swapusage")
	if err != nil {
		return 0, err
	}
	if len(b) < 24 {
		return 0, errors.New("vm.swapusage: unexpected size")
	}
	return binary.NativeEndian.Uint64(b[16:]), nil
}

// statfsMounts reports the volume holding the home directory (on macOS the
// Data volume, where the space actually goes) and the root volume when it is a
// different one.
func statfsMounts() ([]RawDisk, error) {
	paths := []string{"/"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = []string{home, "/"}
	}
	var out []RawDisk
	seen := map[string]bool{}
	var firstErr error
	for _, p := range paths {
		var st unix.Statfs_t
		if err := unix.Statfs(p, &st); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		mount := unix.ByteSliceToString(st.Mntonname[:])
		if seen[mount] {
			continue
		}
		seen[mount] = true
		bs := uint64(st.Bsize)
		out = append(out, RawDisk{Mount: mount, TotalBytes: st.Blocks * bs, FreeBytes: st.Bfree * bs, AvailBytes: st.Bavail * bs})
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}
