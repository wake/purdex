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
	exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("read exe for pid %d: %w", pid, err)
	}
	exePath, err = normalizeExecutablePath(exePath)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("normalize exe path for pid %d: %w", pid, err)
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("read cmdline for pid %d: %w", pid, err)
	}
	cmdline = bytes.TrimRight(cmdline, "\x00")
	argv := []string{}
	if len(cmdline) > 0 {
		argv = strings.Split(string(cmdline), "\x00")
	}
	startTime, err := readProcessStartTime(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	return ProcessInfo{
		PID:       pid,
		PPID:      ppid,
		ExePath:   filepath.Clean(exePath),
		Argv:      argv,
		StartTime: startTime,
	}, nil
}
