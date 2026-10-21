package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// QueuedJob is what the alert rules of the queue (WWD-9 S3-3b) read of one queued job. A queued job's wire form carries no claim, so how many
// times it was put back, and when, come from the table.
type QueuedJob struct {
	ID     string
	Labels []string
	// CreatedAt is when the job was submitted (the origin's clock). It is what a wait is counted from; UpdatedAt is moved by a reorder or a
	// cancel request too, so it stands for "when it was last put back" only for a job that was put back (Attempts > 0).
	CreatedAt time.Time
	UpdatedAt time.Time
	// Attempts is how many times the job was put back in the queue and counted: a release for an infrastructure reason, and every lease that
	// ran out (a lapse is also an attempt, letGo is told to count it). A reclaim (the owner of the machine wants it back) is not counted.
	Attempts int
	// Held says a person put the job on hold: it stays queued and no worker takes it.
	Held bool
}

// QueuedView is the jobs that are queued now and that a worker of another host could take, oldest first. A fast-lane job and an ad-hoc
// command run on the host that submitted them and are not offered to the pool, so they are not here: a wait of theirs is no fault of the pool.
// It reads the table, so the counts survive a restart of wwd.
func (q *Queue) QueuedView(ctx context.Context) ([]QueuedJob, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id, labels, created_at, updated_at, attempts, manual FROM jobs
		WHERE state = ? AND adhoc = 0 AND local = 0 ORDER BY seq`, string(wire.StateQueued))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedJob{}
	for rows.Next() {
		var j QueuedJob
		var labels, manual string
		var created, updated int64
		if err := rows.Scan(&j.ID, &labels, &created, &updated, &j.Attempts, &manual); err != nil {
			return nil, err
		}
		if labels != "" {
			if err := json.Unmarshal([]byte(labels), &j.Labels); err != nil {
				return nil, fmt.Errorf("queue: the labels of job %s do not read: %w", j.ID, err)
			}
		}
		j.CreatedAt, j.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
		j.Held = wire.Position(manual) == wire.PositionHold
		out = append(out, j)
	}
	return out, rows.Err()
}
