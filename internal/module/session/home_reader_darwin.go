//go:build darwin

package session

import (
	"fmt"
	"os/exec"
	"strings"
)

func readShellHomeFromProc(pid string) (string, error) {
	if pid == "" {
		return "", fmt.Errorf("empty pid")
	}

	out, err := exec.Command("ps", "-E", "-ww", "-p", pid, "-o", "command=").Output()
	if err != nil {
		return "", fmt.Errorf("ps env for pid %s: %w", pid, err)
	}

	return extractHomeEnv(strings.Fields(strings.TrimSpace(string(out))), pid)
}
