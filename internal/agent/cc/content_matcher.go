// Package cc provides the Claude Code agent provider implementation.
package cc

import "strings"

// ccContentMatcher implements probe.ContentMatcher for Claude Code.
type ccContentMatcher struct{}

// NewContentMatcher creates a CC content matcher for Liveness fallback.
func NewContentMatcher() *ccContentMatcher {
	return &ccContentMatcher{}
}

// LooksLikeAgent returns true if the terminal content looks like Claude Code.
func (m *ccContentMatcher) LooksLikeAgent(content string) bool {
	return looksLikeCC(content)
}

func looksLikeCC(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "❯") {
			return true
		}
		if strings.Contains(trimmed, "Opus") || strings.Contains(trimmed, "Sonnet") || strings.Contains(trimmed, "Haiku") {
			return true
		}
		if strings.Contains(trimmed, "Allow") && strings.Contains(trimmed, "Deny") {
			return true
		}
	}
	return false
}
