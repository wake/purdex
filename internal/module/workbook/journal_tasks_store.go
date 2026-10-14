package workbook

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// OutboxTask is the kind of an outbox row of the team task mirror: it belongs to no workbook entry (entry_id 0), carries
// one team's new journal entries, late closing lines and the whole current task list for its tasks file.
const OutboxTask = "task"

// TaskOutboxRow is one outbox row of a task page.
type TaskOutboxRow struct {
	UnitKey string
	Payload any
}

// TaskPage is what one page of the team feed changed, to be stored as one unit: the outbox rows and every cursor or state
// value the mirror keeps in wb_journal_cursors.
type TaskPage struct {
	Rows    []TaskOutboxRow
	Cursors map[string]string
}

// JournalCursor reads a value of wb_journal_cursors.
func (s *Store) JournalCursor(name string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM wb_journal_cursors WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read journal cursor %s: %w", name, err)
	}
	return v, true, nil
}

// ApplyTaskPage stores a page in ONE transaction: its outbox rows and its cursors. Either all of it is kept or none, so a
// failure leaves the cursor where it was and the page is read again; a crash cannot move the cursor past rows that were
// not stored.
func (s *Store) ApplyTaskPage(p TaskPage) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now()
	return s.inTx(func(tx execer) error {
		for _, r := range p.Rows {
			if err := insertOutbox(tx, 0, r.UnitKey, OutboxTask, r.Payload, now); err != nil {
				return err
			}
		}
		names := make([]string, 0, len(p.Cursors))
		for n := range p.Cursors {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if _, err := tx.Exec(`INSERT INTO wb_journal_cursors (name, value) VALUES (?, ?)
				ON CONFLICT (name) DO UPDATE SET value = excluded.value`, n, p.Cursors[n]); err != nil {
				return fmt.Errorf("write journal cursor %s: %w", n, err)
			}
		}
		return nil
	})
}
