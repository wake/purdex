package push

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/push"

	_ "modernc.org/sqlite"
)

// Store is the SQLite persistence of the registered devices (push spec §4.4), a small file of its own like
// internal/module/hostconfig's. The module reads every row into memory at Start; the send path never touches SQLite.
type Store struct {
	db  *sql.DB
	now func() int64 // Unix ms; injectable for tests
}

const columns = `token, device_id, bundle_id, env, platform, device_name, host_label, locale, prefs,
	created_at, updated_at, last_sent_at, last_error`

// OpenStore opens (or creates) the store at path. Use ":memory:" for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open push db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS push_devices (
			token TEXT PRIMARY KEY, device_id TEXT NOT NULL UNIQUE,
			bundle_id TEXT NOT NULL, env TEXT NOT NULL, platform TEXT NOT NULL,
			device_name TEXT NOT NULL, host_label TEXT NOT NULL, locale TEXT NOT NULL, prefs TEXT NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
			last_sent_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT ''
		);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate push db: %w", err)
	}
	return &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Upsert stores d (keyed by its token) and returns the stored row. The same token again replaces every registration
// field and bumps updated_at; created_at and the send history (last_sent_at, last_error) stay.
func (s *Store) Upsert(d push.Device) (push.Device, error) {
	prefs, err := json.Marshal(d.Prefs)
	if err != nil {
		return push.Device{}, fmt.Errorf("encode prefs: %w", err)
	}
	now := s.now()
	if _, err := s.db.Exec(`
		INSERT INTO push_devices (token, device_id, bundle_id, env, platform, device_name, host_label, locale, prefs,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(token) DO UPDATE SET
			bundle_id = excluded.bundle_id, env = excluded.env, platform = excluded.platform,
			device_name = excluded.device_name, host_label = excluded.host_label, locale = excluded.locale,
			prefs = excluded.prefs, updated_at = excluded.updated_at`,
		d.Token, d.DeviceID, d.BundleID, d.Env, d.Platform, d.DeviceName, d.HostLabel, d.Locale, string(prefs), now, now,
	); err != nil {
		return push.Device{}, errors.New("store device: write failed")
	}
	row := s.db.QueryRow(`SELECT `+columns+` FROM push_devices WHERE token = ?`, d.Token)
	return scan(row)
}

// DeleteByID removes the device with that id; whether it existed is returned, and a missing one is not an error.
func (s *Store) DeleteByID(deviceID string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM push_devices WHERE device_id = ?`, deviceID)
	if err != nil {
		return false, errors.New("delete device: write failed")
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// List returns every device, oldest registration first.
func (s *Store) List() ([]push.Device, error) {
	rows, err := s.db.Query(`SELECT ` + columns + ` FROM push_devices ORDER BY created_at ASC, device_id ASC`)
	if err != nil {
		return nil, errors.New("list devices: read failed")
	}
	defer rows.Close()
	var out []push.Device
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("list devices: read failed")
	}
	return out, nil
}

// MarkSent records a successful send and clears the last error.
func (s *Store) MarkSent(deviceID string, at int64) error {
	if _, err := s.db.Exec(`UPDATE push_devices SET last_sent_at = ?, last_error = '' WHERE device_id = ?`, at, deviceID); err != nil {
		return errors.New("mark sent: write failed")
	}
	return nil
}

// MarkError records why the last send failed.
func (s *Store) MarkError(deviceID, reason string) error {
	if _, err := s.db.Exec(`UPDATE push_devices SET last_error = ? WHERE device_id = ?`, reason, deviceID); err != nil {
		return errors.New("mark error: write failed")
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (push.Device, error) {
	var d push.Device
	var prefs string
	if err := r.Scan(&d.Token, &d.DeviceID, &d.BundleID, &d.Env, &d.Platform, &d.DeviceName, &d.HostLabel, &d.Locale, &prefs,
		&d.CreatedAt, &d.UpdatedAt, &d.LastSentAt, &d.LastError); err != nil {
		return push.Device{}, errors.New("read device: scan failed")
	}
	if err := json.Unmarshal([]byte(prefs), &d.Prefs); err != nil {
		return push.Device{}, errors.New("read device: stored prefs are not valid")
	}
	return d, nil
}
