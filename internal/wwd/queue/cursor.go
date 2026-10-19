package queue

import (
	"context"
	"fmt"
	"strconv"

	"github.com/wake/purdex/internal/wwd/wire"
)

// CursorString is the wire form of a cursor (JobsListResponse.Cursor, ?after=).
func CursorString(n int64) string { return strconv.FormatInt(n, 10) }

// ParseCursor reads the ?after= of a request. Empty reads from the start. Anything that is not a cursor this queue made is an error: the
// caller answers 400.
func ParseCursor(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if !wire.ValidCursor(s) {
		return 0, fmt.Errorf("queue: %q is not a cursor", s)
	}
	return strconv.ParseInt(s, 10, 64)
}

// Epoch names the database this queue is over (wire.JobsListResponse.Epoch): made with its tables and never changed, so a cursor is
// only meaningful under the epoch it was made in.
func (q *Queue) Epoch(context.Context) (string, error) { return q.epoch, nil }

// Head is the cursor at this moment: the number of the last change. A client that wants only what happens from now on starts from it.
func (q *Queue) Head(ctx context.Context) (int64, error) {
	var n int64
	err := q.db.QueryRowContext(ctx, `SELECT n FROM job_rev`).Scan(&n)
	return n, err
}

// ListAfter returns the jobs that changed after the cursor, each once and in the state it is in now, oldest change first (spec 4.4). f
// narrows them as it does for List (peers' view, states, submitter, limit). The second value is the cursor to ask with next: the number of
// the last job returned, or after itself when nothing came back, so a read that finds nothing does not move. The numbers are the origin's
// own, in the order of its commits: the clock of the worker that ran a job takes no part, so a worker that is slow or wrong cannot make a
// change fall behind the cursor.
func (q *Queue) ListAfter(ctx context.Context, after int64, f Filter) ([]wire.Job, int64, error) {
	jobs, revs, err := q.list(ctx, f, &after)
	if err != nil {
		return nil, 0, err
	}
	if len(revs) > 0 {
		after = revs[len(revs)-1]
	}
	return jobs, after, nil
}
