package dialogue

// The job channel: the queue that hands the mod its model calls (turn summaries, recaps) and takes the answers back.
// Rewritten from the session workbook's engine (base commit 6b10fa6c3, internal/module/workbook/{queue,engine,outcome,
// settle,jobs_service}.go) with the workbook-only parts left out: rewrite, refresh, journal, fork, follow-after-loss and
// the availability sweep are gone. The queue is per session (a job only ever runs in its own session's process), not per
// lineage conversation.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// Job kinds (daemon side). On the wire both go as "turn" (see wireJob.MarshalJSON): the deployed mod runs turn and
// rewrite jobs through $.model.complete and refuses any kind it does not know, and the daemon's lease keeps the real kind.
const (
	kindTurnSummary = "turn-summary"
	kindRecap       = "recap"

	wireKindTurn = "turn"
)

// The states and reasons a kind's finish receives. A reason is text to the store.
const (
	stateSkipped = "skipped"
	stateFailed  = "failed"

	reasonBacklog = "backlog" // skipped: more than three turn summaries of the session were waiting
	reasonCap     = "cap"     // skipped: the hourly cap of calls was reached
	reasonNoMod   = "no_mod"  // skipped: the session's mod did not announce workbook.v2 (set by the caller of enqueue)
	reasonStopped = "stopped" // skipped or failed: the module stopped, or the call was aborted before its timeout
	reasonAuth    = "auth"    // failed
	reasonAPI     = "api"     // failed
	reasonFormat  = "format"  // failed: the answer was not what the kind expects, twice
	reasonTimeout = "timeout" // failed
	reasonRefused = "refused" // failed: the call was rejected in the mod
	reasonLost    = "lost"    // failed: the lease ran out
	reasonBuild   = "build"   // failed: the job's input could not be built when it was handed out
	reasonStore   = "store"   // failed: the answer could not be stored (too many tries, or the row is gone)
)

// maxApplyRetries: an answer whose apply failed is applied again at each reaper tick this many times (about a minute).
const maxApplyRetries = 12

// Limits of the channel (spec section 5.1, plan D1 / D2 / D10).
const (
	maxWaitingTurns = 3                // waiting turn-summary jobs per session
	defaultCallCap  = 300              // calls per host per hour
	leaseSlack      = 10 * time.Second // a lease lasts timeout_ms + this
	completeTimeout = 30_000           // ms: timeout_ms of a job's call
	reapEvery       = 5 * time.Second  // how often leases that ran out are looked for
)

// systemBlock is one block of a call's system prompt.
type systemBlock struct {
	Text  string `json:"text"`
	Cache bool   `json:"cache"`
}

// complete is what the mod passes to $.model.complete (the keys renamed to the API's there).
type complete struct {
	Model     string        `json:"model"`
	System    []systemBlock `json:"system"`
	Prompt    string        `json:"prompt"`
	MaxTokens int           `json:"max_tokens"`
	Effort    string        `json:"effort"`
	TimeoutMS int           `json:"timeout_ms"`
}

// callInput is what a kind supplies for a job: the channel fixes the model, limits and cache mark.
type callInput struct {
	System string
	Prompt string
}

// wireJob is what Next hands out. Kind is the daemon-side kind; the JSON always says "turn".
type wireJob struct {
	ID       string
	Kind     string
	Complete complete
}

func (j wireJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID       string   `json:"id"`
		Kind     string   `json:"kind"`
		Complete complete `json:"complete"`
	}{j.ID, wireKindTurn, j.Complete})
}

// usage is the tokens and the time of a job's calls (summed over its attempts when it is handed to a kind's apply).
type usage struct{ In, Out, CacheRead, LatencyMS int64 }

func (u usage) plus(o usage) usage {
	return usage{u.In + o.In, u.Out + o.Out, u.CacheRead + o.CacheRead, u.LatencyMS + o.LatencyMS}
}

// result is the mod's report of a call.
type result struct {
	Answered  bool
	Text      string
	Reason    string // api-error | empty-reply | aborted | refused (not answered)
	Status    int    // the API status of an api-error, for the log
	Error     string // the API error kind of an api-error, e.g. authentication_failed
	Usage     usage
	LatencyMS int64
}

// job is a unit of queued work, in memory only (a restart ends the rows: T9 marks the pending ones at Start).
type job struct {
	id      string
	sid     string
	kind    string
	attempt int
	payload any   // what the kind needs to build the call and to settle the row (a row id, a turn id ...)
	use     usage // what the earlier attempts of this job used
}

// kindHandler is what a job kind (T9 turn summaries, T10 recaps) provides. The contract between the channel and a kind:
//
//  1. apply is idempotent. It only persists the answer, and uses the row's pending -> done change as a compare-and-set
//     gate: a row that is no longer pending makes it return nil without writing. The channel may call it again for the
//     same answer (a retry after a store error).
//  2. after is called by the channel exactly once per job, after apply returned nil (also when that was a retry): hook the
//     dialogue file, wake the push, queue a recap. It is best-effort and returns nothing.
//  3. finish only moves a row that is still pending to the final state (compare-and-set); a row already done or ended is
//     left alone. The channel may therefore call it where it cannot tell whether an answer was stored.
type kindHandler interface {
	// build makes the call's input from the store as it is now, at hand-out. An error ends the job as failed(build).
	build(j job) (callInput, error)
	// apply takes the answer's text (as the mod gave it) and the usage and latency summed over the job's attempts.
	// errFormat (or an error wrapping it) means the text is not what the kind expects: the channel retries once as
	// attempt 2, then fails the job (format). errGone means the row is gone and there is nothing to store: the job ends
	// as failed(store) and is not retried. Any other error means the result could not be stored: the channel keeps the
	// answer and applies it again at each reaper tick, up to maxApplyRetries times, then ends the job as failed(store).
	apply(j job, text string, u usage) error
	// after: see contract 2.
	after(j job)
	// finish ends a job that did not produce an answer: state skipped or failed, with one of the reasons above.
	finish(j job, state, reason string)
}

type lease struct {
	job        *job
	stream     string
	timeoutMS  int
	expires    time.Time
	processing bool // a result is being applied: reap and Stop leave it to that call
}

type sessQ struct {
	waiting []*job
	lease   *lease
	wake    chan struct{} // closed and replaced when the session's state changes
}

type chanDeps struct {
	capable  func(sid string) bool // whether the session's mod announced workbook.v2 (nil: nobody is)
	handlers map[string]kindHandler
	now      func() time.Time
	logf     func(format string, args ...any)
	callCap  int
}

type channel struct {
	d chanDeps

	qmu      sync.Mutex // the queue structures only; never held across a handler call
	qstopped bool
	qs       map[string]*sessQ
	leases   map[string]*lease
	hour     int64
	hourN    int
	capLog   bool
	inflight sync.WaitGroup // results and reaps being applied; Stop waits for them

	omu     sync.Mutex
	pending map[string]*pendingApply // answers whose apply failed, retried by the reaper (settle.go)
}

func newChannel(d chanDeps) *channel {
	if d.now == nil {
		d.now = time.Now
	}
	if d.logf == nil {
		d.logf = log.Printf
	}
	if d.callCap <= 0 {
		d.callCap = defaultCallCap
	}
	return &channel{d: d, qs: map[string]*sessQ{}, leases: map[string]*lease{}, pending: map[string]*pendingApply{}}
}

func (c *channel) capable(sid string) bool { return c.d.capable != nil && c.d.capable(sid) }

// newJobID is unguessable: the id doubles as the lease's credential, so a caller that holds the stream string still
// cannot finish a job it was not handed.
func newJobID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails on the platforms we run on
	return "dlg-" + hex.EncodeToString(b[:])
}

func (c *channel) queueOf(sid string) *sessQ {
	q := c.qs[sid]
	if q == nil {
		q = &sessQ{wake: make(chan struct{})}
		c.qs[sid] = q
	}
	return q
}

func (q *sessQ) signal() {
	close(q.wake)
	q.wake = make(chan struct{})
}

// why a job was not queued (enqueue's second result); the caller records the row as skipped with it.
const (
	notQueuedNoMod   = reasonNoMod
	notQueuedStopped = reasonStopped
)

// enqueue appends a fresh job of the session. It returns the job id, or why it was not queued: "no_mod" when the
// session's mod cannot run it, "stopped" when the channel has been stopped; nothing is written to a row then, the caller
// does. More than three waiting turn summaries of the session drop the oldest ones (skipped, backlog); a recap is never
// dropped.
func (c *channel) enqueue(sid, kind string, payload any) (id, notQueued string) {
	if !c.capable(sid) {
		return "", notQueuedNoMod
	}
	j := &job{id: newJobID(), sid: sid, kind: kind, attempt: 1, payload: payload}
	c.qmu.Lock()
	if c.qstopped {
		c.qmu.Unlock()
		return "", notQueuedStopped
	}
	q := c.queueOf(sid)
	q.waiting = append(q.waiting, j)
	var dropped []*job
	for {
		fresh := 0
		for _, w := range q.waiting {
			if isFresh(w) {
				fresh++
			}
		}
		if fresh <= maxWaitingTurns {
			break
		}
		for i, w := range q.waiting {
			if isFresh(w) {
				dropped = append(dropped, w)
				q.waiting = append(q.waiting[:i:i], q.waiting[i+1:]...)
				break
			}
		}
	}
	q.signal()
	if len(dropped) > 0 {
		c.inflight.Add(1) // under qmu with qstopped false: Stop waits for the dropped rows to be ended
	}
	c.qmu.Unlock()
	if len(dropped) > 0 {
		for _, d := range dropped {
			c.finishUnrun(d, stateSkipped, reasonBacklog)
		}
		c.inflight.Done()
	}
	return j.id, ""
}

// isFresh: a turn summary nobody has started on. A retry belongs to a row already under way and is never dropped for
// backlog; a recap is never dropped.
func isFresh(j *job) bool { return j.kind == kindTurnSummary && j.attempt == 1 }

// finishUnrun ends the row of a job that never ran (or whose result will never come).
func (c *channel) finishUnrun(j *job, state, reason string) {
	if h := c.d.handlers[j.kind]; h != nil {
		h.finish(*j, state, reason)
		return
	}
	c.d.logf("[dialogue] a %s job ended (%s) with no handler", j.kind, reason)
}

// admit counts one call against the hour's cap; false when the cap is reached (one log line per hour). qmu held.
func (c *channel) admit() bool {
	hour := c.d.now().Unix() / 3600
	if hour != c.hour {
		c.hour, c.hourN, c.capLog = hour, 0, false
	}
	if c.hourN >= c.d.callCap {
		if !c.capLog {
			c.capLog = true
			c.d.logf("[dialogue] the hourly cap of %d calls is reached; jobs are skipped until the hour ends", c.d.callCap)
		}
		return false
	}
	c.hourN++
	return true
}

// Next hands the session its next job, waiting up to wait for one to be ready.
func (c *channel) Next(ctx context.Context, stream, sid string, wait time.Duration) (wireJob, bool) {
	if stream == "" || sid == "" || !c.capable(sid) {
		return wireJob{}, false
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		c.reapLeases()
		c.qmu.Lock()
		if c.qstopped {
			c.qmu.Unlock()
			return wireJob{}, false
		}
		q := c.queueOf(sid)
		var skipped []*job
		var handed *job
		var l *lease
		for q.lease == nil && len(q.waiting) > 0 {
			j := q.waiting[0]
			q.waiting = q.waiting[1:]
			if !c.admit() {
				skipped = append(skipped, j)
				continue
			}
			l = &lease{job: j, stream: stream, timeoutMS: completeTimeout,
				expires: c.d.now().Add(time.Duration(completeTimeout)*time.Millisecond + leaseSlack)}
			q.lease, handed = l, j
			c.leases[j.id] = l
		}
		wake := q.wake
		if len(skipped) > 0 {
			c.inflight.Add(1) // under qmu with qstopped false: Stop waits for the skipped rows to be ended
		}
		c.qmu.Unlock()
		if len(skipped) > 0 {
			for _, s := range skipped {
				c.finishUnrun(s, stateSkipped, reasonCap)
			}
			c.inflight.Done()
		}
		if handed != nil {
			in, err := c.build(handed)
			if err != nil {
				c.d.logf("[dialogue] build a job: %v", err)
				// Only the lease's owner ends the job: when a reaper or Stop took the lease while the input was built, they
				// ended it (lost / stopped) and this must not end it again. Taking the lease as processing makes Stop wait.
				c.qmu.Lock()
				owns := c.ownsLeaseLocked(l)
				if owns {
					l.processing = true
					c.inflight.Add(1)
				}
				c.qmu.Unlock()
				if owns {
					c.finishUnrun(handed, stateFailed, reasonBuild)
					c.release(l, nil)
					c.inflight.Done()
				}
				continue
			}
			// The lease may have been reaped or stopped while the input was built (the store reads take a while): a job
			// is handed out only if its lease is still the session's, and its time starts now.
			c.qmu.Lock()
			valid := c.ownsLeaseLocked(l)
			if valid {
				l.expires = c.d.now().Add(time.Duration(l.timeoutMS)*time.Millisecond + leaseSlack)
			}
			c.qmu.Unlock()
			if !valid {
				return wireJob{}, false
			}
			return wireJob{ID: handed.id, Kind: handed.kind, Complete: complete{
				Model: "haiku", System: []systemBlock{{Text: in.System, Cache: true}}, Prompt: in.Prompt,
				MaxTokens: 4096, Effort: "low", TimeoutMS: l.timeoutMS}}, true
		}
		if wait <= 0 {
			return wireJob{}, false
		}
		select {
		case <-ctx.Done():
			return wireJob{}, false
		case <-deadline.C:
			return wireJob{}, false
		case <-wake:
		}
	}
}

func (c *channel) build(j *job) (callInput, error) {
	h := c.d.handlers[j.kind]
	if h == nil {
		return callInput{}, errNoHandler
	}
	return h.build(*j)
}

// JobWaiting: the session has a job queued that nobody holds. Only a capable session is told, so a mod that cannot run
// the job is never asked to.
func (c *channel) JobWaiting(sid string) bool {
	if !c.capable(sid) {
		return false
	}
	c.qmu.Lock()
	defer c.qmu.Unlock()
	q := c.qs[sid]
	return !c.qstopped && q != nil && q.lease == nil && len(q.waiting) > 0
}

// release drops a lease and puts follow (a retry) at the head of the session, so the row keeps its place; it wakes the
// session's pollers.
func (c *channel) release(l *lease, follow *job) {
	c.qmu.Lock()
	q := c.queueOf(l.job.sid)
	delete(c.leases, l.job.id)
	if q.lease == l {
		q.lease = nil
	}
	stopped := c.qstopped
	if follow != nil && !stopped {
		follow.id = newJobID()
		q.waiting = append([]*job{follow}, q.waiting...)
	}
	q.signal()
	c.qmu.Unlock()
	if follow != nil && stopped { // nobody will ever run it: a queue that has been stopped takes no new work
		c.finishUnrun(follow, stateFailed, reasonStopped)
	}
}

// ownsLeaseLocked: the lease is still the session's current one and nobody is applying or has ended it. qmu held.
func (c *channel) ownsLeaseLocked(l *lease) bool {
	return !c.qstopped && c.leases[l.job.id] == l && c.queueOf(l.job.sid).lease == l && !l.processing
}

// begin registers work that Stop must wait for; false once the channel is stopped.
func (c *channel) begin() bool {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	if c.qstopped {
		return false
	}
	c.inflight.Add(1) // under qmu with qstopped false: Stop's Wait never races an Add
	return true
}

// reap is the reaper goroutine's tick: it retries the answers that could not be stored, ends the waiting jobs of sessions
// that can no longer take them, and ends the leases that ran out. Only the reaper retries stored answers, so one answer
// never has two applies in flight.
func (c *channel) reap() {
	if !c.begin() {
		return
	}
	defer c.inflight.Done()
	c.retryPending()
	c.sweepIncapable()
	c.reapExpired()
}

// reapLeases is the part of the tick that Next and Result also run: leases that ran out are lost and the queue moves on.
func (c *channel) reapLeases() {
	if !c.begin() {
		return
	}
	defer c.inflight.Done()
	c.reapExpired()
}

// sweepIncapable ends the waiting jobs of a session whose mod stopped announcing the capability (skipped, no_mod; a job
// already leased is left to its lease), and forgets the queues that are empty, so the map does not grow with every session
// that was ever seen. A poller parked on a forgotten queue is woken and takes a fresh one.
func (c *channel) sweepIncapable() {
	c.qmu.Lock()
	sids := make([]string, 0, len(c.qs))
	for sid := range c.qs {
		sids = append(sids, sid)
	}
	c.qmu.Unlock()
	capable := make(map[string]bool, len(sids))
	for _, sid := range sids { // capable is the caller's function: never called under qmu
		capable[sid] = c.capable(sid)
	}
	var dead []*job
	c.qmu.Lock()
	if c.qstopped {
		c.qmu.Unlock()
		return
	}
	for sid, q := range c.qs {
		if ok, seen := capable[sid]; seen && !ok {
			dead = append(dead, q.waiting...)
			q.waiting = nil
		}
		if len(q.waiting) == 0 && q.lease == nil {
			q.signal()
			delete(c.qs, sid)
		}
	}
	c.qmu.Unlock()
	for _, j := range dead { // the caller (reap) is registered with inflight
		c.finishUnrun(j, stateSkipped, reasonNoMod)
	}
}

func (c *channel) reapExpired() {
	var lost []*lease
	c.qmu.Lock()
	if c.qstopped {
		c.qmu.Unlock()
		return
	}
	now := c.d.now()
	for _, l := range c.leases {
		if !l.processing && !now.Before(l.expires) {
			lost = append(lost, l)
		}
	}
	for _, l := range lost {
		l.processing = true
	}
	c.qmu.Unlock()
	for _, l := range lost {
		c.d.logf("[dialogue] a job ran out of its lease (kind %s)", l.job.kind)
		c.finishUnrun(l.job, stateFailed, reasonLost)
		c.release(l, nil)
	}
}

// Result takes a leased job's call; more says another job of the session is ready now. The lease must be the stream's.
func (c *channel) Result(stream, jobID string, r result) (bool, error) {
	c.reapLeases()
	c.qmu.Lock()
	l := c.leases[jobID]
	if l == nil || l.stream != stream || l.processing || c.qstopped {
		c.qmu.Unlock()
		return false, modevents.ErrNotLeased
	}
	l.processing = true
	c.inflight.Add(1)
	c.qmu.Unlock()
	defer c.inflight.Done() // Stop waits for a result that is being applied: nothing writes after it returns

	follow := c.apply(l, r)

	c.qmu.Lock()
	stopped := c.qstopped
	c.qmu.Unlock()
	if follow != nil && stopped { // the module stopped while the result was applied
		c.finishUnrun(follow, stateFailed, reasonStopped)
		follow = nil
	}
	c.release(l, follow)
	c.qmu.Lock()
	q := c.qs[l.job.sid]
	more := q != nil && len(q.waiting) > 0
	c.qmu.Unlock()
	return more, nil
}

// Stop ends the channel: every leased job not being applied fails (stopped), every queued job is skipped (stopped), the
// long polls wake and return, a late result finds no lease. It returns after the results and reaps that had already
// started, so nothing writes after it returns.
func (c *channel) Stop() {
	c.qmu.Lock()
	c.qstopped = true
	var leased, queued []*job
	for _, l := range c.leases {
		if !l.processing {
			leased = append(leased, l.job)
		}
	}
	for _, q := range c.qs {
		queued = append(queued, q.waiting...)
		q.waiting = nil
		q.signal()
	}
	c.leases = map[string]*lease{}
	for _, q := range c.qs {
		q.lease = nil
	}
	c.qmu.Unlock()
	for _, j := range leased {
		c.finishUnrun(j, stateFailed, reasonStopped)
	}
	for _, j := range queued {
		c.finishUnrun(j, stateSkipped, reasonStopped)
	}
	c.inflight.Wait() // the results and reaps that had already started
	c.dropPending()
}

// runReaper looks for leases that ran out until ctx ends.
func (c *channel) runReaper(ctx context.Context) {
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.reap()
		}
	}
}
