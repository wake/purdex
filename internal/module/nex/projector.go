package nex

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"lab.protype.tw/wake/nexen/bus"

	"github.com/wake/purdex/internal/core"
)

// The projector (spec 2026-10-08 §3.2–§3.5, #1866).
//
// It turns engine events into execution rows pushed on /ws/host-events, so
// the SPA's list follows an execution within one coalesce window instead of
// refetching whole pages behind a debounced site-wide SSE. A delta is never
// a fold of event payloads: Nexen changes state silently in places
// (SettleIdle, ClaimTurn; a failed append is only logged — §1 F2), so an
// event only says "this execution changed", and the delta is its row read
// again afterwards.
//
// A trigger frame on the engine's bus marks its execution dirty: the bus
// consumer (projector_bus.go) calls markFrame, which must stay cheap and
// never wait on the read slot (§3.2). One flush worker pops due executions in order and, for each,
// reads the row inside the slot and broadcasts it while still holding it
// (readSlot.readThen). One worker means one execution's deltas are read and
// sent in order; the slot makes bseq follow broadcast order and orders every
// delta against every list page (rule V).

const (
	// Coalescing per execution (§3.2): a batch is due coalesceTrailing after
	// its latest event, but never later than coalesceCap after its first —
	// a quiet execution flushes 75 ms after its burst, a busy one (a tool
	// loop, a stream of lease renewals) still at least every 250 ms.
	coalesceTrailing = 75 * time.Millisecond
	coalesceCap      = 250 * time.Millisecond

	// readRetryDelay is how long after a failed read its batch is read once
	// more (§3.3).
	readRetryDelay = time.Second

	// projectorStopWait bounds how long stop waits for the goroutines. They
	// end at once on cancel — a slot wait and the engine's handler both see
	// the context — so running into it means something is wedged.
	projectorStopWait = 5 * time.Second

	deltaEventType = "nex.execution"
)

// projectorTiming is the projector's timing, and the bounds of its list
// walks; a zero field takes its default. A test seam.
type projectorTiming struct {
	trailing   time.Duration   // coalesceTrailing
	maxDelay   time.Duration   // coalesceCap
	retryDelay time.Duration   // readRetryDelay
	recheck    []time.Duration // recheckOffsets (projector_recheck.go)
	helloWait  time.Duration   // helloSlotWait (projector_hello.go)
	backoffMin time.Duration   // resubscribeBackoffMin (projector_bus.go)
	backoffMax time.Duration   // resubscribeBackoffMax (projector_bus.go)
	walkLimit  int             // walkPageLimit (projector_walk.go)
	walkPages  int             // walkMaxPages (projector_walk.go)
	reconcile  time.Duration   // reconcileInterval (projector_reconcile.go)
	grace      time.Duration   // reconcileGrace (projector_reconcile.go)
}

func (t projectorTiming) withDefaults() projectorTiming {
	if t.trailing <= 0 {
		t.trailing = coalesceTrailing
	}
	if t.maxDelay <= 0 {
		t.maxDelay = coalesceCap
	}
	if t.retryDelay <= 0 {
		t.retryDelay = readRetryDelay
	}
	if t.recheck == nil {
		t.recheck = recheckOffsets
	}
	if t.helloWait <= 0 {
		t.helloWait = helloSlotWait
	}
	if t.backoffMin <= 0 {
		t.backoffMin = resubscribeBackoffMin
	}
	if t.backoffMax <= 0 {
		t.backoffMax = resubscribeBackoffMax
	}
	if t.walkLimit <= 0 {
		t.walkLimit = walkPageLimit
	}
	if t.walkPages <= 0 {
		t.walkPages = walkMaxPages
	}
	if t.reconcile <= 0 {
		t.reconcile = reconcileInterval
	}
	if t.grace <= 0 {
		t.grace = reconcileGrace
	}
	return t
}

// dirtyExec is one execution's pending batch: every mark since its last
// flush began.
type dirtyExec struct {
	cause   map[string]struct{} // trigger kinds accumulated for this flush
	first   time.Time           // the batch's first mark: the cap counts from it
	last    time.Time           // its latest event; zero while only re-marks are pending
	forced  time.Time           // its earliest re-mark: due no later than this; zero when none
	seq     uint64              // first-mark order, the FIFO tie-break
	attempt int                 // 1 once a read of this batch failed and was re-marked
	recheck bool                // a terminal recheck (projector_recheck.go) is part of this batch
}

// due is when the batch should be flushed: the trailing edge after its
// latest event, capped at maxDelay after its first mark, and never later
// than a re-mark scheduled for it.
func (e *dirtyExec) due(t projectorTiming) time.Time {
	d := e.first.Add(t.maxDelay)
	if !e.last.IsZero() {
		if tr := e.last.Add(t.trailing); tr.Before(d) {
			d = tr
		}
	}
	if !e.forced.IsZero() && e.forced.Before(d) {
		d = e.forced
	}
	return d
}

// rowDigest is the part of a row the safety reconcile compares with what was
// pushed (§3.7): state, pending_permission.request_id, archived, turn_count,
// last_turn_reason, terminal_reason. The projector keeps it current with
// every delta, and an epoch's seed sets it from the list.
type rowDigest struct {
	State             string
	PermissionRequest string
	Archived          bool
	TurnCount         int64
	LastTurnReason    string
	TerminalReason    string
}

// pushedRow is lastPushed[id] (§3.7): what the clients were last given for
// an execution, and the ver of the read it came from — the last delta
// pushed for it, or the list page an epoch's seed read it on
// (projector_epoch.go), whichever is newer.
type pushedRow struct {
	ver     uint64
	removed bool      // that delta was a removal (row null)
	digest  rowDigest // zero when removed
}

// mark is one reason to read an execution again.
type mark struct {
	kinds   []string  // trigger kinds it adds to the batch's cause
	at      time.Time // when the event was seen, or when a re-mark is due
	reMark  bool      // scheduled by the projector itself: due at `at` exactly, no trailing window
	attempt int       // carried by a retry
	recheck bool      // a terminal recheck's re-mark
}

// deltaValue is a delta's value (§3.5). The versions live here, never in
// HostEvent.Epoch/Seq: those are omitempty, and every field of a nex frame
// must be spelled out. Row nil encodes as null: the execution is gone.
type deltaValue struct {
	Epoch string          `json:"epoch"`
	Bseq  uint64          `json:"bseq"`
	ID    string          `json:"id"`
	Ver   uint64          `json:"ver"`
	Cause []string        `json:"cause"`
	Row   json.RawMessage `json:"row"`
}

// projector: see the top of this file. Build with newProjector, then start
// once and stop once.
type projector struct {
	slot   *readSlot
	rows   rowReader
	events *core.EventsBroadcaster
	bus    frameBus
	logf   func(string, ...any)
	timing projectorTiming
	now    func() time.Time

	ctx       context.Context // cancelled by stop; every read runs under it
	cancel    context.CancelFunc
	wg        sync.WaitGroup // the projector's goroutines
	done      chan struct{}  // closed once they have all returned
	kick      chan struct{}  // capacity 1: a mark changed what the worker waits for
	maintKick chan struct{}  // capacity 1: epoch work was asked for (projector_epoch.go)
	seeds     atomic.Int64   // seed walks finished, whatever their outcome; tests wait on it

	// walkMu is held by a whole seed and a whole reconcile tick: neither
	// ever overlaps the other or itself. Both run in the maintenance
	// goroutine, so it only ever waits for a test calling reconcile itself.
	walkMu sync.Mutex
	// The safety reconcile's counters (projector_reconcile.go), since start:
	// nex_delta_mismatch_total and nex_delta_reconcile_unseen_total.
	mismatchTotal atomic.Int64
	unseenTotal   atomic.Int64

	mu         sync.Mutex
	sub        *bus.Subscription     // the live subscription: set by subscribe, replaced by a resubscribe
	dirty      map[string]*dirtyExec // marked, not yet popped for a flush
	markSeq    uint64
	pushed     map[string]pushedRow     // lastPushed (§3.7)
	rechecks   map[string]*recheckState // terminal rechecks in progress
	wantSeed   bool                     // epoch work pending: seed lastPushed …
	wantRotate bool                     // … after starting a new epoch
}

func newProjector(slot *readSlot, rows rowReader, events *core.EventsBroadcaster, b frameBus, logf func(string, ...any), timing projectorTiming) *projector {
	ctx, cancel := context.WithCancel(context.Background())
	return &projector{
		slot: slot, rows: rows, events: events, bus: b, logf: logf,
		timing: timing.withDefaults(), now: time.Now,
		ctx: ctx, cancel: cancel,
		done: make(chan struct{}), kick: make(chan struct{}, 1), maintKick: make(chan struct{}, 1),
		dirty: map[string]*dirtyExec{}, pushed: map[string]pushedRow{}, rechecks: map[string]*recheckState{},
	}
}

// start subscribes to the bus — before it returns, so no frame published
// after start is missed — asks for the start seed (the daemon's start is an
// epoch start too, §3.6), and starts the bus consumer, the flush worker and
// the maintenance goroutine that seeds. start does not wait for the seed.
func (p *projector) start() {
	p.subscribe()
	p.requestEpochWork(false)
	p.goTracked(p.consume)
	p.goTracked(p.work)
	p.goTracked(p.maintain)
	go func() {
		p.wg.Wait()
		close(p.done)
	}()
}

// goTracked runs fn as one of the projector's goroutines, which stop waits
// for. Only start calls it, before anything waits on wg.
func (p *projector) goTracked(fn func()) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		fn()
	}()
}

// stop cancels the projector — the consumer unsubscribes, a read waiting for
// the slot or inside the engine is abandoned and consumes nothing, pending marks and re-marks are
// dropped — and waits for its goroutines, bounded by ctx and
// projectorStopWait. Idempotent; only after start.
func (p *projector) stop(ctx context.Context) {
	p.cancel()
	t := time.NewTimer(projectorStopWait)
	defer t.Stop()
	select {
	case <-p.done:
	case <-ctx.Done():
		p.logf("nex-delta: stop: projector still running (%v); not waiting for it", ctx.Err())
	case <-t.C:
		p.logf("nex-delta: stop: projector still running after %v; not waiting for it", projectorStopWait)
	}
}

// markFrame marks id dirty for one trigger event of kind and wakes the
// worker: a map update under a short lock, never a wait on the slot.
func (p *projector) markFrame(id, kind string) {
	p.mu.Lock()
	p.markLocked(id, mark{kinds: []string{kind}, at: p.now()})
	p.mu.Unlock()
	p.wake()
}

// markLocked adds m to id's pending batch, starting one if there is none.
// An event moves the batch's trailing edge; a re-mark can only bring its
// due time forward. Caller holds p.mu.
func (p *projector) markLocked(id string, m mark) {
	e := p.dirty[id]
	if e == nil {
		p.markSeq++
		e = &dirtyExec{cause: map[string]struct{}{}, first: m.at, seq: p.markSeq}
		p.dirty[id] = e
	}
	for _, k := range m.kinds {
		e.cause[k] = struct{}{}
	}
	if m.reMark {
		if e.forced.IsZero() || m.at.Before(e.forced) {
			e.forced = m.at
		}
	} else {
		e.last = m.at
	}
	if m.attempt > e.attempt {
		e.attempt = m.attempt
	}
	if m.recheck {
		e.recheck = true
	}
}

// wake tells the worker to re-plan its wait; a wake already pending covers
// this one.
func (p *projector) wake() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// work is the flush worker goroutine.
func (p *projector) work() {
	for {
		id, batch, ok := p.next()
		if !ok {
			return
		}
		p.flush(id, batch)
	}
}

// next waits for a due batch and pops it, or reports false once the
// projector stopped.
func (p *projector) next() (string, *dirtyExec, bool) {
	for {
		if p.ctx.Err() != nil {
			return "", nil, false
		}
		p.mu.Lock()
		id, e, wait := p.popDueLocked(p.now())
		p.mu.Unlock()
		if e != nil {
			return id, e, true
		}
		var timer *time.Timer
		var fire <-chan time.Time
		if wait > 0 {
			timer = time.NewTimer(wait)
			fire = timer.C
		}
		select {
		case <-p.ctx.Done():
		case <-p.kick:
		case <-fire:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// popDueLocked removes and returns the batch to flush now — the earliest
// due, the first marked among equals (FIFO) — or, when none is due yet, how
// long until the next one is (0 when nothing is pending). A linear scan:
// the batches pending at once are the executions active within a quarter
// second, a handful. Caller holds p.mu.
func (p *projector) popDueLocked(now time.Time) (string, *dirtyExec, time.Duration) {
	var bestID string
	var best *dirtyExec
	var bestDue time.Time
	for id, e := range p.dirty {
		d := e.due(p.timing)
		if best == nil || d.Before(bestDue) || (d.Equal(bestDue) && e.seq < best.seq) {
			bestID, best, bestDue = id, e, d
		}
	}
	switch {
	case best == nil:
		return "", nil, 0
	case bestDue.After(now):
		return "", nil, bestDue.Sub(now)
	}
	delete(p.dirty, bestID)
	return bestID, best, 0
}

// flush reads id's row inside the slot (who "row", no wait bound: the
// worker waits in its own goroutine, §3.8) and, on success, pushes it before
// the slot is released. Marks arriving meanwhile start a new batch, so a
// title_changed committed by the read itself (§3.3) only marks it again,
// and the next read finds nothing new.
func (p *projector) flush(id string, b *dirtyExec) {
	cause := sortedKinds(b.cause)
	var row json.RawMessage
	var found bool
	var pushed pushedRow
	_, err := p.slot.readThen(p.ctx, "row", 0, func(ctx context.Context) error {
		var err error
		row, found, err = p.rows.read(ctx, id)
		return err
	}, func(st slotStamp) {
		pushed = p.push(id, st, cause, row, found)
	})
	switch {
	case err == nil:
		p.recheckAfter(id, b, pushed)
	case p.ctx.Err() == nil:
		p.readFailed(id, b, err)
	}
}

// push numbers one delta and broadcasts it. It runs inside the slot, right
// after the read's ver was taken (readThen), so broadcast order is bseq
// order (§3.5). The broadcast reaches only the subscribers that opted into
// nex.v1, and it is strict: one of them that cannot take the frame is
// disconnected rather than left without it.
//
// An epoch whose bseq is exhausted (2^53−1, §3.6) ends here, in the same
// hold: the next epoch starts and its hello goes out first, and the delta
// is bseq 1 of the new epoch — so every client sees the hello before any
// delta it numbers. The delta carries the epoch as it is after that, not
// the one st was stamped with; its ver is st's either way.
func (p *projector) push(id string, st slotStamp, cause []string, row json.RawMessage, found bool) pushedRow {
	if !found {
		row = nil // encodes as null: remove
	}
	if p.slot.bseqExhausted() {
		epoch := p.startEpochLocked()
		p.logf("nex-delta: bseq reached %d; new epoch %s, hello sent to every nex.v1 subscriber", p.slot.bseqLimit, epoch)
	}
	bseq := p.slot.nextBseq()
	value, err := encodeValue(deltaValue{Epoch: p.slot.current().Epoch, Bseq: bseq, ID: id, Ver: st.Ver, Cause: cause, Row: row})
	if err != nil {
		// Cannot happen: row is JSON the row reader just encoded. Were it to,
		// the bseq skipped shows every client a gap, and a gap reconciles.
		p.logf("nex-delta: encoding the delta of exec=%s failed: %v", id, err)
		return pushedRow{}
	}
	p.events.BroadcastStrictTo(core.FeatureNexV1, core.HostEvent{Type: deltaEventType, Value: value})
	return p.recordPushed(id, st.Ver, row, found)
}

// recordPushed keeps lastPushed[id] current and returns what it recorded.
func (p *projector) recordPushed(id string, ver uint64, row json.RawMessage, found bool) pushedRow {
	rec := pushedRow{ver: ver, removed: !found}
	if found {
		d, err := digestOf(row)
		if err != nil {
			p.logf("nex-delta: no status digest for exec=%s: %v", id, err)
		}
		rec.digest = d
	}
	p.mu.Lock()
	p.pushed[id] = rec
	p.mu.Unlock()
	return rec
}

// digestOf extracts a row's status digest.
func digestOf(row json.RawMessage) (rowDigest, error) {
	var r struct {
		State             string `json:"state"`
		PendingPermission *struct {
			RequestID string `json:"request_id"`
		} `json:"pending_permission"`
		Archived       bool   `json:"archived"`
		TurnCount      int64  `json:"turn_count"`
		LastTurnReason string `json:"last_turn_reason"`
		TerminalReason string `json:"terminal_reason"`
	}
	if err := json.Unmarshal(row, &r); err != nil {
		return rowDigest{}, err
	}
	d := rowDigest{State: r.State, Archived: r.Archived, TurnCount: r.TurnCount,
		LastTurnReason: r.LastTurnReason, TerminalReason: r.TerminalReason}
	if r.PendingPermission != nil {
		d.PermissionRequest = r.PendingPermission.RequestID
	}
	return d, nil
}

// readFailed re-marks a batch whose read failed, once, after retryDelay,
// carrying its cause — it was never delivered — and its recheck. A batch
// already retried is dropped with a log line (a recheck it carried ends
// with it): its next change marks it again, and the safety reconcile
// catches one that never comes.
func (p *projector) readFailed(id string, b *dirtyExec, err error) {
	if b.attempt > 0 {
		p.logf("nex-delta: row read exec=%s failed again, giving up until it changes: %v", id, err)
		if b.recheck {
			p.mu.Lock()
			delete(p.rechecks, id)
			p.mu.Unlock()
		}
		return
	}
	p.logf("nex-delta: row read exec=%s failed, retrying in %v: %v", id, p.timing.retryDelay, err)
	p.mu.Lock()
	p.markLocked(id, mark{kinds: sortedKinds(b.cause), at: p.now().Add(p.timing.retryDelay), reMark: true,
		attempt: 1, recheck: b.recheck})
	p.mu.Unlock()
	p.wake()
}

// sortedKinds lists a cause set in order, as [] (never null) when empty.
func sortedKinds(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// encodeValue encodes a nex frame's value without HTML escaping, so the row
// inside a delta is byte for byte what the row reader produced.
func encodeValue(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}
