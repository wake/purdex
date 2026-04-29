package monitor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type systemHostCollector struct{}

func NewSystemHostCollector() HostCollector {
	return systemHostCollector{}
}

func (systemHostCollector) CollectCPU(ctx context.Context) (HostCPUSample, error) {
	switch runtime.GOOS {
	case "darwin":
		return collectDarwinCPU(ctx)
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

func collectDarwinCPU(ctx context.Context) (HostCPUSample, error) {
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "kern.cp_time").Output()
	if err != nil {
		return HostCPUSample{}, fmt.Errorf("sysctl kern.cp_time: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 4 {
		return HostCPUSample{}, fmt.Errorf("sysctl kern.cp_time: expected at least 4 fields")
	}
	values := make([]uint64, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return HostCPUSample{}, fmt.Errorf("parse kern.cp_time: %w", err)
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	return HostCPUSample{Idle: values[3], Total: total}, nil
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
