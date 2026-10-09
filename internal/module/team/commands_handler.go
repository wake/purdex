// internal/module/team/commands_handler.go
package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/config"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// CommandsRoute is the cross-host team commands route (spec §6.2): a lead host's daemon POSTs one TeamCommand.
const CommandsRoute = "/api/peers/team/commands"

// maxCommandBody is the body cap of a command (§3.1 rule 8).
const maxCommandBody = 64 << 10

// maxCommandField bounds every string a command carries.
const maxCommandField = 256

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (m *Module) writeCommandErr(w http.ResponseWriter, status int, code, detail string) {
	m.writeJSON(w, status, team.CommandRefusal{Error: code, Detail: detail})
}

// handleTeamCommand serves POST /api/peers/team/commands. The order is the contract (spec §6.1, as /deliver):
// the binding of the host principal to its live entry, the per-host rate limit and the body cap BEFORE the body
// is decoded, then shape, to_host_id and kind; then the store decides in one transaction (idempotency by id and
// content, consent, the row change, the owed notice, the log entry).
//
// This version applies adopt, release, end and lead_moved. Any other kind is 400 unsupported_kind, never stored.
// The inventory announces no kind yet (X3d), so no lead host sends these.
func (m *Module) handleTeamCommand(w http.ResponseWriter, r *http.Request) {
	if m.stopCtx.Err() != nil {
		m.writeCommandErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping")
		return
	}
	var entry config.PeerHost
	var ourHostID string
	_, _, berr := peersmod.BindHostPrincipal(r, "team commands", func(alias string) (config.PeerHost, bool) {
		m.core.CfgMu.RLock()
		defer m.core.CfgMu.RUnlock()
		ourHostID = m.core.Cfg.HostID
		if i := m.core.Cfg.Peers.FindPeerHostByAlias(alias); i >= 0 {
			entry = m.core.Cfg.Peers.Hosts[i]
			return entry, true
		}
		return config.PeerHost{}, false
	})
	if berr != nil {
		m.logf("[team] command refused (%s): %s", berr.Code, berr.Detail)
		m.writeCommandErr(w, berr.Status, berr.Code, berr.Detail)
		return
	}

	// The JSON value is kept as received (surrounding whitespace is not content): the store hashes those bytes — a replay
	// is the same value bytes, a field of a newer version makes another command — and decodes them itself.
	var raw json.RawMessage
	switch st := peersmod.AdmitDecode(w, r, m.cmdLimit, entry.HostID, maxCommandBody, &raw); st {
	case 0:
	case http.StatusTooManyRequests:
		m.writeCommandErr(w, st, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	case http.StatusRequestEntityTooLarge:
		m.writeCommandErr(w, st, team.ErrCommandBadRequest, "body over 64 KiB")
		return
	default:
		m.writeCommandErr(w, st, team.ErrCommandBadRequest, "invalid JSON body")
		return
	}
	var cmd team.TeamCommand
	if err := json.Unmarshal(raw, &cmd); err != nil {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandBadRequest, "the body is not a command")
		return
	}
	if !uuidV4.MatchString(cmd.ID) || cmd.Kind == "" || cmd.TeamID == "" {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandBadRequest, "id (a UUID v4), kind and team_id are required")
		return
	}
	if cmd.ToHostID != ourHostID {
		m.writeCommandErr(w, http.StatusConflict, team.ErrCommandWrongHost, "this command is addressed to another host")
		return
	}
	switch cmd.Kind {
	case team.CommandAdopt, team.CommandRelease, team.CommandEnd, team.CommandLeadMoved:
	default:
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandUnsupportedKind, "this host does not apply "+boundText(cmd.Kind)+" commands")
		return
	}
	if msg := validateCommand(cmd); msg != "" {
		m.writeCommandErr(w, http.StatusBadRequest, team.ErrCommandBadRequest, msg)
		return
	}

	plan := CommandPlan{LeadHostID: entry.HostID, Body: raw, Consent: entry.AllowTeam, Now: m.now()}
	if cmd.Kind == team.CommandAdopt && entry.AllowTeam {
		o, found, err := m.origins.ResolveOriginBySession(cmd.TargetSessionID)
		if err != nil {
			m.logf("[team] command %s: resolve target: %v", cmd.ID, err)
			m.writeCommandErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry")
			return
		}
		if found && o.Ref == cmd.TargetRef {
			plan.Target = &o
		}
	}
	res, err := m.store.ApplyTeamCommand(plan)
	switch {
	case errors.Is(err, ErrCommandIDConflict):
		m.writeCommandErr(w, http.StatusConflict, team.ErrCommandIDConflict, "the id is already used by a different command")
		return
	case err != nil:
		m.logf("[team] command %s %s: %v", cmd.Kind, cmd.ID, err)
		m.writeCommandErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log")
		return
	}
	if res.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Body)
		return
	}
	m.writeJSON(w, http.StatusOK, team.TeamCommandAnswer{ID: cmd.ID, HostID: ourHostID, Outcome: res.Body})
}

// validateCommand checks the fields each kind needs and that every string is bounded and free of control
// characters (they end up in rows and, through templates, in notices). "" means valid.
func validateCommand(c team.TeamCommand) string {
	for _, s := range []string{c.ID, c.Kind, c.ToHostID, c.TeamID, c.TeamName, c.MK, c.Lead.SessionID, c.Lead.Ref, c.Lead.Title,
		c.Lead.Address, c.Lead.ProcStart, c.TargetSessionID, c.TargetRef, c.LeadSessionID, c.LeadRef, c.CommandID} {
		if len(s) > maxCommandField || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return "a field is over 256 bytes, not UTF-8, or holds a control character"
		}
	}
	if c.Lead.PID < 0 {
		return "lead.pid is negative"
	}
	switch c.Kind {
	case team.CommandAdopt:
		switch {
		case c.MK != c.ID:
			return "adopt: mk is the command id"
		case c.TargetSessionID == "" || c.TargetRef == "":
			return "adopt: target_session_id and target_ref are required"
		case c.Lead.SessionID == "" || c.Lead.Ref == "" || c.Lead.Address == "":
			return "adopt: the lead's session_id, ref and address are required"
		}
	case team.CommandRelease:
		if c.MK == "" {
			return "release: mk is required"
		}
	case team.CommandLeadMoved:
		if c.LeadSessionID == "" || c.LeadRef == "" || c.Lead.Address == "" {
			return "lead_moved: lead_session_id, lead_ref and the lead's address are required"
		}
	}
	return ""
}

// boundText is s cut to 64 bytes on a rune boundary, for an error detail echoing the sender's text.
func boundText(s string) string {
	if len(s) <= 64 {
		return s
	}
	cut := 64
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// newCommandLimiter is the per-lead-host admission limiter of the commands route (the /deliver rate).
func newCommandLimiter() *peersmod.HostLimiter {
	return peersmod.NewHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, time.Now)
}
