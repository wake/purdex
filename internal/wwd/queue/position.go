package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrInvalidPosition is a position a request may not ask for.
var ErrInvalidPosition = errors.New("queue: not a position (front, back, hold or normal)")

// SetPosition moves a queued job in the queue by hand (spec 4.6, "Manual order"): to the front (before every scored job, and before the
// jobs put in front earlier), to the back (after every scored job and the ones put back earlier), on hold (no worker is handed it) or
// to normal (the score decides again). Only a queued job has a place in the queue: any other state is a conflict naming it, and an
// unknown id is not found. Asking for the place a job already has changes nothing and announces nothing; a change moves the job's rev
// and is announced like every change to a job. The job as it is afterwards is returned.
func (q *Queue) SetPosition(ctx context.Context, id string, p wire.Position) (wire.Job, error) {
	if !p.ValidRequest() {
		return wire.Job{}, fmt.Errorf("%w: %q", ErrInvalidPosition, p)
	}
	err := q.write(ctx, func(c *sql.Conn, changed *[]string) error {
		var state, manual string
		var seq int64
		switch err := c.QueryRowContext(ctx, `SELECT state, manual, manual_seq FROM jobs WHERE id = ?`, id).Scan(&state, &manual, &seq); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		}
		if s := wire.JobState(state); s != wire.StateQueued {
			return &ConflictError{State: s}
		}
		newManual, newSeq := string(p), int64(0)
		switch p {
		case wire.PositionNormal:
			newManual = ""
		case wire.PositionFront, wire.PositionBack:
			// the extreme among the other jobs that hold the same kind of place; a job that already is the extreme stays where it is
			agg, step := "min", int64(-1)
			if p == wire.PositionBack {
				agg, step = "max", 1
			}
			var other sql.NullInt64
			if err := c.QueryRowContext(ctx, `SELECT `+agg+`(manual_seq) FROM jobs WHERE manual = ? AND id != ? AND state = 'queued'`, newManual, id).Scan(&other); err != nil {
				return err
			}
			if manual == newManual && (!other.Valid || (p == wire.PositionFront && seq < other.Int64) || (p == wire.PositionBack && seq > other.Int64)) {
				return nil
			}
			newSeq = other.Int64 + step
		}
		if manual == newManual && (p == wire.PositionNormal || p == wire.PositionHold) {
			return nil
		}
		if _, err := c.ExecContext(ctx, `UPDATE jobs SET manual = ?, manual_seq = ?, updated_at = ? WHERE id = ?`, newManual, newSeq, q.now().UnixMilli(), id); err != nil {
			return err
		}
		*changed = append(*changed, id)
		return nil
	})
	if err != nil {
		return wire.Job{}, err
	}
	return getOn(ctx, q.db, id, false)
}
