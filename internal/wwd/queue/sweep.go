package queue

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// defaultQueueTimeout: a queued job with no queue_timeout_s of its own waits this long (spec 4.2).
	defaultQueueTimeout = 2 * time.Hour
	// retention: ended jobs and their results are kept this long (spec 4.2).
	retention = 7 * 24 * time.Hour
	// orphanGrace: a ref whose job id was made less than this long ago is never called an orphan, because Submit pins the snapshot
	// moments before it writes the row, and the sweep must not take the pin from a job that is about to exist. The id carries its
	// time (a ULID), so no row or clock of the pin is needed.
	orphanGrace = 10 * time.Minute
)

// terminalStates is the SQL list of the states from which a job does not move again.
const terminalStates = `'succeeded','failed','cancelled','superseded','expired'`

// SweepResult counts what one Sweep did.
type SweepResult struct {
	Expired     int // queued jobs that waited past their queue timeout
	Unpinned    int // snapshots released for jobs that had ended
	UnpinFailed int // unpins git refused: the mark stays and the next sweep tries again
	Orphans     int // refs under refs/wwd/ with no live job, removed
	Deleted     int // ended jobs older than the retention, deleted
}

// Sweep does the origin's periodic housekeeping (wwd calls it every few seconds): it expires queued jobs that waited too long, releases
// the snapshots of jobs that have ended, removes refs/wwd/ refs that belong to no live job, and deletes jobs kept past the retention.
// It is safe to run at any time and as often as you like, and it does nothing when there is nothing to do. A git failure (an unpin
// refused, a git directory that cannot be listed) never undoes a state change; it is retried by the next sweep, and a directory that
// cannot be listed is reported in the error.
func (q *Queue) Sweep(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	var errs []error

	now := q.now()
	err := q.writeOnly(ctx, func(c *sql.Conn, changed *[]string) error {
		rows, err := c.QueryContext(ctx, `SELECT id FROM jobs WHERE state = 'queued'
			AND created_at + (CASE WHEN queue_timeout_s > 0 THEN queue_timeout_s * 1000 ELSE ? END) < ? ORDER BY seq`,
			defaultQueueTimeout.Milliseconds(), now.UnixMilli())
		if err != nil {
			return err
		}
		var ids []string
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
			if err := finishWithoutRunning(ctx, c, id, wire.StateExpired, "queue_timeout", nil, now.UnixMilli()); err != nil {
				return err
			}
			*changed = append(*changed, id)
		}
		res.Expired = len(ids)
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}

	res.Unpinned, res.UnpinFailed = q.settlePins(ctx)

	// retention: only ended jobs whose snapshot is released; one still marked waits for its unpin
	r, err := q.db.ExecContext(ctx, `DELETE FROM jobs WHERE state IN (`+terminalStates+`) AND pinned = 0
		AND coalesce(finished_at, updated_at) < ?`, now.Add(-retention).UnixMilli())
	if err != nil {
		errs = append(errs, err)
	} else if n, _ := r.RowsAffected(); n > 0 {
		res.Deleted = int(n)
	}

	orphans, failed, err := q.removeOrphanPins(ctx, now)
	res.Orphans, res.UnpinFailed = orphans, res.UnpinFailed+failed
	if err != nil {
		errs = append(errs, err)
	}
	return res, errors.Join(errs...)
}

// settlePins releases the snapshot of every ended job still marked pinned. The state change that ended the job was committed first; a
// git failure here leaves the mark, and the next sweep (or the next write) tries again. A git directory that is gone holds no ref.
// It works on a context that outlives the request: a client that hung up must not leave a ref behind.
func (q *Queue) settlePins(ctx context.Context) (unpinned, failed int) {
	if q.pin == nil {
		return 0, 0
	}
	ctx = context.WithoutCancel(ctx)
	rows, err := q.db.QueryContext(ctx, `SELECT id, git_dir FROM jobs WHERE pinned = 1 AND state IN (`+terminalStates+`) ORDER BY seq LIMIT 100`)
	if err != nil {
		return 0, 0
	}
	type held struct{ id, dir string }
	var todo []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.dir); err == nil {
			todo = append(todo, h)
		}
	}
	rows.Close()
	for _, h := range todo {
		if _, err := os.Stat(h.dir); errors.Is(err, os.ErrNotExist) {
			// the repository is gone, and its refs with it
		} else if err := q.pin.Unpin(ctx, h.dir, h.id); err != nil {
			failed++
			continue
		}
		if _, err := q.db.ExecContext(ctx, `UPDATE jobs SET pinned = 0 WHERE id = ?`, h.id); err == nil {
			unpinned++
		}
	}
	return unpinned, failed
}

// removeOrphanPins lists refs/wwd/ in every git directory the table knows and unpins the ones whose job is gone or has ended. Only
// job ids come back from ListPins, and Unpin only ever removes refs/wwd/<id>, so nothing else in a user's repository is touched.
func (q *Queue) removeOrphanPins(ctx context.Context, now time.Time) (removed, failed int, err error) {
	if q.pin == nil {
		return 0, 0, nil
	}
	ctx = context.WithoutCancel(ctx)
	rows, qerr := q.db.QueryContext(ctx, `SELECT DISTINCT git_dir FROM jobs WHERE git_dir != ''`)
	if qerr != nil {
		return 0, 0, qerr
	}
	var dirs []string
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			dirs = append(dirs, d)
		}
	}
	rows.Close()

	var errs []error
	for _, dir := range dirs {
		if _, serr := os.Stat(dir); errors.Is(serr, os.ErrNotExist) {
			continue
		}
		ids, lerr := q.pin.ListPins(ctx, dir)
		if lerr != nil {
			errs = append(errs, lerr)
			continue
		}
		for _, id := range ids {
			if t, ok := idTime(id); ok && now.Sub(t) < orphanGrace {
				continue // may be a pin whose row is not written yet
			}
			var state string
			switch serr := q.db.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, id).Scan(&state); {
			case errors.Is(serr, sql.ErrNoRows):
			case serr != nil:
				continue
			case !strings.Contains(terminalStates, "'"+state+"'"):
				continue // a live job: its snapshot is needed
			}
			if uerr := q.pin.Unpin(ctx, dir, id); uerr != nil {
				failed++
				continue
			}
			removed++
		}
	}
	return removed, failed, errors.Join(errs...)
}

// idTime is the time inside a job id (the first ten characters after "j_" are the origin's clock in milliseconds), or false for an id
// that is not shaped like one.
func idTime(id string) (time.Time, bool) {
	if !strings.HasPrefix(id, "j_") || len(id) != 2+26 {
		return time.Time{}, false
	}
	var ms int64
	for _, ch := range id[2:12] {
		pos := strings.IndexRune(crockford, ch)
		if pos < 0 {
			return time.Time{}, false
		}
		ms = ms<<5 | int64(pos)
	}
	return time.UnixMilli(ms), true
}
