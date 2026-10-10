package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/wake/purdex/internal/team"
)

// MR-4 (member relay spec v2 D6, D8, §3.5): the 70% ask of a member whose lead is on another host. The ask is an ordinary
// relay_asks row on this host (spawn_op = the member key), the lead host is told by a `relay_ask` fact queued in the same
// transaction, and the lead host's notice and mirror are its own (facts_relay_ask.go). The mod is unchanged: it treats
// relay_unsupported as "stay quiet".

// remoteAskCapsTimeout bounds the lead host's capability read, as the remote adopt's.
const remoteAskCapsTimeout = 3 * time.Second

// askRemote handles the ask of a session that is an active remote member of this host. The capability read comes FIRST and
// writes nothing: a lead host that does not announce relay_ask (or cannot be asked) is relay_unsupported, with no ask row and
// no fact. Only then the ask and its fact are one transaction under createMu.
func (m *Module) askRemote(w http.ResponseWriter, req team.RelayAskRequest, row remoteMemberRow) {
	unsupported := func(detail string) {
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, detail, nil)
	}
	if m.cmdCaller == nil {
		unsupported("cross-host team is not available on this daemon")
		return
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, remoteAskCapsTimeout)
	caps, err := m.cmdCaller.TeamCaps(ctx, row.LeadHostID)
	cancel()
	if err != nil {
		unsupported("the lead host's capabilities could not be read: " + err.Error())
		return
	}
	if !slices.Contains(caps.FactKinds, team.FactRelayAsk) {
		unsupported("the lead host does not announce " + team.FactRelayAsk)
		return
	}
	m.createMu.Lock()
	now := m.now()
	ask, replay, err := m.store.CreateRemoteRelayAsk(RelayAsk{ID: req.RequestID, SessionID: req.SessionID, UsedPct: req.UsedPct, Window: req.Window,
		CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000})
	m.createMu.Unlock()
	switch {
	case errors.Is(err, ErrAskNotMember):
		m.writeErr(w, http.StatusConflict, team.ErrNotMember, "the session is not an active member of a live team", nil)
		return
	case errors.Is(err, ErrAskRelayOpen):
		if !m.writeRelayOpen(w, req.SessionID) {
			m.writeErr(w, http.StatusConflict, team.ErrRelayOpen, "this session already has a relay in progress", nil)
		}
		return
	case err != nil:
		m.logf("[team] relay ask %s: %v", req.RequestID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if ask.SessionID != req.SessionID {
		m.writeErr(w, http.StatusConflict, team.ErrBadRequest, "request_id belongs to another session's ask", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, team.RelayAskResponse{ID: ask.ID, State: ask.State, ExpiresAt: ask.ExpiresAt, Replay: replay})
}

// CreateRemoteRelayAsk is CreateRelayAsk for an active remote member, plus the `relay_ask` fact for its lead host in the same
// transaction (a replay queues none). The write lock is taken first, as CreateRelayAsk does.
func (s *Store) CreateRemoteRelayAsk(a RelayAsk) (stored RelayAsk, replay bool, err error) {
	fail := func(err error) (RelayAsk, bool, error) {
		return RelayAsk{}, false, fmt.Errorf("create remote relay ask %s: %w", a.ID, err)
	}
	if s.newID == nil {
		return fail(errors.New("no id source for the relay_ask fact"))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE remote_members SET state = state WHERE member_session_id = ?`, a.SessionID); err != nil {
		return fail(err)
	}
	if old, err := scanRelayAsk(tx.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE id = ?`, a.ID)); err == nil {
		return old, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	var mk, teamID, leadHost string
	err = tx.QueryRow(`SELECT mk, team_id, lead_host_id FROM remote_members WHERE member_session_id = ? AND state = ?`, a.SessionID, remoteActive).Scan(&mk, &teamID, &leadHost)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(ErrAskNotMember)
	}
	if err != nil {
		return fail(err)
	}
	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM relay_ops WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled') LIMIT 1`, a.SessionID).Scan(&one); {
	case err == nil:
		return fail(ErrAskRelayOpen)
	case !errors.Is(err, sql.ErrNoRows):
		return fail(err)
	}
	if open, err := scanRelayAsk(tx.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE session_id = ? AND state = 'open'`, a.SessionID)); err == nil {
		return open, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	a.TeamID, a.SpawnOp, a.State, a.Reason, a.OpID, a.NotifiedAt, a.ClosedAt = teamID, mk, team.RelayAskOpen, "", "", 0, 0
	if _, err := tx.Exec(`INSERT INTO relay_asks (`+relayAskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TeamID, a.SpawnOp, a.SessionID, a.UsedPct, a.Window, a.State, a.Reason, a.OpID, a.NotifiedAt, a.CreatedAt, a.ExpiresAt, a.ClosedAt); err != nil {
		return fail(err)
	}
	// No clocks cross hosts: the fact carries the window as a duration, fixed here (the pump drops the fact if the ask closes first).
	if err := writeFactIn(tx, team.TeamFact{ID: s.newID(), Kind: team.FactRelayAsk, ToHostID: leadHost, TeamID: teamID, MK: mk,
		AskID: a.ID, UsedPct: a.UsedPct, Window: a.Window, ExpiresInS: team.RelayAskHoldS}, a.CreatedAt); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	if s.onFacts != nil {
		s.onFacts()
	}
	return a, false, nil
}
