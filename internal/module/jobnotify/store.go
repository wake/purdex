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

// overlap is how far behind the legacy window the first catch-up pass still looks. It applies only until that pass has read everything
// wwd has (see catchUp): from then on the origin's cursor decides what is new, and no time stamp of a worker's does.
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
		CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS cursor (id INTEGER PRIMARY KEY CHECK (id = 1), cursor TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS cursor_epoch (id INTEGER PRIMARY KEY CHECK (id = 1), epoch TEXT NOT NULL);`); err != nil {
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

// cursor is wwd's position after the last page of the job list that was fully queued (false when there is none yet). It is opaque: only
// wwd's own answers are stored, and they are sent back as they came.
func (s *store) cursor() (string, bool, error) {
	var c string
	err := s.db.QueryRow(`SELECT cursor FROM cursor WHERE id = 1`).Scan(&c)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return c, err == nil, err
}

// epoch is the database epoch the stored cursor belongs to ("" when wwd gave none, as a wwd that predates epochs does).
func (s *store) epoch() (string, error) {
	var e string
	err := s.db.QueryRow(`SELECT epoch FROM cursor_epoch WHERE id = 1`).Scan(&e)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return e, err
}

// putCursor writes the cursor and, when wwd named one, its epoch; the caller commits.
func putCursor(tx *sql.Tx, c, epoch string) error {
	if _, err := tx.Exec(`INSERT INTO cursor(id, cursor) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET cursor = excluded.cursor`, c); err != nil {
		return err
	}
	if epoch == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO cursor_epoch(id, epoch) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET epoch = excluded.epoch`, epoch)
	return err
}

// setCursor stores the cursor of a page that was fully queued, with the epoch of the database it came from, in one commit.
func (s *store) setCursor(c, epoch string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := putCursor(tx, c, epoch); err != nil {
		return err
	}
	return tx.Commit()
}

// finishFirstPass stores the cursor of the last page and drops the first-pass window in one transaction: a crash between the two would
// leave a cursor with the window still in force, and the next pass would skip a new job.
func (s *store) finishFirstPass(c, epoch string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := putCursor(tx, c, epoch); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM meta WHERE key = 'since'`); err != nil {
		return err
	}
	return tx.Commit()
}

// restart forgets the cursor and its epoch and puts the first-pass window back at now, in one commit: wwd's database is another one,
// so what was read from the old one says nothing, and its history must not be told as news.
func (s *store) restart(now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM cursor`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM cursor_epoch`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('since', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// prune drops the rows that were last changed more than keep ago and are not pending. wwd keeps a job for as long as it keeps its
// row here, and lists it again only when it changes (a log uploaded after the end), so a row this old is no longer anybody's dedup.
func (s *store) prune(now time.Time) error {
	cutoff := now.Add(-keep).UnixMilli()
	_, err := s.db.Exec(`DELETE FROM notices WHERE status != ? AND updated_at < ? AND finished_at < ?`, statusPending, cutoff, cutoff)
	return err
}
