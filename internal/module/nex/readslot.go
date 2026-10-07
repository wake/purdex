package nex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"time"
)

// The read slot (spec 2026-10-08 §3.1, #1866).
//
// Every nex read whose result reaches a client goes through one module-wide
// slot: the projector's single-row reads (projector.go), each list page the list
// wrapper serves (listwrap.go), and the safety reconcile's pages (PR1c). A
// read that succeeded is stamped, inside the slot, with ver — a counter that
// never resets within the process. Because no read can straddle another,
// rule V holds: of two stamped reads, the one with the larger ver started
// after the other one's read finished. A client can therefore order a pushed
// row against a list page by comparing two numbers, which Nexen's own
// updated_at cannot do (several writes never bump it, §1 F4).
//
// Why one global slot rather than a version per execution: a per-execution
// version orders a row against a list page only if that page cannot
// interleave with the row's re-read, and guaranteeing that means holding
// every execution's lock across the page — a global slot by another name.
// Given the slot, one counter is enough, and it also orders the rows a page
// does NOT contain (the SPA's tombstones, §4.3) with a single number.

// slotLogThreshold is how long a wait for the slot, or a hold of it, may
// last before it is logged (§3.8 "Observability"). Every measured case in
// §3.8's table sits well below it, so a line in the log means a read was
// slower than anything benchmarked — worth seeing, not routine noise.
const slotLogThreshold = 250 * time.Millisecond

// errSlotBusy is what acquire returns when maxWait passed before the slot
// freed. It is deliberately distinct from the context errors: the list
// wrapper answers it as 503 nex_busy to a client that opted in with
// pdx=retry (which retries with backoff, §8 R3-3) and serves anyone else
// an unstamped page instead (§3.4), while a context that ended means the
// caller is gone and nothing is written at all.
var errSlotBusy = errors.New("nex read slot busy")

// slotStamp is what a successful read is stamped with. All three values are
// taken inside the slot, together, right after the read succeeded:
//
//   - Epoch names the process's counter, so a client never compares vers
//     across daemon restarts;
//   - Ver orders this read against every other stamped read (rule V);
//   - Bseq is the broadcast high-water mark at that moment: every delta with
//     bseq ≤ Bseq was enqueued before this read finished (§8 R3-1, the SPA
//     reconcile waits for them before judging a mismatch). The projector
//     advances it, inside the slot, with every delta it broadcasts.
//
// The JSON tags are the list wrapper's "pdx" object; no omitempty, so a
// zero bseq is spelled out rather than lost (the §3.5 hello lesson).
type slotStamp struct {
	Epoch string `json:"epoch"`
	Ver   uint64 `json:"ver"`
	Bseq  uint64 `json:"bseq"`
}

// readSlot is a one-holder lock whose acquisition can be abandoned — the
// session module's snapSlot (internal/module/session/slot.go) — plus the
// counters a holder stamps reads with and the wait/hold measurements §3.8
// asks for. Build one with newReadSlot; the zero value is unusable.
type readSlot struct {
	// sem has capacity 1: holding the slot means having sent into it. A
	// channel rather than a sync.Mutex so a waiter can give up when its
	// context ends or maxWait passes.
	sem chan struct{}

	// Only a holder of the slot reads or writes these; the channel
	// operations order every access, so they need no lock of their own.
	epoch string
	ver   uint64 // never resets within the process (rule V)
	bseq  uint64 // broadcast high-water mark; the projector advances it (nextBseq)

	logf      func(string, ...any)
	now       func() time.Time // time.Now in production; test seam
	threshold time.Duration    // slotLogThreshold in production; test seam

	// The longest hold and the longest wait since the slot was built, in
	// ms (nex_delta_slot_max_hold_ms / _max_wait_ms, which the safety
	// reconcile prints in PR1c). Atomics: waiters record their waits
	// concurrently, and a releasing holder records its hold while the next
	// holder may already be running.
	maxHoldMs atomic.Int64
	maxWaitMs atomic.Int64
}

// newReadSlot builds a free slot with a fresh epoch. logf receives the
// slow-wait and slow-hold lines.
func newReadSlot(logf func(string, ...any)) *readSlot {
	return &readSlot{
		sem:       make(chan struct{}, 1),
		epoch:     newEpoch(),
		logf:      logf,
		now:       time.Now,
		threshold: slotLogThreshold,
	}
}

// newEpoch draws a random 64-bit process identity as 16 lowercase hex
// chars, the same shape as the session module's versioned lists use.
// crypto/rand.Read never returns an error (Go ≥1.24), so there is no
// fallback.
func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// acquire takes the slot, or gives up without holding it: with ctx.Err()
// once ctx ends, or with errSlotBusy once maxWait has passed (maxWait <= 0
// waits as long as ctx allows — the projector's case, which waits in its
// own goroutine and never on the bus consumer, §3.8 "Bounds").
//
// As in the session module's slot: when the slot frees just as ctx ends,
// both select cases are ready and select may pick the send, so a successful
// send is followed by a ctx check that hands the slot straight back. A
// caller whose context has ended never comes back holding the slot. maxWait
// gets no such check: it bounds the wait, and a slot taken just as it ran
// out was not waited for any longer than that.
func (s *readSlot) acquire(ctx context.Context, maxWait time.Duration) error {
	var expired <-chan time.Time
	if maxWait > 0 {
		t := time.NewTimer(maxWait)
		defer t.Stop()
		expired = t.C
	}
	select {
	case s.sem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-expired:
		return errSlotBusy
	}
}

// release frees a slot taken by acquire.
func (s *readSlot) release() { <-s.sem }

// read is the one way a client-visible read runs: acquire the slot (see
// acquire for ctx and maxWait), run fn under ctx, and — only if fn returned
// nil — consume the next ver and return the stamp taken inside the slot.
// who names the reader in the log ("list", "row", "reconcile").
//
// A read that never got the slot, or whose fn failed, returns a zero stamp
// and consumes nothing: a failed, timed-out or abandoned read must never
// leave a hole a client would read as a missed version (§3.1). The error is
// returned as is — errSlotBusy or the context's error from the wait, fn's
// own error otherwise — so a caller tells them apart with errors.Is or by
// whether fn ran.
//
// The slot is released in a defer, so it frees whether fn finished, failed,
// was cancelled or panicked (the panic still propagates). The hold is
// measured up to the release and logged after it, so logging never extends
// the hold.
func (s *readSlot) read(ctx context.Context, who string, maxWait time.Duration, fn func(context.Context) error) (slotStamp, error) {
	return s.readThen(ctx, who, maxWait, fn, nil)
}

// readThen is read with one more step inside the same hold: once fn has
// succeeded and its ver was taken, then (when non-nil) runs with that
// stamp, before the slot is released. It is how the projector numbers and
// sends a delta (spec §3.3, §3.5): it takes the next bseq (nextBseq) and
// broadcasts while still holding the slot, so broadcast order is bseq order
// and every stamp taken later carries a high-water mark that includes it.
// then never runs for a read that failed or never got the slot — such a
// read consumes neither ver nor bseq. The hold measured and logged covers
// then too: it held the slot all the same.
func (s *readSlot) readThen(ctx context.Context, who string, maxWait time.Duration, fn func(context.Context) error, then func(slotStamp)) (slotStamp, error) {
	start := s.now()
	err := s.acquire(ctx, maxWait)
	acquired := s.now()
	s.noteWait(who, acquired.Sub(start), err)
	if err != nil {
		return slotStamp{}, err
	}
	defer func() {
		held := s.now().Sub(acquired)
		s.release()
		s.noteHold(who, held)
	}()

	if err := fn(ctx); err != nil {
		return slotStamp{}, err
	}
	s.ver++
	st := slotStamp{Epoch: s.epoch, Ver: s.ver, Bseq: s.bseq}
	if then != nil {
		then(st)
	}
	return st, nil
}

// nextBseq consumes the next broadcast sequence number. Only a holder of the
// slot may call it — in practice readThen's then, right before the
// broadcast it numbers — so bseq stays contiguous and in broadcast order
// (§3.5).
//
// bseq would rotate the epoch at 2^53−1 (§3.5, as the session module's seq
// does), but a rotation is a new epoch every client must be told about with
// a hello to all — the same broadcast PR1c's resubscribe adds (§3.6). At a
// million deltas a second that is 285 years away, so the rotation lands
// with it. TODO(PR1c): rotate here once the epoch-start hello exists.
func (s *readSlot) nextBseq() uint64 {
	s.bseq++
	return s.bseq
}

// noteWait records a wait for the slot and logs it when it ran past the
// threshold. A wait that gave up is recorded and logged too, with why: a
// list page that timed out after 2 s is the case the log exists for.
func (s *readSlot) noteWait(who string, d time.Duration, err error) {
	storeMax(&s.maxWaitMs, d.Milliseconds())
	if d <= s.threshold {
		return
	}
	if err != nil {
		s.logf("nex-delta: slot wait %dms for %s (gave up: %v)", d.Milliseconds(), who, err)
		return
	}
	s.logf("nex-delta: slot wait %dms for %s", d.Milliseconds(), who)
}

// noteHold records how long a read held the slot (failed reads included —
// they held it all the same) and logs it when it ran past the threshold.
func (s *readSlot) noteHold(who string, d time.Duration) {
	storeMax(&s.maxHoldMs, d.Milliseconds())
	if d > s.threshold {
		s.logf("nex-delta: slot held %dms by %s", d.Milliseconds(), who)
	}
}

// maxima reports the longest hold and the longest wait recorded since the
// slot was built, in ms.
func (s *readSlot) maxima() (holdMs, waitMs int64) {
	return s.maxHoldMs.Load(), s.maxWaitMs.Load()
}

// storeMax raises a to v if v is larger, losing no concurrent update.
func storeMax(a *atomic.Int64, v int64) {
	for {
		cur := a.Load()
		if v <= cur || a.CompareAndSwap(cur, v) {
			return
		}
	}
}
