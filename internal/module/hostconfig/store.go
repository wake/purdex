// Package hostconfig stores per-host launcher configuration — projects,
// commands and resume templates — so every client sees the same set.
package hostconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const (
	KeyProjects        = "projects"
	KeyCommands        = "commands"
	KeyResumeTemplates = "resume_templates"
)

// Entry is one stored collection. A missing row reads as Revision 0, Value nil.
type Entry struct {
	Value     json.RawMessage
	Revision  int64
	UpdatedAt int64
}

// Store is the SQLite-backed persistence layer for host config.
type Store struct {
	db        *sql.DB
	now       func() int64 // ms; injectable for tests
	afterRead func()       // test seam; nil in production
}

// OpenStore opens (or creates) a Store at path. Use ":memory:" for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout: a concurrent writer waits on BEGIN IMMEDIATE instead of failing.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open host config db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS host_config (
			key        TEXT PRIMARY KEY,
			value      TEXT    NOT NULL,
			revision   INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate host config db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *Store) Close() error { return s.db.Close() }

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getEntry(ctx context.Context, q querier, key string) (Entry, error) {
	var e Entry
	var value string
	err := q.QueryRowContext(ctx, `SELECT value, revision, updated_at FROM host_config WHERE key = ?`, key).
		Scan(&value, &e.Revision, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, nil
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get host config %s: %w", key, err)
	}
	e.Value = json.RawMessage(value)
	return e, nil
}

// Get returns the stored entry for key (Revision 0 when never written).
func (s *Store) Get(key string) (Entry, error) { return getEntry(context.Background(), s.db, key) }

// ValidationError wraps an error returned by Put's build callback, so callers
// can tell a rejected payload (400) from a storage failure (500).
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// Put stores the value produced by build when baseRevision equals the stored
// revision. On a mismatch it returns the current entry with ok=false and build
// is NOT called — a stale client always receives the server copy, whatever its
// payload. A build error is returned as *ValidationError and nothing is written.
//
// The read and the write run inside one BEGIN IMMEDIATE transaction on a dedicated conn:
// database/sql's Begin is a deferred tx, so two concurrent PUTs could both read
// the same revision and the loser would hit SQLITE_BUSY_SNAPSHOT (500) instead
// of a clean 409. IMMEDIATE takes the write lock first; the second writer waits
// (busy_timeout) and then reads the committed revision. Same pattern as
// internal/module/backup/store.go AppendSnapshot.
func (s *Store) Put(key string, baseRevision int64, build func() (json.RawMessage, error)) (Entry, bool, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return Entry{}, false, fmt.Errorf("host config conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Entry{}, false, fmt.Errorf("begin host config tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	cur, err := getEntry(ctx, conn, key)
	if err != nil {
		return Entry{}, false, err
	}
	if s.afterRead != nil {
		s.afterRead() // test seam: widen the read→write window deterministically
	}
	if cur.Revision != baseRevision {
		return cur, false, nil
	}
	value, err := build()
	if err != nil {
		return Entry{}, false, &ValidationError{Err: err}
	}
	next := Entry{Value: value, Revision: cur.Revision + 1, UpdatedAt: s.now()}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO host_config (key, value, revision, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, revision = excluded.revision, updated_at = excluded.updated_at`,
		key, string(value), next.Revision, next.UpdatedAt); err != nil {
		return Entry{}, false, fmt.Errorf("put host config %s: %w", key, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Entry{}, false, fmt.Errorf("commit host config %s: %w", key, err)
	}
	committed = true
	return next, true, nil
}
