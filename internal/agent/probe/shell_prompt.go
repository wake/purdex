package probe

import (
	"regexp"
	"strings"
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// LooksLikeShellPrompt reports whether the given captured pane content ends
// in a heuristic shell-prompt line. Exported for the agent module
// orchestrator (Slice 3) to refine ScreenStable into shell-prompt cases.
func LooksLikeShellPrompt(content string) bool {
	lines := strings.Split(StripANSI(content), "\n")
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

// StripANSI removes ANSI escape sequences from the given string. Exported
// alongside LooksLikeShellPrompt for the orchestrator's screen-content
// classification.
func StripANSI(content string) string {
	return ansiEscapePattern.ReplaceAllString(content, "")
}
