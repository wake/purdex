// internal/core/info_handler.go
package core

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/config"
)

// HandleHealth returns {"ok": true, "mode": "pairing"|"pending"|"normal"} for connectivity checks.
// Exported because main.go registers it on the outer mux to bypass auth middleware,
// allowing the SPA to test reachability before knowing whether a token is required.
func (c *Core) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"mode":    c.Pairing.Get().String(),
		"version": buildinfo.Version,
		"hash":    buildinfo.Hash,
		"boot_id": c.BootID,
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

// capabilities lists the optional API features this daemon serves. Clients gate
// features on these names rather than on purdex_version, since one user runs
// hosts on different versions. Add a name here in the same change that ships
// the feature; never reuse or remove one.
var capabilities = []string{
	"transcript.v1",          // GET /api/sessions/{code}/transcript
	"terminal.mirror.v1",     // /ws/terminal/{code}?mirror=1 plus window text frames
	"conversations.scope.v1", // GET /api/nex/conversations?scope=test|normal|all
	"relay.unattended.v1",    // GET/PUT /api/team/unattended, team.unattended events (U23)
	"team.name.v1",           // lead request team_name, grant.team_name, Team / TeamRoster team_name
	"team.tasks.v1",          // /api/team/tasks…, task routes (T-1b)
	"team.label.v1",          // lead request team_label, grant.team_label, Team / TeamRoster team_label
	"conversations.v1",       // GET /api/conversations/{provider}/{session_id} (snapshot, ?after= increments, ?around=) and .../subagents/{agent_id}
}

// handleInfo returns daemon metadata: host ID, tmux instance, version, OS, and architecture.
func (c *Core) handleInfo(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	hostID := c.Cfg.HostID
	// configured is the boot value (spec §4.4.2): a saved-but-unapplied
	// change is reported through restart_required, not here.
	nexEnabled := c.bootNex.Enabled
	restartRequired := !c.Cfg.Nex.Equal(c.bootNex)
	c.CfgMu.RUnlock()

	mounted := c.Mounted("nex")
	nex := map[string]any{
		"ready":      mounted,
		"init_error": "",
		"effective":  nil,
	}
	if st, ok := c.ModuleStatus("nex"); ok {
		for k, v := range st {
			nex[k] = v
		}
	}
	// Core-computed fields are set after the reporter's keys so a module
	// can never override them.
	nex["configured"] = nexEnabled
	nex["mounted"] = mounted
	nex["restart_required"] = restartRequired

	info := map[string]any{
		"host_id":        hostID,
		"tmux_instance":  config.GetTmuxInstance(),
		"purdex_version": buildinfo.Version,
		"tmux_version":   getTmuxVersion(),
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"nex":            nex,
		"capabilities":   append([]string(nil), capabilities...),
		"last_shutdown":  nil,
	}
	if r := c.LastShutdown; r != nil {
		info["last_shutdown"] = map[string]any{
			"at":      r.At.UTC().Format(time.RFC3339),
			"errors":  r.Errors,
			"boot_id": c.BootID,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// tmuxExecTimeout bounds lightweight tmux metadata queries (display-message, -V).
// Shorter than TmuxAlive's 5s because these are read-only, single-process commands.
const tmuxExecTimeout = 3 * time.Second

// getTmuxVersion runs `tmux -V` and returns the version string (e.g. "tmux 3.6a").
// Returns "unknown" if tmux is not found, the command fails, or it times out.
func getTmuxVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxExecTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "-V").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
