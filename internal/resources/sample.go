package resources

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
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
		cmd := exec.CommandContext(ctx, name, args...)
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
	if raw.Load1, err = parseLoadavg(buf); err != nil {
		return raw, nil, err
	}
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
	if v, err := sysctlU32("kern.memorystatus_vm_pressure_level"); err == nil {
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

	// LC_ALL=C keeps the decimal point a point in pcpu.
	psOut, err := s.fork(ctx, []string{"LC_ALL=C"}, "ps", "-axo", "pid=,ppid=,pcpu=,rss=")
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
	if len(b) != loadavgSize {
		return 0, fmt.Errorf("vm.loadavg: %d bytes, want %d", len(b), loadavgSize)
	}
	fscale := binary.NativeEndian.Uint64(b[16:])
	if fscale == 0 {
		return 0, errors.New("vm.loadavg: fscale is 0")
	}
	return float64(binary.NativeEndian.Uint32(b[0:])) / float64(fscale), nil
}

// vmStat is the part of vm_stat output D-1 uses.
type vmStat struct {
	PageSize, Free, Inactive, Speculative uint64
}

// parseVMStat reads vm_stat output. It is a copy of the monitor module's
// parseDarwinVMStat extended with the speculative count, and stricter: a
// missing count is an error, because a zero would pass for a full host.
func parseVMStat(raw string) (vmStat, error) {
	var v vmStat
	var sawFree, sawInactive, sawSpeculative bool
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "page size of") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "of" && i+1 < len(fields) {
					v.PageSize, _ = strconv.ParseUint(fields[i+1], 10, 64)
				}
			}
			continue
		}
		value, ok := parseVMStatLine(line)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Pages free:"):
			v.Free, sawFree = value, true
		case strings.HasPrefix(line, "Pages inactive:"):
			v.Inactive, sawInactive = value, true
		case strings.HasPrefix(line, "Pages speculative:"):
			v.Speculative, sawSpeculative = value, true
		}
	}
	if err := scanner.Err(); err != nil {
		return vmStat{}, err
	}
	if v.PageSize == 0 {
		return vmStat{}, errors.New("vm_stat: missing page size")
	}
	if !sawFree || !sawInactive || !sawSpeculative {
		return vmStat{}, errors.New("vm_stat: missing free, inactive or speculative pages")
	}
	return v, nil
}

func parseVMStatLine(line string) (uint64, bool) {
	parts := strings.Split(line, ":")
	if len(parts) != 2 {
		return 0, false
	}
	raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "."))
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

// parsePS reads `ps -axo pid=,ppid=,pcpu=,rss=` output (rss in KiB). A
// malformed line is skipped and counted; output with no valid line at all is
// an error.
func parsePS(out string) (procs []Proc, skipped int, err error) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		p, ok := parsePSFields(fields)
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
	if err != nil {
		return Proc{}, false
	}
	return Proc{PID: pid, PPID: ppid, Pcpu: pcpu, RSSBytes: rssKiB * 1024}, true
}
