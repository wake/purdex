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
	return q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		h, err := loadHeld(ctx, c, id, false)
		if err != nil {
			return err
		}
		if err := h.mayLog(claimID); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), q.logPath(id)); err != nil {
			return err
		}
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

// sweepLogs deletes the logs kept longer than logRetention, forgets them on their jobs and takes the pointer out of the results. The file
// goes first: a job that still points at a file that is gone is only a log that will not open, and the next sweep clears it.
func (q *Queue) sweepLogs(ctx context.Context, c *sql.Conn, now time.Time, changed *[]string) (int, error) {
	rows, err := c.QueryContext(ctx, `SELECT id FROM jobs WHERE log_at IS NOT NULL AND log_at < ? ORDER BY seq`, now.Add(-logRetention).UnixMilli())
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
	removed := 0
	for _, id := range ids {
		if validLogID(id) && q.opt.LogDir != "" {
			if err := os.Remove(q.logPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue // the next sweep tries again
			}
		}
		if _, err := c.ExecContext(ctx, `UPDATE jobs SET log_size = NULL, log_at = NULL,
			result = CASE WHEN result IS NULL THEN NULL ELSE json_set(result, '$.log', '') END WHERE id = ?`, id); err != nil {
			return removed, err
		}
		*changed = append(*changed, id)
		removed++
	}
	return removed, nil
}
