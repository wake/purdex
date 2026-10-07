package nex

import "time"

// The terminal recheck (spec 2026-10-08 §3.7, first bullet).
//
// Nexen emits execution.terminal and only afterwards settles the row from
// running to idle (SettleIdle), silently (§1 F2, nexen#162). A flush
// triggered by the terminal can therefore read the row while it still says
// running, and no event follows to make it read again: the client would
// keep a running row until the execution next changes. So when a flush
// whose cause contains execution.terminal reads a running row, the
// projector reads it again at +150 ms, +600 ms and +2 s after that read,
// and stops at the first read showing anything but running — whatever
// triggered that read.

// recheckOffsets are the recheck times, counted from the read that found
// a terminal execution still running.
var recheckOffsets = []time.Duration{150 * time.Millisecond, 600 * time.Millisecond, 2 * time.Second}

// recheckState is one execution's recheck in progress.
type recheckState struct {
	base time.Time // the read that found it terminal but running
	next int       // index of the offset scheduled next
}

// recheckAfter decides, after a delta was pushed for id from batch b,
// whether id needs another look:
//   - the row is not running (or is gone): any recheck ends;
//   - b carried execution.terminal and the row is running: a recheck
//     (re)starts, the first one due at the first offset;
//   - b was a recheck and the row is still running: the next offset is
//     scheduled, or the recheck ends after the last;
//   - otherwise (an ordinary read of a running row): a recheck already
//     scheduled stays as it is.
//
// A scheduled recheck is a re-mark in id's pending batch, so it never
// needs cancelling on its own: any event for id merges into that same
// batch, whose read then counts as the recheck (b.recheck) — earlier than
// scheduled, never later. Ending a recheck is dropping its state; there is
// no other pending batch for id to drop, since there is one flush worker
// and this runs in it before it pops the next batch.
func (p *projector) recheckAfter(id string, b *dirtyExec, pushed pushedRow) {
	running := !pushed.removed && pushed.digest.State == "running"
	_, terminal := b.cause["execution.terminal"]
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	rs := p.rechecks[id]
	switch {
	case !running:
		delete(p.rechecks, id)
		return
	case terminal:
		rs = &recheckState{base: now}
		p.rechecks[id] = rs
	case b.recheck && rs != nil:
		rs.next++
	default:
		return
	}
	if rs.next >= len(p.timing.recheck) {
		delete(p.rechecks, id)
		return
	}
	p.markLocked(id, mark{at: rs.base.Add(p.timing.recheck[rs.next]), reMark: true, recheck: true})
	p.wake()
}
