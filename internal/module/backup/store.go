package backup

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ManifestEntry is one entry in a snapshot manifest. Directory entries
// (Kind=="dir") carry Hash=="", Size==0, Words==0 so empty dirs round-trip.
type ManifestEntry struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"` // "file" | "dir"
	Hash  string `json:"hash"`
	Size  int64  `json:"size"`
	Words int64  `json:"words"`
}

// BackupStore is the SQLite-backed, content-addressed, append-only snapshot
// store for the In-App /buffer tree (subsystem 2).
type BackupStore struct {
	db *sql.DB
	// now returns the current Unix time in seconds. Default time.Now().Unix();
	// overridable in tests to drive created_at, the 90-day age window, and the
	// blob grace period deterministically.
	now func() int64
	// afterReadHead is an unexported test seam: nil in production (zero cost).
	// AppendSnapshot invokes it (when non-nil) inside the BEGIN IMMEDIATE
	// transaction, right after reading the store head but before inserting, so
	// the serialisation test can force the race window deterministically.
	afterReadHead func()
}

// OpenBackup opens (or creates) a BackupStore at path and runs schema
// migration. Use ":memory:" for tests; concurrency/lock tests need a
// file-backed path (a :memory: DB pins MaxOpenConns(1) and never exercises
// real writer contention).
func OpenBackup(path string) (*BackupStore, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout(500): make concurrent writers WAIT on the BEGIN
		// IMMEDIATE write lock instead of returning SQLITE_BUSY immediately.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open backup db: %w", err)
	}
	// :memory: is a single shared in-memory DB only if the pool is pinned to
	// one connection; file-backed DBs use a small cap to bound fd usage while
	// still allowing a second writer to contend on the write lock.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
	}
	s := &BackupStore{db: db, now: func() int64 { return time.Now().Unix() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate backup db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *BackupStore) Close() error { return s.db.Close() }

// migrate creates the backup tables and index if they don't already exist.
// No foreign keys (parent/manifest references are validated in-handler) — this
// avoids the #850 DSN-pragma footgun entirely.
func (s *BackupStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS backup_blobs (
			hash       TEXT    PRIMARY KEY,
			content    BLOB    NOT NULL,
			size       INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS backup_snapshots (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			store_id   TEXT    NOT NULL,
			device     TEXT    NOT NULL DEFAULT '',
			parent_id  INTEGER,
			is_fork    INTEGER NOT NULL DEFAULT 0,
			trigger    TEXT    NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			manifest   TEXT    NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_snap_store ON backup_snapshots(store_id, id);
	`)
	return err
}

// GC runs garbage collection across all store_ids using the given clock.
// T2a-0 stub: replaced with the real keep-set / grace-period implementation in
// T2a-4. Present now so Start()'s wiring lands once and stays green.
func (s *BackupStore) GC(now int64) error {
	return nil
}
