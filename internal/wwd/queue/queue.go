// Package queue is the origin side of wwd's job queue (spec §4): the jobs table in wwd.db and the state machine over it. This file
// is the type and its options; submit.go, read.go and the files after them are the operations. Every state change is one
// BEGIN IMMEDIATE transaction on the database, so the first writer wins and two origins' worth of callers never interleave.
package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrNotFound: no job has that id.
var ErrNotFound = errors.New("queue: no such job")

// ErrSnapshotRefused: the submission's snapshot cannot be pinned (the git directory is not one, or does not hold the commit). A job whose
// snapshot cannot be fetched must not be queued, so the submission is refused; the client reports it (pdx test exit 4).
var ErrSnapshotRefused = errors.New("queue: the snapshot cannot be pinned")

// Pinner keeps a job's snapshot reachable: the temporary-index commit a snapshot makes has no ref, so gc would drop it. The ref is
// refs/wwd/<job id> in the repo's common git directory (spec 4.1). The implementation runs git on this host, through the clean
// git environment (snapshot.Pin, Unpin and ListPins satisfy it).
type Pinner interface {
	// Pin points refs/wwd/<jobID> at commit; idempotent for the same commit.
	Pin(ctx context.Context, gitDir, jobID, commit string) error
	// Unpin removes that ref; a ref that is not there is not an error.
	Unpin(ctx context.Context, gitDir, jobID string) error
	// ListPins returns the job ids of every ref under refs/wwd/ (and nothing else).
	ListPins(ctx context.Context, gitDir string) ([]string, error)
}

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
	// Pinner pins the snapshots (see Pinner). Nil: nothing is pinned, which is for tests and for a host with no git.
	Pinner Pinner
	// OnSucceeded is told, after the end is committed, of each job a worker ended with a reusable success (see reusable), with what the
	// claim that ended it said about itself. It runs on the caller's goroutine and must be quick; it cannot fail the result. Optional.
	OnSucceeded func(ctx context.Context, j wire.Job, p Produced)
	// LeaseS and RenewS are the lease a claim and each heartbeat hand out, and how often the worker should renew (default 60 and 15).
	// Tests shorten them.
	LeaseS, RenewS int
}

// Queue is the job queue of one origin.
type Queue struct {
	db   *sql.DB
	opt  Options
	now  func() time.Time
	pub  func(wire.JobEvent)
	newI func() string
	seq  *sequencer
	pin  Pinner

	leaseS, renewS int
	maxLog         int64      // the most one uploaded log may be
	epoch          string     // the database's name (db_epoch), read once: it never changes
	logMu          sync.Mutex // an upload's putting its file in place and recording it, against the sweep removing files

	// afterCommit is a test hook: it runs between a transaction's commit and the publishing of its events.
	afterCommit func()
	// beforeLogCommit is a test hook: it runs after a log body has been received and before the transaction that puts it in place.
	beforeLogCommit func()
	// afterLogForget is a test hook: it runs after the sweep has committed the forgetting of old logs and before it removes their files.
	afterLogForget func()
	// inLogRemoval is a test hook: it runs while the sweep holds logMu, after it has decided which files to remove and before it removes them.
	inLogRemoval func()
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
	q := &Queue{db: db, opt: o, now: o.Now, pub: o.Publish, newI: o.NewID, seq: newSequencer(), pin: o.Pinner, leaseS: o.LeaseS, renewS: o.RenewS, maxLog: defaultMaxLog}
	if q.now == nil {
		q.now = time.Now
	}
	if q.newI == nil {
		q.newI = func() string { return newJobID(q.now()) }
	}
	if q.leaseS == 0 {
		q.leaseS = defaultLeaseS
	}
	if q.renewS == 0 {
		q.renewS = defaultRenewS
	}
	if q.leaseS < 0 || q.renewS < 0 || q.renewS >= q.leaseS {
		return nil, fmt.Errorf("queue: Options.RenewS (%d) must be positive and shorter than LeaseS (%d)", q.renewS, q.leaseS)
	}
	if o.LogDir != "" {
		// the log operations write into it; nothing else makes it, and an upload into a directory that is not there is a 500
		if err := os.MkdirAll(o.LogDir, 0o700); err != nil {
			return nil, fmt.Errorf("queue: Options.LogDir: %w", err)
		}
		// logs are written and removed by name inside it: a link standing in for the directory would send them elsewhere
		if fi, err := os.Lstat(o.LogDir); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("queue: Options.LogDir %s is not a directory of its own (a link, or not a directory): %v", o.LogDir, err)
		} else if fi.Mode().Perm()&0o077 != 0 {
			// MkdirAll's mode only applies to what it makes: a directory that was there is closed to group and others too, because
			// job logs hold command output
			if err := os.Chmod(o.LogDir, 0o700); err != nil {
				return nil, fmt.Errorf("queue: Options.LogDir %s is open to group or others and cannot be closed: %w", o.LogDir, err)
			}
		}
	}
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM db_epoch`).Scan(&q.epoch); err != nil {
		return nil, fmt.Errorf("queue: the database has no epoch (is it migrated?): %w", err)
	}
	// a restart gives the jobs workers still hold a full lease before the first sweep can lapse them (spec 4.2)
	if err := extendHeldLeases(context.Background(), db, q.now().UnixMilli(), q.leaseS); err != nil {
		return nil, fmt.Errorf("queue: restart lease: %w", err)
	}
	return q, nil
}

// write runs fn in one immediate transaction on its own connection, then tells Publish about the jobs fn reported as changed (after
// the commit, so no subscriber sees a state that was rolled back). Each event is the job as this change left it, read inside the
// transaction, so a later transaction that changes the same job cannot turn this event into its own; and events leave in commit
// order, one transaction's after the previous one's (see sequencer).
func (q *Queue) write(ctx context.Context, fn func(c *sql.Conn, changed *[]string) error) error {
	if err := q.writeOnly(ctx, fn); err != nil {
		return err
	}
	// the snapshots of jobs this change ended are released after the events went out, so git never holds up the next writer's turn
	q.settlePins(ctx)
	return nil
}

// writeOnly is write without the release of snapshots: the sweep does that itself, to count it.
func (q *Queue) writeOnly(ctx context.Context, fn func(c *sql.Conn, changed *[]string) error) error {
	events, ticket, err := q.commit(ctx, fn)
	if err != nil {
		return err
	}
	if q.afterCommit != nil {
		q.afterCommit()
	}
	q.announce(events, ticket)
	return nil
}

// announce publishes events in commit order and passes the turn on, whatever the subscriber does.
func (q *Queue) announce(events []wire.JobEvent, ticket uint64) {
	q.seq.await(ticket)
	defer q.seq.done(ticket)
	q.publish(events)
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
