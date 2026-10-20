// internal/core/info_handler.go
package core

import (
	"context"
	"encoding/json"
	"github.com/wake/purdex/internal/execstat"
	"net/http"
	"os"
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
		// `pdx start` compares it with the child it spawned, so another daemon answering on the same address is not
		// taken for the new one (#959).
		"pid": os.Getpid(),
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
	"transcript.v1",           // GET /api/sessions/{code}/transcript
	"terminal.mirror.v1",      // /ws/terminal/{code}?mirror=1 plus window text frames
	"conversations.scope.v1",  // GET /api/nex/conversations?scope=test|normal|all
	"relay.unattended.v1",     // GET/PUT /api/team/unattended, team.unattended events (U23)
	"team.name.v1",            // lead request team_name, grant.team_name, Team / TeamRoster team_name
	"team.tasks.v1",           // /api/team/tasks…, task routes (T-1b)
	"team.label.v1",           // lead request team_label, grant.team_label, Team / TeamRoster team_label
	"conversations.v1",        // GET /api/conversations/{provider}/{session_id} (snapshot, ?after= increments, ?around=) and .../subagents/{agent_id}
	"team.adopt.v1",           // lead request kind adopt (POST /api/team/approvals {kind:"adopt", target}), adopt members (U24)
	"team.relay_quota.v1",     // PUT /api/team/relay-quota, team.relay_quota events, relay_quota on Member / RosterSession, UnattendedView.quotas (#2062)
	"team.max_members.v1",     // PUT /api/team/max-members, max_members and in_use on TeamRoster
	"team.edit.v1",            // PUT /api/team/appearance (name, label, colour of a live team), team_color on TeamRoster (TR-1)
	"conversations.submit.v1", // POST /api/conversations/{provider}/{session_id}/submit and /interrupt through the session's mod (needs a prompt.v1 stream: else 409 no_mod); not a device route
	"session.launch.v1",       // GET /api/sessions/{code}/launch-plan, POST /api/sessions/{code}/launch: the no-agent launch panel (resume the last conversation / start a new one)
	"team.ask_chat.v1",        // decide a hook_ask with decision deny + hook.message (the reply instead of answers); its wait is answered_remote with hook.message
	"team.pause.v1",           // an unattended session with no relay quota left is paused, not held for a card: GET /api/team/paused, POST /api/team/paused/{session_id}/release, roster paused, 409 paused_no_quota / member_paused_no_quota, begin reask_id (UP-4)
	"session.read_sync.v1",    // read sync (badge spec §7): POST /api/sessions/{code}/read, /ws/host-events?reads=v1 (session.reads / session.read), prefs.read_sync, purdex.reads / purdex.event_cursor on pushes, kind "reads" flush
	"team.person.v1",          // person-typed commands: person tokens, direct /relay, decided_by.kind person, unattended_refused, PUT unattended refused (RT-1)
	"relay.unanswered.v1",     // pause cause relay_unanswered, approval unanswered_at, a person's release = deny, the bounce text by cause (RT-2a)
	"relay.half.v1",           // an idle card of 300 s whose timeout writes a half (relay_ops.mode, begin --half / --reuse, POST /api/relay/fresh), GET /api/relay/fresh, relay.fresh events, fresh_handoff on RosterSession, fresh_half on hello (RT-3a)
	"team.decision_log.v1",    // GET / PUT /api/team/unattended carry `log`: the latest team decisions and the switch's on/off records, newest first (NP-6b)
	"team.max_members.v2",     // the member cap's ceiling is 9, not 8: PUT /api/team/max-members, max_members on a lead request, pdx lead request --max-members (NP-4a)
	"team.lead_name.v1",       // a lead takes its name at approval: Grant.lead_name, 409 name_taken, LeadPayload.lead_names, GET /api/team/approvals/{id}/lead-names, and a person token that carries lead_name or card (NP-4c)
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

// moduleFlag is a true boolean in the module's Status under key.
func (c *Core) moduleFlag(name, key string) bool {
	st, ok := c.ModuleStatus(name)
	if !ok {
		return false
	}
	on, _ := st[key].(bool)
	return on
}

// wwdIdentity is the local wwd's {host_id, alias} as the pool module last read it from wwd, or false while it is not known (no pool module,
// wwd not read yet, wwd away). It reads the module's own Status and never calls wwd, so /api/info does not wait on it.
func (c *Core) wwdIdentity() (map[string]string, bool) {
	if !c.moduleReady("pool") {
		return nil, false
	}
	st, ok := c.ModuleStatus("pool")
	if !ok {
		return nil, false
	}
	id, ok := st["wwd"].(map[string]string)
	if !ok || id["host_id"] == "" {
		return nil, false
	}
	return id, true
}

// capabilityList is the static list plus the conditional capabilities: push.v1 while the push module is ready, and
// devices.v1 while the devices module is. Every other name above is unconditional; keep it that way unless a feature really
// can be switched off at boot.
func (c *Core) capabilityList() []string {
	out := append([]string(nil), capabilities...)
	if c.pushReady() {
		out = append(out, "push.v1")          // POST/GET /api/push/devices, DELETE /api/push/devices/{device_id}, PUT /api/push/presence
		out = append(out, "push.presence.v2") // …/presence accepts v:2 (the machine's idle / lock state); same condition as push.v1
		out = append(out, "push.badge.v2")    // badge kinds (adopt, terminal-only asks, permission cards), c: keys, purdex.member, the member gate and prefs.muted; same condition as push.v1
		out = append(out, "push.badge_bg.v1") // a card that closes sends the phones with prefs.badge_push a background badge push (apns-push-type background); same condition as push.v1
	}
	if c.moduleReady("conversation") {
		out = append(out, "sessions.send_now.v1")    // POST /api/sessions/{code}/send-now {client_msg_id, expected_tmux_instance}: Claude Code's send now (Ctrl+X Ctrl+S) for its queued messages; answers sent_now / refused + reason / unknown; conversation capabilities send_now: "keys"; only while the conversation module runs
		out = append(out, "conversations.stages.v1") // conversation.stages WS frames and stages[] in conversation.snapshot: where each submitted client_msg_id is (received / filled / entered / queued / read / failed / unknown / recalled); only while the conversation module runs
	}
	if c.moduleReady("devices") {
		out = append(out, "devices.v1") // POST/GET/DELETE /api/devices, PUT /api/devices/self; device tokens (pdxd_) as bearers
	}
	if c.moduleReady("workbook") {
		out = append(out, "workbook.v1") // GET /api/workbook/conversations/{provider}/{session_id}, GET /api/workbook/entries, workbook.entry / workbook.status events
		out = append(out, "workbook.v2") // entries' kind / usage / todo_changes, the conversation's todos and refresh_available, GET …/todos, POST …/refresh, workbook.todos / workbook.refresh_available events
		if c.moduleFlag("workbook", "journal") {
			out = append(out, "journal.v1")          // GET /api/journal, /api/journal/tasks, /api/journal/roadmap, journal.changed events (entries carry no pin; pages carry confirms[]; GET /api/journal answers one leader session with ?page=); only while the journal runs ([journal] enabled, and wired)
			out = append(out, "journal.sessions.v1") // GET /api/journal/sessions: the leader's session chain over relay and /clear; GET /api/journal?page=<session> answers that session's entries (oldest first, capped, truncated), no page = the current one, ?before= still answered; same condition as journal.v1
			out = append(out, "journal.confirm.v1")  // PUT /api/journal/confirm {session_id, entry_id, idx, done}: a person checks / unchecks a 待確認 item (manual, Haiku leaves it); same condition as journal.v1
		}
		if c.moduleFlag("workbook", "journal_members") {
			out = append(out, "journal.members.v1") // GET /api/journal/members: the team's current members with their task and last report; only while the journal runs with a team feed
		}
	}
	if c.moduleReady("fleet") {
		out = append(out, "fleet.v1") // GET /api/fleet, GET /api/fleet/ssh-aliases, the opt-in fleet.changed push (?fleet=v1)
	}
	if c.moduleReady("watch") {
		out = append(out, "watch.v1") // GET /api/watch/status, /events, /unhandled, POST /api/watch/ack, the watch.changed and watch.events pushes
	}
	if c.moduleReady("pool") {
		out = append(out, "pool.v1")         // GET /api/pool/hosts, GET /api/pool/facts/history (the resource pool from the local wwd; 503 wwd_unavailable while wwd is not running)
		out = append(out, "pool.series.v1")  // GET /api/pool/facts/series?host=&since=&step=: the history of a host cut down to the points a chart draws (at most 2000, the latest kept); same condition as pool.v1
		out = append(out, "pool.assess.v1")  // GET /api/pool/hosts: each host with facts also has headroom_reasons {heavy, light} (why a side's headroom is 0) and load {score, driver}; same condition as pool.v1
		out = append(out, "pool.control.v1") // PUT /api/pool/hosts/{alias}/worker, POST /api/pool/hosts/{alias}/exceptions, DELETE .../exceptions/{name}: the admin token changes a host's worker through the local wwd (a device token gets 403); same condition as pool.v1
	}
	if c.moduleReady("pool") {
		out = append(out, "pool.observations.v1") // GET /api/pool/hosts/{alias}/observations?since=&until= and /undeclared?since=: a host's process-tree observations (wwd spec 8.4) through the local wwd, next_until sent back as it came; the admin token only; same condition as pool.v1
	}
	if c.moduleReady("pool") && c.moduleFlag("pool", "events") {
		out = append(out, "pool.events.v1") // the pool.changed host event {host}: a host's entry in GET /api/pool/hosts changed (read every 5 s from the local wwd, at most one notice per host per 2 s); the App then stops polling the hosts; only while the pool module's watch runs
	}
	if c.moduleReady("pool") && c.moduleFlag("pool", "alerts") {
		out = append(out, "pool.alerts.v1") // GET /api/pool/hosts/{alias}/alerts and the alerts.changed host event {host}: a host's alert list changed (the local host at once on wwd's alert event, another host within 30 s); the App then stops polling the alerts; only while the pool module's watch runs
	}
	if c.moduleReady("jobs") {
		out = append(out, "jobs.v1") // GET /api/pool/jobs, /api/pool/jobs/{id}, /api/pool/jobs/{id}/log (wwd's jobs, read through the local wwd; ?host= for another host; 503 wwd_unavailable while wwd is not running)
	}
	if c.moduleReady("jobs") {
		out = append(out, "jobs.control.v1") // POST /api/pool/jobs/{id}/cancel?host=, POST /api/pool/jobs/{id}/position: the admin token cancels a job (on any host, through the local wwd) or sets a queued job's place (this host only, 400 host_not_local otherwise; a device token gets 403); same condition as jobs.v1
	}
	if c.moduleReady("gate") {
		out = append(out, "gate.decisions.v1") // GET /api/gate/decisions (admin token): the gate's records, newest first
	}
	if c.moduleReady("hostconfig") {
		out = append(out, "personas.v1") // GET/PUT /api/hostconfig/personas (not in GET /api/hostconfig), the hostconfig.personas event; the name pool and the journal follow the personas (name pool v2, NP-1)
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

	// wwdalerts: the module's own Status ({push: "on"|"off", deliverer, suppressed}, docs/wwd-deploy.md §7), or null while the module is not
	// mounted. Not a core-computed shape like nex and push: nothing here is the core's to add.
	var wwdalerts any
	if st, ok := c.ModuleStatus("wwdalerts"); ok {
		wwdalerts = st
	}

	// one read of the identity gives both the wwd key and its capability, so a watch that changes between two reads cannot make them disagree
	caps := c.capabilityList()
	wwdID, ok := c.wwdIdentity()
	if ok {
		caps = append(caps, "pool.identity.v1") // /api/info's wwd {host_id, alias}: who the local wwd is (its own host_id, not this daemon's), read by the pool module's watch; only while that is known
	}

	info := map[string]any{
		"host_id":        hostID,
		"tmux_instance":  config.GetTmuxInstance(),
		"purdex_version": buildinfo.Version,
		"tmux_version":   getTmuxVersion(),
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"nex":            nex,
		"push":           push,
		"wwdalerts":      wwdalerts,
		"capabilities":   caps,
		"last_shutdown":  nil,
	}
	if ok {
		info["wwd"] = wwdID
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
	out, err := execstat.CommandContext(ctx, "core.tmux-version", "tmux", "-V").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
