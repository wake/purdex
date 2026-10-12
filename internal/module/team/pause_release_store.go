// internal/module/team/pause_release_store.go — the release token and the card it opens (unattended quota pause spec 2026-10-12
// §3.4, UP-3b).
package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// errTokenGone: the release token a begin meant to spend was not there when its transaction ran.
var errTokenGone = errors.New("release token is gone")

// ReleaseTokenValid says whether token is a release token of sessionID written at or after notBefore (unix ms): it exists, is this
// session's, and is under 24 h old. A token is spent by deleting it, so "unused" is "still there".
func (s *Store) ReleaseTokenValid(token, sessionID string, notBefore int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM pause_release_tokens WHERE token = ? AND session_id = ? AND created_at >= ?`, token, sessionID, notBefore).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("release token: %w", err)
	}
	return true, nil
}

// CreateSelfRelayCard is the begin of a re-ask that carries a valid release token: in ONE transaction the token is spent (deleted;
// errTokenGone when it is no longer valid), the op is inserted awaiting_approval and its card (the self_relay row) inserted open. The
// card is a person's: nothing approves it for them. A failure writes nothing and spends nothing, so the mod's retry still has its token.
func (s *Store) CreateSelfRelayCard(op team.RelayOp, a team.Approval, hash, token string, notBefore int64) (team.Approval, error) {
	fail := func(err error) (team.Approval, error) {
		return team.Approval{}, fmt.Errorf("create reask card %s: %w", a.ID, err)
	}
	if err := sameSelfRelay(op, a); err != nil {
		return fail(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// The first statement is a write, so the write lock is taken before anything is read.
	res, err := tx.Exec(`DELETE FROM pause_release_tokens WHERE token = ? AND session_id = ? AND created_at >= ?`, token, a.Origin.SessionID, notBefore)
	if err != nil {
		return fail(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fail(err)
	} else if n != 1 {
		return fail(errTokenGone)
	}
	if err := insertRelayOpIn(tx, op); err != nil {
		return fail(err)
	}
	if _, err := insertRowIn(tx, a, hash, ""); err != nil {
		return fail(err)
	}
	stored, _, err := getRowIn(tx, a.ID)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return stored, nil
}
