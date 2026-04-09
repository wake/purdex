package codex

import (
	"os/exec"
	"strings"
)

var codexProcessNames = []string{"codex"}

func isCodexProcess(processName string) bool {
	for _, name := range codexProcessNames {
		if processName == name {
			return true
		}
	}
	return false
}

func checkPaneProcess(tmuxTarget string) string {
	out, err := exec.Command("tmux", "display-message", "-t", tmuxTarget, "-p", "#{pane_current_command}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
