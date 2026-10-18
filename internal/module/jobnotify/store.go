package jobnotify

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Notice statuses.
const (
	statusPending = "pending"
	statusSent    = "sent"
	statusGivenUp = "given_up"
)

// maxTries is how many times a notice is tried in all: a session that was only just started may not have its inbox yet, one that
// is not on this host never will. The count is stored, so a restart does not start it over.
const maxTries = 6

// keep is how long a notice row is kept after its last change, like the job it is about.
const keep = 7 * 24 * time.Hour

// overlap is how far behind the catch-up window a catch-up looks again. Looking twice at a job costs nothing (job_id is unique). It
// covers a job that ended while the previous catch-up was reading, and a finished time stamped by a worker whose clock runs behind
// this host's; the window is a hint, so a worker more than an hour behind could still be missed (see the PR).
const overlap = time.Hour

// store is the notifier's outbox: one row per job to tell, unique by job id, so "told once" holds across reconnects, restarts and
// overlapping catch-ups without any in-memory set.
type store struct{ db *sql.DB }

// notice is a row that is still to be delivered.
type notice struct {
	JobID, Session, Text string
	Tries                int
}

func openStore(path string) (*store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open job notice db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS notices (
			job_id      TEXT PRIMARY KEY,
			session     TEXT    NOT NULL,
			text        TEXT    NOT NULL,
			state       TEXT    NOT NULL,
			finished_at INTEGER NOT NULL,
			status      TEXT    NOT NULL,
			tries       INTEGER NOT NULL DEFAULT 0,
			last_error  TEXT    NOT NULL DEFAULT '',
			updated_at  INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS notices_status ON notices(status);
		CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate job notice db: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) close() error { return s.db.Close() }

// enqueue adds a pending notice for a job; false when the job already has a row (told, waiting or given up).
func (s *store) enqueue(jobID, session, text, state string, finished, now time.Time) (bool, error) {
	r, err := s.db.Exec(`INSERT OR IGNORE INTO notices(job_id, session, text, state, finished_at, status, tries, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, 0, ?)`, jobID, session, text, state, finished.UnixMilli(), statusPending, now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

// pending lists the notices still to be delivered, oldest first. A pending row that already used all its tries (a crash between
// the stored try and its result) is closed as given up here, so it can neither be tried again nor stay pending for ever.
func (s *store) pending(now time.Time) ([]notice, error) {
	if _, err := s.db.Exec(`UPDATE notices SET status = ?, updated_at = ? WHERE status = ? AND tries >= ?`,
		statusGivenUp, now.UnixMilli(), statusPending, maxTries); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT job_id, session, text, tries FROM notices WHERE status = ? ORDER BY finished_at, job_id`, statusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notice
	for rows.Next() {
		var n notice
		if err := rows.Scan(&n.JobID, &n.Session, &n.Text, &n.Tries); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// beginTry stores that a try is about to be made and returns the number of tries including it. It is written before the send, so a
// crash during the send still counts it.
func (s *store) beginTry(jobID string, now time.Time) (int, error) {
	var tries int
	err := s.db.QueryRow(`UPDATE notices SET tries = tries + 1, updated_at = ? WHERE job_id = ? AND status = ? RETURNING tries`,
		now.UnixMilli(), jobID, statusPending).Scan(&tries)
	return tries, err
}

// settle sets a notice's status and last error; a failed try that still has tries left keeps it pending and only records the error.
func (s *store) settle(jobID, status, lastErr string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE notices SET status = ?, last_error = ?, updated_at = ? WHERE job_id = ?`, status, lastErr, now.UnixMilli(), jobID)
	return err
}

// status returns a job's row status ("" when it has none) and its tries.
func (s *store) status(jobID string) (string, int, error) {
	var st string
	var tries int
	err := s.db.QueryRow(`SELECT status, tries FROM notices WHERE job_id = ?`, jobID).Scan(&st, &tries)
	if err == sql.ErrNoRows {
		return "", 0, nil
	}
	return st, tries, err
}

// since is the catch-up window: the time the last complete catch-up began (false when there was none).
func (s *store) since() (time.Time, bool, error) {
	var ms int64
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'since'`).Scan(&ms)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.UnixMilli(ms), true, nil
}

func (s *store) setSince(t time.Time) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES('since', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, t.UnixMilli())
	return err
}

// prune drops the rows that were last changed more than keep ago and are not pending. A row is also kept while the catch-up could
// still list its job again (its finished time is not before the window less the overlap): after a daemon that was down for longer
// than keep, the window is old and the rows it covers are the dedup of whatever wwd still lists.
func (s *store) prune(now time.Time) error {
	cutoff := now.Add(-keep)
	since, ok, err := s.since()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if w := since.Add(-overlap); w.Before(cutoff) {
		cutoff = w
	}
	_, err = s.db.Exec(`DELETE FROM notices WHERE status != ? AND updated_at < ? AND finished_at < ?`, statusPending, cutoff.UnixMilli(), cutoff.UnixMilli())
	return err
}
