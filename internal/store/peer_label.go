// internal/store/peer_label.go
package store

import (
	"database/sql"
	"time"
)

// PeerLabel is one peer_labels row. Label is "" when the row was released.
type PeerLabel struct {
	SessionID string
	Label     string
	Rev       int64
	SetAt     time.Time
}

// PeerLabelStore persists Peer Address v2 labels on the shared meta DB.
type PeerLabelStore struct{ db *sql.DB }

// PeerLabels returns the label store backed by this MetaStore's DB.
func (m *MetaStore) PeerLabels() *PeerLabelStore { return &PeerLabelStore{db: m.db} }

// Snapshot returns every row, released ones included (Label "").
func (s *PeerLabelStore) Snapshot() ([]PeerLabel, error) {
	rows, err := s.db.Query(`SELECT session_id, label, rev, set_at FROM peer_labels ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PeerLabel, 0)
	for rows.Next() {
		var (
			p     PeerLabel
			label sql.NullString
			setAt int64
		)
		if err := rows.Scan(&p.SessionID, &label, &p.Rev, &setAt); err != nil {
			return nil, err
		}
		p.Label = label.String
		p.SetAt = time.UnixMilli(setAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// nextRev bumps peer_label_seq inside tx and returns the new value.
func nextRev(tx *sql.Tx) (int64, error) {
	var rev int64
	err := tx.QueryRow(`UPDATE peer_label_seq SET rev = rev + 1 WHERE id = 1 RETURNING rev`).Scan(&rev)
	return rev, err
}

// Claim gives label to sessionID: evicts any other row holding label (the
// caller has proven that holder is not live), bumps the revision and
// upserts, all in one transaction.
func (s *PeerLabelStore) Claim(sessionID, label string, now time.Time) (PeerLabel, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return PeerLabel{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM peer_labels WHERE label = ? AND session_id <> ?`, label, sessionID); err != nil {
		return PeerLabel{}, err
	}
	rev, err := nextRev(tx)
	if err != nil {
		return PeerLabel{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO peer_labels (session_id, label, rev, set_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET label = excluded.label, rev = excluded.rev, set_at = excluded.set_at
	`, sessionID, label, rev, now.UnixMilli()); err != nil {
		return PeerLabel{}, err
	}
	if err := tx.Commit(); err != nil {
		return PeerLabel{}, err
	}
	return PeerLabel{SessionID: sessionID, Label: label, Rev: rev, SetAt: now}, nil
}

// Release clears sessionID's label (row kept, rev bumped). ok is false
// when no row existed; nothing is written then.
//
// The transaction's FIRST statement is the no-op-or-not write below
// (UPDATE ... SET label = NULL WHERE session_id = ?), not a read: SQLite
// takes the write lock the instant that statement runs, rather than only
// after a preceding SELECT decides one is needed. A read-then-write here
// would open a window between the SELECT and the UPDATE in which another
// transaction could begin its own write and force this one to abort with
// BUSY_SNAPSHOT once it finally tries to upgrade to a write lock — leading
// with the write closes that window entirely. RowsAffected reports
// whether a row actually existed, so the "no row" case still short-
// circuits (via rollback) without a second round-trip.
func (s *PeerLabelStore) Release(sessionID string, now time.Time) (PeerLabel, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return PeerLabel{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE peer_labels SET label = NULL WHERE session_id = ?`, sessionID)
	if err != nil {
		return PeerLabel{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return PeerLabel{}, false, err
	}
	if n == 0 {
		// No row for this session: the rollback above discards this
		// statement's no-op write; nothing changes.
		return PeerLabel{}, false, nil
	}
	rev, err := nextRev(tx)
	if err != nil {
		return PeerLabel{}, false, err
	}
	if _, err := tx.Exec(`UPDATE peer_labels SET rev = ?, set_at = ? WHERE session_id = ?`, rev, now.UnixMilli(), sessionID); err != nil {
		return PeerLabel{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PeerLabel{}, false, err
	}
	return PeerLabel{SessionID: sessionID, Label: "", Rev: rev, SetAt: now}, true, nil
}
