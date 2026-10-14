package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// A session's fresh half (RT-3a1 T31): the handoff file an idle half wrote, kept so the next full relay can reuse it instead of writing it
// again. relay_fresh_half holds at most one row per session; it goes when the file is no longer current: the mod says so, a compaction, a
// relay that claims, the session ending.

// FreshHalfOf is sid's fresh half; ok false when it has none.
func (s *Store) FreshHalfOf(sid string) (team.RelayFreshHalf, bool, error) {
	var h team.RelayFreshHalf
	err := s.db.QueryRow(`SELECT op_id, path, written_at FROM relay_fresh_half WHERE session_id = ?`, sid).Scan(&h.OpID, &h.Path, &h.WrittenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayFreshHalf{}, false, nil
	}
	if err != nil {
		return team.RelayFreshHalf{}, false, fmt.Errorf("fresh half of %s: %w", sid, err)
	}
	return h, true, nil
}

// DeleteFreshHalf drops sid's fresh half; opID, when not empty, drops it only if it is that op's.
func (s *Store) DeleteFreshHalf(sid, opID string) error {
	var err error
	if opID == "" {
		_, err = s.db.Exec(`DELETE FROM relay_fresh_half WHERE session_id = ?`, sid)
	} else {
		_, err = s.db.Exec(`DELETE FROM relay_fresh_half WHERE session_id = ? AND op_id = ?`, sid, opID)
	}
	if err != nil {
		return fmt.Errorf("delete fresh half of %s: %w", sid, err)
	}
	return nil
}

// FreshSessions lists the sessions that hold a fresh half.
func (s *Store) FreshSessions() ([]string, error) {
	rows, err := s.db.Query(`SELECT session_id FROM relay_fresh_half`)
	if err != nil {
		return nil, fmt.Errorf("list fresh halves: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, fmt.Errorf("list fresh halves: %w", err)
		}
		out = append(out, sid)
	}
	return out, rows.Err()
}

// reusableHalf is the file a begin may reuse: opID must be sid's fresh half, the op sid's own cancelled / half op, and the file there and
// not empty. ok false is 409 reuse_invalid (a storage error is err).
func (m *Module) reusableHalf(sid, opID string) (path string, ok bool, err error) {
	h, found, err := m.store.FreshHalfOf(sid)
	if err != nil || !found || h.OpID != opID {
		return "", false, err
	}
	op, found, err := m.store.GetRelayOp(opID)
	if err != nil || !found || op.SessionID != sid || op.State != team.RelayCancelled || op.Reason != team.RelayReasonHalf || op.Mode != team.RelayModeHalf {
		return "", false, err
	}
	st, serr := os.Stat(h.Path)
	if serr != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return "", false, nil
	}
	return h.Path, true, nil
}

// handleRelayFresh is POST /api/relay/fresh: the mod reports the half's file stale (S-10), and the row goes. An op that is not the fresh
// half's leaves it.
func (m *Module) handleRelayFresh(w http.ResponseWriter, r *http.Request) {
	var req team.RelayFreshRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.OpID) == "" || !req.Stale {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id, op_id and stale:true are required", nil)
		return
	}
	// behind a begin that is validating a reuse (createMu): either the begin sees the row gone (409 reuse_invalid) or it has opened its op
	// and this report follows it - never between the validation and the insert (codex)
	m.createMu.Lock()
	err := m.store.DeleteFreshHalf(req.SessionID, req.OpID)
	m.createMu.Unlock()
	if err != nil {
		m.logf("[team] relay fresh %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// sweepFreshHalves drops the fresh half of a session that is gone (the liveness tick).
func (m *Module) sweepFreshHalves() {
	sids, err := m.store.FreshSessions()
	if err != nil {
		m.logf("[team] sweep fresh halves: %v", err)
		return
	}
	for _, sid := range sids {
		if m.sessionGone("fresh half of "+sid, sid) {
			if err := m.store.DeleteFreshHalf(sid, ""); err != nil {
				m.logf("[team] sweep fresh halves: %v", err)
			}
		}
	}
}
