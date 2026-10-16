package resources

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/wake/purdex/internal/execstat"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/hostmem"
)

// ErrUnsupported is what a sampler returns on a platform it cannot read. The
// owning module then publishes Available = false, Reason =
// ReasonUnsupportedPlatform.
var ErrUnsupported = errors.New("resources: unsupported platform")

// Sampler reads the host once. It forks, so it is called from the module's
// own ticker and never from a request or hook path.
type Sampler interface {
	Sample(ctx context.Context) (HostRaw, []Proc, error)
}

// forkTimeout bounds each forked command.
const forkTimeout = 2 * time.Second

// Seams for tests. The darwin build points the sysctl ones at x/sys/unix;
// other platforms return ErrUnsupported.
var (
	sysctlRaw func(name string) ([]byte, error) = osSysctlRaw
	sysctlU32 func(name string) (uint32, error) = osSysctlU32
	sysctlU64 func(name string) (uint64, error) = osSysctlU64

	// runCmd runs one command and returns its stdout. env is appended to the
	// process environment.
	runCmd = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		cmd := execstat.CommandContext(ctx, "resources."+filepath.Base(name), name, args...)
		cmd.Env = append(os.Environ(), env...)
		return cmd.Output()
	}
)

// sysSampler is the sampler built on the seams above.
type sysSampler struct {
	psSkipped atomic.Uint64
}

// PSLinesSkipped is how many malformed ps lines this sampler has skipped in
// all its samples so far.
func (s *sysSampler) PSLinesSkipped() uint64 { return s.psSkipped.Load() }

func (s *sysSampler) Sample(ctx context.Context) (HostRaw, []Proc, error) {
	var raw HostRaw

	buf, err := sysctlRaw("vm.loadavg")
	if err != nil {
		return raw, nil, fmt.Errorf("sysctl vm.loadavg: %w", err)
	}
	load, err := parseLoadavgAll(buf)
	if err != nil {
		return raw, nil, err
	}
	raw.Load1, raw.Load5, raw.Load15 = load[0], load[1], load[2]
	ncpu, err := sysctlU32("hw.ncpu")
	if err != nil {
		return raw, nil, fmt.Errorf("sysctl hw.ncpu: %w", err)
	}
	raw.NCPU = int(ncpu)
	if raw.MemBytes, err = sysctlU64("hw.memsize"); err != nil {
		return raw, nil, fmt.Errorf("sysctl hw.memsize: %w", err)
	}

	// Pressure and the kernel's free percentage are extras: when they are
	// unreadable the sample still stands, with "unknown" in their place.
	if v, err := sysctlU32(hostmem.LevelSysctl); err == nil {
		raw.Pressure = int(v)
	}
	raw.MemorystatusLevel = -1
	if v, err := sysctlU32("kern.memorystatus_level"); err == nil {
		raw.MemorystatusLevel = int(v)
	}

	vmOut, err := s.fork(ctx, nil, "vm_stat")
	if err != nil {
		return raw, nil, fmt.Errorf("vm_stat: %w", err)
	}
	vm, err := parseVMStat(string(vmOut))
	if err != nil {
		return raw, nil, err
	}
	raw.PageSize, raw.Free, raw.Inactive, raw.Speculative = vm.PageSize, vm.Free, vm.Inactive, vm.Speculative
	raw.Pageouts, raw.Swapouts, raw.PageCounters = vm.Pageouts, vm.Swapouts, vm.Counters
	if _, ok := raw.availableBytes(); !ok {
		return raw, nil, fmt.Errorf("vm_stat: page counts overflow (%d + %d + %d pages of %d bytes)", vm.Free, vm.Inactive, vm.Speculative, vm.PageSize)
	}

	// LC_ALL=C keeps the decimal point a point in pcpu.
	psOut, err := s.fork(ctx, []string{"LC_ALL=C"}, "ps", "-axo", "pid=,ppid=,pcpu=,rss=,ucomm=")
	if err != nil {
		return raw, nil, fmt.Errorf("ps: %w", err)
	}
	procs, skipped, err := parsePS(string(psOut))
	s.psSkipped.Add(uint64(skipped))
	if err != nil {
		return raw, nil, err
	}
	for _, p := range procs {
		raw.PcpuSum += p.Pcpu
	}
	return raw, procs, nil
}

func (s *sysSampler) fork(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, forkTimeout)
	defer cancel()
	return runCmd(ctx, env, name, args...)
}

// loadavgSize is sizeof(struct loadavg) on darwin: three uint32 fixed-point
// values, 4 bytes of padding, then fscale as uint64.
const loadavgSize = 24

// parseLoadavg returns load1 from a vm.loadavg buffer.
func parseLoadavg(b []byte) (float64, error) {
	l, err := parseLoadavgAll(b)
	return l[0], err
}

// parseLoadavgAll returns the 1, 5 and 15 minute load averages of a vm.loadavg buffer (the same read, no more forks).
func parseLoadavgAll(b []byte) ([3]float64, error) {
	var l [3]float64
	if len(b) != loadavgSize {
		return l, fmt.Errorf("vm.loadavg: %d bytes, want %d", len(b), loadavgSize)
	}
	fscale := binary.NativeEndian.Uint64(b[16:])
	if fscale == 0 {
		return l, errors.New("vm.loadavg: fscale is 0")
	}
	for i := range l {
		l[i] = float64(binary.NativeEndian.Uint32(b[4*i:])) / float64(fscale)
	}
	return l, nil
}

// vmStat is the part of vm_stat output D-1 uses.
type vmStat struct {
	PageSize, Free, Inactive, Speculative uint64
	// Pageouts and Swapouts are vm_stat's running counters of pages written out; both is whether the two lines were there (a
	// missing line is not a failed sample: the rate is only unknown).
	Pageouts, Swapouts uint64
	Counters           bool
}

// parseVMStat reads vm_stat output with the shared parser, and stricter than it: a missing count is an error, because a zero
// would pass for a full host.
func parseVMStat(raw string) (vmStat, error) {
	v, err := hostmem.ParseVMStat([]byte(raw))
	if err != nil {
		return vmStat{}, err
	}
	if err := v.MemoryErr(); err != nil {
		return vmStat{}, err
	}
	return vmStat{PageSize: v.PageSize, Free: v.Free, Inactive: v.Inactive, Speculative: v.Speculative,
		Pageouts: v.Pageouts, Swapouts: v.Swapouts, Counters: v.HaveCounters}, nil
}

// parsePS reads `ps -axo pid=,ppid=,pcpu=,rss=,ucomm=` output (rss in KiB; ucomm is the
// command's name, which may hold spaces, so it is the rest of the line). A line without
// the ucomm column is still a process, with an empty Comm. A malformed line is skipped
// and counted; output with no valid line at all is an error.
func parsePS(out string) (procs []Proc, skipped int, err error) {
	for _, line := range strings.Split(out, "\n") {
		fields, comm := splitPSLine(line)
		if len(fields) == 0 {
			continue
		}
		p, ok := parsePSFields(fields)
		p.Comm = comm
		if !ok {
			skipped++
			continue
		}
		procs = append(procs, p)
	}
	if len(procs) == 0 {
		return nil, skipped, fmt.Errorf("ps: no valid process lines (%d skipped)", skipped)
	}
	return procs, skipped, nil
}

// splitPSLine is the four numeric columns of a ps line and the rest of it, trimmed, as the command's name. A line with fewer
// than four words comes back whole (parsePSFields then refuses it).
func splitPSLine(line string) (fields []string, comm string) {
	rest := strings.TrimSpace(line)
	if rest == "" {
		return nil, ""
	}
	for len(fields) < 4 {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			return append(fields, rest), ""
		}
		fields = append(fields, rest[:i])
		rest = strings.TrimLeft(rest[i:], " \t")
	}
	return fields, strings.TrimSpace(rest)
}

func parsePSFields(f []string) (Proc, bool) {
	if len(f) != 4 {
		return Proc{}, false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		return Proc{}, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil || ppid < 0 {
		return Proc{}, false
	}
	pcpu, err := strconv.ParseFloat(f[2], 64)
	if err != nil || math.IsNaN(pcpu) || math.IsInf(pcpu, 0) || pcpu < 0 {
		return Proc{}, false
	}
	rssKiB, err := strconv.ParseUint(f[3], 10, 64)
	if err != nil || rssKiB > math.MaxUint64/1024 { // beyond that, KiB * 1024 would wrap to a small number
		return Proc{}, false
	}
	return Proc{PID: pid, PPID: ppid, Pcpu: pcpu, RSSBytes: rssKiB * 1024}, true
}
