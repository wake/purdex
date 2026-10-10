package monitor

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type systemHostCollector struct{}

func NewSystemHostCollector() HostCollector {
	if runtime.GOOS == "darwin" {
		return &darwinHostCollector{sampler: newCPUSampler(runDarwinIostat, log.Printf)}
	}
	return systemHostCollector{}
}

// darwinHostCollector reads the host CPU from a background iostat sampler (macOS 26 has no kern.cp_time, #2013); the rest
// is the system collector's. Close stops the sampler.
type darwinHostCollector struct {
	systemHostCollector
	sampler *cpuSampler
}

func (c *darwinHostCollector) CPUPercent(interval time.Duration) (float64, error) {
	return c.sampler.CPUPercent(interval)
}

func (c *darwinHostCollector) Close() { c.sampler.Close() }

// runDarwinIostat is one `iostat -c 2 -w 1`: two samples a second apart, the second being the last second. LC_ALL=C keeps
// the numbers and the header in the form the parser reads; the context kills the process.
func runDarwinIostat(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "iostat", "-c", "2", "-w", "1")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("iostat: %w", err)
	}
	return string(out), nil
}

func (systemHostCollector) CollectCPU(ctx context.Context) (HostCPUSample, error) {
	switch runtime.GOOS {
	case "linux":
		return collectLinuxCPU()
	default:
		return HostCPUSample{}, fmt.Errorf("unsupported host cpu platform: %s", runtime.GOOS)
	}
}

func (systemHostCollector) CollectMemory(ctx context.Context) (HostMemorySample, error) {
	switch runtime.GOOS {
	case "darwin":
		return collectDarwinMemory(ctx)
	case "linux":
		return collectLinuxMemory()
	default:
		return HostMemorySample{}, fmt.Errorf("unsupported host memory platform: %s", runtime.GOOS)
	}
}

func (systemHostCollector) CollectDisk(context.Context) (HostDiskSample, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return HostDiskSample{}, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free
	return HostDiskSample{TotalBytes: total, UsedBytes: used}, nil
}

func collectDarwinMemory(ctx context.Context) (HostMemorySample, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return HostMemorySample{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return HostMemorySample{}, fmt.Errorf("vm_stat: %w", err)
	}
	pageSize, freePages, inactivePages, err := parseDarwinVMStat(string(out))
	if err != nil {
		return HostMemorySample{}, err
	}
	available := (freePages + inactivePages) * pageSize
	used := uint64(0)
	if total > available {
		used = total - available
	}
	return HostMemorySample{TotalBytes: total, UsedBytes: used}, nil
}

func parseDarwinVMStat(raw string) (pageSize, freePages, inactivePages uint64, err error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "page size of") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "of" && i+1 < len(fields) {
					pageSize, _ = strconv.ParseUint(fields[i+1], 10, 64)
				}
			}
			continue
		}
		value, ok := parseDarwinVMStatLine(line)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Pages free:"):
			freePages = value
		case strings.HasPrefix(line, "Pages inactive:"):
			inactivePages = value
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, err
	}
	if pageSize == 0 {
		return 0, 0, 0, fmt.Errorf("vm_stat: missing page size")
	}
	return pageSize, freePages, inactivePages, nil
}

func parseDarwinVMStatLine(line string) (uint64, bool) {
	parts := strings.Split(line, ":")
	if len(parts) != 2 {
		return 0, false
	}
	raw := strings.TrimSpace(strings.TrimSuffix(parts[1], "."))
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

func collectLinuxCPU() (HostCPUSample, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return HostCPUSample{}, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return HostCPUSample{}, fmt.Errorf("/proc/stat: malformed cpu line")
	}
	var total uint64
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return HostCPUSample{}, fmt.Errorf("parse /proc/stat: %w", err)
		}
		values = append(values, value)
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return HostCPUSample{Idle: idle, Total: total}, nil
}

func collectLinuxMemory() (HostMemorySample, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return HostMemorySample{}, err
	}
	values := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return HostMemorySample{}, err
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total == 0 {
		return HostMemorySample{}, fmt.Errorf("/proc/meminfo: missing MemTotal")
	}
	used := uint64(0)
	if total > available {
		used = total - available
	}
	return HostMemorySample{TotalBytes: total, UsedBytes: used}, nil
}
