package workbook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/wake/purdex/internal/convmodel"
)

// Job kinds on the wire (spec §5.1). A retry is the same kind with attempt 2, never a new kind.
const (
	JobTurn    = "turn"
	JobRewrite = "rewrite"
	JobRefresh = "refresh"
)

// ErrNotLeased: the result's job is not leased to that stream (it ran out, was never handed out, was answered already,
// or another stream holds it).
var ErrNotLeased = errors.New("workbook: the job is not leased to this stream")

// SystemBlock is one block of a call's system prompt.
type SystemBlock struct {
	Text  string `json:"text"`
	Cache bool   `json:"cache"`
}

// Complete is what the mod passes to $.model.complete (the keys renamed to the API's there).
type Complete struct {
	Model     string        `json:"model"`
	System    []SystemBlock `json:"system"`
	Prompt    string        `json:"prompt"`
	MaxTokens int           `json:"max_tokens"`
	Effort    string        `json:"effort"`
	TimeoutMS int           `json:"timeout_ms"`
}

// Fork is a refresh job's call (spec §5.6): the mod passes it to $.model.fork, which sends the conversation itself.
type Fork struct {
	Prompt    string `json:"prompt"`
	TimeoutMS int    `json:"timeout_ms"`
}

// Job is what Next hands out: turn and re-write jobs carry a Complete, a refresh job carries a Fork instead.
type Job struct {
	ID       string
	Kind     string
	Complete Complete
	Fork     *Fork
}

// MarshalJSON writes {id, kind, complete} or {id, kind, fork}: the wire kinds are exactly the spec's three.
func (j Job) MarshalJSON() ([]byte, error) {
	if j.Kind == JobRefresh {
		return json.Marshal(struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Fork *Fork  `json:"fork"`
		}{j.ID, j.Kind, j.Fork})
	}
	return json.Marshal(struct {
		ID       string   `json:"id"`
		Kind     string   `json:"kind"`
		Complete Complete `json:"complete"`
	}{j.ID, j.Kind, j.Complete})
}

// Result is the mod's report of a call (spec §5.1).
type Result struct {
	Answered  bool
	Text      string
	Reason    string // api-error | empty-reply | aborted | refused (not answered)
	Status    int    // the API status of an api-error, for the log
	Error     string // the API error kind of an api-error, e.g. authentication_failed
	Usage     Usage
	LatencyMS int64
}

// JobSource is what the mod's socket routes call (WB-1b′-c). Stream is the mod's stream id: a lease is bound to the stream
// that took it.
type JobSource interface {
	// Next hands the session's conversation its next job, waiting up to wait for one to be ready.
	Next(ctx context.Context, stream, sessionID string, wait time.Duration) (Job, bool)
	// Result reports a leased job's call; more says another job of the conversation is ready now.
	Result(stream, jobID string, r Result) (more bool, err error)
}

var _ JobSource = (*Engine)(nil)

// job is a unit of queued work, in memory only (a restart fails the rows: plan D9).
type job struct {
	id      string
	conv    string
	entryID int64
	session string
	kind    string
	attempt int
	turn    convmodel.Turn // kind turn: what the input is built from at hand-out

	// carried from the turn call to its re-write
	entry string // kind rewrite: the entry text that was over 150
	out   Output // thing / push / thing_done already written at the push line
	usage Usage  // the tokens and time of every call of this entry so far
	lat   int64
}

type lease struct {
	job        *job
	stream     string
	ids        map[int]int64 // the job's n -> todo id map (plan D12)
	timeoutMS  int
	expires    time.Time
	processing bool // a result is being applied: reap and Stop leave it to that call
}

type convQ struct {
	waiting []*job
	lease   *lease
	wake    chan struct{} // closed and replaced when the conversation's state changes

	noCapSince time.Time // a refresh at the head and nobody able to run it since then (refresh.go)
}

// newJobID is unguessable: the id doubles as the lease's credential, so a caller that holds the stream string still
// cannot finish a job it was not handed.
func newJobID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails on the platforms we run on
	return "wbj-" + hex.EncodeToString(b[:])
}

func (e *Engine) queueOf(conv string) *convQ {
	q := e.convs[conv]
	if q == nil {
		q = &convQ{wake: make(chan struct{})}
		e.convs[conv] = q
	}
	return q
}

func (q *convQ) signal() {
	close(q.wake)
	q.wake = make(chan struct{})
}

// enqueue appends a fresh turn job to its conversation; more than three waiting turns drop the oldest ones (backlog).
func (e *Engine) enqueue(j *job) {
	e.qmu.Lock()
	if e.qstopped {
		e.qmu.Unlock()
		e.finishUnrun(j, StateSkipped, ReasonStopped)
		return
	}
	j.id = newJobID()
	q := e.queueOf(j.conv)
	q.waiting = append(q.waiting, j)
	var dropped []*job
	for fresh := 0; ; {
		fresh = 0
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
	e.qmu.Unlock()
	for _, d := range dropped {
		e.finishUnrun(d, StateSkipped, ReasonBacklog)
	}
}

// isFresh: a turn job nobody has started on. Retries and re-writes belong to an entry already under way and are never
// dropped for backlog.
func isFresh(j *job) bool { return j.kind == JobTurn && j.attempt == 1 }

// finishUnrun ends the entry of a job that never ran (or whose result will never come) as skipped or failed.
func (e *Engine) finishUnrun(j *job, state, reason string) {
	if j.kind == JobRefresh && state == StateSkipped {
		state = StateFailed // a refresh was asked for: it fails (stopped), it is not skipped
	}
	if j.kind == JobRewrite {
		e.finishCut(j) // its thing and push are out already: keep them, cut the entry
		return
	}
	e.finishRow(j.entryID, state, reason, Output{LatencyMS: j.lat})
	e.notifyLine(j.entryID, false)
}

// finishCut ends an entry whose re-write will not run: the entry text is cut at its last sentence end ≤ 150.
func (e *Engine) finishCut(j *job) {
	out := j.out
	out.Entry = CutEntry(j.entry)
	out.LatencyMS = j.lat
	out.Usage = j.usage
	e.finishRow(j.entryID, StateOK, "", out)
}

// admit counts one call against the hour's cap; false when the cap is reached (one log line per hour).
func (e *Engine) admit() bool {
	hour := e.d.Now().Unix() / 3600
	if hour != e.hour {
		e.hour, e.hourN, e.capLog = hour, 0, false
	}
	if e.hourN >= e.callCap {
		if !e.capLog {
			e.capLog = true
			e.d.Logf("[workbook] the hourly cap of %d calls is reached; turns are skipped until the hour ends", e.callCap)
		}
		return false
	}
	e.hourN++
	return true
}

func (e *Engine) capReached() bool {
	e.admitHourRoll()
	return e.hourN >= e.callCap
}

func (e *Engine) admitHourRoll() {
	if hour := e.d.Now().Unix() / 3600; hour != e.hour {
		e.hour, e.hourN, e.capLog = hour, 0, false
	}
}

// Next implements JobSource.
func (e *Engine) Next(ctx context.Context, stream, sessionID string, wait time.Duration) (Job, bool) {
	if stream == "" || sessionID == "" || !e.capable(sessionID) {
		return Job{}, false
	}
	conv, err := e.convKey(sessionID)
	if err != nil {
		e.d.Logf("[workbook] conversation of a session: %v", err)
		return Job{}, false
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		e.reap()
		canRefresh := e.refreshCapable(sessionID) // read before qmu: it may reach the lineage
		e.qmu.Lock()
		if e.qstopped {
			e.qmu.Unlock()
			return Job{}, false
		}
		q := e.queueOf(conv)
		var skipped []*job
		var handed *job
		var l *lease
		for q.lease == nil && len(q.waiting) > 0 {
			j := q.waiting[0]
			if j.kind == JobRefresh && !canRefresh {
				break // a refresh goes only to a session whose mod can run it; the queue behind it waits (D10)
			}
			q.waiting = q.waiting[1:]
			if j.kind != JobRefresh && !e.admit() { // a manual refresh is not a summariser call of the hourly cap
				skipped = append(skipped, j)
				continue
			}
			timeout := completeTimeout
			if j.kind == JobRefresh {
				timeout = refreshTimeout
			}
			l = &lease{job: j, stream: stream, timeoutMS: timeout,
				expires: e.d.Now().Add(time.Duration(timeout)*time.Millisecond + leaseSlack)}
			q.lease, handed = l, j
			e.leases[j.id] = l
		}
		wake := q.wake
		e.qmu.Unlock()
		for _, s := range skipped {
			e.finishUnrun(s, StateSkipped, ReasonCap)
		}
		if handed != nil {
			out, fork, ids, err := e.buildCall(handed, conv)
			if err != nil {
				e.d.Logf("[workbook] build a job: %v", err)
				e.release(l, nil)
				e.finishUnrun(handed, StateFailed, ReasonAPI)
				continue
			}
			if e.afterBuild != nil {
				e.afterBuild()
			}
			// The lease may have been reaped or stopped while the input was built (the store reads take a while):
			// a job is handed out only if its lease is still the conversation's, and its time starts now.
			e.qmu.Lock()
			valid := !e.qstopped && e.leases[handed.id] == l && q.lease == l && !l.processing
			if valid {
				l.ids = ids
				l.expires = e.d.Now().Add(time.Duration(l.timeoutMS)*time.Millisecond + leaseSlack)
			}
			e.qmu.Unlock()
			if !valid {
				return Job{}, false
			}
			if handed.kind == JobRefresh && handed.session != sessionID {
				// the capable session that actually took it is the one it runs in (D14)
				if err := e.d.Store.RepointSession(handed.entryID, sessionID); err != nil {
					e.d.Logf("[workbook] re-point a refresh: %v", err)
				}
				handed.session = sessionID
			}
			return Job{ID: handed.id, Kind: handed.kind, Complete: out, Fork: fork}, true
		}
		if wait <= 0 {
			return Job{}, false
		}
		select {
		case <-ctx.Done():
			return Job{}, false
		case <-deadline.C:
			return Job{}, false
		case <-wake:
		}
	}
}

// buildCall makes what a job sends: a refresh its fork, any other kind its complete.
func (e *Engine) buildCall(j *job, conv string) (Complete, *Fork, map[int]int64, error) {
	if j.kind != JobRefresh {
		c, ids, err := e.buildComplete(j, conv)
		return c, nil, ids, err
	}
	st, _, err := e.d.Store.Status(conv)
	if err != nil {
		return Complete{}, nil, nil, err
	}
	open, err := e.d.Store.OpenTodos(conv, maxPromptTodos)
	if err != nil {
		return Complete{}, nil, nil, err
	}
	prompt, ids := BuildRefreshInput(st.Status, open)
	return Complete{}, &Fork{Prompt: prompt, TimeoutMS: refreshTimeout}, ids, nil
}

// buildComplete makes the call of a job from the store as it is now (plan D12): the previous output is applied, since
// a job is handed out only after the one before it was finished.
func (e *Engine) buildComplete(j *job, conv string) (Complete, map[int]int64, error) {
	c := Complete{Model: "haiku", MaxTokens: 4096, Effort: "low", TimeoutMS: completeTimeout}
	if j.kind == JobRewrite {
		c.System = []SystemBlock{{Text: RewritePrompt}}
		c.Prompt = j.entry
		return c, nil, nil
	}
	recent, err := e.d.Store.RecentForPrompt(conv, maxRecentEntries)
	if err != nil {
		return Complete{}, nil, err
	}
	st, _, err := e.d.Store.Status(conv)
	if err != nil {
		return Complete{}, nil, err
	}
	open, err := e.d.Store.OpenTodos(conv, maxPromptTodos)
	if err != nil {
		return Complete{}, nil, err
	}
	prompt, ids, err := BuildTurnInput(TurnSource{PreviousStatus: st.Status, Recent: recent, Open: open, Turn: j.turn})
	if err != nil {
		return Complete{}, nil, err
	}
	c.System = []SystemBlock{{Text: SystemPrompt, Cache: true}}
	c.Prompt = prompt
	return c, ids, nil
}

// release drops a lease and puts follow (a retry or a re-write) at the head of the conversation, so the entry keeps its
// place; it wakes the conversation's pollers.
func (e *Engine) release(l *lease, follow *job) {
	e.qmu.Lock()
	defer e.qmu.Unlock()
	q := e.queueOf(l.job.conv)
	delete(e.leases, l.job.id)
	if q.lease == l {
		q.lease = nil
	}
	if follow != nil {
		follow.id = newJobID()
		q.waiting = append([]*job{follow}, q.waiting...)
	}
	q.signal()
}

// reap ends the leases that ran out: the call is lost, and the queue moves on.
func (e *Engine) reap() {
	e.retryOrphans()
	if e.refreshWaiting() {
		e.failStaleRefreshHeads(e.refreshConvs())
	}
	var lost []*lease
	e.qmu.Lock()
	now := e.d.Now()
	for _, l := range e.leases {
		if !e.qstopped && !l.processing && !now.Before(l.expires) {
			lost = append(lost, l)
		}
	}
	for _, l := range lost {
		l.processing = true
		e.inflight.Add(1) // under qmu with qstopped false: Stop's Wait never races an Add
	}
	e.qmu.Unlock()
	for _, l := range lost {
		e.d.Logf("[workbook] a job ran out of its lease (kind %s)", l.job.kind)
		e.finishUnrun(l.job, StateFailed, ReasonLost)
		e.release(l, nil)
		e.inflight.Done()
	}
}

// Result implements JobSource.
func (e *Engine) Result(stream, jobID string, r Result) (bool, error) {
	e.reap()
	e.qmu.Lock()
	l := e.leases[jobID]
	if l == nil || l.stream != stream || l.processing || e.qstopped {
		e.qmu.Unlock()
		return false, ErrNotLeased
	}
	l.processing = true
	e.inflight.Add(1)
	e.qmu.Unlock()
	defer e.inflight.Done() // Stop waits for a result that is being applied: nothing writes after it returns

	follow := e.apply(l, r)

	e.qmu.Lock()
	stopped := e.qstopped
	e.qmu.Unlock()
	if follow != nil && stopped { // the module stopped while the result was applied
		e.finishUnrun(follow, StateFailed, ReasonStopped)
		follow = nil
	}
	e.release(l, follow)
	e.qmu.Lock()
	more := len(e.queueOf(l.job.conv).waiting) > 0
	e.qmu.Unlock()
	return more, nil
}

// Stop ends the engine (plan D9): every leased turn job's entry fails (stopped) — a leased or queued re-write keeps its
// entry ok, cut — and every queued turn is skipped (stopped); a late result finds no lease.
func (e *Engine) Stop() {
	e.life.Lock()
	e.stopped = true
	e.life.Unlock()

	e.qmu.Lock()
	e.qstopped = true
	var leased, queued []*job
	for _, l := range e.leases {
		if !l.processing {
			leased = append(leased, l.job)
		}
	}
	for _, q := range e.convs {
		queued = append(queued, q.waiting...)
		q.waiting = nil
		q.signal()
	}
	e.leases = map[string]*lease{}
	for _, q := range e.convs {
		q.lease = nil
	}
	e.qmu.Unlock()
	for _, j := range leased {
		e.finishUnrun(j, StateFailed, ReasonStopped)
	}
	for _, j := range queued {
		e.finishUnrun(j, StateSkipped, ReasonStopped)
	}
	e.inflight.Wait() // the results and reaps that had already started
}

// RunReaper looks for leases that ran out until ctx ends.
func (e *Engine) RunReaper(ctx context.Context) {
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.reap()
			if e.d.OnAvailability != nil {
				if ch := e.SweepAvailability(); len(ch) > 0 {
					e.d.OnAvailability(ch)
				}
			}
		}
	}
}
