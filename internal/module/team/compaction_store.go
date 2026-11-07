package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

const compactionSchema = `
	CREATE TABLE IF NOT EXISTS session_compaction (
		session_id TEXT PRIMARY KEY,
		auto       INTEGER,
		threshold  INTEGER,
		idle       INTEGER,
		updated_at INTEGER NOT NULL
	);`

func boolToNull(b *bool) any {
	if b == nil {
		return nil
	}
	if *b {
		return 1
	}
	return 0
}

func intToNull(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

// CompactionOverride reads the per-session override; a session without a row follows the host default (zero value).
func (s *Store) CompactionOverride(sessionID string) (team.CompactionOverride, error) {
	var o team.CompactionOverride
	var a, i sql.NullBool
	var t sql.NullInt64
	err := s.db.QueryRow(`SELECT auto, threshold, idle FROM session_compaction WHERE session_id = ?`, sessionID).Scan(&a, &t, &i)
	if errors.Is(err, sql.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, fmt.Errorf("compaction override %s: %w", sessionID, err)
	}
	if a.Valid {
		o.Auto = &a.Bool
	}
	if i.Valid {
		o.Idle = &i.Bool
	}
	if t.Valid {
		v := int(t.Int64)
		o.Threshold = &v
	}
	return o, nil
}

// SetCompactionOverride stores o; all-nil removes the row.
func (s *Store) SetCompactionOverride(sessionID string, o team.CompactionOverride, now int64) error {
	if o.Auto == nil && o.Threshold == nil && o.Idle == nil {
		if _, err := s.db.Exec(`DELETE FROM session_compaction WHERE session_id = ?`, sessionID); err != nil {
			return fmt.Errorf("clear compaction override %s: %w", sessionID, err)
		}
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO session_compaction (session_id, auto, threshold, idle, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET auto = excluded.auto, threshold = excluded.threshold, idle = excluded.idle, updated_at = excluded.updated_at`,
		sessionID, boolToNull(o.Auto), intToNull(o.Threshold), boolToNull(o.Idle), now); err != nil {
		return fmt.Errorf("set compaction override %s: %w", sessionID, err)
	}
	return nil
}

// MergeCompactionOverride writes the non-nil fields of patch over the stored override in one statement, so two
// concurrent patches of different fields both land. The caller validates patch (checkCompactionOverride): only the
// threshold is checked and a stored one was already valid, so patch valid means the merged value is valid.
func (s *Store) MergeCompactionOverride(sessionID string, patch team.CompactionOverride, now int64) error {
	if _, err := s.db.Exec(`INSERT INTO session_compaction (session_id, auto, threshold, idle, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET auto = COALESCE(excluded.auto, auto), threshold = COALESCE(excluded.threshold, threshold),
		idle = COALESCE(excluded.idle, idle), updated_at = excluded.updated_at`,
		sessionID, boolToNull(patch.Auto), intToNull(patch.Threshold), boolToNull(patch.Idle), now); err != nil {
		return fmt.Errorf("merge compaction override %s: %w", sessionID, err)
	}
	return nil
}

// copyCompactionTx gives the successor session the predecessor's override; no source row, nothing to do.
func copyCompactionTx(tx *sql.Tx, from, to string, now int64) error {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_compaction (session_id, auto, threshold, idle, updated_at)
		SELECT ?, auto, threshold, idle, ? FROM session_compaction WHERE session_id = ?`, to, now, from); err != nil {
		return fmt.Errorf("copy compaction override %s -> %s: %w", from, to, err)
	}
	return nil
}
