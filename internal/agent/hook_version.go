package agent

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hookVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// DetectHookAgentVersion returns a normalized dotted version string for a CLI.
func DetectHookAgentVersion(binary string, versionArgs ...string) string {
	path, err := exec.LookPath(binary)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, versionArgs...).Output()
	if err != nil {
		return ""
	}
	return ExtractHookAgentVersion(string(out))
}

// ExtractHookAgentVersion pulls the first dotted version token from CLI output.
func ExtractHookAgentVersion(raw string) string {
	return hookVersionPattern.FindString(strings.TrimSpace(raw))
}

// CompareHookAgentVersions compares dotted numeric versions.
// Returns -1 when a < b, 0 when equal, and 1 when a > b.
func CompareHookAgentVersions(a, b string) int {
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}
	for i := 0; i < maxLen; i++ {
		segA := 0
		segB := 0
		if i < len(partsA) {
			segA, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			segB, _ = strconv.Atoi(partsB[i])
		}
		switch {
		case segA < segB:
			return -1
		case segA > segB:
			return 1
		}
	}
	return 0
}
