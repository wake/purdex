package gitenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClean_DropsEveryGitVariableAndKeepsTheRest(t *testing.T) {
	got := Clean([]string{
		"PATH=/usr/bin", "HOME=/h", "GITHUB_TOKEN=x", "GIT_DIR=/other/.git", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=touch /tmp/x", "GIT_CONFIG_PARAMETERS='core.fsmonitor=x'", "GIT_EXEC_PATH=/evil", "GIT_TERMINAL_PROMPT=1",
	})
	assert.Equal(t, []string{"PATH=/usr/bin", "HOME=/h", "GITHUB_TOKEN=x", "GIT_TERMINAL_PROMPT=0"}, got, "GITHUB_TOKEN is not a git variable")
}

// The owner of the process may isolate git from the user's configuration; that stays.
func TestClean_KeepsTheVariablesThatChooseWhereConfigurationIsRead(t *testing.T) {
	got := Clean([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_COUNT=1"})
	assert.Equal(t, []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0"}, got)
}

func TestClean_DoesNotChangeItsInput(t *testing.T) {
	in := []string{"GIT_DIR=/x", "A=b"}
	Clean(in)
	assert.Equal(t, []string{"GIT_DIR=/x", "A=b"}, in)
}
