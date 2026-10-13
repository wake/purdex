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
	// UnansweredAt (unix ms): the ask was final and its window passed with no answer; ExpiresAt is then the far sentinel (RT-2b T23).
	UnansweredAt int64
}

const relayAskCols = `id, team_id, spawn_op, session_id, used_pct, window, state, reason, op_id, notified_at, created_at, expires_at, closed_at, unanswered_at`

func scanRelayAsk(r rowScanner) (RelayAsk, error) {
	var a RelayAsk
	err := r.Scan(&a.ID, &a.TeamID, &a.SpawnOp, &a.SessionID, &a.UsedPct, &a.Window, &a.State, &a.Reason, &a.OpID, &a.NotifiedAt, &a.CreatedAt, &a.ExpiresAt, &a.ClosedAt, &a.UnansweredAt)
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
	a.TeamID, a.SpawnOp, a.State, a.Reason, a.OpID, a.NotifiedAt, a.ClosedAt, a.UnansweredAt = teamID, spawnOp, team.RelayAskOpen, "", "", 0, 0, 0
	if _, err := tx.Exec(`INSERT INTO relay_asks (`+relayAskCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TeamID, a.SpawnOp, a.SessionID, a.UsedPct, a.Window, a.State, a.Reason, a.OpID, a.NotifiedAt, a.CreatedAt, a.ExpiresAt, a.ClosedAt, 0); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return a, false, nil
}

// ExpireRelayAsks closes every open ask whose window has passed at now; n says how many. A FINAL ask (used ≥ RelayLastAskPct) of a
// member of this host is not closed: it stays open for MarkFinalAsksUnanswered (RT-2b T23); a mirror of a remote member's ask expires
// as it always did (S-8).
func (s *Store) ExpireRelayAsks(now int64) (int64, error) {
	loc, locArgs := s.local("m.host_id")
	res, err := s.db.Exec(`UPDATE relay_asks SET state = 'expired', closed_at = ? WHERE state = 'open' AND expires_at <= ?
		AND NOT (used_pct >= ? AND EXISTS (SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.spawn_op = relay_asks.spawn_op AND m.session_id = relay_asks.session_id AND m.state = 'active' AND t.ended_at = 0 AND `+loc+`))`,
		append([]any{now, now, team.RelayLastAskPct}, locArgs...)...)
	if err != nil {
		return 0, fmt.Errorf("expire relay asks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

const inactiveMemberCond = `NOT EXISTS (
			SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.spawn_op = relay_asks.spawn_op AND m.session_id = relay_asks.session_id AND m.state = 'active' AND t.ended_at = 0)
		AND NOT EXISTS (
			SELECT 1 FROM remote_members r
			WHERE r.mk = relay_asks.spawn_op AND r.member_session_id = relay_asks.session_id AND r.team_id = relay_asks.team_id AND r.state = 'active')`

// closeAsksIn withdraws, in the caller's transaction, every open ask matching cond (with reason) and ends the relay_unanswered pause
// of each one that was unanswered, telling its session text. n is how many asks closed.
func closeAsksIn(tx dbtxq, cond string, args []any, reason string, now int64, text string) (int64, error) {
	rows, err := tx.Query(`SELECT session_id FROM relay_asks WHERE state = 'open' AND unanswered_at > 0 AND (`+cond+`)`, args...)
	if err != nil {
		return 0, err
	}
	var unanswered []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return 0, err
		}
		unanswered = append(unanswered, sid)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	res, err := tx.Exec(`UPDATE relay_asks SET state = 'withdrawn', reason = ?, closed_at = ? WHERE state = 'open' AND (`+cond+`)`,
		append([]any{reason, now}, args...)...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	for _, sid := range unanswered {
		if err := endUnansweredPauseIn(tx, sid, text, now); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// WithdrawAsksOfInactiveMembers closes as withdrawn / member_left every open ask whose member row is no longer an
// active row of a live team (released, killing, killed, gone, or its team ended). A member left paused by its unanswered ask is
// resumed with the "ended" text.
func (s *Store) WithdrawAsksOfInactiveMembers(now int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("withdraw relay asks of inactive members: %w", err)
	}
	defer tx.Rollback()
	n, err := closeAsksIn(tx, inactiveMemberCond, nil, team.RelayAskWithdrawMemberLeft, now, team.PauseResumedUnansweredEnded)
	if err != nil {
		return 0, fmt.Errorf("withdraw relay asks of inactive members: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("withdraw relay asks of inactive members: %w", err)
	}
	return n, nil
}

// WithdrawRelayAsk closes the session's open ask as withdrawn with reason; ok says one was open. A session its unanswered ask had
// paused is resumed with the "ended" text.
func (s *Store) WithdrawRelayAsk(sessionID, reason string, now int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("withdraw relay ask of %s: %w", sessionID, err)
	}
	defer tx.Rollback()
	n, err := closeAsksIn(tx, `session_id = ?`, []any{sessionID}, reason, now, team.PauseResumedUnansweredEnded)
	if err != nil {
		return false, fmt.Errorf("withdraw relay ask of %s: %w", sessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("withdraw relay ask of %s: %w", sessionID, err)
	}
	return n == 1, nil
}

// OpenAskOf is the session's open ask, if it has one.
func (s *Store) OpenAskOf(sessionID string) (RelayAsk, bool, error) {
	a, err := scanRelayAsk(s.db.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE session_id = ? AND state = 'open'`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return RelayAsk{}, false, nil
	}
	if err != nil {
		return RelayAsk{}, false, fmt.Errorf("open relay ask of %s: %w", sessionID, err)
	}
	return a, true, nil
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

// OpenAskDeadlines maps the session of each of the team's members with an open, unexpired ask to when it runs out
// (unix ms): what GET /api/team shows.
func (s *Store) OpenAskDeadlines(teamID string, now int64) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT session_id, expires_at FROM relay_asks WHERE team_id = ? AND state = 'open' AND unanswered_at = 0 AND expires_at > ?`, teamID, now)
	if err != nil {
		return nil, fmt.Errorf("open relay asks of team %s: %w", teamID, err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var sid string
		var until int64
		if err := rows.Scan(&sid, &until); err != nil {
			return nil, fmt.Errorf("open relay asks of team %s: %w", teamID, err)
		}
		out[sid] = until
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

// DenyRelayAsk closes the session's open ask as withdrawn / denied in ONE transaction and returns it; closed says one was open. A
// member its unanswered ask had paused is resumed with text (the lead's, or the person's).
func (s *Store) DenyRelayAsk(sessionID string, now int64, text string) (RelayAsk, bool, error) {
	fail := func(err error) (RelayAsk, bool, error) {
		return RelayAsk{}, false, fmt.Errorf("deny relay ask of %s: %w", sessionID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	a, err := scanRelayAsk(tx.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE session_id = ? AND state = 'open'`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return RelayAsk{}, false, nil
	}
	if err != nil {
		return fail(err)
	}
	if _, err := closeAsksIn(tx, `session_id = ?`, []any{sessionID}, team.RelayAskWithdrawDenied, now, text); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return a, true, nil
}

// ReleaseDeniedAsk is a person's release of a member its unanswered final ask paused, in ONE write transaction: the member must
// still be an active member of a live team of this host (the write lock is taken first, so a release of the member cannot slip
// between the check and the close), its open unanswered ask closes withdrawn / denied, its relay_unanswered pause goes with it, and
// the other causes are released without a reask. closed false (nothing written) when the member has left or no unanswered ask is open.
func (s *Store) ReleaseDeniedAsk(sessionID string, now int64, newMod func(string) bool, newToken func() string) (RelayAsk, bool, ResumeResult, error) {
	fail := func(err error) (RelayAsk, bool, ResumeResult, error) {
		return RelayAsk{}, false, ResumeResult{}, fmt.Errorf("release denied ask of %s: %w", sessionID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE team_members SET state = state WHERE session_id = ?`, sessionID); err != nil {
		return fail(err)
	}
	loc, locArgs := s.local("m.host_id")
	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0 AND `+loc, append([]any{sessionID}, locArgs...)...).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return RelayAsk{}, false, ResumeResult{}, nil
	case err != nil:
		return fail(err)
	}
	a, err := scanRelayAsk(tx.QueryRow(`SELECT `+relayAskCols+` FROM relay_asks WHERE session_id = ? AND state = 'open' AND unanswered_at > 0`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return RelayAsk{}, false, ResumeResult{}, nil
	}
	if err != nil {
		return fail(err)
	}
	if _, err := closeAsksIn(tx, `session_id = ?`, []any{sessionID}, team.RelayAskWithdrawDenied, now, team.PauseResumedMemberDeniedByUser); err != nil {
		return fail(err)
	}
	if s.afterAskDeny != nil {
		if err := s.afterAskDeny(); err != nil {
			return fail(err)
		}
	}
	res, err := s.resumePausesIn(tx, ResumeScope{Release: sessionID, NoReask: true}, now, newMod, newToken)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return a, true, res, nil
}
