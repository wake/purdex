//go:build linux

package session

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

func readShellHomeFromProc(pid string) (string, error) {
	if pid == "" {
		return "", fmt.Errorf("empty pid")
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%s/environ", pid))
	if err != nil {
		return "", fmt.Errorf("read environ for pid %s: %w", pid, err)
	}

	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return "", fmt.Errorf("home not found for pid %s", pid)
	}

	return extractHomeEnv(strings.Split(string(data), "\x00"), pid)
}
