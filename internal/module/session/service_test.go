package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildTerminalRelayArgs_Auto(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "auto")
	assert.Equal(t, []string{"attach-session", "-t", "dev"}, args)
}

func TestBuildTerminalRelayArgs_TerminalFirst(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "terminal-first")
	assert.Equal(t, []string{"attach-session", "-t", "dev", "-f", "ignore-size"}, args)
}

func TestBuildTerminalRelayArgs_MinimalFirst(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "minimal-first")
	// minimal-first does NOT add ignore-size — sizing is handled via OnStart callback
	assert.Equal(t, []string{"attach-session", "-t", "dev"}, args)
}

func TestWindowSizeForMode(t *testing.T) {
	assert.Equal(t, "latest", windowSizeForMode("auto"))
	assert.Equal(t, "smallest", windowSizeForMode("minimal-first"))
	assert.Equal(t, "latest", windowSizeForMode("terminal-first"))
	assert.Equal(t, "latest", windowSizeForMode(""))
}
