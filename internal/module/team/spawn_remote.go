// internal/module/team/spawn_remote.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// A spawn forwarded from a lead host (cross-host team spec §5.5, §6.2, plan X4a-2). The `spawn` command is accepted on
// this (the member) host as a spawn_ops row marked with the lead host; the SAME runner then creates the tmux session,
// launches the member and waits for its registration, with two differences: the cwd is judged against the roots the lead
// host's peer entry grants (re-resolved every time, #2254) and the end is a fact for the lead host, written in the
// transaction that ends the op: `registered` (with the remote_members row) or `spawn_failed`.

// remoteSpawnLead is what a forwarded spawn remembers of its lead (spawn_ops.lead_json).
type remoteSpawnLead struct {
	Lead     team.TeamLead `json:"lead"`
	TeamName string        `json:"team_name,omitempty"`
	// the label and colour a team.appearance command left (#2288)
	TeamLabel string `json:"team_label,omitempty"`
	TeamColor *int   `json:"team_color,omitempty"`
}

// maxPerLeadHost bounds what one lead host may hold on this host at once: the forwarded spawns still running plus its
// active remote members. A paired host that is allowed to spawn here cannot be allowed to fill the machine (a team's own
// member limit lives on the lead host, which this host does not trust for it).
const maxPerLeadHost = 16

// peerEntryByHostID is the live config entry carrying hostID, by host id and never by alias.
func (m *Module) peerEntryByHostID(hostID string) (config.PeerHost, bool) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	for _, h := range m.core.Cfg.Peers.Hosts {
		if hostID != "" && h.HostID == hostID {
			return h, true
		}
	}
	return config.PeerHost{}, false
}

// resolveUnderRoots is dir (absolute, symlinks evaluated) when it lies under one of roots — each root re-resolved now,
// still a directory and still itself (a root replaced by a symlink, or behind one, since it was granted is skipped),
// containment judged by path components on the canonical paths — else "", false.
// No roots is no spawn.
//
// What this cannot close (#2254): the check and tmux's own chdir into the directory are two steps, and tmux takes a path,
// not an open directory, so a directory swapped in that gap is not prevented here. It is caught afterwards: the runner reads
// the new pane's real directory and judges it the same way (checkLaunchPane) before any key reaches the pane, and kills the
// session if it left the roots. What the window does allow is the pane's shell starting in the swapped directory: its
// startup files and prompt hooks (a direnv hook, say) run there before the check, though nothing the lead sent does. The
// pane's cwd is a directory object, not a path, so what the check verified is what the launch runs in even if that
// directory is renamed afterwards. Whoever can swap a directory under a granted root can already put anything in the
// roots, which is the trust the grant gives; the check removes the case where a spawn is *used* to reach elsewhere.
func resolveUnderRoots(roots []string, dir string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	var live []string
	for _, root := range roots {
		// A granted root is stored canonical (config.CanonicalTeamRoots). One that no longer resolves to itself was
		// replaced by a symlink (or sits behind one) since: it is not the directory that was granted.
		if r, err := filepath.EvalSymlinks(root); err != nil || r != filepath.Clean(root) {
			continue
		}
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			live = append(live, root)
		}
	}
	if !underRoots(resolved, live) {
		return "", false
	}
	return resolved, true
}

// underOpRoots is underTeamRoots for a spawn op of either kind: a local op is judged by its team's grant, a forwarded one
// by the TeamRoots of the lead host's entry, and only while that host is still paired and still allowed.
func (m *Module) underOpRoots(op spawnRow, dir string) (string, bool) {
	if op.LeadHostID == "" {
		return m.underTeamRoots(op.TeamID, dir)
	}
	h, ok := m.peerEntryByHostID(op.LeadHostID)
	if !ok || !h.AllowTeam {
		return "", false
	}
	return resolveUnderRoots(h.TeamRoots, dir)
}

// applySpawnIn is the `spawn` command: consent, a cwd under the granted roots, then the op accepted (step accepted,
// running) in the command's transaction. The runner is started after the commit. The answer is {accepted}; what comes of
// it arrives as a fact.
func applySpawnIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	switch {
	case !p.Consent:
		return refusal(http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team commands from the lead host"), nil
	case p.SpawnCwd == "":
		return refusal(http.StatusConflict, team.ErrCwdOutsideGrant, "the cwd is under none of the roots this host granted"), nil
	}
	var held int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM spawn_ops WHERE lead_host_id = ? AND state = 'running')
		+ (SELECT COUNT(*) FROM remote_members WHERE lead_host_id = ? AND state = ?)`, p.LeadHostID, p.LeadHostID, remoteActive).Scan(&held); err != nil {
		return CommandResult{}, err
	}
	if held >= maxPerLeadHost {
		return refusal(http.StatusConflict, team.ErrCommandCapacity, "this host already runs as many sessions for the lead host as it allows"), nil
	}
	name, err := team.SpawnTmuxName(c.ID)
	if err != nil {
		return refusal(http.StatusBadRequest, team.ErrCommandBadRequest, "the command id gives no tmux name"), nil
	}
	lead, err := json.Marshal(remoteSpawnLead{Lead: c.Lead, TeamName: c.TeamName, TeamLabel: c.TeamLabel, TeamColor: c.TeamColor})
	if err != nil {
		return CommandResult{}, err
	}
	op := spawnRow{ID: c.ID, TeamID: c.TeamID, HostID: p.HostID, OriginSessionID: c.Lead.SessionID, Cwd: p.SpawnCwd, Title: c.Title,
		Model: c.Model, Effort: c.Effort, TmuxName: name, Step: team.StepAccepted, State: team.SpawnRunning,
		CreatedAt: p.Now, UpdatedAt: p.Now, LeadHostID: p.LeadHostID, LeadJSON: string(lead)}
	if _, _, inserted, err := insertSpawnOp(tx, op, p.hash); err != nil {
		return CommandResult{}, err
	} else if !inserted {
		return CommandResult{}, fmt.Errorf("spawn %s: the op id is already a spawn op", c.ID)
	}
	return okResult(map[string]string{"state": "accepted"})
}

// startRemoteSpawn starts the runner of a spawn the commands route just accepted (not for a replay: that op is running or
// was resumed at boot).
func (m *Module) startRemoteSpawn(id string) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		return // the op stays running at its step: the next boot resumes it
	}
	m.startSpawn(id)
}

// killEndedSpawnSessions kills the tmux sessions of the forwarded ops a committed `end` failed (#2327), each only under
// the generation it was created in. An error (listing them, or the kill itself) is returned so the handler answers a
// retryable failure and the lead host sends the same command again, which lists them again; a generation that moved or
// a session already gone is not an error.
func (m *Module) killEndedSpawnSessions(leadHost, teamID string) error {
	ops, err := m.store.AbandonedSpawnSessions(leadHost, teamID)
	if err != nil {
		return err
	}
	if len(ops) > 0 && m.tmux == nil { // a daemon without tmux cannot kill them now; the retry finds them again
		return fmt.Errorf("%d abandoned spawn session(s) of team %s and no tmux on this daemon", len(ops), teamID)
	}
	var first error
	for _, op := range ops {
		if _, err := m.tmux.KillSessionIfInstance(op.TmuxID, op.TmuxInstance); err != nil && !errors.Is(err, tmux.ErrNoSession) { // gone already: a replay after a lost answer
			m.logf("[team] spawn %s: tmux session %s not killed on end: %v", op.ID, op.TmuxID, err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// validSpawnCommand is the shape of a `spawn` command ("" = fine).
func validSpawnCommand(c team.TeamCommand) string {
	switch {
	case c.MK != c.ID:
		return "spawn: mk is the command id"
	case c.Cwd == "" || !filepath.IsAbs(c.Cwd) || strings.ContainsRune(c.Cwd, 0):
		return "spawn: cwd must be an absolute path"
	case c.Model != "" && !team.ValidModel(c.Model), c.Effort != "" && !team.ValidEffort(c.Effort):
		return "spawn: unknown model or effort"
	case c.Title != "" && ipeers.ValidateTitle(c.Title) != nil:
		return "spawn: " + ipeers.ValidateTitle(c.Title).Error()
	case !completeLead(c.Lead):
		return "spawn: the lead's origin tuple (session_id, ref, address, pid, proc_start) is required"
	}
	return ""
}

// spawnFinishRemote is registered → done for a forwarded op: the remote member's row and the `registered` fact are
// written in the transaction that closes the op (CAS on step registered), no task and no local member row. A refused write
// aborts the op like a local one. o is the registry entry the registration saw (nil: read again, the ref alone if gone).
func (m *Module) spawnFinishRemote(op spawnRow, o *team.Origin) {
	var lead remoteSpawnLead
	if err := json.Unmarshal([]byte(op.LeadJSON), &lead); err != nil || lead.Lead.SessionID == "" {
		m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, fmt.Errorf("forwarded op %s: its lead is unreadable: %v", op.ID, err))
		return
	}
	if o == nil || o.SessionID != op.SessionID {
		r, ok, err := m.origins.ResolveOriginBySession(op.SessionID)
		if err != nil {
			m.logf("[team] spawn %s: %v", op.ID, err)
			return
		}
		if !ok {
			r = team.Origin{SessionID: op.SessionID, Ref: ipeers.RefID(op.SessionID)}
		}
		o = &r
	}
	now := m.now()
	mem := remoteMemberRow{MK: op.ID, MemberSessionID: op.SessionID, Ref: o.Ref, TeamID: op.TeamID, TeamName: lead.TeamName,
		LeadHostID: op.LeadHostID, LeadSessionID: lead.Lead.SessionID, LeadRef: lead.Lead.Ref, LeadAddress: lead.Lead.Address,
		LeadTitle: lead.Lead.Title, LeadPID: lead.Lead.PID, LeadProcStart: lead.Lead.ProcStart, Origin: team.MemberOriginSpawned,
		State: remoteActive, PID: o.PID, ProcStart: o.ProcStart, PaneID: op.PaneID, TmuxSession: op.TmuxName, Cwd: op.Cwd,
		Title: op.Title, Model: op.Model, Effort: op.Effort, CreatedAt: now, UpdatedAt: now}
	fact := team.TeamFact{ID: m.newID(), Kind: team.FactRegistered, ToHostID: op.LeadHostID, TeamID: op.TeamID, MK: op.ID,
		MemberSession: op.SessionID, Ref: o.Ref, PID: o.PID, ProcStart: o.ProcStart, Pane: op.PaneID, Title: op.Title}
	mem.TeamLabel = lead.TeamLabel
	if lead.TeamColor != nil {
		mem.TeamColor = sql.NullInt64{Int64: int64(*lead.TeamColor), Valid: true}
	}
	won, err := m.store.FinishRemoteSpawn(op.ID, mem, fact, now)
	if err != nil {
		m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
		return
	}
	if !won {
		return
	}
	if op.Title != "" && m.titleSet != nil {
		if _, err := m.titleSet.Claim(op.SessionID, op.Title, time.UnixMilli(now)); err != nil {
			m.logf("[team] spawn %s: title %q for %s: %v", op.ID, op.Title, op.SessionID, err)
		}
	}
	m.logf("[team] spawn %s done: remote member %s (%s) in %s for host %s", op.ID, o.Ref, op.SessionID, op.TmuxName, op.LeadHostID)
	m.kickFacts() // the registered fact is committed: tell the lead host now
	m.wake(op.ID)
}
