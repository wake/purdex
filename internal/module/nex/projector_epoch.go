package nex

import (
	"context"

	"github.com/wake/purdex/internal/core"
)

// Epochs (spec 2026-10-08 §3.5, §3.6, §8 R3-2).
//
// An epoch names one unbroken stream of deltas: bseq counts from 1 within
// it, and a client that saw every delta of it holds every change. A new
// epoch starts when that can no longer be promised — the projector lost
// bus frames (its subscription closed and was replaced, projector_bus.go)
// or bseq ran out — and every client that opted in is told with a hello
// {"epoch": E, "bseq": 0}. A hello of a new epoch makes a client reconcile:
// it re-reads the list, and pages read after the hello reflect everything
// the lost frames would have pushed.
//
// The projector needs that baseline too. lastPushed is what the safety
// reconcile (§3.7) compares the list with, to find pushes that went
// missing; after a lost stretch of frames it is stale, and right after the
// daemon starts it is empty. Marking every execution dirty to rebuild it
// would push every row at once, overflowing subscribers that are strict
// about their buffer (§8 R3-2). So an epoch start seeds lastPushed from a
// walk of the list instead, pushing nothing: the clients reconcile against
// list pages of the same epoch, so the baseline they hold is the one
// seeded. The daemon's start is an epoch start too, and seeds the same way.

// requestEpochWork asks the maintenance goroutine to seed lastPushed —
// after starting a new epoch, when rotate is set. Requests coalesce: one
// rotation and one seed after the latest request cover every earlier one,
// as the clients' reconcile and the seed both read pages from after it.
func (p *projector) requestEpochWork(rotate bool) {
	p.mu.Lock()
	p.wantSeed = true
	p.wantRotate = p.wantRotate || rotate
	p.mu.Unlock()
	select {
	case p.maintKick <- struct{}{}:
	default:
	}
}

// takeEpochWork returns the pending epoch work and clears it.
func (p *projector) takeEpochWork() (seed, rotate bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seed, rotate = p.wantSeed, p.wantRotate
	p.wantSeed, p.wantRotate = false, false
	return seed, rotate
}

// maintain is the maintenance goroutine: it does the epoch work asked for,
// one request at a time. It runs apart from the bus consumer (which must
// never wait on the slot, §3.2) and from the flush worker (which must keep
// flushing while a walk is between pages), and it is the only goroutine
// that walks, so walks never overlap.
func (p *projector) maintain() {
	for {
		if seed, rotate := p.takeEpochWork(); seed {
			if rotate && !p.rotateAfterResubscribe() {
				return // stopping
			}
			p.seed()
			continue
		}
		select {
		case <-p.ctx.Done():
			return
		case <-p.maintKick:
		}
	}
}

// rotateAfterResubscribe starts the epoch for a subscription that replaced
// a closed one: under the slot, so no delta is numbered meanwhile, it
// rotates the epoch and sends the hello to every opted-in subscriber. It
// waits for the slot as the flush worker does (no bound, Stop ends the
// wait) and reports false only when Stop did.
func (p *projector) rotateAfterResubscribe() bool {
	var epoch string
	err := p.slot.hold(p.ctx, "epoch", 0, func(context.Context) error {
		epoch = p.startEpochLocked()
		return nil
	})
	if err != nil {
		return false // without a maxWait, only the context ends a wait
	}
	p.logf("nex-delta: bus resubscribed; new epoch %s, hello sent to every nex.v1 subscriber", epoch)
	return true
}

// startEpochLocked rotates the slot's epoch (bseq back to 0, ver going on)
// and broadcasts the new epoch's hello, {"epoch": E, "bseq": 0}, to every
// subscriber that opted into nex.v1 — strictly, as a delta is: one that
// cannot take it is disconnected, and its reconnect gets a hello of its
// own. It returns the new epoch. Only a holder of the slot may call it, and
// the hello goes out before the holder releases the slot, so every client
// gets it before any delta or page of the new epoch.
func (p *projector) startEpochLocked() string {
	epoch := p.slot.rotateEpoch()
	value, _ := encodeValue(helloValue{Epoch: epoch}) // a string and a number: cannot fail
	p.events.BroadcastStrictTo(core.FeatureNexV1, core.HostEvent{Type: helloEventType, Value: value})
	return epoch
}

// seed walks the list and records every row as lastPushed (seedPage),
// pushing nothing. A seed that stops early (a failed page, the page cap)
// keeps the pages it read and logs why; executions it did not reach keep
// whatever entry they had. Its pages wait for the slot without a bound, as
// the flush worker does: it runs in the maintenance goroutine, and Stop
// ends the wait.
func (p *projector) seed() {
	defer p.seeds.Add(1)
	prev := ""
	err := p.walk(p.ctx, "seed", 0, func(pg walkPage) error {
		p.seedPage(pg, prev)
		prev = pg.upTo
		return nil
	})
	if err != nil && p.ctx.Err() == nil {
		p.logf("nex-delta: seeding lastPushed stopped: %v", err)
	}
}

// seedPage records one walked page as what the clients hold, page by page
// under ver:
//   - a listed row becomes its execution's entry, with the page's ver —
//     unless the entry is newer than the page (a delta read after the page,
//     flushed between it and now), which stays;
//   - an entry the page covers but does not list (the execution was
//     archived or is gone) is dropped when it is older than the page: no
//     client holds a row for it either, and an entry left saying otherwise
//     would read as a missed push.
//
// Entries outside the page's range are not the page's to judge.
func (p *projector) seedPage(pg walkPage, prevUpTo string) {
	listed := make(map[string]bool, len(pg.rows))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range pg.rows {
		listed[r.id] = true
		if cur, ok := p.pushed[r.id]; ok && cur.ver > pg.ver {
			continue
		}
		p.pushed[r.id] = pushedRow{ver: pg.ver, digest: r.digest}
	}
	for id, cur := range p.pushed {
		if !listed[id] && pg.covers(prevUpTo, id) && cur.ver < pg.ver {
			delete(p.pushed, id)
		}
	}
}
