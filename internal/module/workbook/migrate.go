package workbook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// migrations are the schema steps, in order: step i takes a file at version i to version i+1. A step runs in ONE
// transaction together with its version write, so a failure half way leaves the file at the old version with the old
// shape and the next start retries from there. The transaction is BEGIN IMMEDIATE and the version is read again inside
// it: a second opener (a daemon started twice, a rolling restart) waits for the first, then finds the step done and
// skips it. Never edit a step that has shipped (the file exists on a host as soon as the module has run): add the next one.
var migrations = []func(x stepExec) error{migrateV1, migrateV2}

// stepExec is what a step runs its statements on: the open migration transaction.
type stepExec interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func init() {
	if len(migrations) != schemaVersion {
		panic("workbook: schemaVersion and the migration steps disagree")
	}
}

func migrate(db *sql.DB) error {
	err := retryBusy(func() error {
		_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`)
		return err
	})
	if err != nil {
		return fmt.Errorf("migrate workbook db: %w", err)
	}
	for i := range migrations {
		if err := retryBusy(func() error { return runStep(db, i) }); err != nil {
			return fmt.Errorf("migrate workbook db (to version %d): %w", i+1, err)
		}
	}
	return nil
}

// retryBusy repeats f for up to 5 s while SQLite answers "database is locked". The busy timeout covers a statement
// waiting for a lock, but not a new connection switching a file to WAL while another opener does the same; whatever f
// did is rolled back by its own failure, so running it again is safe.
func retryBusy(f func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := f()
		if err == nil || time.Now().After(deadline) || !(strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked")) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// connTx is a BEGIN IMMEDIATE transaction on one connection, as a stepExec.
type connTx struct {
	ctx  context.Context
	conn *sql.Conn
}

func (c connTx) Exec(query string, args ...any) (sql.Result, error) {
	return c.conn.ExecContext(c.ctx, query, args...)
}

// runStep applies step i if the file is at version i: it takes the write lock first, reads the version under it, and
// skips the step when another opener has done it meanwhile. A file newer than this daemon is refused.
func runStep(db *sql.DB, i int) (err error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	have := 0
	switch e := conn.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&have); {
	case errors.Is(e, sql.ErrNoRows):
		have = 0
	case e != nil:
		return e
	}
	if have > schemaVersion {
		return fmt.Errorf("schema version %d is newer than this daemon's %d", have, schemaVersion)
	}
	if have < i {
		return fmt.Errorf("schema version %d is behind step %d", have, i+1)
	}
	if have == i {
		if err := migrations[i](connTx{ctx, conn}); err != nil {
			return err
		}
	} // have > i: another opener did this step while we waited for the lock
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	done = true
	return nil
}

// migrateV1 creates the first schema (spec §6, WB-1a-ii).
func migrateV1(tx stepExec) error {
	_, err := tx.Exec(`
		CREATE TABLE wb_entries (
			id INTEGER PRIMARY KEY,
			conv_key TEXT NOT NULL,
			host_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			session_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			turn_at INTEGER NOT NULL,
			turn_seq INTEGER NOT NULL,
			state TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			thing TEXT, push TEXT, entry TEXT,
			thing_done INTEGER NOT NULL DEFAULT 0,
			push_ready_at INTEGER NOT NULL DEFAULT 0,
			team_id TEXT, role TEXT, ref TEXT,
			prompt_ver INTEGER NOT NULL,
			latency_ms INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (session_id, turn_id));
		CREATE INDEX wb_entries_conv ON wb_entries (conv_key, id);
		CREATE INDEX wb_entries_turn_at ON wb_entries (turn_at);
		CREATE TABLE wb_status (
			conv_key TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			entry_id INTEGER NOT NULL,
			session_id TEXT NOT NULL,
			updated_at INTEGER NOT NULL);
		INSERT INTO schema_version (version) VALUES (1);`)
	return err
}

// migrateV2 is the v2 schema (spec §6 v2, plan WB-1b′-a): the entry kind and the call's token usage, and the todo list.
// The new reasons (failed: api | lost | refused | nothing_to_fork, skipped: no_mod) are values only.
func migrateV2(tx stepExec) error {
	_, err := tx.Exec(`
		ALTER TABLE wb_entries ADD COLUMN kind TEXT NOT NULL DEFAULT 'turn';
		ALTER TABLE wb_entries ADD COLUMN usage_in INTEGER;
		ALTER TABLE wb_entries ADD COLUMN usage_out INTEGER;
		ALTER TABLE wb_entries ADD COLUMN usage_cache_read INTEGER;
		CREATE TABLE wb_todos (
			id INTEGER PRIMARY KEY,
			conv_key TEXT NOT NULL,
			title TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			added_entry_id INTEGER NOT NULL,
			closed_entry_id INTEGER,
			closed_by TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			closed_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL);
		CREATE INDEX wb_todos_conv ON wb_todos (conv_key, state, id);
		UPDATE schema_version SET version = 2;`)
	return err
}
