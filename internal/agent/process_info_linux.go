package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func readProcessInfoPlatform(pid int) (ProcessInfo, error) {
	ppid, err := readProcessPPID(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	exePath, argv, err := readProcExeCmdline(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	startTime, err := readProcessStartTime(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	return ProcessInfo{
		PID:       pid,
		PPID:      ppid,
		ExePath:   exePath,
		Argv:      argv,
		StartTime: startTime,
	}, nil
}

// readProcExeCmdline is the per-PID reader's ExePath / Argv, from /proc. The
// process snapshot reads them through exactly this, so the two readers cannot
// drift apart.
func readProcExeCmdline(pid int) (string, []string, error) {
	exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return "", nil, fmt.Errorf("read exe for pid %d: %w", pid, err)
	}
	exePath, err = normalizeExecutablePath(exePath)
	if err != nil {
		return "", nil, fmt.Errorf("normalize exe path for pid %d: %w", pid, err)
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", nil, fmt.Errorf("read cmdline for pid %d: %w", pid, err)
	}
	cmdline = bytes.TrimRight(cmdline, "\x00")
	argv := []string{}
	if len(cmdline) > 0 {
		argv = strings.Split(string(cmdline), "\x00")
	}
	return filepath.Clean(exePath), argv, nil
}
