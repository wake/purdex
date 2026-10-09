package workbook

import (
	"database/sql"
	"errors"
	"fmt"
)

// PendingRefresh reports whether the conversation has a refresh entry that is still pending (one at a time, spec §5.6).
func (s *Store) PendingRefresh(convKey string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM wb_entries WHERE conv_key = ? AND kind = 'refresh' AND state = 'pending' LIMIT 1`, convKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read pending workbook refresh: %w", err)
	}
	return true, nil
}

// RepointSession moves a pending entry to the session that actually runs it (plan D14: a refresh asked for one session
// may be taken by another capable session of the conversation). No event: the entry's text did not change.
func (s *Store) RepointSession(entryID int64, sessionID, ref, teamID, role string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.failRepoint != nil { // test seam
		if err := s.failRepoint(); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE wb_entries SET session_id = ?, ref = ?, team_id = ?, role = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		sessionID, ref, teamID, role, s.now(), entryID); err != nil {
		return fmt.Errorf("re-point workbook entry: %w", err)
	}
	return nil
}

// RefreshDone is a refresh job's validated result.
type RefreshDone struct {
	Status    string
	Todos     TodoChanges
	Usage     Usage
	LatencyMS int64
}

// FinishRefresh writes a refresh's whole result in ONE transaction (plan D14): the status replaced, the todo changes
// applied (closed_by refresh, up to 10 adds), and the entry made ok with the conversation's current thing and the line
// 「重整：完成 a、移除 b、新增 c」 — no push. ok is false, and nothing is written, when the entry is not a pending refresh.
// Events follow the commit: status, todos, entry.
func (s *Store) FinishRefresh(entryID int64, d RefreshDone) (res TodoResult, ok bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var conv, session string
	now := s.now()
	err = s.inTx(func(tx execer) error {
		if err := tx.QueryRow(`SELECT conv_key, session_id FROM wb_entries WHERE id = ? AND kind = 'refresh' AND state = 'pending'`, entryID).
			Scan(&conv, &session); errors.Is(err, sql.ErrNoRows) {
			return errNotPending
		} else if err != nil {
			return fmt.Errorf("read workbook entry %d: %w", entryID, err)
		}
		var thing string
		if err := tx.QueryRow(`SELECT COALESCE(thing, '') FROM wb_entries WHERE conv_key = ? AND kind = 'turn' AND COALESCE(thing, '') != ''
			ORDER BY id DESC LIMIT 1`, conv).Scan(&thing); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read the current workbook thing: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO wb_status (conv_key, status, entry_id, session_id, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (conv_key) DO UPDATE SET status = excluded.status, entry_id = excluded.entry_id,
				session_id = excluded.session_id, updated_at = excluded.updated_at`, conv, d.Status, entryID, session, now); err != nil {
			return fmt.Errorf("set workbook status: %w", err)
		}
		if res, err = applyTodoChanges(tx, conv, entryID, d.Todos, ClosedByRefresh, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE wb_entries SET state = 'ok', reason = '', thing = ?, entry = ?, latency_ms = ?,
				usage_in = ?, usage_out = ?, usage_cache_read = ?, updated_at = ? WHERE id = ?`,
			thing, RefreshLine(entryID, res.Changed), d.LatencyMS, nullInt(d.Usage.In), nullInt(d.Usage.Out), nullInt(d.Usage.CacheRead), now, entryID); err != nil {
			return fmt.Errorf("finish workbook refresh: %w", err)
		}
		return nil
	})
	if errors.Is(err, errNotPending) {
		return TodoResult{}, false, nil
	}
	if err != nil {
		return TodoResult{}, false, err
	}
	s.emit(Event{Kind: EventStatus, ConvKey: conv, SessionID: session,
		Status: StatusRow{ConvKey: conv, Status: d.Status, EntryID: entryID, SessionID: session, UpdatedAt: now}})
	s.emitTodos(conv, session, res.Changed)
	s.emitEntry(entryID)
	return res, true, nil
}

// RefreshLine is the entry text of a refresh: what it did to the list, counted from the rows it changed (spec §5.6).
func RefreshLine(entryID int64, changed []Todo) string {
	var done, dropped, added int
	for _, t := range changed {
		switch {
		case t.State == TodoDone:
			done++
		case t.State == TodoDropped:
			dropped++
		case t.AddedEntryID == entryID:
			added++
		}
	}
	return fmt.Sprintf("重整：完成 %d、移除 %d、新增 %d", done, dropped, added)
}
