package teammod

import (
	"database/sql"
	"errors"
	"fmt"
)

// SetSelfRelayPaused records the per-session pause (spec §8.7 "a pause").
func (s *Store) SetSelfRelayPaused(sessionID string, paused bool, now int64) error {
	v := 0
	if paused {
		v = 1
	}
	if _, err := s.db.Exec(`INSERT INTO session_prefs (session_id, self_relay_paused, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET self_relay_paused = excluded.self_relay_paused, updated_at = excluded.updated_at`,
		sessionID, v, now); err != nil {
		return fmt.Errorf("set self relay paused %s: %w", sessionID, err)
	}
	return nil
}

// SelfRelayPaused reads the pause; a session without a row is not paused.
func (s *Store) SelfRelayPaused(sessionID string) (bool, error) {
	var v int
	err := s.db.QueryRow(`SELECT self_relay_paused FROM session_prefs WHERE session_id = ?`, sessionID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("self relay paused %s: %w", sessionID, err)
	}
	return v != 0, nil
}
