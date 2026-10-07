package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// OpenByToolUse returns the open hook row of the origin session for this
// tool_use_id, if any (spec §6.6: one open request per tool_use_id). The
// id lives in the payload, so the match is on json_extract.
func (s *Store) OpenByToolUse(sessionID, toolUseID string) (team.Approval, bool, error) {
	a, _, err := scanRow(s.db.QueryRow(`SELECT `+selectCols+` FROM approval_requests
		WHERE origin_session_id = ? AND state = 'open' AND kind IN ('hook_ask', 'hook_permission')
		  AND json_extract(payload_json, '$.tool_use_id') = ?
		ORDER BY created_at, id LIMIT 1`, sessionID, toolUseID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, false, nil
	}
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("open approval by tool use %s/%s: %w", sessionID, toolUseID, err)
	}
	return a, true, nil
}

// OpenTerminalOnlyBySession returns the open terminal_only hook rows of a
// session, oldest first (the settings-hook backstop closes them by
// tool_use_id, tool_name, or all at once on Stop).
func (s *Store) OpenTerminalOnlyBySession(sessionID string) ([]team.Approval, error) {
	rows, err := s.db.Query(`SELECT `+selectCols+` FROM approval_requests
		WHERE origin_session_id = ? AND state = 'open' AND kind IN ('hook_ask', 'hook_permission')
		  AND json_extract(payload_json, '$.terminal_only') = 1
		ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("open terminal_only approvals of %s: %w", sessionID, err)
	}
	defer rows.Close()
	out := []team.Approval{}
	for rows.Next() {
		a, _, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("open terminal_only approvals of %s: %w", sessionID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("open terminal_only approvals of %s: %w", sessionID, err)
	}
	return out, nil
}

// terminalOnlyGuard narrows closeRowIn's open CAS to an open terminal_only
// hook row (its one ? is bound to 1).
const terminalOnlyGuard = ` AND kind IN ('hook_ask', 'hook_permission') AND json_extract(payload_json, '$.terminal_only') = ?`

// ReplaceTerminalOnly is the takeover of spec §6.6 step 1: the mod's begin
// for a tool use whose read-only (terminal_only) card the settings hook
// opened first. In one transaction it closes the old row by the open CAS
// (guarded to an open terminal_only hook row) and inserts newRow (state
// open; an existing id is an error). Any error rolls both back: the old row
// stays open and the caller announces nothing. won says whether this call
// closed the old row and closedOld is that row after the close; when
// something else closed it first, won is false, closedOld is zero and only
// newRow is inserted. newStored is the inserted row.
func (s *Store) ReplaceTerminalOnly(oldID string, c Close, newRow team.Approval, hash string) (closedOld team.Approval, won bool, newStored team.Approval, err error) {
	fail := func(err error) (team.Approval, bool, team.Approval, error) {
		return team.Approval{}, false, team.Approval{}, fmt.Errorf("replace terminal_only %s with %s: %w", oldID, newRow.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at once.
	n, err := closeRowIn(tx, oldID, c, terminalOnlyGuard, 1)
	if err != nil {
		return fail(err)
	}
	if s.beforeReplaceInsert != nil {
		if err := s.beforeReplaceInsert(); err != nil {
			return fail(err)
		}
	}
	ins, err := insertRowIn(tx, newRow, hash, "")
	if err != nil {
		return fail(err)
	}
	if ins != 1 {
		return fail(fmt.Errorf("insert affected %d rows", ins))
	}
	if n == 1 {
		if closedOld, _, err = getRowIn(tx, oldID); err != nil {
			return fail(err)
		}
	}
	if newStored, _, err = getRowIn(tx, newRow.ID); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return closedOld, n == 1, newStored, nil
}

// OverrideIfDecided is the second CAS of spec §6.6 step 5: a hook row a
// remote decide already closed — approved, or denied (a hook_permission
// deny) — becomes terminal_override, carrying the terminal's answer and
// decided_by terminal: the terminal's answer stands whichever way the
// remote one went. The UPDATE is guarded by state IN ('approved',
// 'denied'), so a row closed any other way (or already overridden) is left
// as it is (won=false) and the caller answers with it.
func (s *Store) OverrideIfDecided(id string, decidedAt int64, hook *team.HookDecision) (team.Approval, bool, error) {
	var hookJSON any
	if hook != nil {
		b, err := json.Marshal(hook)
		if err != nil {
			return team.Approval{}, false, fmt.Errorf("encode hook decision: %w", err)
		}
		hookJSON = string(b)
	}
	by, err := json.Marshal(team.Client{Kind: team.ClientKindTerminal, Label: team.ClientKindTerminal})
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("encode decided_by: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE approval_requests
		SET state = ?, decided_at = ?, decided_by_json = ?, grant_json = ?
		WHERE id = ? AND state IN ('approved', 'denied') AND kind IN ('hook_ask', 'hook_permission')`,
		string(team.StateTerminalOverride), decidedAt, string(by), hookJSON, id)
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("override approval %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("override approval %s rows affected: %w", id, err)
	}
	a, _, err := s.getRow(id)
	if err != nil {
		return team.Approval{}, false, err
	}
	return a, n == 1, nil
}

// ListOpenNonHook is ListOpen without the hook kinds: what a restart would
// interrupt (GET /api/team/inflight). A hook row rides out a restart: its
// poller is restart-aware and its lease gets the boot grace.
func (s *Store) ListOpenNonHook() ([]team.Approval, error) {
	all, err := s.ListOpen()
	if err != nil {
		return nil, err
	}
	out := []team.Approval{}
	for _, a := range all {
		if !team.IsHookKind(a.Kind) {
			out = append(out, a)
		}
	}
	return out, nil
}
