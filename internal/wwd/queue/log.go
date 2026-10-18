package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	// logRetention: logs are kept this long, the jobs longer (spec 4.2: logs 3 days, jobs 7).
	logRetention = 3 * 24 * time.Hour
	// defaultMaxLog caps one uploaded log. A worker is trusted with a claim, not with the origin's disk; a test run's output is
	// kilobytes to a few megabytes. (Queue.maxLog, so a test can lower it.)
	defaultMaxLog = 64 << 20
)

// ErrLogTooLarge: the uploaded log is bigger than the cap; nothing of it is kept.
var ErrLogTooLarge = errors.New("queue: the log is too large")

// ErrNoLogDir: the queue was opened without a place to keep logs.
var ErrNoLogDir = errors.New("queue: no log directory")

// PutLog stores a job's log from body, replacing the one there. It takes the claim that holds the job, or the claim that posted its result
// (the worker may post the result first and upload afterwards); any other claim is a ConflictError and nothing is kept (spec 4.2). The
// body is written to a temporary file outside the database's write lock and put in place inside the transaction that checks the claim again,
// so a claim that went stale while the body was coming cannot replace the log of the one that holds the job now, and an upload that
// breaks half way leaves the log that was there. Once the job has ended, its result points at the log (Result.Log).
func (q *Queue) PutLog(ctx context.Context, id, claimID string, body io.Reader) error {
	if q.opt.LogDir == "" {
		return ErrNoLogDir
	}
	if !validLogID(id) {
		return ErrNotFound
	}
	if err := q.checkLogRight(ctx, id, claimID); err != nil { // before the body is read: a stale claim costs nothing
		return err
	}
	tmp, err := os.CreateTemp(q.opt.LogDir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once it has been renamed into place
	n, err := io.Copy(tmp, io.LimitReader(body, q.maxLog+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("queue: log upload: %w", err)
	}
	if n > q.maxLog {
		return ErrLogTooLarge
	}
	if q.beforeLogCommit != nil {
		q.beforeLogCommit()
	}
	// The new file replaces the old inside the transaction, with the old one kept beside it until the commit: a transaction that fails
	// puts the old log back. A process that dies between the rename and the commit leaves the new file in place with the database still
	// describing the old one (the log is the holder's latest upload either way; its size is read from the file, not the database); the
	// job's deletion removes a file by id, so nothing is left for good. The old content is the one thing that is not kept, and it was
	// being replaced.
	final, backup := q.logPath(id), tmp.Name()+".prev"
	var placed, hadOld bool
	err = q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return err
		}
		if err := h.mayLog(claimID); err != nil {
			return err
		}
		if err := os.Rename(final, backup); err == nil {
			hadOld = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(tmp.Name(), final); err != nil {
			return err
		}
		placed = true
		if _, err := c.ExecContext(ctx, `UPDATE jobs SET log_size = ?, log_at = ? WHERE id = ?`, n, q.now().UnixMilli(), id); err != nil {
			return err
		}
		if h.ended() {
			if _, err := c.ExecContext(ctx, `UPDATE jobs SET result = json_set(result, '$.log', ?) WHERE id = ?`, id+"/log", id); err != nil {
				return err
			}
			*changed = append(*changed, id)
		}
		return nil
	})
	if err != nil {
		if placed {
			_ = os.Remove(final)
		}
		if hadOld {
			_ = os.Rename(backup, final)
		}
		return err
	}
	if hadOld {
		_ = os.Remove(backup)
	}
	return nil
}

func (q *Queue) checkLogRight(ctx context.Context, id, claimID string) error {
	c, err := q.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	h, err := loadHeld(ctx, c, id, false)
	if err != nil {
		return err
	}
	return h.mayLog(claimID)
}

// mayLog is nil for the claim that holds the job, or for the one whose result ended it.
func (h held) mayLog(claimID string) error {
	if err := h.live(claimID); err == nil {
		return nil
	}
	if h.ended() && h.resultClaimID.Valid && h.resultClaimID.String == claimID {
		return nil
	}
	return &ConflictError{State: h.state}
}

// OpenLog opens a job's log for reading and returns its size. A job with no log (none uploaded, or past the retention) is ErrNotFound;
// forPeers hides the logs of ad-hoc and fast-lane jobs, as every peer read does.
func (q *Queue) OpenLog(ctx context.Context, id string, forPeers bool) (io.ReadCloser, int64, error) {
	if q.opt.LogDir == "" || !validLogID(id) {
		return nil, 0, ErrNotFound
	}
	query := `SELECT 1 FROM jobs WHERE id = ? AND log_size IS NOT NULL`
	if forPeers {
		query += ` AND adhoc = 0 AND local = 0`
	}
	var one int
	switch err := q.db.QueryRowContext(ctx, query, id).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, 0, ErrNotFound
	case err != nil:
		return nil, 0, err
	}
	f, err := os.Open(q.logPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func (q *Queue) logPath(id string) string { return filepath.Join(q.opt.LogDir, id+".log") }

// validLogID: a job id is a file name here, so it must not be able to leave the log directory.
func validLogID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id
}

// uploadLeftover: a temporary file (or backup) of an upload older than this is a crash's, not an upload in flight.
const uploadLeftover = time.Hour

// removeLogFiles deletes the log files of ids. It runs after the database change that made them unwanted has committed, never before:
// a change that rolls back must not have taken the file with it. An id whose file cannot be removed is left for the job's own deletion,
// which removes the file by id whether or not the job still says it has one.
func (q *Queue) removeLogFiles(ids []string) {
	if q.opt.LogDir == "" {
		return
	}
	for _, id := range ids {
		if validLogID(id) {
			_ = os.Remove(q.logPath(id))
		}
	}
}

// deleteRetainedJobs deletes the ended jobs kept past the retention, in one statement, and then their log files by id. The order is the
// point: a late upload (a result's claim may upload long after) either commits before the delete, and its file is removed after it, or
// finds no job. A file for a job whose row says it has no log (an upload the process died in the middle of) goes too.
func (q *Queue) deleteRetainedJobs(ctx context.Context, cutoffMS int64) (int, error) {
	rows, err := q.db.QueryContext(ctx, `DELETE FROM jobs WHERE state IN (`+terminalStates+`) AND pinned = 0
		AND coalesce(finished_at, updated_at) < ? RETURNING id`, cutoffMS)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	q.removeLogFiles(ids)
	return len(ids), nil
}

// sweepLogs forgets the logs kept longer than logRetention (on their jobs, and in their results), in a transaction of its own that the
// rest of the sweep cannot roll back, and deletes their files once that has committed: a job that points at a log that is gone is a log
// that will not open, and a file nothing points at is found again by the job's deletion. It also clears the leftovers of uploads a crash
// cut short.
func (q *Queue) sweepLogs(ctx context.Context, now time.Time) (int, error) {
	var ids []string
	err := q.writeOnly(ctx, func(c *sql.Conn, changed *[]string) error {
		ids = nil
		rows, err := c.QueryContext(ctx, `SELECT id FROM jobs WHERE log_at IS NOT NULL AND log_at < ? ORDER BY seq`, now.Add(-logRetention).UnixMilli())
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := c.ExecContext(ctx, `UPDATE jobs SET log_size = NULL, log_at = NULL,
				result = CASE WHEN result IS NULL THEN NULL ELSE json_set(result, '$.log', '') END WHERE id = ?`, id); err != nil {
				return err
			}
			*changed = append(*changed, id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	q.removeLogFiles(ids)
	if q.opt.LogDir != "" {
		left, _ := filepath.Glob(filepath.Join(q.opt.LogDir, ".upload-*"))
		for _, p := range left {
			if st, err := os.Stat(p); err == nil && now.Sub(st.ModTime()) > uploadLeftover {
				_ = os.Remove(p)
			}
		}
	}
	return len(ids), nil
}
