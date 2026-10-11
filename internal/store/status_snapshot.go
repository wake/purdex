package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// StatusSnapshotRow is the last statusLine snapshot the UI shows for one tmux session, kept so a daemon restart does not blank
// the status line until the session's next activity. Value is the JSON the daemon broadcasts (without its "at").
type StatusSnapshotRow struct {
	Code         string
	Value        string
	ReceivedAt   int64  // unix ms when the daemon received it
	TmuxInstance string // tmux server generation at write time; "" when unknown
}

// StatusSnapshotStore is the persisted side of the agent module's statusSnapshots map. Disposable: the table may be absent
// or empty and the module must still run.
type StatusSnapshotStore struct{ db *sql.DB }

const statusSnapshotSchema = `
	CREATE TABLE IF NOT EXISTS status_snapshots (
		code          TEXT PRIMARY KEY,
		value         TEXT    NOT NULL,
		received_at   INTEGER NOT NULL,
		tmux_instance TEXT    NOT NULL DEFAULT ''
	);`

// StatusSnapshots opens the persisted snapshots on this DB; a new table, so CREATE IF NOT EXISTS is the whole migration.
func (s *AgentEventStore) StatusSnapshots() (*StatusSnapshotStore, error) {
	if _, err := s.db.Exec(statusSnapshotSchema); err != nil {
		return nil, fmt.Errorf("migrate status_snapshots: %w", err)
	}
	return &StatusSnapshotStore{db: s.db}, nil
}

// Upsert writes the rows in one transaction.
func (c *StatusSnapshotStore) Upsert(rows []StatusSnapshotRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("status snapshots: %w", err)
	}
	defer tx.Rollback()
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO status_snapshots (code, value, received_at, tmux_instance) VALUES (?, ?, ?, ?)
			ON CONFLICT (code) DO UPDATE SET value = excluded.value, received_at = excluded.received_at, tmux_instance = excluded.tmux_instance`,
			r.Code, r.Value, r.ReceivedAt, r.TmuxInstance); err != nil {
			return fmt.Errorf("status snapshot %s: %w", r.Code, err)
		}
	}
	return tx.Commit()
}

// Delete removes the codes' rows (none is fine).
func (c *StatusSnapshotStore) Delete(codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	args := make([]any, len(codes))
	for i, id := range codes {
		args[i] = id
	}
	if _, err := c.db.Exec(`DELETE FROM status_snapshots WHERE code IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...); err != nil {
		return fmt.Errorf("status snapshots delete: %w", err)
	}
	return nil
}

// DeleteAll empties the table (the statusline was removed).
func (c *StatusSnapshotStore) DeleteAll() error {
	if _, err := c.db.Exec(`DELETE FROM status_snapshots`); err != nil {
		return fmt.Errorf("status snapshots delete all: %w", err)
	}
	return nil
}

// LoadAll returns every persisted snapshot, newest first.
func (c *StatusSnapshotStore) LoadAll() ([]StatusSnapshotRow, error) {
	rows, err := c.db.Query(`SELECT code, value, received_at, tmux_instance FROM status_snapshots ORDER BY received_at DESC, code`)
	if err != nil {
		return nil, fmt.Errorf("status snapshots load: %w", err)
	}
	defer rows.Close()
	var out []StatusSnapshotRow
	for rows.Next() {
		var r StatusSnapshotRow
		if err := rows.Scan(&r.Code, &r.Value, &r.ReceivedAt, &r.TmuxInstance); err != nil {
			return nil, fmt.Errorf("status snapshots load: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
