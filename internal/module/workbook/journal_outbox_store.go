package workbook

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// OutboxItem is an outbox row the drainer may write now.
type OutboxItem struct {
	ID, EntryID int64
	UnitKey     string
	Kind        string
	Payload     string
}

// ReadyOutbox lists the undone rows whose entry is not pending any more, oldest first. A `journal` row also waits for
// its turn's `turn` row to be done, so the order of one turn's writes is kept. skip are rows the caller will not try
// now (waiting out a retry delay): they are left out in the query, so that a batch of failing rows cannot fill the
// limit and starve the rows behind them.
func (s *Store) ReadyOutbox(limit int, skip []int64) ([]OutboxItem, error) {
	if skip == nil {
		skip = []int64{}
	}
	skipJSON, err := json.Marshal(skip)
	if err != nil {
		return nil, fmt.Errorf("read journal outbox: %w", err)
	}
	rows, err := s.db.Query(`SELECT o.id, o.entry_id, o.unit_key, o.kind, o.payload
		FROM wb_journal_outbox o JOIN wb_entries e ON e.id = o.entry_id
		WHERE o.done_at = 0 AND e.state <> 'pending'
			AND (o.kind = 'turn' OR NOT EXISTS (SELECT 1 FROM wb_journal_outbox t WHERE t.entry_id = o.entry_id AND t.kind = 'turn' AND t.done_at = 0))
			AND o.id NOT IN (SELECT value FROM json_each(?))
		ORDER BY o.id LIMIT ?`, string(skipJSON), limit)
	if err != nil {
		return nil, fmt.Errorf("read journal outbox: %w", err)
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		if err := rows.Scan(&it.ID, &it.EntryID, &it.UnitKey, &it.Kind, &it.Payload); err != nil {
			return nil, fmt.Errorf("read journal outbox: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkOutboxDone records that a row is written.
func (s *Store) MarkOutboxDone(id int64) error {
	if _, err := s.db.Exec(`UPDATE wb_journal_outbox SET done_at = ? WHERE id = ? AND done_at = 0`, s.now(), id); err != nil {
		return fmt.Errorf("mark journal outbox row %d done: %w", id, err)
	}
	return nil
}

// UnitRow is a registered journal unit: where its project's files live, fixed at its first write.
type UnitRow struct {
	Key, Kind, ProjectID, WtPath, Label, LeadConv string
}

// Unit reads a registered unit.
func (s *Store) Unit(key string) (UnitRow, bool, error) {
	var u UnitRow
	err := s.db.QueryRow(`SELECT unit_key, kind, project_id, wt_path, label, lead_conv FROM wb_journal_units WHERE unit_key = ?`, key).
		Scan(&u.Key, &u.Kind, &u.ProjectID, &u.WtPath, &u.Label, &u.LeadConv)
	if errors.Is(err, sql.ErrNoRows) {
		return UnitRow{}, false, nil
	}
	if err != nil {
		return UnitRow{}, false, fmt.Errorf("read journal unit: %w", err)
	}
	return u, true, nil
}

// RegisterUnit stores a unit the first time it writes. A unit that is registered already keeps its row (the first
// writer wins); the row that stands is returned.
func (s *Store) RegisterUnit(u UnitRow) (UnitRow, error) {
	if _, err := s.db.Exec(`INSERT INTO wb_journal_units (unit_key, kind, project_id, wt_path, label, lead_conv, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (unit_key) DO NOTHING`, u.Key, u.Kind, u.ProjectID, u.WtPath, u.Label, u.LeadConv, s.now()); err != nil {
		return UnitRow{}, fmt.Errorf("register journal unit: %w", err)
	}
	got, ok, err := s.Unit(u.Key)
	if err == nil && !ok {
		err = errors.New("register journal unit: row vanished")
	}
	return got, err
}

// ConvTodos is every todo of a conversation (open, done and dropped) by number.
func (s *Store) ConvTodos(convKey string) ([]Todo, error) {
	return queryTodos(s.db, `SELECT `+todoCols+` FROM wb_todos WHERE conv_key = ? ORDER BY seq, id`, convKey)
}
