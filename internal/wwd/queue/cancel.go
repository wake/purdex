package queue

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Cancel ends a job the person (or the App) asked to stop (spec 4.2, 4.4). A queued job is cancelled at once: nothing ran, so it has
// no exit. A claimed or running one belongs to its worker: the origin only marks cancel_requested, the next heartbeat answers
// cancel: true, and the worker ends the command and posts its own result, so the state stays until then. Asking again changes
// nothing. A job that has ended is a conflict with its state, and an unknown id is not found. The job as it is afterwards is returned.
func (q *Queue) Cancel(ctx context.Context, id string) (wire.Job, error) {
	return q.cancel(ctx, id, false)
}

// CancelForPeers is Cancel for a request from another host: an ad-hoc or fast-lane job is not found, as in every peer read.
func (q *Queue) CancelForPeers(ctx context.Context, id string) (wire.Job, error) {
	return q.cancel(ctx, id, true)
}

func (q *Queue) cancel(ctx context.Context, id string, forPeers bool) (wire.Job, error) {
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		var state string
		var requested int
		query := `SELECT state, cancel_requested FROM jobs WHERE id = ?`
		if forPeers {
			query += ` AND adhoc = 0 AND local = 0`
		}
		switch err := c.QueryRowContext(ctx, query, id).Scan(&state, &requested); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		}
		now := q.now().UnixMilli()
		switch s := wire.JobState(state); s {
		case wire.StateQueued:
			if err := finishWithoutRunning(ctx, c, id, wire.StateCancelled, "cancelled", nil, now); err != nil {
				return err
			}
		case wire.StateClaimed, wire.StateRunning:
			if requested == 1 {
				return nil // already asked: nothing changes, nothing is announced
			}
			if _, err := c.ExecContext(ctx, `UPDATE jobs SET cancel_requested = 1, updated_at = ? WHERE id = ?`, now, id); err != nil {
				return err
			}
		default:
			return &ConflictError{State: s}
		}
		*changed = append(*changed, id)
		return nil
	})
	if err != nil {
		return wire.Job{}, err
	}
	return getOn(ctx, q.db, id, forPeers)
}
