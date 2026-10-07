package nex

import (
	"context"

	"lab.protype.tw/wake/nexen/bus"
)

// The projector's input: one subscription to the engine's bus (spec
// 2026-10-08 §3.2), drained by the consumer goroutine, which turns each
// trigger frame into a mark (markFrame). The consumer never waits on the
// read slot: a consumer that fell behind would be kicked off the bus (a full
// channel drops its subscriber), so per frame it does no more than a kind
// lookup and, for a trigger, a map update under a short lock.

// projectorBusBuffer is the subscription's channel capacity (§3.2). The
// consumer only marks, so it drains far faster than the engine publishes —
// a commit publishes a raw frame plus the few events derived from it — and
// the buffer covers a scheduling hiccup, not a slow consumer.
const projectorBusBuffer = 1024

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
	p.sub = p.bus.Subscribe("", projectorBusBuffer)
}

// consume is the bus consumer goroutine.
func (p *projector) consume() {
	for {
		select {
		case <-p.ctx.Done():
			p.bus.Unsubscribe(p.sub)
			return
		case f, ok := <-p.sub.Ch:
			if !ok {
				p.busClosed()
				return
			}
			p.observe(f)
		}
	}
}

// busClosed handles the subscription's channel closing while the projector
// runs: the bus kicked the consumer for falling behind, or the engine closed
// the bus before Stop reached the projector. Deltas stop until the daemon
// restarts; a client holding rows keeps them, unrefreshed. The flush worker
// keeps running, so what was already marked still flushes.
//
// TODO(PR1c, spec §3.6): back off (100 ms doubling to 5 s), subscribe again,
// then — under the slot — start a new epoch with bseq 0, send every client a
// hello and mark every execution in pushed dirty, so each client reconciles
// against pages read after the new subscription existed.
func (p *projector) busClosed() {
	if p.ctx.Err() != nil {
		return // stop is under way: the closing is ours
	}
	p.logf("nex-delta: bus subscription closed; execution deltas stopped")
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
