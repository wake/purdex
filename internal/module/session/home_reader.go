package session

import (
	"fmt"
	"strings"
)

func extractHomeEnv(entries []string, pid string) (string, error) {
	for _, entry := range entries {
		if strings.HasPrefix(entry, "HOME=") {
			home := strings.TrimPrefix(entry, "HOME=")
			if home != "" {
				return home, nil
			}
			break
		}
	}
	return "", fmt.Errorf("home not found for pid %s", pid)
}
