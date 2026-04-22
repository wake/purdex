package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const psLstartLayout = "Mon Jan _2 15:04:05 2006"

type ProcessInfo struct {
	PID       int
	PPID      int
	ExePath   string
	Argv      []string
	StartTime time.Time
}

func ReadProcessInfo(pid int) (ProcessInfo, error) {
	if pid <= 0 {
		return ProcessInfo{}, fmt.Errorf("invalid pid %d", pid)
	}
	return readProcessInfoPlatform(pid)
}

func readProcessStartTime(pid int) (time.Time, error) {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "lstart=").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("read start time for pid %d: %w", pid, err)
	}
	parsed, err := time.ParseInLocation(psLstartLayout, strings.TrimSpace(string(out)), time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse start time for pid %d: %w", pid, err)
	}
	return parsed, nil
}

func readProcessPPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "ppid=").Output()
	if err != nil {
		return 0, fmt.Errorf("read ppid for pid %d: %w", pid, err)
	}
	var ppid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &ppid); err != nil {
		return 0, fmt.Errorf("parse ppid for pid %d: %w", pid, err)
	}
	return ppid, nil
}

func normalizeExecutablePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty executable path")
	}
	if !filepath.IsAbs(raw) {
		switch {
		case strings.ContainsRune(raw, os.PathSeparator):
			abs, err := filepath.Abs(raw)
			if err == nil {
				raw = abs
			}
		default:
			lookedUp, err := exec.LookPath(raw)
			if err == nil {
				raw = lookedUp
			}
		}
	}
	if abs, err := filepath.Abs(raw); err == nil {
		raw = abs
	}
	return raw, nil
}

func IsJSRuntime(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "node", "bun", "deno":
		return true
	default:
		return false
	}
}

func ArgvContainsFragment(argv []string, fragments ...string) bool {
	if len(argv) == 0 || len(fragments) == 0 {
		return false
	}
	for _, arg := range argv {
		normalizedArg := strings.ToLower(strings.ReplaceAll(arg, "\\", "/"))
		for _, fragment := range fragments {
			if strings.Contains(normalizedArg, strings.ToLower(fragment)) {
				return true
			}
		}
	}
	return false
}
