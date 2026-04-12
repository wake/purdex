package cc

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/history"
)

const maxJSONLBytes = 2 * 1024 * 1024

func (p *Provider) GetHistory(cwd string, ccSessionID string) ([]map[string]any, error) {
	if ccSessionID == "" || strings.ContainsAny(ccSessionID, "/\\") {
		return []map[string]any{}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("user home dir: %w", err)
	}
	projectHash := history.CCProjectPath(cwd)
	jsonlPath := filepath.Join(home, ".claude", "projects", projectHash, ccSessionID+".jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, fmt.Errorf("open jsonl: %w", err)
	}
	defer f.Close()
	messages, err := history.ParseJSONL(f, maxJSONLBytes)
	if err != nil {
		log.Printf("history: parse jsonl %s: %v", jsonlPath, err)
		return []map[string]any{}, nil
	}
	if messages == nil {
		return []map[string]any{}, nil
	}
	return messages, nil
}
