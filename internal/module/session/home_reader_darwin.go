//go:build darwin

package session

import (
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/execstat"
)

func readShellHomeFromProc(pid string) (string, error) {
	if pid == "" {
		return "", fmt.Errorf("empty pid")
	}

	out, err := execstat.Command("session.ps-env", "ps", "-E", "-ww", "-p", pid, "-o", "command=").Output()
	if err != nil {
		return "", fmt.Errorf("ps env for pid %s: %w", pid, err)
	}

	return extractHomeEnv(strings.Fields(strings.TrimSpace(string(out))), pid)
}
