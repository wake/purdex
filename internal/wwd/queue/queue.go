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
	seq  *sequencer

	// afterCommit is a test hook: it runs between a transaction's commit and the publishing of its events.
	afterCommit func()
	// afterTicket is a test hook: it runs inside the transaction right after the ticket was taken, before COMMIT.
	afterTicket func()
}

// New returns the queue over db, whose tables Migrate has created.
func New(db *sql.DB, o Options) (*Queue, error) {
	if db == nil {
		return nil, errors.New("queue: nil database")
	}
	if o.Origin == "" {
		return nil, errors.New("queue: Options.Origin is required")
	}
	q := &Queue{db: db, opt: o, now: o.Now, pub: o.Publish, newI: o.NewID, seq: newSequencer()}
	if q.now == nil {
		q.now = time.Now
	}
	if q.newI == nil {
		q.newI = func() string { return newJobID(q.now()) }
	}
	return q, nil
}

// write runs fn in one immediate transaction on its own connection, then tells Publish about the jobs fn reported as changed (after
// the commit, so no subscriber sees a state that was rolled back). Each event is the job as this change left it, read inside the
// transaction, so a later transaction that changes the same job cannot turn this event into its own; and events leave in commit
// order, one transaction's after the previous one's (see sequencer).
func (q *Queue) write(ctx context.Context, fn func(c *sql.Conn, changed *[]string) error) error {
	events, ticket, err := q.commit(ctx, fn)
	if err != nil {
		return err
	}
	if q.afterCommit != nil {
		q.afterCommit()
	}
	q.seq.await(ticket)
	defer q.seq.done(ticket)
	q.publish(events)
	return nil
}

// commit is the transaction alone: it gives its connection back before it returns, so whatever a subscriber does next can have a
// connection even when the database was opened with one (SetMaxOpenConns(1)). It returns the events and the ticket that orders
// them; when the commit fails after the ticket was taken, the ticket is passed on here, so nobody waits for it.
func (q *Queue) commit(ctx context.Context, fn func(c *sql.Conn, changed *[]string) error) (events []wire.JobEvent, ticket uint64, err error) {
	c, err := q.db.Conn(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close()
	taken := false
	err = inImmediateTx(ctx, c, func() error {
		var changed []string
		if err := fn(c, &changed); err != nil {
			return err
		}
		for _, id := range changed {
			j, err := getOn(ctx, c, id, false)
			if err != nil {
				return err
			}
			events = append(events, wire.JobEvent{Host: q.opt.Origin, Job: j})
		}
		ticket, taken = q.seq.ticket(), true
		if q.afterTicket != nil {
			q.afterTicket()
		}
		return nil
	})
	if err != nil {
		if taken {
			q.seq.await(ticket)
			q.seq.done(ticket)
		}
		return nil, 0, err
	}
	return events, ticket, nil
}

// publish announces committed changes. The change is durable already and the payload is in hand, so neither the request's
// cancellation nor a subscriber's failure may turn it into a failed request (the client would submit the job again): a panic in
// Publish is caught. Publish must not block (wwd's hub buffers) and must not call a queue method that writes: the queue does not wait
// on a slow subscriber on purpose, because it cannot tell it from a stuck one.
func (q *Queue) publish(events []wire.JobEvent) {
	if q.pub == nil {
		return
	}
	for _, ev := range events {
		q.deliver(ev)
	}
}

func (q *Queue) deliver(ev wire.JobEvent) {
	defer func() { _ = recover() }()
	q.pub(ev)
}
