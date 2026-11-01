package dialogue

import (
	"database/sql"
	"errors"
	"fmt"
)

const (
	statePending = "pending"
	stateDone    = "done"

	reasonModel = "model" // skipped: the model said there was nothing new
)

// SummaryRow is one turn's summary (dlg_summaries).
type SummaryRow struct {
	ID          int64
	SessionID   string
	ConvKey     string
	TurnID      string
	TurnNo      int
	TurnAt      int64 // unix ms: the Stop event's time for the turn it names (the push matches on it), else the turn's end
	TurnSeq     int
	State       string // pending | done | skipped | failed
	Reason      string
	Title       string
	Push        string
	Summary     string
	PushReadyAt int64 // > 0 once the answer is stored: the push line is final
	Usage       usage
	CreatedAt   int64
	UpdatedAt   int64
}

const summaryCols = `id, session_id, conv_key, turn_id, turn_no, turn_at, turn_seq, state, reason, title, push, summary,
	push_ready_at, usage_in, usage_out, usage_cache_read, latency_ms, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanSummary(r rowScanner) (SummaryRow, error) {
	var x SummaryRow
	err := r.Scan(&x.ID, &x.SessionID, &x.ConvKey, &x.TurnID, &x.TurnNo, &x.TurnAt, &x.TurnSeq, &x.State, &x.Reason, &x.Title, &x.Push,
		&x.Summary, &x.PushReadyAt, &x.Usage.In, &x.Usage.Out, &x.Usage.CacheRead, &x.Usage.LatencyMS, &x.CreatedAt, &x.UpdatedAt)
	return x, err
}

func (s *Store) querySummaries(query string, args ...any) ([]SummaryRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read turn summaries: %w", err)
	}
	defer rows.Close()
	var out []SummaryRow
	for rows.Next() {
		x, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("read turn summaries: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// InsertPendingSummary records a pending summary for a turn. A turn that already has one keeps it: inserted is false and
// id is the existing row's. Only the identity fields of r are read (state is pending).
func (s *Store) InsertPendingSummary(r SummaryRow) (id int64, inserted bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now()
	res, err := s.db.Exec(`INSERT OR IGNORE INTO dlg_summaries
		(session_id, conv_key, turn_id, turn_no, turn_at, turn_seq, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		r.SessionID, r.ConvKey, r.TurnID, r.TurnNo, r.TurnAt, r.TurnSeq, now, now)
	if err != nil {
		return 0, false, fmt.Errorf("insert turn summary: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		id, _ = res.LastInsertId()
		return id, true, nil
	}
	if err := s.db.QueryRow(`SELECT id FROM dlg_summaries WHERE session_id = ? AND turn_id = ?`, r.SessionID, r.TurnID).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("insert turn summary: %w", err)
	}
	return id, false, nil
}

// SetSummaryDone stores the model's answer in ONE transaction, gated by `state = 'pending'`: a row that is no longer
// pending is left alone and applied is false. skip ends the row as skipped(model) with no push line; otherwise it ends
// as done with push_ready_at set and the session's since_recap goes up by one in the same transaction.
func (s *Store) SetSummaryDone(id int64, title, push, summary string, skip bool, u usage) (applied bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("store turn summary: %w", err)
	}
	defer tx.Rollback()
	now := s.now()
	state, reason, ready := stateDone, "", now
	if skip {
		state, reason, ready = stateSkipped, reasonModel, 0
		title, push, summary = "", "", ""
	}
	res, err := tx.Exec(`UPDATE dlg_summaries SET state = ?, reason = ?, title = ?, push = ?, summary = ?, push_ready_at = ?,
		usage_in = ?, usage_out = ?, usage_cache_read = ?, latency_ms = ?, updated_at = ?
		WHERE id = ? AND state = 'pending'`,
		state, reason, title, push, summary, ready, u.In, u.Out, u.CacheRead, u.LatencyMS, now, id)
	if err != nil {
		return false, fmt.Errorf("store turn summary: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	if s.failInSummaryDone != nil {
		if err := s.failInSummaryDone(); err != nil {
			return false, err
		}
	}
	if !skip {
		if _, err := tx.Exec(`UPDATE dlg_sessions SET since_recap = since_recap + 1
			WHERE session_id = (SELECT session_id FROM dlg_summaries WHERE id = ?)`, id); err != nil {
			return false, fmt.Errorf("store turn summary: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store turn summary: %w", err)
	}
	return true, nil
}

// FinishSummary ends a row that is still pending as skipped or failed; any other row is left alone (applied false).
func (s *Store) FinishSummary(id int64, state, reason string) (applied bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.Exec(`UPDATE dlg_summaries SET state = ?, reason = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		state, reason, s.now(), id)
	if err != nil {
		return false, fmt.Errorf("finish turn summary: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FailPendingSummaries ends every pending row as failed(stopped): their jobs lived in memory and are gone.
func (s *Store) FailPendingSummaries() (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.Exec(`UPDATE dlg_summaries SET state = 'failed', reason = ?, updated_at = ? WHERE state = 'pending'`, reasonStopped, s.now())
	if err != nil {
		return 0, fmt.Errorf("fail pending turn summaries: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SummaryByID returns one row; false when there is none.
func (s *Store) SummaryByID(id int64) (SummaryRow, bool, error) {
	x, err := scanSummary(s.db.QueryRow(`SELECT `+summaryCols+` FROM dlg_summaries WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SummaryRow{}, false, nil
	}
	if err != nil {
		return SummaryRow{}, false, fmt.Errorf("read turn summary: %w", err)
	}
	return x, true, nil
}

// ClosestSummary is the session's row whose turn_at is nearest to `at` within [at-before, at+after] (the push hold's
// match: a Stop's own row carries the event's time); false when there is none.
func (s *Store) ClosestSummary(sid string, at, beforeMS, afterMS int64) (SummaryRow, bool, error) {
	x, err := scanSummary(s.db.QueryRow(`SELECT `+summaryCols+` FROM dlg_summaries WHERE session_id = ? AND turn_at BETWEEN ? AND ?
		ORDER BY ABS(turn_at - ?) ASC, id DESC LIMIT 1`, sid, at-beforeMS, at+afterMS, at))
	if errors.Is(err, sql.ErrNoRows) {
		return SummaryRow{}, false, nil
	}
	if err != nil {
		return SummaryRow{}, false, fmt.Errorf("read turn summary near a stop: %w", err)
	}
	return x, true, nil
}

// RecentTitles returns the titles of the session's last n done summaries that have one, oldest first.
func (s *Store) RecentTitles(sid string, n int) ([]string, error) {
	rows, err := s.db.Query(`SELECT title FROM dlg_summaries WHERE session_id = ? AND state = 'done' AND title != ''
		ORDER BY turn_no DESC, id DESC LIMIT ?`, sid, n)
	if err != nil {
		return nil, fmt.Errorf("read recent titles: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("read recent titles: %w", err)
		}
		out = append([]string{t}, out...)
	}
	return out, rows.Err()
}

// SummariesByTurnID returns the done summaries of one turn (a rebuild puts them back after the turn).
func (s *Store) SummariesByTurnID(sid, turnID string) ([]SummaryRow, error) {
	return s.querySummaries(`SELECT `+summaryCols+` FROM dlg_summaries WHERE session_id = ? AND turn_id = ? AND state = 'done' ORDER BY id`, sid, turnID)
}

// SinceRecap is the number of summaries done since the session's last recap; 0 for an unknown session.
func (s *Store) SinceRecap(sid string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT since_recap FROM dlg_sessions WHERE session_id = ?`, sid).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read since_recap: %w", err)
	}
	return n, nil
}

// SummariesRange returns the DONE summaries with fromNo <= turn_no <= toNo, oldest first. A limit > 0 keeps the newest
// `limit` rows of the range (still returned oldest first). Pending, skipped and failed rows carry no text, so they are
// not part of a recap's input.
func (s *Store) SummariesRange(sid string, fromNo, toNo, limit int) ([]SummaryRow, error) {
	if limit <= 0 {
		return s.querySummaries(`SELECT `+summaryCols+` FROM dlg_summaries WHERE session_id = ? AND state = 'done' AND turn_no BETWEEN ? AND ?
			ORDER BY turn_no ASC, id ASC`, sid, fromNo, toNo)
	}
	return s.querySummaries(`SELECT * FROM (SELECT `+summaryCols+` FROM dlg_summaries WHERE session_id = ? AND state = 'done' AND turn_no BETWEEN ? AND ?
		ORDER BY turn_no DESC, id DESC LIMIT ?) ORDER BY turn_no ASC, id ASC`, sid, fromNo, toNo, limit)
}
