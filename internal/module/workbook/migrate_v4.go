package workbook

// migrateV4 lets the outbox carry rows that belong to no workbook entry: the team task mirror (J-1c) writes its rows with
// entry_id 0. The one-row-per-(entry, kind) index stays for entries (a result applied twice is not a second row), and
// stops applying to entry 0.
func migrateV4(tx stepExec) error {
	_, err := tx.Exec(`
		DROP INDEX wb_journal_outbox_entry_kind;
		CREATE UNIQUE INDEX wb_journal_outbox_entry_kind ON wb_journal_outbox (entry_id, kind) WHERE entry_id > 0;
		UPDATE schema_version SET version = 4;`)
	return err
}
