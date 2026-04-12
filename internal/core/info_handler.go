// internal/core/info_handler.go
package core

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/wake/purdex/internal/config"
)

// Version is the purdex daemon version, set via ldflags at build time.
// Defaults to "dev" for local development builds.
var Version = "dev"

// HandleHealth returns {"ok": true, "mode": "pairing"|"pending"|"normal"} for connectivity checks.
// Exported because main.go registers it on the outer mux to bypass auth middleware,
// allowing the SPA to test reachability before knowing whether a token is required.
func (c *Core) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"mode": c.Pairing.Get().String(),
	})
}

// handleReady returns tmux readiness status, registered on the inner mux (behind auth).
func (c *Core) handleReady(w http.ResponseWriter, r *http.Request) {
	tmuxAlive := false
	if c.TmuxAliveFunc != nil {
		tmuxAlive = c.TmuxAliveFunc()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tmux": tmuxAlive})
}

// handleInfo returns daemon metadata: host ID, tmux instance, version, OS, and architecture.
func (c *Core) handleInfo(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	hostID := c.Cfg.HostID
	c.CfgMu.RUnlock()

	info := map[string]string{
		"host_id":       hostID,
		"tmux_instance": config.GetTmuxInstance(),
		"purdex_version": Version,
		"tmux_version":  getTmuxVersion(),
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
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
