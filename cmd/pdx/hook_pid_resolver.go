package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type procInfo struct {
	PID     int
	PPID    int
	ExePath string
	Argv    []string
}

var knownShims = map[string]bool{
	"sh":   true,
	"bash": true,
	"zsh":  true,
	"dash": true,
	"fish": true,
	"npx":  true,
	"yarn": true,
	"pnpm": true,
	"env":  true,
}

var readResolverProcessInfo = readProcessInfo
var readResolverProcessStartTime = readProcessStartTime
var getParentPID = os.Getppid

func resolveAgentPID(startPpid int) (pid int, uncertain bool) {
	current := startPpid
	for current > 1 {
		info, err := readResolverProcessInfo(current)
		if err != nil {
			return current, true
		}
		if !knownShims[baseName(info.ExePath)] {
			return current, false
		}
		current = info.PPID
	}
	return 1, true
}

func resolveHookProvenance() hookProvenance {
	pid, uncertain := resolveAgentPID(getParentPID())
	startTime, err := readResolverProcessStartTime(pid)
	if err != nil {
		uncertain = true
	}
	return hookProvenance{
		TmuxPaneID:      queryTmuxPaneIDFn(),
		SenderPID:       pid,
		SenderStartTime: startTime,
		SenderUncertain: uncertain,
	}
}

func readProcessInfo(pid int) (procInfo, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "args=").Output()
	if err != nil {
		return procInfo{}, err
	}

	line := strings.TrimSpace(string(out))
	if line == "" {
		return procInfo{}, fmt.Errorf("empty ps output for pid %d", pid)
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return procInfo{}, fmt.Errorf("unexpected ps output for pid %d: %q", pid, line)
	}

	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return procInfo{}, fmt.Errorf("parse ppid for pid %d: %w", pid, err)
	}

	argv := fields[1:]
	return procInfo{
		PID:     pid,
		PPID:    ppid,
		ExePath: argv[0],
		Argv:    argv,
	}, nil
}

func readProcessStartTime(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func baseName(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}
