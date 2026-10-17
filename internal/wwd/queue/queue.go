// Package queue is the origin side of wwd's job queue (spec §4): the jobs table in wwd.db and the state machine over it. This file
// is the type and its options; submit.go, read.go and the files after them are the operations. Every state change is one
// BEGIN IMMEDIATE transaction on the database, so the first writer wins and two origins' worth of callers never interleave.
package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrNotFound: no job has that id.
var ErrNotFound = errors.New("queue: no such job")

// ConflictError is a refused transition: the job is in a state that does not allow it (the wire's 409 {state}).
type ConflictError struct{ State wire.JobState }

func (e *ConflictError) Error() string { return fmt.Sprintf("queue: conflict: the job is %s", e.State) }

// Options configures a Queue.
type Options struct {
	// Origin is this host's alias: it is on every job this queue creates and on every event it publishes.
	Origin string
	// Now is the origin's clock (default time.Now). Lease deadlines, expiry and retention all read it, never a worker's clock.
	Now func() time.Time
	// Publish is told once per changed job, after the change is committed (wwd turns it into a job event). Optional.
	Publish func(wire.JobEvent)
	// LogDir is where job logs are kept (one file per job). Used by the log operations.
	LogDir string
	// NewID makes a job id (default: a ULID from Now). Tests set it.
	NewID func() string
}

// Queue is the job queue of one origin.
type Queue struct {
	db   *sql.DB
	opt  Options
	now  func() time.Time
	pub  func(wire.JobEvent)
	newI func() string
}

// New returns the queue over db, whose tables Migrate has created.
func New(db *sql.DB, o Options) (*Queue, error) {
	if db == nil {
		return nil, errors.New("queue: nil database")
	}
	if o.Origin == "" {
		return nil, errors.New("queue: Options.Origin is required")
	}
	q := &Queue{db: db, opt: o, now: o.Now, pub: o.Publish, newI: o.NewID}
	if q.now == nil {
		q.now = time.Now
	}
	if q.newI == nil {
		q.newI = func() string { return newJobID(q.now()) }
	}
	return q, nil
}

// write runs fn in one immediate transaction on its own connection, then tells Publish about the jobs fn reported as changed (after
// the commit, so no subscriber sees a state that was rolled back).
func (q *Queue) write(ctx context.Context, fn func(c *sql.Conn, changed *[]string) error) error {
	changed, err := q.commit(ctx, fn)
	if err != nil {
		return err
	}
	q.publish(ctx, changed)
	return nil
}

// commit is the transaction alone: it gives its connection back before it returns, so the event reads that follow can have a
// connection even when the database was opened with one (SetMaxOpenConns(1)).
func (q *Queue) commit(ctx context.Context, fn func(c *sql.Conn, changed *[]string) error) ([]string, error) {
	c, err := q.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var changed []string
	if err := inImmediateTx(ctx, c, func() error { return fn(c, &changed) }); err != nil {
		return nil, err
	}
	return changed, nil
}

// publish announces committed changes. The change is durable already, so the request's own cancellation (a client that hung up
// after the commit) must not lose the event: the reads ignore the caller's context.
func (q *Queue) publish(ctx context.Context, ids []string) {
	if q.pub == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, id := range ids {
		if j, err := q.Get(ctx, id); err == nil {
			q.pub(wire.JobEvent{Host: q.opt.Origin, Job: j})
		}
	}
}
