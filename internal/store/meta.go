// internal/store/meta.go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// SessionMeta is the DB representation of session meta cache.
// It stores ONLY metadata that can't be retrieved from tmux in real-time.
type SessionMeta struct {
	TmuxID string
	Mode   string
	Cwd    string
}

// MetaUpdate supports partial updates (nil = no change).
type MetaUpdate struct {
	Mode *string
	Cwd  *string
}

// MetaStore is a lightweight DB for session metadata cache.
type MetaStore struct{ db *sql.DB }

// OpenMeta opens (or creates) a MetaStore DB at path, runs migration, and
// enables WAL mode. Use ":memory:" for tests.
func OpenMeta(path string) (*MetaStore, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout(500): make transient write contention WAIT instead of
		// returning SQLITE_BUSY immediately — protects tail latency under
		// concurrent hooks/sweep/checkpoint without changing durability.
		// 500ms catches typical SSD checkpoint contention (<300ms observed)
		// without blocking the hot path past user-perceived UI latency.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open meta db: %w", err)
	}
	// :memory: is a test-only DSN (production always uses a file path from
	// cfg.DataDir). Go's database/sql pool can open multiple connections to
	// the same DSN, and each :memory: connection is an independent database
	// — so a second pool connection would see an empty schema. Pin the pool
	// to a single connection so all goroutines share the same in-memory DB.
	// File-backed DBs use a small cap to bound fd usage; meta has lower
	// write volume than agent_event so 2/2 is plenty.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
	}
	if err := migrateMetaDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate meta db: %w", err)
	}
	return &MetaStore{db: db}, nil
}

func migrateMetaDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS session_meta (
			tmux_id       TEXT PRIMARY KEY,
			mode          TEXT DEFAULT 'terminal',
			cwd           TEXT DEFAULT '',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// peer_messages: audit trail of every cross-host message delivery
	// attempt (Peer Bridge P3). ts is unix milliseconds.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_messages (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			msg_id          TEXT NOT NULL,
			native_msg_id   TEXT NOT NULL DEFAULT '',
			direction       TEXT NOT NULL,
			ts              INTEGER NOT NULL,
			from_host_id    TEXT,
			from_session_id TEXT,
			to_host_id      TEXT,
			to_session_id   TEXT,
			declared_mode   TEXT,
			effective_mode  TEXT,
			bytes           INTEGER,
			result          TEXT,
			error           TEXT
		)
	`)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS peer_messages_ts ON peer_messages(ts)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS peer_messages_msg ON peer_messages(msg_id, direction)`); err != nil {
		return err
	}

	// peer_labels: one self-declared display label per conversation
	// (sessionId); label is NULL after a release so the row keeps carrying
	// rev. peer_label_seq is the host-wide strictly increasing revision.
	//
	// label is deliberately NOT UNIQUE (Peer Address v3 D5): two
	// conversations may call themselves the same thing. Uniqueness lived
	// here while a label was the head of an address; now that a label
	// routes nothing, the only thing that must be unique is the canonical
	// id — which is derived, not claimed, so no table can collide over it.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_labels (
			session_id TEXT PRIMARY KEY,
			label      TEXT,
			rev        INTEGER NOT NULL,
			set_at     INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_label_seq (
			id  INTEGER PRIMARY KEY CHECK (id = 1),
			rev INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO peer_label_seq (id, rev) VALUES (1, 0)`); err != nil {
		return err
	}
	if err := dropPeerLabelsLabelUnique(db); err != nil {
		return err
	}

	return nil
}

// dropPeerLabelsLabelUnique removes the UNIQUE on peer_labels.label from a
// database that already exists. CREATE TABLE IF NOT EXISTS above only shapes
// brand new files; every machine that has run an older daemon still carries
// the pre-v3 table, where a second session claiming a held label fails at the
// SQLite layer and the claim route answers 503 instead of D5's 200 + warning.
//
// SQLite cannot DROP the implicit index a UNIQUE creates
// (sqlite_autoindex_peer_labels_1) and has no ALTER TABLE ... DROP
// CONSTRAINT, so the only way out is the standard table rebuild: new table,
// copy, drop, rename — all inside one transaction so a crash mid-way leaves
// either the old table or the new one, never neither.
//
// The trigger is read off sqlite_master rather than pragma index_list because
// the recorded DDL answers the question directly. The word UNIQUE appears in
// the legacy definition and nowhere in the current one (session_id is spelled
// PRIMARY KEY), so its presence is an exact test for "this table is legacy",
// and a DB already on the new schema is left completely alone.
func dropPeerLabelsLabelUnique(db *sql.DB) error {
	var ddl string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'peer_labels'`,
	).Scan(&ddl)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToUpper(ddl), "UNIQUE") {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds

	if _, err := tx.Exec(`
		CREATE TABLE peer_labels_v3 (
			session_id TEXT PRIMARY KEY,
			label      TEXT,
			rev        INTEGER NOT NULL,
			set_at     INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO peer_labels_v3 (session_id, label, rev, set_at)
		SELECT session_id, label, rev, set_at FROM peer_labels
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE peer_labels`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE peer_labels_v3 RENAME TO peer_labels`); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the underlying DB connection.
func (m *MetaStore) Close() error { return m.db.Close() }

// SetMeta upserts a SessionMeta record (INSERT OR REPLACE).
func (m *MetaStore) SetMeta(tmuxID string, meta SessionMeta) error {
	_, err := m.db.Exec(`
		INSERT INTO session_meta (tmux_id, mode, cwd)
		VALUES (?, ?, ?)
		ON CONFLICT(tmux_id) DO UPDATE SET
			mode = excluded.mode,
			cwd  = excluded.cwd
	`, tmuxID, meta.Mode, meta.Cwd)
	return err
}

// GetMeta returns the SessionMeta for tmuxID, or nil if not found (not an error).
func (m *MetaStore) GetMeta(tmuxID string) (*SessionMeta, error) {
	return m.GetMetaContext(context.Background(), tmuxID)
}

// GetMetaContext is GetMeta bounded by ctx: the session-list read (#1293)
// runs it under that read's deadline, and an ended ctx fails it with an error
// wrapping ctx.Err().
func (m *MetaStore) GetMetaContext(ctx context.Context, tmuxID string) (*SessionMeta, error) {
	var meta SessionMeta
	err := m.db.QueryRowContext(ctx, `
		SELECT tmux_id, mode, cwd
		FROM session_meta WHERE tmux_id = ?
	`, tmuxID).Scan(&meta.TmuxID, &meta.Mode, &meta.Cwd)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

// ListMeta returns all SessionMeta records ordered by tmux_id.
func (m *MetaStore) ListMeta() ([]SessionMeta, error) {
	rows, err := m.db.Query(`
		SELECT tmux_id, mode, cwd
		FROM session_meta ORDER BY tmux_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var meta SessionMeta
		if err := rows.Scan(&meta.TmuxID, &meta.Mode, &meta.Cwd); err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, rows.Err()
}

// UpdateMeta performs a partial update; only non-nil fields are written.
func (m *MetaStore) UpdateMeta(tmuxID string, update MetaUpdate) error {
	var setClauses []string
	var args []any

	if update.Mode != nil {
		setClauses = append(setClauses, "mode = ?")
		args = append(args, *update.Mode)
	}
	if update.Cwd != nil {
		setClauses = append(setClauses, "cwd = ?")
		args = append(args, *update.Cwd)
	}

	if len(setClauses) == 0 {
		return nil // nothing to update
	}

	args = append(args, tmuxID)
	query := fmt.Sprintf("UPDATE session_meta SET %s WHERE tmux_id = ?",
		strings.Join(setClauses, ", "))
	_, err := m.db.Exec(query, args...)
	return err
}

// DeleteMeta removes the record for tmuxID (no-op if not found).
func (m *MetaStore) DeleteMeta(tmuxID string) error {
	_, err := m.db.Exec("DELETE FROM session_meta WHERE tmux_id = ?", tmuxID)
	return err
}

// CleanOrphans deletes meta records whose tmux_id is not in liveTmuxIDs.
// Returns the number of rows deleted.
// If liveTmuxIDs is empty, does nothing — an empty set means "tmux unavailable",
// not "nothing is alive".
func (m *MetaStore) CleanOrphans(liveTmuxIDs []string) (int, error) {
	return m.CleanOrphansContext(context.Background(), liveTmuxIDs)
}

// CleanOrphansContext is CleanOrphans bounded by ctx (see GetMetaContext).
func (m *MetaStore) CleanOrphansContext(ctx context.Context, liveTmuxIDs []string) (int, error) {
	if len(liveTmuxIDs) == 0 {
		return 0, nil // tmux unavailable or no sessions — don't delete anything
	}

	placeholders := strings.Repeat("?,", len(liveTmuxIDs))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma

	args := make([]any, len(liveTmuxIDs))
	for i, id := range liveTmuxIDs {
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM session_meta WHERE tmux_id NOT IN (%s)", placeholders)
	res, err := m.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResetStaleModes resets all sessions with a non-'terminal' mode back to 'terminal'.
// Called on daemon startup to clear modes that were active when the daemon last stopped.
func (m *MetaStore) ResetStaleModes() error {
	_, err := m.db.Exec("UPDATE session_meta SET mode = 'terminal' WHERE mode != 'terminal'")
	return err
}
