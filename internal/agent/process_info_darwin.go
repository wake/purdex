package agent

import (
	"context"
	"fmt"
	"strings"
)

func readProcessInfoPlatform(pid int) (ProcessInfo, error) {
	ppid, err := readProcessPPID(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	exePath, argv, err := readCommArgsPS(pid)
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

// readCommArgsPS is the per-PID reader's ExePath / Argv, from ps's comm and
// args columns. The process snapshot falls back to exactly this for any
// process whose argument area it cannot reproduce byte for byte, so the two
// readers cannot drift apart on those processes.
func readCommArgsPS(pid int) (string, []string, error) {
	commOut, err := runPS(context.Background(), "-p", fmt.Sprintf("%d", pid), "-o", "comm=")
	if err != nil {
		return "", nil, fmt.Errorf("read command for pid %d: %w", pid, err)
	}
	exePath, err := exePathFromComm(pid, string(commOut))
	if err != nil {
		return "", nil, err
	}
	out, err := runPS(context.Background(), "-p", fmt.Sprintf("%d", pid), "-o", "args=")
	if err != nil {
		return "", nil, fmt.Errorf("read args for pid %d: %w", pid, err)
	}
	argv, err := argvFromArgs(pid, string(out))
	if err != nil {
		return "", nil, err
	}
	return exePath, argv, nil
}

// exePathFromComm and argvFromArgs turn ps's comm / args text into ProcessInfo
// fields. The snapshot's fast path feeds them the text ps would have printed,
// so both readers share one normalisation and one set of errors.
func exePathFromComm(pid int, comm string) (string, error) {
	exePath, err := normalizeExecutablePath(strings.TrimSpace(comm))
	if err != nil {
		return "", fmt.Errorf("normalize exe path for pid %d: %w", pid, err)
	}
	return exePath, nil
}

func argvFromArgs(pid int, args string) ([]string, error) {
	rawArgs := strings.TrimSpace(args)
	if rawArgs == "" {
		return nil, fmt.Errorf("read args for pid %d: empty args", pid)
	}
	argv := strings.Fields(rawArgs)
	if len(argv) == 0 {
		return nil, fmt.Errorf("read args for pid %d: empty argv", pid)
	}
	return argv, nil
}
