// Package devicestate stores per-device workspace/tab structure snapshots so a
// client can back up and later restore its layout from the daemon.
package devicestate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Record is one device's latest uploaded state.
type Record struct {
	ClientID       string          `json:"clientId"`
	DeviceName     string          `json:"deviceName"`
	AppVersion     string          `json:"appVersion"`
	CapturedAt     int64           `json:"capturedAt"`
	UpdatedAt      int64           `json:"updatedAt"`
	WorkspaceCount int             `json:"workspaceCount"`
	TabCount       int             `json:"tabCount"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

// Store is the SQLite-backed persistence layer for device state.
type Store struct {
	db  *sql.DB
	now func() int64 // daemon clock in ms; injectable for tests
}

// OpenStore opens (or creates) a Store at path. Use ":memory:" for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open device state db: %w", err)
	}
	if path == ":memory:" {
		// Each pooled connection would otherwise see its own empty DB.
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate device state db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS device_state (
			client_id       TEXT PRIMARY KEY,
			device_name     TEXT    NOT NULL,
			app_version     TEXT    NOT NULL DEFAULT '',
			captured_at     INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
			workspace_count INTEGER NOT NULL,
			tab_count       INTEGER NOT NULL,
			payload         TEXT    NOT NULL
		);
	`)
	return err
}

// Upsert inserts or replaces the record for r.ClientID. It returns
// stored=false (and leaves the row untouched) when the existing row has a
// newer captured_at than r.CapturedAt; an equal captured_at overwrites.
// UpdatedAt is always set from the store clock; r.UpdatedAt is ignored.
func (s *Store) Upsert(r Record) (bool, error) {
	res, err := s.db.Exec(`
		INSERT INTO device_state
			(client_id, device_name, app_version, captured_at, updated_at, workspace_count, tab_count, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET
			device_name     = excluded.device_name,
			app_version     = excluded.app_version,
			captured_at     = excluded.captured_at,
			updated_at      = excluded.updated_at,
			workspace_count = excluded.workspace_count,
			tab_count       = excluded.tab_count,
			payload         = excluded.payload
		WHERE excluded.captured_at >= device_state.captured_at`,
		r.ClientID, r.DeviceName, r.AppVersion, r.CapturedAt, s.now(),
		r.WorkspaceCount, r.TabCount, string(r.Payload),
	)
	if err != nil {
		return false, fmt.Errorf("upsert device state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("upsert device state rows affected: %w", err)
	}
	return n > 0, nil
}

// List returns all records without payload, most recently updated first.
// It never returns a nil slice.
func (s *Store) List() ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT client_id, device_name, app_version, captured_at, updated_at, workspace_count, tab_count
		FROM device_state
		ORDER BY updated_at DESC, client_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list device state: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ClientID, &r.DeviceName, &r.AppVersion, &r.CapturedAt,
			&r.UpdatedAt, &r.WorkspaceCount, &r.TabCount); err != nil {
			return nil, fmt.Errorf("scan device state: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate device state: %w", err)
	}
	return out, nil
}

// Get returns the full record (with payload) for clientID.
func (s *Store) Get(clientID string) (Record, bool, error) {
	var r Record
	var payload string
	err := s.db.QueryRow(`
		SELECT client_id, device_name, app_version, captured_at, updated_at, workspace_count, tab_count, payload
		FROM device_state WHERE client_id = ?`, clientID,
	).Scan(&r.ClientID, &r.DeviceName, &r.AppVersion, &r.CapturedAt,
		&r.UpdatedAt, &r.WorkspaceCount, &r.TabCount, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("get device state: %w", err)
	}
	r.Payload = json.RawMessage(payload)
	return r, true, nil
}

// Delete removes the record for clientID. Deleting a missing row is not an error.
func (s *Store) Delete(clientID string) error {
	if _, err := s.db.Exec(`DELETE FROM device_state WHERE client_id = ?`, clientID); err != nil {
		return fmt.Errorf("delete device state: %w", err)
	}
	return nil
}
