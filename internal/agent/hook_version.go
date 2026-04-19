package agent

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var hookVersionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

type cachedHookVersion struct {
	value string
	at    time.Time
}

// hookVersionTTL bounds how long a detected CLI version is cached.
// CheckHooks runs on every status poll; without caching each call spawns a
// subprocess that can block the handler for up to 5 s when the binary is
// missing or the system is slow.
var hookVersionTTL = 60 * time.Second

var (
	hookVersionMu    sync.Mutex
	hookVersionCache = map[string]cachedHookVersion{}
)

// DetectHookAgentVersion returns a normalized dotted version string for a CLI.
// Results are cached per binary for hookVersionTTL to keep CheckHooks cheap.
func DetectHookAgentVersion(binary string, versionArgs ...string) string {
	hookVersionMu.Lock()
	if c, ok := hookVersionCache[binary]; ok && time.Since(c.at) < hookVersionTTL {
		v := c.value
		hookVersionMu.Unlock()
		return v
	}
	hookVersionMu.Unlock()

	v := detectHookAgentVersionNow(binary, versionArgs...)

	hookVersionMu.Lock()
	hookVersionCache[binary] = cachedHookVersion{value: v, at: time.Now()}
	hookVersionMu.Unlock()
	return v
}

func detectHookAgentVersionNow(binary string, versionArgs ...string) string {
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

// ResetHookAgentVersionCache clears cached detections. Intended for tests.
func ResetHookAgentVersionCache() {
	hookVersionMu.Lock()
	defer hookVersionMu.Unlock()
	hookVersionCache = map[string]cachedHookVersion{}
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
