// Package store is wwd.db's host-state side: the worker settings and the active
// exceptions (spec §9), and later the per-minute rollups. The queue keeps its own
// tables in the same file with its own version table (queue.Migrate); neither
// migrates the other's.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/wire"
)

// schemaVersion is the version of the host tables. Once wwd is deployed the
// tables exist: a change from here on is a case in migrate, never an edit of
// createSchema.
const schemaVersion = 1

// Store is the open wwd.db.
type Store struct{ db *sql.DB }

// Open opens (creating if need be) wwd.db at path and migrates the host tables.
// WAL and a long busy timeout let the queue and the host state share the file.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// DB is the underlying handle, for queue.Migrate and queue.New.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS host_schema (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var have int
	switch err := tx.QueryRowContext(ctx, `SELECT version FROM host_schema`).Scan(&have); {
	case errors.Is(err, sql.ErrNoRows):
		for _, stmt := range []string{
			`CREATE TABLE IF NOT EXISTS worker_settings (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				enabled INTEGER NOT NULL, heavy INTEGER NOT NULL, light INTEGER NOT NULL,
				owner_rule TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS exceptions (
				name TEXT PRIMARY KEY,
				until_ms INTEGER, pid INTEGER,
				holds TEXT NOT NULL, since_ms INTEGER NOT NULL)`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO host_schema (version) VALUES (?)`, schemaVersion); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if have > schemaVersion {
			return fmt.Errorf("store: wwd.db has host schema %d, this wwd knows %d: a newer wwd wrote it", have, schemaVersion)
		}
		// have < schemaVersion: the cases of the migrations go here, one per version step
	}
	return tx.Commit()
}

// LoadWorker returns the saved worker settings; ok is false when none was saved
// (the config's starting values apply).
func (s *Store) LoadWorker() (ws wire.WorkerSettings, ok bool, err error) {
	var en int
	err = s.db.QueryRow(`SELECT enabled, heavy, light, owner_rule FROM worker_settings WHERE id = 1`).
		Scan(&en, &ws.Slots.Heavy, &ws.Slots.Light, &ws.OwnerRule)
	if errors.Is(err, sql.ErrNoRows) {
		return wire.WorkerSettings{}, false, nil
	}
	if err != nil {
		return wire.WorkerSettings{}, false, err
	}
	ws.Enabled = en != 0
	return ws, true, nil
}

// SaveWorker replaces the saved worker settings.
func (s *Store) SaveWorker(ws wire.WorkerSettings) error {
	en := 0
	if ws.Enabled {
		en = 1
	}
	_, err := s.db.Exec(`INSERT INTO worker_settings (id, enabled, heavy, light, owner_rule) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, heavy = excluded.heavy, light = excluded.light, owner_rule = excluded.owner_rule`,
		en, ws.Slots.Heavy, ws.Slots.Light, ws.OwnerRule)
	return err
}

// Stored is an exception with the time it began.
type Stored struct {
	Exception wire.Exception
	Since     time.Time
}

// SaveException inserts the exception or replaces the one of the same name.
func (s *Store) SaveException(e Stored) error {
	holds, err := json.Marshal(append([]wire.Hold{}, e.Exception.Holds...))
	if err != nil {
		return err
	}
	var until, pid any
	if e.Exception.Until != nil {
		until = e.Exception.Until.UnixMilli()
	}
	if e.Exception.PID != nil {
		pid = *e.Exception.PID
	}
	_, err = s.db.Exec(`INSERT INTO exceptions (name, until_ms, pid, holds, since_ms) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET until_ms = excluded.until_ms, pid = excluded.pid, holds = excluded.holds, since_ms = excluded.since_ms`,
		e.Exception.Name, until, pid, string(holds), e.Since.UnixMilli())
	return err
}

// DeleteException removes an exception; one that is not there is not an error.
func (s *Store) DeleteException(name string) error {
	_, err := s.db.Exec(`DELETE FROM exceptions WHERE name = ?`, name)
	return err
}

// LoadExceptions returns every saved exception, oldest first.
func (s *Store) LoadExceptions() ([]Stored, error) {
	rows, err := s.db.Query(`SELECT name, until_ms, pid, holds, since_ms FROM exceptions ORDER BY since_ms, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var (
			name, holds string
			until, pid  sql.NullInt64
			since       int64
		)
		if err := rows.Scan(&name, &until, &pid, &holds, &since); err != nil {
			return nil, err
		}
		e := Stored{Exception: wire.Exception{Name: name}, Since: time.UnixMilli(since).UTC()}
		if err := json.Unmarshal([]byte(holds), &e.Exception.Holds); err != nil {
			return nil, fmt.Errorf("store: exception %q holds: %w", name, err)
		}
		if e.Exception.Holds == nil {
			e.Exception.Holds = []wire.Hold{}
		}
		if until.Valid {
			t := time.UnixMilli(until.Int64).UTC()
			e.Exception.Until = &t
		}
		if pid.Valid {
			p := int(pid.Int64)
			e.Exception.PID = &p
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
