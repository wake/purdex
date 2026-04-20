package probe

import (
	"regexp"
	"strings"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func looksLikeShellPrompt(content string) bool {
	lines := strings.Split(stripANSI(content), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if hasPromptMarker(line) {
			return true
		}
	}
	return false
}

func hasPromptMarker(line string) bool {
	return strings.HasSuffix(line, "$") ||
		strings.HasSuffix(line, "#") ||
		strings.HasSuffix(line, "%") ||
		strings.HasSuffix(line, ">") ||
		strings.HasPrefix(line, "$ ") ||
		strings.HasPrefix(line, "# ") ||
		strings.HasPrefix(line, "% ") ||
		strings.HasPrefix(line, "> ")
}

func stripANSI(content string) string {
	return ansiEscapePattern.ReplaceAllString(content, "")
}
