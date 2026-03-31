// internal/core/info_handler.go
package core

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
)

// Version is the tbox daemon version, set via ldflags at build time.
// Defaults to "dev" for local development builds.
var Version = "dev"

// HandleHealth returns a simple {"ok": true} for connectivity checks.
// Exported because main.go registers it on the outer mux to bypass auth middleware,
// allowing the SPA to test reachability before knowing whether a token is required.
func (c *Core) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleInfo returns daemon metadata: version, tmux version, OS, and architecture.
func (c *Core) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]string{
		"tbox_version": Version,
		"tmux_version": getTmuxVersion(),
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// getTmuxVersion runs `tmux -V` and returns the version string (e.g. "tmux 3.6a").
// Returns "unknown" if tmux is not found or the command fails.
func getTmuxVersion() string {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
