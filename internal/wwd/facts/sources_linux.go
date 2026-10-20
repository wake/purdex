//go:build linux

package facts

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The two slices whose pressure the zone reads (spec §3.1): the larger of them,
// field by field. The host-wide /proc/pressure/memory is never read.
var productionSlices = []string{
	"/sys/fs/cgroup/system.slice/memory.pressure",
	"/sys/fs/cgroup/user.slice/memory.pressure",
}

// SystemSources are the real reads of this Linux host: /proc, the production
// slices' memory.pressure and statfs. Each sample reads four small files; nothing forks.
func SystemSources() Sources { return SystemSourcesWith(SourceOptions{}) }

// SystemSourcesWith is SystemSources; the owner rule is macOS only, so the
// options change nothing here but the tool directories, and owner stays null.
func SystemSourcesWith(opt SourceOptions) Sources {
	procs := NewProcfsReader("/proc", DefaultSessionsDir())
	return Sources{
		Now:    time.Now,
		Tools:  systemTools(opt.ToolDirs),
		Procs:  procs.Snapshot,
		Boot:   procs.BootTime,
		Self:   linuxSelf,
		CPU:    linuxCPU,
		DiskIO: linuxDiskIO,
		Mem:    linuxMem,
		Disks:  linuxDisks,
	}
}

// linuxSelf is wwd's own use: the CPU time from getrusage and the resident set from /proc/self/statm.
func linuxSelf() (RawSelf, error) {
	cpu, err := selfCPUTime()
	if err != nil {
		return RawSelf{}, err
	}
	text, err := readFile("/proc/self/statm")
	if err != nil {
		return RawSelf{}, err
	}
	rss, err := ParseStatmRSS(text, uint64(os.Getpagesize()))
	if err != nil {
		return RawSelf{}, err
	}
	return RawSelf{CPUTime: cpu, RSSBytes: rss}, nil
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// linuxDiskIO is the counters of the physical disks: /proc/diskstats narrowed by /sys/block (see PhysicalDiskIO).
func linuxDiskIO() ([]RawDiskIO, error) {
	text, err := readFile("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	all, err := ParseDiskstats(text)
	if err != nil {
		return nil, err
	}
	return PhysicalDiskIO(all, "/sys/block")
}

func linuxCPU() (RawCPU, error) {
	stat, err := readFile("/proc/stat")
	if err != nil {
		return RawCPU{}, err
	}
	d, err := ParseCPUStatDetail(stat)
	if err != nil {
		return RawCPU{}, err
	}
	la, err := readFile("/proc/loadavg")
	if err != nil {
		return RawCPU{}, err
	}
	l1, l5, l15, err := ParseLoadavg(la)
	if err != nil {
		return RawCPU{}, err
	}
	// Kinds stay nil: Linux does not say which cores are performance and which efficiency
	return RawCPU{Load1: l1, Load5: l5, Load15: &l15, NCPU: len(d.Cores), Busy: d.All.Total - d.All.Idle, Total: d.All.Total,
		User: d.All.User, System: d.All.System, HaveSplit: true, Cores: d.Cores}, nil
}

func linuxMem() (RawMem, error) {
	text, err := readFile("/proc/meminfo")
	if err != nil {
		return RawMem{}, err
	}
	mi, err := ParseMeminfo(text)
	if err != nil {
		return RawMem{}, err
	}
	if !mi.HasAvailable {
		return RawMem{}, errors.New("meminfo: no MemAvailable (kernel older than 3.14)")
	}
	ps := uint64(os.Getpagesize())
	m := rawMemFromMeminfo(mi, ps)
	m.PSI = productionPSI()
	if vm, err := readFile("/proc/vmstat"); err == nil {
		if kib, swpout, perr := ParseVmstat(vm); perr == nil {
			m.Pageouts, m.Swapouts = kib*1024/ps, swpout
		} else {
			m.NoPageouts, m.NoSwapouts = true, true
		}
	} else {
		m.NoPageouts, m.NoSwapouts = true, true
	}
	return m, nil
}

// productionPSI is the larger of system.slice and user.slice per field. nil when
// either cannot be read or parsed (a kernel without PSI or cgroup v2, a slice
// missing): the unreadable one may be the one under pressure, so the other's
// zeros prove nothing.
func productionPSI() *wire.PSI {
	texts := make([]string, len(productionSlices))
	for i, p := range productionSlices {
		texts[i], _ = readFile(p) // unreadable stays "" and fails to parse
	}
	return MergePSI(texts)
}

// linuxDisks reports the root file system and, when it is another one, the one
// holding the home directory, each under its real mount point.
func linuxDisks() ([]RawDisk, error) {
	var points []string
	if text, err := readFile("/proc/self/mountinfo"); err == nil {
		points = ParseMountPoints(text)
	}
	paths := []string{"/"}
	if home, err := os.UserHomeDir(); err == nil && home != "/" {
		// statfs follows a symlinked home, so the mount is looked up for where it points
		if real, err := filepath.EvalSymlinks(home); err == nil {
			home = real
		}
		paths = append(paths, home)
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
		mount := MountOf(points, p)
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
