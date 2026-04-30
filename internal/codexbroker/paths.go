package codexbroker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// envStateRoot overrides the plugin data root path. Used for tests.
const envStateRoot = "PDX_CODEX_STATE_ROOT"

// envSocketRoots is a colon-separated override list for socket-glob roots.
const envSocketRoots = "PDX_CODEX_SOCKET_ROOTS"

// PluginDataRoot returns the absolute path to the codex plugin state
// directory. The default is `<HOME>/.claude/plugins/data/codex-openai-codex/state`.
//
// Override via PDX_CODEX_STATE_ROOT env var (used for tests).
func PluginDataRoot() (string, error) {
	if v := os.Getenv(envStateRoot); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".claude", "plugins", "data", "codex-openai-codex", "state"), nil
}

// SocketGlobRoots returns the directories under which `cxc-XXXXXX` broker
// socket directories may appear. The default includes TMPDIR plus on Darwin
// the `/var/folders/*/T` glob root that mktemp uses.
//
// Override via PDX_CODEX_SOCKET_ROOTS env var (colon-separated paths). Used
// for tests.
//
// Returned roots are deduplicated, in stable order: explicit override (or
// platform-specific roots) first, then $TMPDIR if not already present.
func SocketGlobRoots() ([]string, error) {
	if v := os.Getenv(envSocketRoots); v != "" {
		parts := strings.Split(v, ":")
		out := make([]string, 0, len(parts))
		seen := make(map[string]bool, len(parts))
		for _, p := range parts {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
		return out, nil
	}

	roots := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		roots = append(roots, p)
	}

	// Platform-specific roots first so /var/folders comes before /tmp on darwin.
	if runtime.GOOS == "darwin" {
		// macOS mktemp puts cxc-* under per-user /var/folders/<X>/<Y>/T.
		matches, err := filepath.Glob("/var/folders/*/*/T")
		if err == nil {
			for _, m := range matches {
				add(m)
			}
		}
	}

	add(os.TempDir())
	return roots, nil
}
