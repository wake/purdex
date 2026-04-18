package cc

import (
	"os"
	"path/filepath"
)

// ccSettingsPath returns the absolute path to the user's Claude Code
// settings.json (~/.claude/settings.json). It returns an error only if
// the user's home directory cannot be determined.
func ccSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}
