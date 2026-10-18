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
	// orphanGrace: a pin recorded less than this long ago is never called an orphan, because Submit records the pin moments before it
	// writes the row, and the sweep must not take the pin from a job that is about to exist.
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
		_, _ = q.db.ExecContext(ctx, `DELETE FROM git_pins WHERE id = ?`, h.id)
	}
	return unpinned, failed
}

// removeOrphanPins releases the pins this queue recorded for which no live job exists: no row (a submission that failed after pinning,
// or died between the two), or an ended job whose release had not gone through. Only ids in git_pins are considered, so a ref that
// some other wwd, or a person, keeps under refs/wwd/ in a shared repository is never touched. A record younger than the grace is left
// alone (its row may not be written yet); one whose git directory is gone is dropped without asking git.
func (q *Queue) removeOrphanPins(ctx context.Context, now time.Time) (removed, failed int, err error) {
	if q.pin == nil {
		return 0, 0, nil
	}
	ctx = context.WithoutCancel(ctx)
	rows, qerr := q.db.QueryContext(ctx, `SELECT id, git_dir, at FROM git_pins ORDER BY at`)
	if qerr != nil {
		return 0, 0, qerr
	}
	type rec struct {
		id, dir string
		at      int64
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if rows.Scan(&r.id, &r.dir, &r.at) == nil {
			recs = append(recs, r)
		}
	}
	rows.Close()

	forget := func(id string) { _, _ = q.db.ExecContext(ctx, `DELETE FROM git_pins WHERE id = ?`, id) }
	for _, r := range recs {
		if now.Sub(time.UnixMilli(r.at)) < orphanGrace {
			continue
		}
		var state string
		switch serr := q.db.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, r.id).Scan(&state); {
		case errors.Is(serr, sql.ErrNoRows):
			// no job: the pin is an orphan
		case serr != nil:
			continue
		case !strings.Contains(terminalStates, "'"+state+"'"):
			continue // a live job: its snapshot is needed
		}
		if _, serr := os.Stat(r.dir); errors.Is(serr, os.ErrNotExist) {
			forget(r.id)
			continue
		}
		if uerr := q.pin.Unpin(ctx, r.dir, r.id); uerr != nil {
			failed++
			continue
		}
		forget(r.id)
		removed++
	}
	return removed, failed, nil
}
