package nex

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/core"
)

// The hello (spec 2026-10-08 §3.5): the first nex frame a new /ws/host-events
// subscriber gets, {"epoch": E, "bseq": n}. It is the client's baseline: a
// delta received before it is ignored, the next one must be n+1, anything
// else is a gap that makes the client reconcile.
//
// Why it is sent under the slot: the core registers a subscriber (Add)
// before it runs the OnSubscribe callbacks (§1 F6), so deltas can reach the
// new subscriber before its hello. Every delta is numbered and broadcast
// inside the slot, so a hello that reads bseq inside it splits the
// subscriber's stream cleanly: every delta enqueued to it before the hello
// has bseq ≤ n, and every one after it has bseq n+1, n+2, ….

const (
	helloEventType = "nex.executions.hello"

	// helloSlotWait bounds how long a new connection's callback waits for
	// the slot, as the list wrapper's wait does (listSlotWaitDefault): the
	// longest measured hold is far below it (§3.8), and the core runs the
	// callbacks before the connection's read loop starts.
	helloSlotWait = 2 * time.Second
)

// helloValue is the hello's value. No omitempty: bseq 0 at an epoch's start
// must be spelled out (round 2 #3).
type helloValue struct {
	Epoch string `json:"epoch"`
	Bseq  uint64 `json:"bseq"`
}

// sendHello is the projector's OnSubscribe callback: it holds the slot
// (who "hello", at most helloWait), reads epoch and bseq, and queues the
// hello for sub alone, strictly. A hello that is not queued — sub's buffer
// is full, or the slot stayed busy — closes sub's connection (§3.5, round
// 2 #4): the client reconnects and gets a new hello, rather than running
// without a baseline. A projector that stopped sends nothing: no delta will
// follow, so a baseline would mean nothing.
func (p *projector) sendHello(sub *core.EventSubscriber) {
	if p.ctx.Err() != nil {
		return
	}
	err := p.slot.hold(p.ctx, "hello", p.timing.helloWait, func(context.Context) error {
		st := p.slot.current()
		value, err := encodeValue(helloValue{Epoch: st.Epoch, Bseq: st.Bseq})
		if err != nil {
			return err // cannot happen: a string and a number
		}
		p.events.SendStrict(sub, core.HostEvent{Type: helloEventType, Value: value})
		return nil
	})
	if err == nil || p.ctx.Err() != nil {
		return // sent (or SendStrict already closed sub), or stopping
	}
	p.logf("nex-delta: no hello for a new subscriber (%v); closing the connection so the client reconnects", err)
	p.events.Remove(sub)
}
