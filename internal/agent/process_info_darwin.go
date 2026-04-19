package agent

import (
	"fmt"
	"os/exec"
	"strings"
)

func readProcessInfoPlatform(pid int) (ProcessInfo, error) {
	ppid, err := readProcessPPID(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "args=").Output()
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("read args for pid %d: %w", pid, err)
	}
	rawArgs := strings.TrimSpace(string(out))
	if rawArgs == "" {
		return ProcessInfo{}, fmt.Errorf("read args for pid %d: empty args", pid)
	}
	argv := strings.Fields(rawArgs)
	if len(argv) == 0 {
		return ProcessInfo{}, fmt.Errorf("read args for pid %d: empty argv", pid)
	}
	exePath, err := normalizeExecutablePath(argv[0])
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("normalize exe path for pid %d: %w", pid, err)
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
