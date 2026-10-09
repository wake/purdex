package workbook

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrations are the schema steps, in order: step i takes a file at version i to version i+1. A step runs in ONE
// transaction together with its version write, so a failure half way leaves the file at the old version with the old
// shape and the next start retries from there. Never edit a step that has shipped (the file exists on a host as soon as
// the module has run): add the next one.
var migrations = []func(tx *sql.Tx) error{migrateV1, migrateV2}

func init() {
	if len(migrations) != schemaVersion {
		panic("workbook: schemaVersion and the migration steps disagree")
	}
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("migrate workbook db: %w", err)
	}
	var have int
	err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&have)
	if errors.Is(err, sql.ErrNoRows) {
		have = 0
	} else if err != nil {
		return fmt.Errorf("migrate workbook db: %w", err)
	}
	if have > schemaVersion {
		return fmt.Errorf("migrate workbook db: schema version %d is newer than this daemon's %d", have, schemaVersion)
	}
	for ; have < schemaVersion; have++ {
		if err := runStep(db, migrations[have]); err != nil {
			return fmt.Errorf("migrate workbook db (to version %d): %w", have+1, err)
		}
	}
	return nil
}

func runStep(db *sql.DB, step func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := step(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV1 creates the first schema (spec §6, WB-1a-ii).
func migrateV1(tx *sql.Tx) error {
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
func migrateV2(tx *sql.Tx) error {
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
