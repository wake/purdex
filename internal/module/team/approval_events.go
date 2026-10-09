package teammod

import (
	"sync"

	"github.com/wake/purdex/internal/team"
)

// approvalSubQueue bounds each SubscribeApprovals subscriber's queue. Approval events are rare; a subscriber that falls
// this far behind is stuck, and its overflow is dropped (counted) rather than allowed to slow the approval paths.
const approvalSubQueue = 256

type approvalEvent struct {
	op string
	a  team.Approval
}

type approvalSub struct {
	ch   chan approvalEvent
	stop chan struct{}
}

// SubscribeApprovals implements team.ApprovalEvents. The open list and the arming are one step under eventMu - the lock
// every opened / closed broadcast holds - so an approval that closes during the call is in the list with its closed op
// to follow, or is already out of it. fn itself runs on the subscriber's own goroutine, never under eventMu.
func (m *Module) SubscribeApprovals(fn func(op string, a team.Approval)) (open []team.Approval, unsubscribe func()) {
	m.eventMu.Lock()
	all, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] SubscribeApprovals: could not read the open set: %v", err)
		all = nil
	}
	if m.afterSnapshotRead != nil {
		m.afterSnapshotRead()
	}
	open = append([]team.Approval{}, all...)
	sub := &approvalSub{ch: make(chan approvalEvent, approvalSubQueue), stop: make(chan struct{})}
	m.nextApprovalID++
	id := m.nextApprovalID
	if m.approvalSubs == nil {
		m.approvalSubs = map[uint64]*approvalSub{}
	}
	m.approvalSubs[id] = sub
	m.eventMu.Unlock()

	go m.runApprovalSub(sub, fn)

	var once sync.Once
	unsubscribe = func() {
		once.Do(func() {
			m.eventMu.Lock()
			if _, ok := m.approvalSubs[id]; ok {
				delete(m.approvalSubs, id)
				close(sub.stop)
			}
			m.eventMu.Unlock()
		})
	}
	return open, unsubscribe
}

func (m *Module) runApprovalSub(sub *approvalSub, fn func(string, team.Approval)) {
	stopped := func() bool {
		select {
		case <-sub.stop:
			return true
		default:
			return false
		}
	}
	// The checks below are not one atomic step with the call: a callback already taken off the queue when unsubscribe is
	// called may still run once (see team.ApprovalEvents). Making unsubscribe wait for it would deadlock a callback that
	// unsubscribes itself, which the interface allows.
	for {
		// Stopping wins over a waiting event: a select with both ready picks at random, and unsubscribe must mean that
		// nothing already queued is delivered after it.
		if stopped() {
			return
		}
		select {
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			if stopped() {
				return
			}
			m.callApprovalSub(fn, ev)
		}
	}
}

// callApprovalSub keeps a panicking consumer from ending its own stream (or the process).
func (m *Module) callApprovalSub(fn func(string, team.Approval), ev approvalEvent) {
	defer func() {
		if r := recover(); r != nil {
			m.logf("[team] approval subscriber panicked (%v); the stream goes on", r)
		}
	}()
	fn(ev.op, ev.a)
}

// publishApprovalEvent queues one op to every SubscribeApprovals subscriber without ever blocking. The caller holds
// eventMu - allowed here because each send is non-blocking and the consumers run elsewhere.
func (m *Module) publishApprovalEvent(op string, a *team.Approval) {
	for _, sub := range m.approvalSubs {
		select {
		case sub.ch <- approvalEvent{op: op, a: *a}:
		default:
			m.approvalDrops.Add(1)
		}
	}
}

// ApprovalEventDrops is how many ops a full subscriber queue refused.
func (m *Module) ApprovalEventDrops() int64 { return m.approvalDrops.Load() }

// dropApprovalSubs ends every subscriber (Stop).
func (m *Module) dropApprovalSubs() {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	for id, sub := range m.approvalSubs {
		delete(m.approvalSubs, id)
		close(sub.stop)
	}
}

var _ team.ApprovalEvents = (*Module)(nil)
