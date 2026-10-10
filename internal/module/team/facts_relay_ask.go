package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// MR-4 (member relay spec v2 D6, §2, §3.2): `relay_ask` on the lead host. The member host stored the ask and sent it; this host
// keeps a MIRROR in relay_asks so the lead's notice is retried like a local ask's and a `pdx relay` accepts it.

// maxMirrorWindowS caps the window a member host may claim: no clocks are compared across hosts, so the mirror lives
// `min(expires_in_s, 300)` seconds from the moment this host received the fact.
const maxMirrorWindowS = team.RelayAskHoldS

// relayAskSuperseded is the reason of a mirror the member host's next ask replaced.
const relayAskSuperseded = "superseded"

// applyRelayAskIn applies a `relay_ask` fact. The row is the member of THIS host in this team with this mk, active, in a live team
// (never another host's, never a local row). The mirror is keyed by the member host's ask id, so a second fact for the same ask
// (another fact id) makes neither a second mirror nor a second notice ("ignored"); so does an ask when the member already has
// an open ask or an open relay op here.
func (s *Store) applyRelayAskIn(tx dbtx, p FactPlan) (CommandResult, error) {
	f := p.fact
	var spawnOp, sessionID, state string
	err := tx.QueryRow(`SELECT m.spawn_op, m.session_id, m.state FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.host_id = ? AND m.host_id <> ? AND m.mk = ? AND m.team_id = ? AND t.ended_at = 0`,
		p.FromHostID, s.localHostID, f.MK, f.TeamID).Scan(&spawnOp, &sessionID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no such membership of that host in that team"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	if state == rowJoining {
		return CommandResult{}, errMemberNotSettled // as `moved`: the adopt answer and the facts travel on separate queues
	}
	if state != string(team.MemberActive) {
		return okResult(map[string]string{"state": team.FactIgnored})
	}
	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM relay_ops WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled') LIMIT 1`, sessionID).Scan(&one); {
	case err == nil:
		return okResult(map[string]string{"state": team.FactIgnored})
	case !errors.Is(err, sql.ErrNoRows):
		return CommandResult{}, err
	}
	window := f.ExpiresInS
	if window > maxMirrorWindowS {
		window = maxMirrorWindowS
	}
	// An id that another row owns is refused BEFORE anything is written (a refusal has no side effect); this very ask's id is the
	// dedupe below.
	var ownTeam, ownSession string
	switch err := tx.QueryRow(`SELECT team_id, session_id FROM relay_asks WHERE id = ?`, f.AskID).Scan(&ownTeam, &ownSession); {
	case err == nil:
		if ownTeam != f.TeamID || ownSession != sessionID {
			return refusal(http.StatusConflict, team.ErrCommandIDConflict, "that ask id is already used by another ask here"), nil
		}
		return okResult(map[string]string{"state": team.FactIgnored})
	case !errors.Is(err, sql.ErrNoRows):
		return CommandResult{}, err
	}
	// The member host allows one open ask per session, so a mirror of the session that is still open belongs to an ask the member
	// host has closed since: this is its new one, and the old mirror goes (the one-open index would swallow the new one otherwise).
	// The same ask id again is the dedupe below, not a replacement.
	if _, err := tx.Exec(`UPDATE relay_asks SET state = 'withdrawn', reason = ?, closed_at = ? WHERE session_id = ? AND state = 'open' AND id <> ?`,
		relayAskSuperseded, p.Now, sessionID, f.AskID); err != nil {
		return CommandResult{}, err
	}
	r, err := tx.Exec(`INSERT INTO relay_asks (id, team_id, spawn_op, session_id, used_pct, window, state, reason, op_id, notified_at, created_at, expires_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, 'open', '', '', 0, ?, ?, 0) ON CONFLICT(id) DO NOTHING`,
		f.AskID, f.TeamID, spawnOp, sessionID, f.UsedPct, f.Window, p.Now, p.Now+int64(window)*1000)
	if err != nil {
		return CommandResult{}, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return okResult(map[string]string{"state": team.FactIgnored}) // taken meanwhile (the id was free above, this tx holds the write lock: not reachable)
	}
	return okResult(map[string]string{"state": team.FactApplied})
}

// bodyHasState says whether a fact's outcome body is {"state": want}.
func bodyHasState(body []byte, want string) bool {
	var o struct {
		State string `json:"state"`
	}
	return json.Unmarshal(body, &o) == nil && o.State == want
}

// remoteAskMember is the mirror's member and team on this (lead) host: the active remote row the ask is about.
func (s *Store) remoteAskMember(a RelayAsk) (memberRow, team.Team, bool, error) {
	var mr memberRow
	var t team.Team
	var grantJSON string
	err := s.db.QueryRow(`SELECT `+qualify("m", memberCols)+`, `+qualify("t", teamCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.team_id = ? AND m.spawn_op = ? AND m.session_id = ? AND m.state = 'active' AND t.ended_at = 0 AND m.host_id <> ?`,
		a.TeamID, a.SpawnOp, a.SessionID, s.localHostID).
		Scan(append(mr.dest(), teamDest(&t, &grantJSON)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return memberRow{}, team.Team{}, false, nil
	}
	if err == nil {
		err = decodeTeamGrant(&t, grantJSON)
	}
	if err != nil {
		return memberRow{}, team.Team{}, false, fmt.Errorf("remote member of ask %s: %w", a.ID, err)
	}
	return mr, t, true, nil
}

// remoteAskNoticeText is the lead's notice for a mirror ask: the same words as a local ask's, naming the member as the lead can
// address it — <alias>/_<ref> — or "" when this host cannot name the member host (no alias: nothing the lead could type).
func (m *Module) remoteAskNoticeText(mr memberRow, minutes, usedPct int) string {
	alias := m.remoteAlias(mr.HostID)
	if alias == "" {
		return ""
	}
	ref := strings.TrimPrefix(mr.Ref, "_")
	title := mr.Title
	if title == "" {
		title = mr.TmuxSession
	}
	return fmt.Sprintf(team.RelayAskRemoteNoticeFmt, alias+"/_"+ref, ref, title, usedPct, minutes, alias+"/_"+ref)
}
