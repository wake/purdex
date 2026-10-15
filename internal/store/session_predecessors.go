package store

import (
	"database/sql"
	"errors"
)

// migrateSessionPredecessorsTable creates the /clear record (additive, IF NOT EXISTS): which session a Claude Code
// session replaced by a `/clear` (manual or a relay's). Rows are never deleted (~150 bytes each).
func migrateSessionPredecessorsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS session_predecessors (
			session_id             TEXT PRIMARY KEY,
			predecessor_session_id TEXT NOT NULL,
			at                     INTEGER NOT NULL,
			tmux_code              TEXT NOT NULL
		)
	`)
	return err
}

// RecordPredecessor remembers that sid replaced prev (at is unix ms; code is the tmux session code, possibly empty).
// The first record for a sid wins.
func (s *FramesStore) RecordPredecessor(sid, prev string, at int64, code string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO session_predecessors (session_id, predecessor_session_id, at, tmux_code)
		VALUES (?, ?, ?, ?)`, sid, prev, at, code)
	return err
}

// PredecessorOf returns the session sid replaced and when (unix ms); ok is false when none was recorded.
func (s *FramesStore) PredecessorOf(sid string) (prev string, at int64, ok bool, err error) {
	err = s.db.QueryRow(`SELECT predecessor_session_id, at FROM session_predecessors WHERE session_id = ?`, sid).Scan(&prev, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return prev, at, true, nil
}
