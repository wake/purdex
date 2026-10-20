//go:build darwin

package facts

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
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
		Boot:  bootTime,
		Self:  selfSource(),
		Tools: systemTools(opt.ToolDirs),
		Now:   time.Now,
		CPU: func() (RawCPU, error) {
			if mach == nil {
				return RawCPU{}, machErr
			}
			l1, l5, l15, err := loadavg()
			if err != nil {
				return RawCPU{}, err
			}
			n, err := unix.SysctlUint32("hw.ncpu")
			if err != nil {
				return RawCPU{}, err
			}
			ticks, err := mach.CPUCoreTicks()
			if err != nil {
				return RawCPU{}, err
			}
			busy, total := hostmem.SumTicks(ticks)
			raw := RawCPU{Load1: l1, Load5: l5, Load15: &l15, NCPU: int(n), Busy: busy, Total: total, HaveSplit: true,
				Cores: make([]RawCore, len(ticks)), Kinds: DarwinCoreKinds(len(ticks))}
			for i, t := range ticks {
				c := RawCore{ID: i, User: t.User + t.Nice, System: t.System, Idle: t.Idle}
				c.Total = c.User + c.System + c.Idle
				raw.Cores[i] = c
				raw.User += c.User
				raw.System += c.System
			}
			return raw, nil
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
			if total, used, err := swapUsage(); err == nil {
				m.SwapUsedBytes, m.SwapTotalBytes = &used, &total
			}
			m.ActiveBytes, m.WiredBytes, m.FileCacheBytes = pagesBytes(vm.Active, m.PageSize), pagesBytes(vm.Wired, m.PageSize), pagesBytes(vm.External, m.PageSize)
			m.InactiveBytes = pagesBytes(vm.Inactive, m.PageSize)
			if b, err := unix.SysctlUint64("vm.compressor_bytes_used"); err == nil {
				m.CompressorBytes = &b
			}
			return m, nil
		},
		Disks: statfsMounts,
	}
}

// loadavg reads vm.loadavg (see ParseDarwinLoadavg).
func loadavg() (l1, l5, l15 float64, err error) {
	b, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	return ParseDarwinLoadavg(b)
}

// swapUsage reads struct xsw_usage (vm.swapusage): the swap total and the part used, from the one read.
func swapUsage() (total, used uint64, err error) {
	b, err := unix.SysctlRaw("vm.swapusage")
	if err != nil {
		return 0, 0, err
	}
	return parseSwapUsage(b)
}

// parseSwapUsage reads struct xsw_usage: total, avail, used (uint64 each), then pagesize and the encrypted flag.
func parseSwapUsage(b []byte) (total, used uint64, err error) {
	if len(b) < 24 {
		return 0, 0, errors.New("vm.swapusage: unexpected size")
	}
	return binary.NativeEndian.Uint64(b[0:]), binary.NativeEndian.Uint64(b[16:]), nil
}

// bootFromTimeval is the boot time of a kern.boottime reading; a zero or missing one is unknown.
func bootFromTimeval(tv *unix.Timeval) (time.Time, bool) {
	if tv == nil || (tv.Sec == 0 && tv.Usec == 0) {
		return time.Time{}, false
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), true
}

// bootTime reads kern.boottime once; the boot time does not change while the host is up.
var bootTime = sync.OnceValues(func() (time.Time, bool) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, false
	}
	return bootFromTimeval(tv)
})

// pagesBytes is a page count in bytes, as a reading.
func pagesBytes(pages, pageSize uint64) *uint64 {
	v := pages * pageSize
	return &v
}

// selfSource reads wwd's own use: the CPU time from getrusage, the memory as the physical footprint (the figure every process in the list
// carries; proc_pid_rusage on this pid). Nil when the footprint cannot be bound: self then stays null.
func selfSource() func() (RawSelf, error) {
	fp, err := hostmem.NewFootprinter()
	if err != nil {
		return nil
	}
	pid := os.Getpid()
	return func() (RawSelf, error) {
		cpu, err := selfCPUTime()
		if err != nil {
			return RawSelf{}, err
		}
		rss, err := fp.Of(pid)
		if err != nil {
			return RawSelf{}, err
		}
		return RawSelf{CPUTime: cpu, RSSBytes: rss}, nil
	}
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
