package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ContextUsageRow is the last statusline context reading of one CC session (#2406), kept so a daemon restart does not blank
// it until the session's next turn: Claude Code re-runs the statusline only on activity.
type ContextUsageRow struct {
	SessionID      string
	UsedPercentage *float64
	WindowSize     int
	ModelID        string
	Effort         string
	At             int64 // unix ms when the daemon received it
}

// ContextUsageStore is the persisted side of the agent module's contextUsage map. Rows are written coalesced by the
// module (one transaction per flush), never per statusline.
type ContextUsageStore struct{ db *sql.DB }

const contextUsageSchema = `
	CREATE TABLE IF NOT EXISTS context_usage (
		session_id      TEXT PRIMARY KEY,
		used_percentage REAL,
		window_size     INTEGER NOT NULL DEFAULT 0,
		model_id        TEXT    NOT NULL DEFAULT '',
		effort          TEXT    NOT NULL DEFAULT '',
		at              INTEGER NOT NULL
	);`

// ContextUsage opens the persisted context readings on this DB; a new table, so CREATE IF NOT EXISTS is the whole migration.
func (s *AgentEventStore) ContextUsage() (*ContextUsageStore, error) {
	if _, err := s.db.Exec(contextUsageSchema); err != nil {
		return nil, fmt.Errorf("migrate context_usage: %w", err)
	}
	return &ContextUsageStore{db: s.db}, nil
}

// Upsert writes the rows in one transaction; a row for a session replaces the one there.
func (c *ContextUsageStore) Upsert(rows []ContextUsageRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("context usage: %w", err)
	}
	defer tx.Rollback()
	for _, r := range rows {
		var pct any
		if r.UsedPercentage != nil {
			pct = *r.UsedPercentage
		}
		if _, err := tx.Exec(`INSERT INTO context_usage (session_id, used_percentage, window_size, model_id, effort, at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (session_id) DO UPDATE SET used_percentage = excluded.used_percentage, window_size = excluded.window_size,
				model_id = excluded.model_id, effort = excluded.effort, at = excluded.at`,
			r.SessionID, pct, r.WindowSize, r.ModelID, r.Effort, r.At); err != nil {
			return fmt.Errorf("context usage %s: %w", r.SessionID, err)
		}
	}
	return tx.Commit()
}

// Delete removes the sessions' rows (none is fine).
func (c *ContextUsageStore) Delete(sessionIDs []string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	args := make([]any, len(sessionIDs))
	for i, id := range sessionIDs {
		args[i] = id
	}
	_, err := c.db.Exec(`DELETE FROM context_usage WHERE session_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return fmt.Errorf("context usage delete: %w", err)
	}
	return nil
}

// DeleteAll empties the table (the statusline was removed: no stale reading may come back).
func (c *ContextUsageStore) DeleteAll() error {
	if _, err := c.db.Exec(`DELETE FROM context_usage`); err != nil {
		return fmt.Errorf("context usage delete all: %w", err)
	}
	return nil
}

// LoadAll returns every persisted reading, newest first.
func (c *ContextUsageStore) LoadAll() ([]ContextUsageRow, error) {
	rows, err := c.db.Query(`SELECT session_id, used_percentage, window_size, model_id, effort, at FROM context_usage ORDER BY at DESC, session_id`)
	if err != nil {
		return nil, fmt.Errorf("context usage load: %w", err)
	}
	defer rows.Close()
	var out []ContextUsageRow
	for rows.Next() {
		var r ContextUsageRow
		var pct sql.NullFloat64
		if err := rows.Scan(&r.SessionID, &pct, &r.WindowSize, &r.ModelID, &r.Effort, &r.At); err != nil {
			return nil, fmt.Errorf("context usage load: %w", err)
		}
		if pct.Valid {
			v := pct.Float64
			r.UsedPercentage = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
