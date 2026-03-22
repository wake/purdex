package cc

import (
	"os"
	"path/filepath"

	"github.com/wake/tmux-box/internal/history"
)

const maxJSONLBytes = 2 * 1024 * 1024

// GetHistory retrieves CC conversation history from the JSONL session file.
// Returns an empty slice (not nil) when session ID is empty or the file is missing.
func (m *CCModule) GetHistory(cwd string, ccSessionID string) ([]map[string]any, error) {
	if ccSessionID == "" {
		return []map[string]any{}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return []map[string]any{}, nil
	}
	projectHash := history.CCProjectPath(cwd)
	jsonlPath := filepath.Join(home, ".claude", "projects", projectHash, ccSessionID+".jsonl")

	f, err := os.Open(jsonlPath)
	if err != nil {
		return []map[string]any{}, nil
	}
	defer f.Close()

	messages, err := history.ParseJSONL(f, maxJSONLBytes)
	if err != nil || messages == nil {
		return []map[string]any{}, nil
	}
	return messages, nil
}
