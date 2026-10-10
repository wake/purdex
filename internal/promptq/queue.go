// Package promptq is the daemon's side of sending a prompt (or an interrupt) to a Claude Code session through its Purdex
// mod, never by keystrokes (interface U3 plan D7). The App's request waits here; the mod fetches it on the mod socket
// (`/mod/v1/prompt/next`) and reports what Claude Code did (`/mod/v1/prompt/result`).
//
// The rules (D7, rev 5):
//   - One owner stream per session: the live mod stream that announced prompt.v1 most recently for the session's current
//     id. `Next` from any other stream gets nothing; a result from a stream that is no longer the owner is refused and
//     its request becomes `unknown`.
//   - At most once: a ledger keyed by client_msg_id (10 minutes). A request is handed to a mod once. A repeat of the same
//     client_msg_id answers the ledger and never queues a second time: it joins the first one's wait while that is open,
//     and only a request that was never handed (it timed out in the queue) or came back `busy` may be sent again.
//   - A request handed out whose result does not come within HandTimeout (or whose stream ends) is `unknown`.
package promptq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Key is the core service-registry key of the queue (the mod socket looks it up per request).
const Key = "prompt.queue"

// Kinds of request.
const (
	KindSubmit    = "submit"
	KindInterrupt = "interrupt"
)

// Statuses an App is answered, and a mod reports (accepted | dropped | busy).
const (
	Accepted = "accepted"
	Dropped  = "dropped"
	Busy     = "busy"
	Timeout  = "timeout" // nothing was handed out within the wait: nothing was sent, it may be sent again
	Unknown  = "unknown" // handed out, no result: it may or may not have run
	NoMod    = "no_mod"  // no owner stream (the caller answers 409)
)

// Timings; tests shorten them.
const (
	DefaultWait        = 10 * time.Second // how long an App's request waits in all
	DefaultHandTimeout = 10 * time.Second // how long a handed-out request may stay without a result
	LedgerTTL          = 10 * time.Minute
	maxQueued          = 16 // requests waiting per session
	maxLedger          = 4096
)

// Errors of the mod-facing calls.
var (
	ErrNotLeased = errors.New("promptq: the job is not leased to this stream")
	ErrNotOwner  = errors.New("promptq: the stream is no longer the owner of the session")
	ErrBusy      = errors.New("promptq: too many requests waiting for the session")
)

// Owners says which stream owns a session's prompts now ("" / false: none).
type Owners interface {
	OwnerOf(sessionID string) (stream string, ok bool)
}

// Job is what the mod fetches.
type Job struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
	Text      string `json:"text,omitempty"`
}

// Outcome is the mod's report.
type Outcome struct {
	Status string // accepted | dropped | busy
	Reason string // dropped: the mod's drop reason
}

// Result is what the App is answered.
type Result struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type state int

const (
	stQueued state = iota
	stHanded
	stDone
)

// entry is one request: a ledger row (submit) or a transient one (interrupt).
type entry struct {
	key        string // client_msg_id; "" for an interrupt
	job        Job
	state      state
	res        Result
	done       chan struct{} // closed when state becomes stDone
	stream     string        // the stream it was handed to
	queuedAt   time.Time
	handedAt   time.Time
	finishedAt time.Time
}

// Queue is the prompt queue.
type Queue struct {
	owners Owners
	// Wait and HandTimeout are the timings (zero: the defaults).
	Wait, HandTimeout time.Duration
	Now               func() time.Time

	mu     sync.Mutex
	queues map[string][]*entry // per session, waiting
	leases map[string]*entry   // job id -> entry handed out
	busy   map[string]*entry   // session -> the entry handed out (one at a time)
	ledger map[string]*entry   // client_msg_id -> entry (submits)
	wake   map[string]chan struct{}
}

// New returns a queue over the owners.
func New(o Owners) *Queue {
	return &Queue{owners: o, Now: time.Now, queues: map[string][]*entry{}, leases: map[string]*entry{},
		busy: map[string]*entry{}, ledger: map[string]*entry{}, wake: map[string]chan struct{}{}}
}

func (q *Queue) wait() time.Duration {
	if q.Wait > 0 {
		return q.Wait
	}
	return DefaultWait
}

func (q *Queue) handTimeout() time.Duration {
	if q.HandTimeout > 0 {
		return q.HandTimeout
	}
	return DefaultHandTimeout
}

// HasOwner: some live mod stream can take a prompt for the session now (the conversation's `send` capability).
func (q *Queue) HasOwner(sessionID string) bool {
	_, ok := q.owners.OwnerOf(sessionID)
	return ok
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "pj-" + hex.EncodeToString(b[:])
}

// signal wakes the session's pollers. q.mu held.
func (q *Queue) signal(sid string) {
	if c, ok := q.wake[sid]; ok {
		close(c)
	}
	q.wake[sid] = make(chan struct{})
}

func (q *Queue) waitCh(sid string) chan struct{} {
	c, ok := q.wake[sid]
	if !ok {
		c = make(chan struct{})
		q.wake[sid] = c
	}
	return c
}

// finish settles an entry. q.mu held; a settled entry is final.
func (q *Queue) finish(e *entry, res Result) {
	if e.state == stDone {
		return
	}
	e.state, e.res, e.finishedAt = stDone, res, q.Now()
	close(e.done)
	if q.leases[e.job.ID] == e {
		delete(q.leases, e.job.ID)
	}
	if q.busy[e.job.SessionID] == e {
		delete(q.busy, e.job.SessionID)
	}
	q.signal(e.job.SessionID)
}

// removeQueued takes an entry out of its session's waiting list. q.mu held.
func (q *Queue) removeQueued(e *entry) {
	l := q.queues[e.job.SessionID]
	for i, x := range l {
		if x == e {
			q.queues[e.job.SessionID] = append(l[:i:i], l[i+1:]...)
			return
		}
	}
}

// sweepLedger drops old settled rows. q.mu held.
func (q *Queue) sweepLedger() {
	now := q.Now()
	for k, e := range q.ledger {
		if e.state == stDone && now.Sub(e.finishedAt) > LedgerTTL {
			delete(q.ledger, k)
		}
	}
	// A flood of distinct ids: forget the OLDEST settled rows first; an open (queued or handed-out) row is never forgotten,
	// so a request that may still run cannot be sent a second time.
	for len(q.ledger) > maxLedger {
		var oldest string
		var at time.Time
		for k, e := range q.ledger {
			if e.state == stDone && (oldest == "" || e.finishedAt.Before(at)) {
				oldest, at = k, e.finishedAt
			}
		}
		if oldest == "" {
			return // nothing settled to forget: the open rows are bounded by the per-session queue
		}
		delete(q.ledger, oldest)
	}
}

// Submit queues a prompt for the session and waits for the mod's report (or the wait to end). clientMsgID identifies the
// request for the at-most-once ledger. The Result's status is accepted | dropped | busy | timeout | unknown | no_mod.
func (q *Queue) Submit(ctx context.Context, sessionID, clientMsgID, text string) (Result, error) {
	q.mu.Lock()
	q.sweepLedger()
	if e, ok := q.ledger[clientMsgID]; ok && clientMsgID != "" {
		switch {
		case e.job.SessionID != sessionID || e.job.Text != text:
			q.mu.Unlock() // the same id for a different request is a client bug, not a repeat
			return Result{Status: Dropped, Reason: "client_msg_id_reused"}, nil
		case e.state == stDone && e.res.Status == Busy:
			delete(q.ledger, clientMsgID) // busy never ran: it may be sent again below
		default:
			q.mu.Unlock()
			return q.await(ctx, e), nil // joins the first request's wait, or answers the settled ledger row
		}
	}
	if _, ok := q.owners.OwnerOf(sessionID); !ok {
		q.mu.Unlock()
		return Result{Status: NoMod}, nil
	}
	if len(q.queues[sessionID]) >= maxQueued {
		q.mu.Unlock()
		return Result{}, ErrBusy
	}
	e := &entry{key: clientMsgID, job: Job{ID: newID(), Kind: KindSubmit, SessionID: sessionID, Text: text}, done: make(chan struct{}), queuedAt: q.Now()}
	if clientMsgID != "" {
		q.ledger[clientMsgID] = e
	}
	q.queues[sessionID] = append(q.queues[sessionID], e)
	q.signal(sessionID)
	q.mu.Unlock()
	return q.await(ctx, e), nil
}

// Interrupt asks the mod to abort the session's running turn; there is no ledger (an abort twice is harmless).
func (q *Queue) Interrupt(ctx context.Context, sessionID string) (Result, error) {
	q.mu.Lock()
	if _, ok := q.owners.OwnerOf(sessionID); !ok {
		q.mu.Unlock()
		return Result{Status: NoMod}, nil
	}
	if len(q.queues[sessionID]) >= maxQueued {
		q.mu.Unlock()
		return Result{}, ErrBusy
	}
	e := &entry{job: Job{ID: newID(), Kind: KindInterrupt, SessionID: sessionID}, done: make(chan struct{}), queuedAt: q.Now()}
	q.queues[sessionID] = append(q.queues[sessionID], e)
	q.signal(sessionID)
	q.mu.Unlock()
	return q.await(ctx, e), nil
}

// await waits for the entry to settle: until the wait ends (a request never handed out is withdrawn and answers
// `timeout`; one handed out and unanswered is `unknown`), the hand timeout after hand-out, or the caller's context.
func (q *Queue) await(ctx context.Context, e *entry) Result {
	deadline := time.NewTimer(q.wait())
	defer deadline.Stop()
	for {
		q.mu.Lock()
		if e.state == stDone {
			r := e.res
			q.mu.Unlock()
			return r
		}
		var hand <-chan time.Time
		var handTimer *time.Timer
		wake := q.waitCh(e.job.SessionID)
		if e.state == stHanded {
			left := q.handTimeout() - q.Now().Sub(e.handedAt)
			if left < 0 {
				left = 0
			}
			handTimer = time.NewTimer(left)
			hand = handTimer.C
		}
		q.mu.Unlock()
		select {
		case <-e.done:
		case <-wake: // the queue moved (this one may have been handed out): look again
		case <-hand:
			q.mu.Lock()
			if e.state == stHanded {
				q.finish(e, Result{Status: Unknown, Reason: "no_result"})
			}
			q.mu.Unlock()
		case <-deadline.C:
			q.mu.Lock()
			switch e.state {
			case stQueued: // never handed out: withdrawn, so it may be sent again
				q.removeQueued(e)
				if e.key != "" {
					delete(q.ledger, e.key)
				}
				q.finish(e, Result{Status: Timeout})
			case stHanded:
				q.finish(e, Result{Status: Unknown, Reason: "no_result"})
			}
			r := e.res
			q.mu.Unlock()
			if handTimer != nil {
				handTimer.Stop()
			}
			return r
		case <-ctx.Done():
			if handTimer != nil {
				handTimer.Stop()
			}
			// the caller went away: the request goes on (a handed-out one has run or will), the ledger keeps it
			return Result{Status: Unknown, Reason: "client_gone"}
		}
		if handTimer != nil {
			handTimer.Stop()
		}
	}
}

// Next hands the session's next request to the stream if it is the session's owner; it waits up to wait for one. A
// session has one request out at a time, so the order is the order they were made in.
func (q *Queue) Next(ctx context.Context, stream, sessionID string, wait time.Duration) (Job, bool) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		q.mu.Lock()
		// The owner is read inside the critical section that creates the lease, so a request is never leased on the word of
		// an owner read before another poll or an expiry changed the queue. (The registry itself can still move on right
		// after: the mod re-reads its session id before it submits, and the result's owner re-check settles the rest.)
		owner, ok := q.owners.OwnerOf(sessionID)
		// A handed-out request whose result never came is unknown after HandTimeout whether or not its caller is still
		// waiting (a caller that went away leaves nobody to enforce it): the session must not stay blocked for good.
		if b := q.busy[sessionID]; b != nil && q.Now().Sub(b.handedAt) >= q.handTimeout() {
			q.finish(b, Result{Status: Unknown, Reason: "no_result"})
		}
		if ok && owner == stream && q.busy[sessionID] == nil {
			// A request nobody fetched within the wait is stale whether or not its caller is still there (a caller that
			// went away leaves it queued): a prompt must never be typed minutes after it was asked for.
			for l := q.queues[sessionID]; len(l) > 0 && q.Now().Sub(l[0].queuedAt) > q.wait(); l = q.queues[sessionID] {
				q.queues[sessionID] = l[1:]
				if l[0].key != "" {
					delete(q.ledger, l[0].key)
				}
				q.finish(l[0], Result{Status: Timeout})
			}
			if l := q.queues[sessionID]; len(l) > 0 {
				e := l[0]
				q.queues[sessionID] = l[1:]
				e.state, e.stream, e.handedAt = stHanded, stream, q.Now()
				q.leases[e.job.ID] = e
				q.busy[sessionID] = e
				q.signal(sessionID) // the waiter re-arms its hand timer
				job := e.job
				q.mu.Unlock()
				return job, true
			}
		}
		ch := q.waitCh(sessionID)
		// a poll that sits through a lease's expiry must wake for it, or the request behind waits out the whole poll
		var expire <-chan time.Time
		var expireTimer *time.Timer
		if b := q.busy[sessionID]; b != nil {
			left := q.handTimeout() - q.Now().Sub(b.handedAt)
			if left < 0 {
				left = 0
			}
			expireTimer = time.NewTimer(left)
			expire = expireTimer.C
		}
		q.mu.Unlock()
		if wait <= 0 {
			if expireTimer != nil {
				expireTimer.Stop()
			}
			return Job{}, false
		}
		select {
		case <-ch:
		case <-expire:
		case <-timer.C:
			if expireTimer != nil {
				expireTimer.Stop()
			}
			return Job{}, false
		case <-ctx.Done():
			if expireTimer != nil {
				expireTimer.Stop()
			}
			return Job{}, false
		}
		if expireTimer != nil {
			expireTimer.Stop()
		}
	}
}

// Result reports what the mod did with a job. The stream must be the one the job was handed to AND still the session's
// owner, else ErrNotLeased / ErrNotOwner (the request, if still open, becomes unknown).
func (q *Queue) Result(stream, jobID string, o Outcome) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.leases[jobID]
	if e == nil || e.stream != stream || e.state != stHanded {
		return ErrNotLeased
	}
	if owner, ok := q.owners.OwnerOf(e.job.SessionID); !ok || owner != stream {
		q.finish(e, Result{Status: Unknown, Reason: "not_owner"})
		return ErrNotOwner
	}
	switch o.Status {
	case Accepted, Busy:
		q.finish(e, Result{Status: o.Status})
	default:
		q.finish(e, Result{Status: Dropped, Reason: o.Reason})
	}
	return nil
}
