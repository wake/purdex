package nex

import (
	"github.com/wake/purdex/internal/core"
)

// Epochs (spec 2026-10-08 §3.5, §3.6).
//
// An epoch names one unbroken stream of deltas: bseq counts from 1 within
// it, and a client that saw every delta of it holds every change. A new
// epoch starts when that can no longer be promised — here, when bseq runs
// out (projector.push) — and every client that opted in is told with a
// hello {"epoch": E, "bseq": 0}. A hello of a new epoch makes a client
// reconcile: it re-reads the list, and pages read after the hello reflect
// every change.

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
