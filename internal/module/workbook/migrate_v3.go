package workbook

// migrateV3 is the journal's schema (J-1b2): the outbox the result transactions write and the drainer empties, the
// unit registry, the cursors (the team task mirror uses them), and the per-conversation todo number. Nothing reads
// these tables unless a journal source is wired.
func migrateV3(tx stepExec) error {
	_, err := tx.Exec(`
		CREATE TABLE wb_journal_outbox (
			id INTEGER PRIMARY KEY,
			entry_id INTEGER NOT NULL,
			unit_key TEXT NOT NULL,
			kind TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			done_at INTEGER NOT NULL DEFAULT 0);
		CREATE INDEX wb_journal_outbox_undone ON wb_journal_outbox (id) WHERE done_at = 0;
		CREATE UNIQUE INDEX wb_journal_outbox_entry_kind ON wb_journal_outbox (entry_id, kind);
		CREATE TABLE wb_journal_units (
			unit_key TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			project_id TEXT NOT NULL,
			wt_path TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			lead_conv TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL);
		CREATE TABLE wb_journal_cursors (name TEXT PRIMARY KEY, value TEXT NOT NULL);
		ALTER TABLE wb_todos ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
		UPDATE wb_todos SET seq = r.rn FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY conv_key ORDER BY id) AS rn FROM wb_todos) AS r WHERE r.id = wb_todos.id;
		CREATE UNIQUE INDEX wb_todos_conv_seq ON wb_todos (conv_key, seq) WHERE seq > 0;
		UPDATE schema_version SET version = 3;`)
	return err
}
