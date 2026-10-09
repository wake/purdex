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
	"team.adopt.v1",          // lead request kind adopt (POST /api/team/approvals {kind:"adopt", target}), adopt members (U24)
	"team.relay_quota.v1",    // PUT /api/team/relay-quota, team.relay_quota events, relay_quota on Member / RosterSession, UnattendedView.quotas (#2062)
	"team.max_members.v1",    // PUT /api/team/max-members, max_members and in_use on TeamRoster
	"team.edit.v1",           // PUT /api/team/appearance (name, label, colour of a live team), team_color on TeamRoster (TR-1)
	"team.ask_chat.v1",       // decide a hook_ask with decision deny + hook.message (the reply instead of answers); its wait is answered_remote with hook.message
}

// pushReady reports whether the push module is mounted AND has its key (its Status says ready). A mounted module whose
// key could not be loaded is soft-failed: its routes do not exist and nothing is announced (push spec §3).
func (c *Core) pushReady() bool {
	st, ok := c.ModuleStatus("push")
	if !ok {
		return false
	}
	ready, _ := st["ready"].(bool)
	return ready
}

// moduleReady reports whether a mounted module's Status says ready (a module that soft-failed at Init says not).
func (c *Core) moduleReady(name string) bool {
	st, ok := c.ModuleStatus(name)
	if !ok {
		return false
	}
	ready, _ := st["ready"].(bool)
	return ready
}

// capabilityList is the static list plus the conditional capabilities: push.v1 while the push module is ready, and
// devices.v1 while the devices module is. Every other name above is unconditional; keep it that way unless a feature really
// can be switched off at boot.
func (c *Core) capabilityList() []string {
	out := append([]string(nil), capabilities...)
	if c.pushReady() {
		out = append(out, "push.v1") // POST/GET /api/push/devices, DELETE /api/push/devices/{device_id}, PUT /api/push/presence
	}
	if c.moduleReady("devices") {
		out = append(out, "devices.v1") // POST/GET/DELETE /api/devices, PUT /api/devices/self; device tokens (pdxd_) as bearers
	}
	return out
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

	// push: {configured, ready, init_error}, the same shape as nex's. configured is the boot value (spec §3: [push] is
	// boot-only) and the core's to set; ready / init_error are the module's own report.
	c.CfgMu.RLock()
	pushConfigured := c.Cfg.PushAPNsDir() != ""
	c.CfgMu.RUnlock()
	push := map[string]any{"ready": false, "init_error": ""}
	if st, ok := c.ModuleStatus("push"); ok {
		for k, v := range st {
			push[k] = v
		}
	}
	push["configured"] = pushConfigured

	info := map[string]any{
		"host_id":        hostID,
		"tmux_instance":  config.GetTmuxInstance(),
		"purdex_version": buildinfo.Version,
		"tmux_version":   getTmuxVersion(),
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"nex":            nex,
		"push":           push,
		"capabilities":   c.capabilityList(),
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
