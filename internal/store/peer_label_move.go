package store

import (
	"database/sql"
	"errors"
	"time"
)

// Move carries fromSessionID's label to toSessionID (lead-team-relay spec
// §8.4: "the title moves to the new session id"), in one transaction: the
// new row takes the label with a fresh rev, the old row is released (kept,
// label NULL, rev bumped). moved is false — and nothing is written — when
// the old session has no label; that is what makes a retry (the daemon's
// boot reconciliation re-runs the move for a cleared op) a no-op. A label
// the new session already held is replaced: the relay's identity wins.
//
// The first statement is a write on the old row (same reasoning as
// Release): SQLite takes the write lock at once, so a concurrent Claim
// cannot slip between the read and the write. It is a no-op UPDATE with
// RETURNING, which reads the current label under that lock in one step.
func (s *PeerLabelStore) Move(fromSessionID, toSessionID string, now time.Time) (moved bool, err error) {
	if fromSessionID == "" || toSessionID == "" || fromSessionID == toSessionID {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var label sql.NullString
	err = tx.QueryRow(`UPDATE peer_labels SET set_at = set_at WHERE session_id = ? RETURNING label`, fromSessionID).Scan(&label)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!label.Valid || label.String == "")) {
		return false, nil // no row, or already released: nothing to move
	}
	if err != nil {
		return false, err
	}
	ms := now.UnixMilli()
	newRev, err := nextRev(tx)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO peer_labels (session_id, label, rev, set_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET label = excluded.label, rev = excluded.rev, set_at = excluded.set_at
	`, toSessionID, label.String, newRev, ms); err != nil {
		return false, err
	}
	oldRev, err := nextRev(tx)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE peer_labels SET label = NULL, rev = ?, set_at = ? WHERE session_id = ?`, oldRev, ms, fromSessionID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
