package nex

import (
	"context"
	"time"

	"lab.protype.tw/wake/nexen/bus"
)

// The projector's input: one subscription to the engine's bus (spec
// 2026-10-08 §3.2), drained by the consumer goroutine, which turns each
// trigger frame into a mark (markFrame). The consumer never waits on the
// read slot: a consumer that fell behind would be kicked off the bus (a full
// channel drops its subscriber), so per frame it does no more than a kind
// lookup and, for a trigger, a map update under a short lock.
//
// A subscription that closes while the projector runs — the bus kicked the
// consumer, or the bus itself closed — is replaced (§3.6): back off,
// subscribe again, and once the new subscription is registered start a new
// epoch, whose hello makes every client reconcile against list pages read
// after it existed. Whatever was published between the old subscription's
// end and the new one's start is lost to the projector, and covered that
// way.

const (
	// projectorBusBuffer is the subscription's channel capacity (§3.2). The
	// consumer only marks, so it drains far faster than the engine
	// publishes — a commit publishes a raw frame plus the few events derived
	// from it — and the buffer covers a scheduling hiccup, not a slow
	// consumer.
	projectorBusBuffer = 1024

	// The wait before each new Subscribe after the subscription closed
	// (§3.6): 100 ms, doubling up to 5 s. A kicked consumer is back within
	// 100 ms; a closed bus, which answers every Subscribe with a
	// subscription that is already over, costs one attempt every 5 s until
	// Stop instead of a spin.
	resubscribeBackoffMin = 100 * time.Millisecond
	resubscribeBackoffMax = 5 * time.Second
)

// triggerKinds are the frame kinds that mark their execution dirty (§3.2):
// the kinds whose writes can change what a list row shows. Lease writes bump
// updated_at; observer counts, a pending permission and the tool/task
// rollups are row fields; result carries the cost rollup. It is an
// allowlist on purpose: the raw provider frames (assistant, user, system,
// rate_limit_event, control_*) are an open set, and stream_event /
// stream_snapshot carry tokens — none of them changes a row, and a kind
// Nexen adds later is noise until someone decides it does.
var triggerKinds = map[string]bool{
	"execution.delegated": true, "execution.rejected": true, "execution.running": true,
	"execution.terminal": true, "execution.interrupted": true, "execution.error": true,
	"execution.message_accepted": true, "execution.interrupt_requested": true,
	// The peer equivalent of message_accepted (Nexen v0.20.0): a turn a
	// peer creates publishes peer_message instead of it, and changes the
	// same row fields (turn_count).
	"peer_message":           true,
	"execution.turn_stalled": true, "execution.turn_orphaned": true, "execution.terminated": true,
	"execution.archived": true, "execution.unarchived": true, "execution.title_changed": true,
	"execution.observer_attached": true, "execution.observer_detached": true,
	"execution.credential_repaired": true,
	"permission.requested":          true, "permission.resolved": true,
	"tool_use": true, "tool_result": true, "task_start": true, "task_end": true, "result": true,
	"lease.acquired": true, "lease.released": true, "lease.renewed": true,
}

// frameBus is the slice of Nexen's *bus.Bus (System.Bus, §1 F5) the
// projector uses; tests hand it a bus of their own.
type frameBus interface {
	Subscribe(execID string, buf int) *bus.Subscription
	Unsubscribe(s *bus.Subscription)
}

var _ frameBus = (*bus.Bus)(nil)

// subscribe registers the projector on every execution's frames. start
// calls it before any goroutine runs, so no frame published after start
// returns is missed. The subscription's Snapshot (in-progress partial
// messages) is ignored: it never changes a row.
func (p *projector) subscribe() {
	p.setSubscription(p.bus.Subscribe("", projectorBusBuffer))
}

// subscription is the live subscription.
func (p *projector) subscription() *bus.Subscription {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sub
}

func (p *projector) setSubscription(s *bus.Subscription) {
	p.mu.Lock()
	p.sub = s
	p.mu.Unlock()
}

// consume is the bus consumer goroutine. The flush worker runs on whatever
// happens to the subscription, so what was marked still flushes while the
// consumer resubscribes.
func (p *projector) consume() {
	sub := p.subscription()
	for {
		select {
		case <-p.ctx.Done():
			p.bus.Unsubscribe(sub)
			return
		case f, ok := <-sub.Ch:
			if ok {
				p.observe(f)
				continue
			}
			if sub = p.resubscribe(); sub == nil {
				return // stopping
			}
		}
	}
}

// resubscribe replaces a subscription that closed while the projector runs
// (§3.6) — the bus kicked the consumer for falling behind, or the engine
// closed the bus before Stop reached the projector — and returns the new
// one, or nil once the projector is stopping.
//
// It backs off before every attempt (backoffMin, doubling to backoffMax;
// Stop ends a wait at once) and checks what Subscribe handed back before
// using it: a closed bus answers with a subscription that is already over,
// and starting an epoch for that would send every client a hello — and a
// reconcile — for nothing, every few seconds until Stop. A receive that
// does not block tells the cases apart: closed means try again later; a
// frame means the subscription is live, and the frame is kept (observed
// right after); nothing yet means live too.
//
// A live subscription becomes the projector's, then the maintenance
// goroutine is asked to start the new epoch and seed (projector_epoch.go).
// The consumer does not wait for either — both take the slot, and the
// consumer never waits on the slot (§3.2): it goes straight back to
// draining the new subscription. A delta flushed before the new epoch
// starts still goes out in the old one, contiguous; the hello that follows
// is what sends every client to reconcile.
func (p *projector) resubscribe() *bus.Subscription {
	if p.ctx.Err() != nil {
		return nil // stop is under way: the closing is ours
	}
	p.logf("nex-delta: bus subscription closed; resubscribing")
	delay := p.timing.backoffMin
	for {
		t := time.NewTimer(delay)
		select {
		case <-p.ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		delay = min(2*delay, p.timing.backoffMax)

		sub := p.bus.Subscribe("", projectorBusBuffer)
		var first *bus.Frame
		select {
		case f, ok := <-sub.Ch:
			if !ok {
				continue // over before it started: the bus is closed
			}
			first = &f
		default:
		}
		p.setSubscription(sub)
		p.requestEpochWork(true)
		if first != nil {
			p.observe(*first)
		}
		return sub
	}
}

// observe marks a trigger frame's execution dirty. A frame with no
// execution, a non-trigger kind, or an id the row reader would refuse
// (§3.3's pattern) marks nothing.
func (p *projector) observe(f bus.Frame) {
	if f.ExecutionID == "" || !triggerKinds[f.Kind] {
		return
	}
	if !executionIDPattern.MatchString(f.ExecutionID) {
		p.logf("nex-delta: ignoring %s for an invalid execution id %q", f.Kind, f.ExecutionID)
		return
	}
	p.markFrame(f.ExecutionID, f.Kind)
}

// startProjector starts the projector when there is something to project
// from and to: an engine bus (a fake engine has none) and the core's events
// broadcaster. Start calls it after a successful Init. The projector reads
// through the module's one read slot (reads), the list wrapper's, so every
// delta is ordered against every list page.
//
// Only a running projector registers the hello (projector_hello.go) as an
// OnSubscribe callback: without it no delta will ever come, and a client
// that never gets a hello stays on its legacy path. Only subscribers that
// opted into nex.v1 get a hello or a delta; every other one never sees a
// nex frame. The core has no way to unregister a callback; after stop the
// hello is a no-op.
func (m *Module) startProjector() {
	if m.sys.bus == nil || m.core == nil || m.core.Events == nil {
		return
	}
	logf := func(format string, args ...any) { m.logf(format, args...) }
	p := newProjector(m.reads(), rowReader{handler: m.sys.handler, logf: logf}, m.core.Events, m.sys.bus, logf, m.projTiming)
	m.projMu.Lock()
	m.proj = p
	m.projMu.Unlock()
	p.start()
	m.core.Events.OnSubscribe(p.sendHello)
}

// stopProjector stops a running projector (see projector.stop), once. Stop
// calls it before the engine drains, and Close before the store closes, so
// the projector never reads from an engine on its way down.
func (m *Module) stopProjector(ctx context.Context) {
	m.projMu.Lock()
	p := m.proj
	m.proj = nil
	m.projMu.Unlock()
	if p != nil {
		p.stop(ctx)
	}
}
