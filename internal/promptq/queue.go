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
	"strings"
	"sync"
	"time"
)

// Key is the core service-registry key of the queue (the mod socket looks it up per request).
const Key = "prompt.queue"

// Kinds of request.
const (
	KindSubmit    = "submit"
	KindInterrupt = "interrupt"
	KindFill      = "fill"     // type text into the empty composer without pressing Enter (K-1)
	KindClearIf   = "clear_if" // clear the composer only if it still holds exactly the job's text
	KindRead      = "read"     // report the composer's text
)

// Wire format (daemon <-> mod). prompt/next answers {"job":{"id","kind","session_id","text"?}} with kind submit |
// interrupt | fill | clear_if | read (text: submit, fill, clear_if). The mod reports with prompt/result
// {"stream","job_id","status":"accepted|dropped|busy|unknown","reason"?,"filled_at"?,"text"?}: filled_at (unix ms, read from the
// mod's clock right after a successful fill) on an accepted fill; text on an accepted read. Drop reasons of a fill:
// session_changed, input_not_empty, dialog_open, no_composer, refused, fill_mismatch; of a clear_if: not_ours.
// A fill result is posted once by the mod (no retries): a lost report is `unknown` here.

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
	maxOpenTyped       = 256 // typed rows in filled / entering / unknown: never forgotten, so bounded by count
	// A typed row still open after OpenTypedMaxAge (or an unknown one whose Enter may have gone) is settled by the sweep;
	// its id is then kept as a tombstone (at most maxTombstones, TombstoneTTL) so a replay never types again.
	OpenTypedMaxAge = 10 * time.Minute
	maxTombstones   = 4096
	TombstoneTTL    = 24 * time.Hour
	// MaxTextBytes is the largest text a fill / clear_if carries and a read reports, in UTF-8 BYTES. The mod parses jobs
	// with the same limit (workbook.js MAX_TYPED_TEXT_BYTES): its limit must never be smaller than this one.
	MaxTextBytes = 16 << 10
)

// Errors of the mod-facing calls.
var (
	ErrBadTransition = errors.New("promptq: the ledger row is not in a state that allows this step")
	ErrNotLeased     = errors.New("promptq: the job is not leased to this stream")
	ErrNotOwner      = errors.New("promptq: the stream is no longer the owner of the session")
	ErrBusy          = errors.New("promptq: too many requests waiting for the session")
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
	Status   string // accepted | dropped | busy
	Reason   string // dropped: the mod's drop reason
	FilledAt int64  // accepted fill: unix ms, from the mod's clock (clamped, never trusted: see Result.ReceivedAt)
	Text     string // accepted read: the composer's text
	// Truncated: accepted read whose text was cut to MaxTextBytes by the mod.
	Truncated bool
}

// Result is what the App is answered.
type Result struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Phase is the ledger phase of a typed submit (fill) at the time of the answer; "" for any other kind.
	Phase string `json:"phase,omitempty"`
	// FilledAt is when the fill happened (unix ms) clamped to [handed out, ReceivedAt]; the mod's clock is not trusted.
	FilledAt int64 `json:"filled_at,omitempty"`
	// ReceivedAt is the DAEMON's time (unix ms) when the mod's accepted fill result arrived: the freshness check of the
	// Enter (1.5 s) counts from here.
	ReceivedAt int64  `json:"received_at,omitempty"`
	Text       string `json:"text,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"` // read: Text was cut to MaxTextBytes
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
	phase      string        // fill only
	enterMark  bool          // fill only: MarkEntering was written, so the text may have been sent
	settled    bool          // fill only: an open row the sweep gave up on (phase unknown); no longer counted as open
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
	// seen: when a mod owner was last seen per session (HasOwner, NoOwnerFor, Next); started: the queue's start, the first
	// sighting of every session (NoOwnerFor).
	seen    map[string]time.Time
	started time.Time
	tombs   map[string]time.Time // client_msg_id -> when its settled typed row was evicted
}

// New returns a queue over the owners.
func New(o Owners) *Queue {
	return &Queue{owners: o, Now: time.Now, queues: map[string][]*entry{}, leases: map[string]*entry{},
		busy: map[string]*entry{}, ledger: map[string]*entry{}, wake: map[string]chan struct{}{},
		seen: map[string]time.Time{}, started: time.Now(), tombs: map[string]time.Time{}}
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
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.owners.OwnerOf(sessionID)
	if ok {
		q.seen[sessionID] = q.Now()
	}
	return ok
}

// NoOwnerFor is 0 while a mod owns the session, else how long it has been since one was last seen (the queue's start counts
// as the first sighting, so a daemon that just started does not call a session ownerless before its mod could connect).
func (q *Queue) NoOwnerFor(sessionID string) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.Now()
	if _, ok := q.owners.OwnerOf(sessionID); ok {
		q.seen[sessionID] = now
		return 0
	}
	at := q.seen[sessionID]
	if at.Before(q.started) {
		at = q.started
	}
	return now.Sub(at)
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
	if e.job.Kind == KindFill {
		switch res.Status {
		case Accepted:
			e.phase = PhaseFilled
		case Unknown:
			e.phase = PhaseUnknown
		default: // dropped | busy | timeout: nothing was left in the composer
			e.phase = PhaseCleared
		}
	}
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
	for _, e := range q.ledger {
		if e.job.Kind == KindFill && e.state == stDone && openPhase(e.phase) && !e.settled &&
			((e.phase == PhaseUnknown && e.enterMark) || now.Sub(e.finishedAt) > OpenTypedMaxAge) {
			e.phase, e.settled = PhaseUnknown, true // it may or may not have been sent; nothing more will be done for it
		}
	}
	for k, e := range q.ledger {
		if forgettable(e) && now.Sub(e.finishedAt) > LedgerTTL {
			q.evict(k, e)
		}
	}
	for k, at := range q.tombs {
		if now.Sub(at) > TombstoneTTL {
			delete(q.tombs, k)
		}
	}
	for k, at := range q.seen {
		if now.Sub(at) > LedgerTTL {
			delete(q.seen, k)
		}
	}
	// A flood of distinct ids: forget the OLDEST settled rows first; an open (queued or handed-out) row is never forgotten,
	// so a request that may still run cannot be sent a second time.
	for len(q.ledger) > maxLedger {
		var oldest string
		var at time.Time
		for k, e := range q.ledger {
			if forgettable(e) && (oldest == "" || e.finishedAt.Before(at)) {
				oldest, at = k, e.finishedAt
			}
		}
		if oldest == "" {
			return // nothing settled to forget: the open rows are bounded by the per-session queue and maxOpenTyped
		}
		q.evict(oldest, q.ledger[oldest])
	}
}

// evict forgets a ledger row; a settled unknown typed row leaves a tombstone. q.mu held.
func (q *Queue) evict(k string, e *entry) {
	delete(q.ledger, k)
	if e.settled {
		q.tombstone(k)
	}
}

// tombstone remembers an id, dropping the oldest when full. q.mu held.
func (q *Queue) tombstone(id string) {
	for len(q.tombs) >= maxTombstones {
		var oldest string
		var at time.Time
		for k, t := range q.tombs {
			if oldest == "" || t.Before(at) {
				oldest, at = k, t
			}
		}
		delete(q.tombs, oldest)
	}
	q.tombs[id] = q.Now()
}

// Submit queues a prompt for the session and waits for the mod's report (or the wait to end). clientMsgID identifies the
// request for the at-most-once ledger. The Result's status is accepted | dropped | busy | timeout | unknown | no_mod.
func (q *Queue) Submit(ctx context.Context, sessionID, clientMsgID, text string) (Result, error) {
	return q.submitKeyed(ctx, KindSubmit, sessionID, clientMsgID, text)
}

// SubmitFill types text into the session's composer through the mod (no Enter) and waits for the report: accepted carries
// FilledAt and Phase "filled". It shares Submit's ledger and key space: a repeat of the id answers the recorded phase and
// never types again. The route drives the later phases with MarkEntering / MarkEntered / MarkCleared / MarkUnknown.
func (q *Queue) SubmitFill(ctx context.Context, sessionID, clientMsgID, text string) (Result, error) {
	if badText(text) {
		return Result{Status: Dropped, Reason: "bad_text"}, nil
	}
	return q.submitKeyed(ctx, KindFill, sessionID, clientMsgID, text)
}

func (q *Queue) submitKeyed(ctx context.Context, kind, sessionID, clientMsgID, text string) (Result, error) {
	q.mu.Lock()
	q.sweepLedger()
	if e, ok := q.ledger[clientMsgID]; ok && clientMsgID != "" {
		switch {
		case e.job.SessionID != sessionID || e.job.Text != text || e.job.Kind != kind:
			q.mu.Unlock() // the same id for a different request is a client bug, not a repeat
			return Result{Status: Dropped, Reason: "client_msg_id_reused"}, nil
		case e.state == stDone && e.res.Status == Busy:
			delete(q.ledger, clientMsgID) // busy never ran: it may be sent again below
		default:
			q.mu.Unlock()
			return q.await(ctx, e), nil // joins the first request's wait, or answers the settled ledger row
		}
	}
	if _, dead := q.tombs[clientMsgID]; dead && clientMsgID != "" && kind == KindFill {
		q.mu.Unlock() // an evicted unknown row: it may have been sent, so it is never typed again
		return Result{Status: Unknown, Phase: PhaseUnknown}, nil
	}
	if _, ok := q.owners.OwnerOf(sessionID); !ok {
		q.mu.Unlock()
		return Result{Status: NoMod}, nil
	}
	if len(q.queues[sessionID]) >= maxQueued {
		q.mu.Unlock()
		return Result{}, ErrBusy
	}
	if kind == KindFill && q.openTyped() >= maxOpenTyped {
		q.mu.Unlock()
		return Result{Status: Busy}, nil // rows that may hold our text are never forgotten, so they are capped instead
	}
	e := &entry{key: clientMsgID, job: Job{ID: newID(), Kind: kind, SessionID: sessionID, Text: text}, done: make(chan struct{}), queuedAt: q.Now()}
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
	return q.transient(ctx, KindInterrupt, sessionID, "")
}

// ClearIf asks the mod to clear the composer only if it still holds exactly text (the cleanup of a fill that was not
// sent; never clears someone else's draft). No ledger: it is best effort and harmless twice.
func (q *Queue) ClearIf(ctx context.Context, sessionID, text string) (Result, error) {
	if badText(text) {
		return Result{Status: Dropped, Reason: "bad_text"}, nil
	}
	return q.transient(ctx, KindClearIf, sessionID, text)
}

// Read asks the mod for the composer's text (accepted: Result.Text).
func (q *Queue) Read(ctx context.Context, sessionID string) (Result, error) {
	return q.transient(ctx, KindRead, sessionID, "")
}

func (q *Queue) transient(ctx context.Context, kind, sessionID, text string) (Result, error) {
	q.mu.Lock()
	if _, ok := q.owners.OwnerOf(sessionID); !ok {
		q.mu.Unlock()
		return Result{Status: NoMod}, nil
	}
	if len(q.queues[sessionID]) >= maxQueued {
		q.mu.Unlock()
		return Result{}, ErrBusy
	}
	e := &entry{job: Job{ID: newID(), Kind: kind, SessionID: sessionID, Text: text}, done: make(chan struct{}), queuedAt: q.Now()}
	q.queues[sessionID] = append(q.queues[sessionID], e)
	q.signal(sessionID)
	q.mu.Unlock()
	return q.await(ctx, e), nil
}

// await waits for the entry to settle: until the wait ends (a request never handed out is withdrawn and answers
// `timeout`; one handed out and unanswered is `unknown`), the hand timeout after hand-out, or the caller's context.
func (q *Queue) await(ctx context.Context, e *entry) Result {
	deadline := time.NewTimer(q.wait())
	defer func() { deadline.Stop() }() // the timer is replaced once the request is handed out
	for {
		q.mu.Lock()
		if e.state == stDone {
			r := q.view(e)
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
			// The wait bounds the time in the queue only: a request handed out meanwhile has its own full HandTimeout from
			// the hand-out (a prompt handed out at 9 s must not be called unknown at 10 s).
			q.mu.Lock()
			if e.state == stQueued { // never handed out: withdrawn, so it may be sent again
				q.removeQueued(e)
				if e.key != "" {
					delete(q.ledger, e.key)
				}
				q.finish(e, Result{Status: Timeout})
			}
			done := e.state == stDone
			r := q.view(e)
			q.mu.Unlock()
			if handTimer != nil {
				handTimer.Stop()
			}
			if done {
				return r
			}
			deadline = time.NewTimer(q.handTimeout() + q.wait()) // handed out: the hand timer decides; this is only a backstop
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
		if ok {
			q.seen[sessionID] = q.Now()
		}
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
	case Accepted:
		res := Result{Status: Accepted}
		switch e.job.Kind {
		case KindFill:
			// The mod's clock is not trusted: the freshness of the Enter counts from the daemon's own arrival time.
			recv, handed := q.Now().UnixMilli(), e.handedAt.UnixMilli()
			res.ReceivedAt = recv
			res.FilledAt = o.FilledAt
			if res.FilledAt <= 0 || res.FilledAt > recv {
				res.FilledAt = recv
			}
			if res.FilledAt < handed {
				res.FilledAt = handed
			}
		case KindRead:
			res.Text, res.Truncated = o.Text, o.Truncated
		}
		q.finish(e, res)
	case Busy:
		q.finish(e, Result{Status: Busy})
	case Unknown: // the mod could not tell what its job left in the composer
		q.finish(e, Result{Status: Unknown, Reason: o.Reason})
	default:
		q.finish(e, Result{Status: Dropped, Reason: o.Reason})
	}
	return nil
}

// badText: a fill / clear_if text the mod would refuse (blank, or over MaxTextBytes bytes) is refused here at once.
func badText(text string) bool {
	return strings.TrimSpace(text) == "" || len(text) > MaxTextBytes
}
