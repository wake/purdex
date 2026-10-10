package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// A member's relay ask (spec 2026-10-10-member-relay-ask §2): the member's mod asks its lead to relay it; the lead
// answers with the existing `pdx relay _<ref>`, which marks the open ask accepted in the op's own transaction. The rows
// only record the ask and its window; the daemon decides nothing (U9).

// relayAskSchema is the new table (CREATE IF NOT EXISTS: a new table needs no migration).
const relayAskSchema = `
	CREATE TABLE IF NOT EXISTS relay_asks (
		id          TEXT PRIMARY KEY,
		team_id     TEXT    NOT NULL,
		spawn_op    TEXT    NOT NULL,
		session_id  TEXT    NOT NULL,
		used_pct    INTEGER NOT NULL,
		window      INTEGER NOT NULL,
		state       TEXT    NOT NULL CHECK (state IN ('open','accepted','expired','withdrawn')),
		reason      TEXT    NOT NULL DEFAULT '',
		op_id       TEXT    NOT NULL DEFAULT '',
		notified_at INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL,
		closed_at   INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX IF NOT EXISTS relay_asks_one_open ON relay_asks (session_id) WHERE state = 'open';`

// Refusals of CreateRelayAsk; nothing was written.
var (
	ErrAskNotMember = errors.New("the session is not an active member of a live team")
	ErrAskRemote    = errors.New("the member lives on another host")
	ErrAskRelayOpen = errors.New("the session has a relay op open")
)

// RelayAsk is one row of relay_asks. Times are unix ms.
type RelayAsk struct {
	ID         string
	TeamID     string
	SpawnOp    string
	SessionID  string
	UsedPct    int
	Window     int
	State      string
	Reason     string
	OpID       string
	NotifiedAt int64
	CreatedAt  int64
	ExpiresAt  int64
	ClosedAt   int64
}

const relayAskCols = `id, team_id, spawn_op, session_id, used_pct, window, state, reason, op_id, notified_at, created_at, expires_at, closed_at`

func scanRelayAsk(r rowScanner) (RelayAsk, error) {
	var a RelayAsk
	err := r.Scan(&a.ID, &a.TeamID, &a.SpawnOp, &a.SessionID, &a.UsedPct, &a.Window, &a.State, &a.Reason, &a.OpID, &a.NotifiedAt, &a.CreatedAt, &a.ExpiresAt, &a.ClosedAt)
	return a, err
}

// GetRelayAsk reads one ask by id.
func (s *Store) GetRelayAsk(id string) (RelayAsk, bool, error) {
	a, err := scanRelayAsk(s.db.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RelayAsk{}, false, nil
	}
	if err != nil {
		return RelayAsk{}, false, fmt.Errorf("get relay ask %s: %w", id, err)
	}
	return a, true, nil
}

// CreateRelayAsk is the §3.1 step-3 transaction. The write lock is taken before any read (as CreateMemberRelayOp does),
// so a lead's `pdx relay`, a release or an EndTeam in another connection cannot slip between the reads and the insert.
// In it, in order:
//   - the request id is already stored → that ask, replay true (whatever its state);
//   - the session is no active member of a live team → ErrAskNotMember; a member row of another host → ErrAskRemote;
//   - the session has a non-terminal relay op → ErrAskRelayOpen;
//   - the session has an open ask → that ask, replay true;
//   - else a.ID is inserted open (TeamID and SpawnOp come from the member row; State, NotifiedAt, ClosedAt are set here).
func (s *Store) CreateRelayAsk(a RelayAsk) (stored RelayAsk, replay bool, err error) {
	fail := func(err error) (RelayAsk, bool, error) {
		return RelayAsk{}, false, fmt.Errorf("create relay ask %s: %w", a.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE team_members SET state = state WHERE session_id = ?`, a.SessionID); err != nil {
		return fail(err)
	}
	if old, err := scanRelayAsk(tx.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE id = ?`, a.ID)); err == nil {
		return old, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	var teamID, spawnOp, hostID string
	err = tx.QueryRow(`SELECT m.team_id, m.spawn_op, m.host_id FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`, a.SessionID).Scan(&teamID, &spawnOp, &hostID)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(ErrAskNotMember)
	}
	if err != nil {
		return fail(err)
	}
	if s.localHostID != "" && hostID != "" && hostID != s.localHostID {
		return fail(ErrAskRemote)
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
	a.TeamID, a.SpawnOp, a.State, a.Reason, a.OpID, a.NotifiedAt, a.ClosedAt = teamID, spawnOp, team.RelayAskOpen, "", "", 0, 0
	if _, err := tx.Exec(`INSERT INTO relay_asks (`+relayAskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TeamID, a.SpawnOp, a.SessionID, a.UsedPct, a.Window, a.State, a.Reason, a.OpID, a.NotifiedAt, a.CreatedAt, a.ExpiresAt, a.ClosedAt); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return a, false, nil
}

// ExpireRelayAsks closes every open ask whose window has passed at now; n says how many.
func (s *Store) ExpireRelayAsks(now int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE relay_asks SET state = 'expired', closed_at = ? WHERE state = 'open' AND expires_at <= ?`, now, now)
	if err != nil {
		return 0, fmt.Errorf("expire relay asks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// WithdrawAsksOfInactiveMembers closes as withdrawn / member_left every open ask whose member row is no longer an
// active row of a live team (released, killing, killed, gone, or its team ended).
func (s *Store) WithdrawAsksOfInactiveMembers(now int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE relay_asks SET state = 'withdrawn', reason = ?, closed_at = ?
		WHERE state = 'open' AND NOT EXISTS (
			SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.spawn_op = relay_asks.spawn_op AND m.session_id = relay_asks.session_id AND m.state = 'active' AND t.ended_at = 0)`,
		team.RelayAskWithdrawMemberLeft, now)
	if err != nil {
		return 0, fmt.Errorf("withdraw relay asks of inactive members: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// WithdrawRelayAsk closes the session's open ask as withdrawn with reason; ok says one was open.
func (s *Store) WithdrawRelayAsk(sessionID, reason string, now int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE relay_asks SET state = 'withdrawn', reason = ?, closed_at = ? WHERE session_id = ? AND state = 'open'`, reason, now, sessionID)
	return oneRow(res, err, "withdraw relay ask of "+sessionID)
}

// MarkAskNotified records that the lead's notice was delivered, once, while the ask is open.
func (s *Store) MarkAskNotified(id string, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE relay_asks SET notified_at = ? WHERE id = ? AND state = 'open' AND notified_at = 0`, at, id)
	return oneRow(res, err, "mark relay ask "+id+" notified")
}

// ListUnnotifiedAsks returns the open asks that still owe their lead a notice at now: not delivered yet, window not
// passed. Oldest first.
func (s *Store) ListUnnotifiedAsks(now int64) ([]RelayAsk, error) {
	rows, err := s.db.Query(`SELECT `+relayAskCols+` FROM relay_asks WHERE state = 'open' AND notified_at = 0 AND expires_at > ? ORDER BY created_at, id`, now)
	if err != nil {
		return nil, fmt.Errorf("list unnotified relay asks: %w", err)
	}
	defer rows.Close()
	var out []RelayAsk
	for rows.Next() {
		a, err := scanRelayAsk(rows)
		if err != nil {
			return nil, fmt.Errorf("list unnotified relay asks: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneRelayAsks deletes the closed asks that closed before the cut-off (unix ms). An open ask is never deleted.
func (s *Store) PruneRelayAsks(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM relay_asks WHERE state != 'open' AND closed_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("prune relay asks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
