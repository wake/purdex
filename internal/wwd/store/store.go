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
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/wire"
)

// schemaVersion is the version of the host tables. Once wwd is deployed the
// tables exist: a change from here on is a case in migrate, never an edit of
// createSchema.
const schemaVersion = 9

// workerDiskCol (v9, ed3920-61): the free-space floor of the work volume, in GB (0: none). A file made before v9 gets it with an ALTER and
// its one row gets the default 10 (the pdx gate's disk_min_gb, which the host has been under until now); a new file has the column in
// the table's own DDL. An older wwd refuses a v9 file (a newer schema), so the way back is the backup taken before the upgrade.
const workerDiskCol = `ALTER TABLE worker_settings ADD COLUMN disk_min_gb REAL NOT NULL DEFAULT 10`

// exceptionHoldCols (v7, WWD-8 8b-3): the two hold fields of an exception (spec §13.4), NULL for an exception made without them. Both
// are JSON: allow as {"classes":[..],"repos":[..]}, flags as a list. A file made before v7 gets them with an ALTER; a new file gets them
// the same way right after the table, so there is one definition. An older wwd refuses a v7 file (a newer schema), so the way back is the
// backup taken before the upgrade.
var exceptionHoldCols = []struct{ name, ddl string }{
	{"allow_json", `ALTER TABLE exceptions ADD COLUMN allow_json TEXT`},
	{"flags", `ALTER TABLE exceptions ADD COLUMN flags TEXT`},
}

// addColumn runs an ALTER TABLE ADD COLUMN unless the table has the column already (a step run again, or a file that was labelled with an
// older version by hand).
func addColumn(ctx context.Context, tx *sql.Tx, table, column, ddl string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, ddl)
	return err
}

// hostMinutesDDL (v2): one row per finished minute, the whole rollup record as
// JSON (minutes.go).
const hostMinutesDDL = `CREATE TABLE IF NOT EXISTS host_minutes (at INTEGER PRIMARY KEY, rec TEXT NOT NULL)`

// observationsDDL (v3): one row per finished undeclared heavy tree, the record as JSON (observations.go).
const observationsDDL = `CREATE TABLE IF NOT EXISTS observations (id TEXT PRIMARY KEY, started_ms INTEGER NOT NULL, rec TEXT NOT NULL)`
const observationsIndexDDL = `CREATE INDEX IF NOT EXISTS observations_started ON observations (started_ms)`

// usageDDL (v4): one row per finished job, what it used and the host around it, as JSON (usage.go).
const usageDDL = `CREATE TABLE IF NOT EXISTS job_usage (job_id TEXT PRIMARY KEY, ended_ms INTEGER NOT NULL, rec TEXT NOT NULL)`
const usageIndexDDL = `CREATE INDEX IF NOT EXISTS job_usage_ended ON job_usage (ended_ms)`

// startsDDL (v5): one row per wwd run (spec §13.1): when it started, which version, and how the run before it ended if that is known
// (” when not). The rule "starts in the last hour" reads it. This PR makes the table; the start record is written by 8a-0.
const startsDDL = `CREATE TABLE IF NOT EXISTS wwd_starts (id INTEGER PRIMARY KEY AUTOINCREMENT, started_ms INTEGER NOT NULL UNIQUE, version TEXT NOT NULL, prev_exit TEXT NOT NULL DEFAULT '')`

// alertsDDL (v5): a host's alerts (spec §13.3). The key is one alert across time; cleared_ms is NULL while it is active. This PR
// makes the table and reads it (ActiveAlerts); the rules that write it come in 8a-1 and 8a-2.
const alertsDDL = `CREATE TABLE IF NOT EXISTS alerts (key TEXT PRIMARY KEY, kind TEXT NOT NULL, level TEXT NOT NULL, since_ms INTEGER NOT NULL, last_ms INTEGER NOT NULL, cleared_ms INTEGER, detail TEXT NOT NULL DEFAULT '')`

// Store is the open wwd.db.
type Store struct {
	db *sql.DB
	// alertMu keeps the alert writers of this process in a line, so that one waits for the other here and not on the database. It is not
	// what keeps RaiseAlert from SQLITE_BUSY against the other tables' writers: that is the immediate transaction (Open).
	alertMu sync.Mutex
}

// Open opens (creating if need be) wwd.db at path and migrates the host tables.
// WAL and a long busy timeout let the queue and the host state share the file. A transaction takes the write lock when it begins
// (_txlock=immediate, #3839): one that read first and wrote after (RaiseAlert) could not become the writer once another connection had
// written in between, and SQLite answers that with SQLITE_BUSY at once, not after the busy timeout. Every transaction of this store writes,
// so none holds the lock longer than before; the queue takes its own with an explicit BEGIN IMMEDIATE.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
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
				owner_rule TEXT NOT NULL, disk_min_gb REAL NOT NULL DEFAULT 10)`,
			`CREATE TABLE IF NOT EXISTS exceptions (
				name TEXT PRIMARY KEY,
				until_ms INTEGER, pid INTEGER, pid_ident TEXT NOT NULL DEFAULT '',
				holds TEXT NOT NULL, since_ms INTEGER NOT NULL)`,
			exceptionHoldCols[0].ddl,
			exceptionHoldCols[1].ddl,
			hostMinutesDDL,
			observationsDDL,
			observationsIndexDDL,
			usageDDL,
			usageIndexDDL,
			startsDDL,
			alertsDDL,
			earnedLabelsDDL,
			resultCacheDDL,
			resultCacheIndexDDL,
			resultCacheLabelIndexDDL,
			qualifyAttemptsDDL,
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
		// have < schemaVersion: one case per version step
		if have < 2 { // v2: the per-minute rollups
			if _, err := tx.ExecContext(ctx, hostMinutesDDL); err != nil {
				return err
			}
		}
		if have < 3 { // v3: the observations of undeclared heavy trees
			for _, stmt := range []string{observationsDDL, observationsIndexDDL} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
		}
		if have < 4 { // v4: the usage of finished jobs
			for _, stmt := range []string{usageDDL, usageIndexDDL} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
		}
		if have < 5 { // v5: the start records and the alerts of spec §13 (every statement is IF NOT EXISTS: a re-run changes nothing)
			for _, stmt := range []string{startsDDL, alertsDDL} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
		}
		if have < 6 { // v6: earned labels and the result cache (WWD-5; every statement is IF NOT EXISTS)
			for _, stmt := range []string{earnedLabelsDDL, resultCacheDDL, resultCacheIndexDDL, resultCacheLabelIndexDDL} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
		}
		if have < 7 { // v7: the hold fields of an exception (WWD-8 8b-3). ALTER has no IF NOT EXISTS, so a column that is there is left alone
			for _, c := range exceptionHoldCols {
				if err := addColumn(ctx, tx, "exceptions", c.name, c.ddl); err != nil {
					return err
				}
			}
		}
		if have < 8 { // v8: the last unsuccessful qualification pair of a task (WWD-5b-2c-2; IF NOT EXISTS: a re-run changes nothing)
			if _, err := tx.ExecContext(ctx, qualifyAttemptsDDL); err != nil {
				return err
			}
		}
		if have < 9 { // v9: the disk floor of the worker settings (ed3920-61); a column that is there is left alone
			if err := addColumn(ctx, tx, "worker_settings", "disk_min_gb", workerDiskCol); err != nil {
				return err
			}
		}
		if have < schemaVersion {
			if _, err := tx.ExecContext(ctx, `UPDATE host_schema SET version = ?`, schemaVersion); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// LoadWorker returns the saved worker settings; ok is false when none was saved
// (the config's starting values apply).
func (s *Store) LoadWorker() (ws wire.WorkerSettings, ok bool, err error) {
	var en int
	var disk float64
	err = s.db.QueryRow(`SELECT enabled, heavy, light, owner_rule, disk_min_gb FROM worker_settings WHERE id = 1`).
		Scan(&en, &ws.Slots.Heavy, &ws.Slots.Light, &ws.OwnerRule, &disk)
	if errors.Is(err, sql.ErrNoRows) {
		return wire.WorkerSettings{}, false, nil
	}
	if err != nil {
		return wire.WorkerSettings{}, false, err
	}
	ws.Enabled = en != 0
	ws.DiskMinGB = &disk
	return ws, true, nil
}

// SaveWorker replaces the saved worker settings.
func (s *Store) SaveWorker(ws wire.WorkerSettings) error {
	en := 0
	if ws.Enabled {
		en = 1
	}
	disk := wire.DefaultDiskMinGB // settings that carry none are stored with the default
	if ws.DiskMinGB != nil {
		disk = *ws.DiskMinGB
	}
	_, err := s.db.Exec(`INSERT INTO worker_settings (id, enabled, heavy, light, owner_rule, disk_min_gb) VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, heavy = excluded.heavy, light = excluded.light, owner_rule = excluded.owner_rule,
			disk_min_gb = excluded.disk_min_gb`,
		en, ws.Slots.Heavy, ws.Slots.Light, ws.OwnerRule, disk)
	return err
}

// Stored is an exception with the time it began.
type Stored struct {
	Exception wire.Exception
	Since     time.Time
	// PIDIdent identifies the process of a pid-bound exception (its start time), so
	// a later process that reuses the pid is not taken for it. "" when unknown.
	PIDIdent string
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
	// the hold fields: NULL when absent. An empty allow is not an absent one (it admits nothing), so it is stored as {"classes":[],"repos":[]}.
	var allow, flags any
	if a := e.Exception.Allow; a != nil {
		b, err := json.Marshal(wire.HoldAllow{Classes: append([]wire.JobClass{}, a.Classes...), Repos: append([]string{}, a.Repos...)})
		if err != nil {
			return err
		}
		allow = string(b)
	}
	if len(e.Exception.Flags) > 0 {
		b, err := json.Marshal(e.Exception.Flags)
		if err != nil {
			return err
		}
		flags = string(b)
	}
	_, err = s.db.Exec(`INSERT INTO exceptions (name, until_ms, pid, pid_ident, holds, since_ms, allow_json, flags) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET until_ms = excluded.until_ms, pid = excluded.pid, pid_ident = excluded.pid_ident,
			holds = excluded.holds, since_ms = excluded.since_ms, allow_json = excluded.allow_json, flags = excluded.flags`,
		e.Exception.Name, until, pid, e.PIDIdent, string(holds), e.Since.UnixMilli(), allow, flags)
	return err
}

// DeleteException removes an exception; one that is not there is not an error.
func (s *Store) DeleteException(name string) error {
	_, err := s.db.Exec(`DELETE FROM exceptions WHERE name = ?`, name)
	return err
}

// LoadExceptions returns every saved exception, oldest first.
func (s *Store) LoadExceptions() ([]Stored, error) {
	rows, err := s.db.Query(`SELECT name, until_ms, pid, pid_ident, holds, since_ms, allow_json, flags FROM exceptions ORDER BY since_ms, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var (
			name, holds, ident string
			until, pid         sql.NullInt64
			since              int64
			allow, flags       sql.NullString
		)
		if err := rows.Scan(&name, &until, &pid, &ident, &holds, &since, &allow, &flags); err != nil {
			return nil, err
		}
		e := Stored{Exception: wire.Exception{Name: name}, Since: time.UnixMilli(since).UTC(), PIDIdent: ident}
		if err := json.Unmarshal([]byte(holds), &e.Exception.Holds); err != nil {
			return nil, fmt.Errorf("store: exception %q holds: %w", name, err)
		}
		if e.Exception.Holds == nil {
			e.Exception.Holds = []wire.Hold{}
		}
		if allow.Valid {
			var a wire.HoldAllow
			if err := json.Unmarshal([]byte(allow.String), &a); err != nil {
				return nil, fmt.Errorf("store: exception %q allow: %w", name, err)
			}
			if a.Classes == nil {
				a.Classes = []wire.JobClass{}
			}
			if a.Repos == nil {
				a.Repos = []string{}
			}
			e.Exception.Allow = &a
		}
		if flags.Valid {
			if err := json.Unmarshal([]byte(flags.String), &e.Exception.Flags); err != nil {
				return nil, fmt.Errorf("store: exception %q flags: %w", name, err)
			}
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
