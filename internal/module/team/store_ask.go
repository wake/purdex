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

// OverrideIfApproved is the second CAS of spec §6.6 step 5: a hook row a
// remote decide already closed as approved becomes terminal_override,
// carrying the terminal's answers and decided_by terminal. The UPDATE is
// guarded by state='approved', so a row closed any other way is left as it
// is (won=false) and the caller answers with it.
func (s *Store) OverrideIfApproved(id string, decidedAt int64, hook *team.HookDecision) (team.Approval, bool, error) {
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
		WHERE id = ? AND state = 'approved' AND kind IN ('hook_ask', 'hook_permission')`,
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
