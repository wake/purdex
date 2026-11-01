package dialogue

import (
	"database/sql"
	"errors"
	"fmt"
)

// RecapRow is one recap (dlg_recaps).
type RecapRow struct {
	ID        int64
	JobID     string
	SessionID string
	ConvKey   string
	At        int64 // unix ms: when it was stored
	FromNo    int   // the turn numbers of the summaries it was written from
	ToNo      int
	Trigger   string // every30 | open | manual
	Headline  string
	BodyJSON  string // the recapOut as JSON
	UsageIn   int64
	UsageOut  int64
	LatencyMS int64
	CreatedAt int64
}

const recapCols = `id, job_id, session_id, conv_key, at, from_no, to_no, trigger, headline, body_json, usage_in, usage_out, latency_ms, created_at`

func scanRecap(r rowScanner) (RecapRow, error) {
	var x RecapRow
	err := r.Scan(&x.ID, &x.JobID, &x.SessionID, &x.ConvKey, &x.At, &x.FromNo, &x.ToNo, &x.Trigger, &x.Headline, &x.BodyJSON,
		&x.UsageIn, &x.UsageOut, &x.LatencyMS, &x.CreatedAt)
	return x, err
}

func (s *Store) queryRecaps(query string, args ...any) ([]RecapRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read recaps: %w", err)
	}
	defer rows.Close()
	var out []RecapRow
	for rows.Next() {
		x, err := scanRecap(rows)
		if err != nil {
			return nil, fmt.Errorf("read recaps: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) oneRecap(query string, args ...any) (RecapRow, bool, error) {
	x, err := scanRecap(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return RecapRow{}, false, nil
	}
	if err != nil {
		return RecapRow{}, false, fmt.Errorf("read recap: %w", err)
	}
	return x, true, nil
}

// InsertRecap stores a recap in ONE transaction: the row (INSERT OR IGNORE on job_id) and the session's since_recap, reset to the done summaries past the recap's to_no.
// A job that already has its recap changes nothing and applied is false. The row's At and CreatedAt are set here.
func (s *Store) InsertRecap(r RecapRow) (id int64, applied bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, fmt.Errorf("store recap: %w", err)
	}
	defer tx.Rollback()
	now := s.now()
	res, err := tx.Exec(`INSERT OR IGNORE INTO dlg_recaps
		(job_id, session_id, conv_key, at, from_no, to_no, trigger, headline, body_json, usage_in, usage_out, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.JobID, r.SessionID, r.ConvKey, now, r.FromNo, r.ToNo, r.Trigger, r.Headline, r.BodyJSON, r.UsageIn, r.UsageOut, r.LatencyMS, now)
	if err != nil {
		return 0, false, fmt.Errorf("store recap: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, false, nil
	}
	id, _ = res.LastInsertId()
	// The count restarts from the summaries this recap does not cover: those done (SetSummaryDone counts only state
	// 'done') for turns after its to_no, finished while it was being written.
	if _, err := tx.Exec(`UPDATE dlg_sessions SET since_recap =
		(SELECT COUNT(*) FROM dlg_summaries WHERE session_id = ? AND state = 'done' AND turn_no > ?)
		WHERE session_id = ?`, r.SessionID, r.ToNo, r.SessionID); err != nil {
		return 0, false, fmt.Errorf("store recap: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("store recap: %w", err)
	}
	return id, true, nil
}

// RecapByID returns one recap; false when there is none.
func (s *Store) RecapByID(id int64) (RecapRow, bool, error) {
	return s.oneRecap(`SELECT `+recapCols+` FROM dlg_recaps WHERE id = ?`, id)
}

// RecapByJob returns the recap a job stored; false when it stored none.
func (s *Store) RecapByJob(jobID string) (RecapRow, bool, error) {
	return s.oneRecap(`SELECT `+recapCols+` FROM dlg_recaps WHERE job_id = ?`, jobID)
}

// LatestRecap is the session's newest recap (by id); false when it has none.
func (s *Store) LatestRecap(sid string) (RecapRow, bool, error) {
	return s.oneRecap(`SELECT `+recapCols+` FROM dlg_recaps WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sid)
}

// LatestRecapUpTo is the session's newest recap whose to_no is at most maxToNo (a new segment carries it); false when none.
func (s *Store) LatestRecapUpTo(sid string, maxToNo int) (RecapRow, bool, error) {
	return s.oneRecap(`SELECT `+recapCols+` FROM dlg_recaps WHERE session_id = ? AND to_no <= ? ORDER BY to_no DESC, id DESC LIMIT 1`, sid, maxToNo)
}

// RecapsEndingAt returns the session's recaps whose to_no is turnNo, oldest first (a rebuild puts them after that turn).
func (s *Store) RecapsEndingAt(sid string, turnNo int) ([]RecapRow, error) {
	return s.queryRecaps(`SELECT `+recapCols+` FROM dlg_recaps WHERE session_id = ? AND to_no = ? ORDER BY id`, sid, turnNo)
}

// ListRecaps returns the session's recaps, newest first; limit <= 0 means all.
func (s *Store) ListRecaps(sid string, limit int) ([]RecapRow, error) {
	if limit <= 0 {
		limit = -1
	}
	return s.queryRecaps(`SELECT `+recapCols+` FROM dlg_recaps WHERE session_id = ? ORDER BY id DESC LIMIT ?`, sid, limit)
}

// LatestRecapToNo is the to_no of the session's newest recap by turn range; 0 when it has none.
func (s *Store) LatestRecapToNo(sid string) (int, error) {
	var n sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(to_no) FROM dlg_recaps WHERE session_id = ?`, sid).Scan(&n); err != nil {
		return 0, fmt.Errorf("read recap range: %w", err)
	}
	return int(n.Int64), nil
}
