package teammod

import (
	"errors"
	"sync"

	"github.com/wake/purdex/internal/team"
)

// maxSessionSubs bounds the SubscribeSession subscriptions of the whole host: one per open conversation stream,
// each pinned by a cache entry (at most 16) in the one caller there is today, so the bound is far above any real use
// and only stops a leak or a flood from growing the table without end.
const maxSessionSubs = 256

// ErrTooManySubscriptions is returned by SubscribeSession at the bound.
var ErrTooManySubscriptions = errors.New("team: too many session subscriptions")

// SubscribeSession implements team.ApprovalFeed: the open approvals whose origin is sessionID, and fn armed for every
// later opened / closed op of that session. The read of the open set and the arming happen under eventMu — the lock
// every opened / closed broadcast holds — so an approval that closes during the call is in the list and its closed op
// follows through fn, or is already out of the list: it is never listed open without a closed to follow (the
// guarantee the host-events approval.request snapshot gives, spec §6.2).
//
// fn runs under eventMu, on the goroutine that opened or closed the approval: it must only enqueue and return, and
// must never call back into the team module (including cancel). The filter is Origin.SessionID only: after a relay
// the successor has a new session id and its own subscription; nothing is forwarded across the lineage.
func (m *Module) SubscribeSession(sessionID string, fn func(op string, a team.Approval)) (open []team.Approval, cancel func(), err error) {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	if m.sessionSubN >= maxSessionSubs {
		return nil, nil, ErrTooManySubscriptions
	}
	all, err := m.store.ListOpen()
	if err != nil {
		return nil, nil, err
	}
	if m.afterSnapshotRead != nil {
		m.afterSnapshotRead()
	}
	open = []team.Approval{}
	for _, a := range all {
		if sameSession(a.Origin.SessionID, sessionID) {
			open = append(open, a)
		}
	}
	m.nextSessionSub++
	id := m.nextSessionSub
	if m.sessionSubs == nil {
		m.sessionSubs = map[string]map[uint64]func(string, team.Approval){}
	}
	if m.sessionSubs[sessionID] == nil {
		m.sessionSubs[sessionID] = map[uint64]func(string, team.Approval){}
	}
	m.sessionSubs[sessionID][id] = fn
	m.sessionSubN++

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			m.eventMu.Lock()
			defer m.eventMu.Unlock()
			m.removeSessionSub(sessionID, id)
		})
	}
	return open, cancel, nil
}

// removeSessionSub drops one subscription if it is still there. The caller holds eventMu.
func (m *Module) removeSessionSub(sessionID string, id uint64) {
	subs := m.sessionSubs[sessionID]
	if _, ok := subs[id]; !ok {
		return
	}
	delete(subs, id)
	m.sessionSubN--
	if len(subs) == 0 {
		delete(m.sessionSubs, sessionID)
	}
}

// dropSessionSubs ends every subscription (Stop): nothing is delivered afterwards, and a cancel that comes later is
// a no-op.
func (m *Module) dropSessionSubs() {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	m.sessionSubs, m.sessionSubN = nil, 0
}

// deliverToSessionSubs hands one op to the subscriptions of the approval's session (an index lookup, so the cost does
// not grow with other sessions' subscriptions). The caller holds eventMu. A callback that panics is removed and the
// others still get the op: a failing consumer must not take the host's approval stream down with it.
func (m *Module) deliverToSessionSubs(op string, a *team.Approval) {
	if a.Origin.SessionID == "" {
		return
	}
	for id, fn := range m.sessionSubs[a.Origin.SessionID] {
		m.callSub(a.Origin.SessionID, id, fn, op, *a)
	}
}

func (m *Module) callSub(sessionID string, id uint64, fn func(string, team.Approval), op string, a team.Approval) {
	defer func() {
		if r := recover(); r != nil {
			m.logf("[team] session subscription callback panicked (%v); subscription removed", r)
			m.removeSessionSub(sessionID, id)
		}
	}()
	fn(op, a)
}

// HoldResponder implements team.ApprovalFeed: one more remote responder is held until release, with exactly the effect
// a connected /ws/host-events client has — terminal-only rows are created and can be answered remotely. release is
// idempotent: calling it again never drives the count below what other holders keep.
func (m *Module) HoldResponder() (release func()) {
	m.responderHolds.Add(1)
	var once sync.Once
	return func() { once.Do(func() { m.responderHolds.Add(-1) }) }
}

// anyResponder is whether anyone remote could answer a terminal-only row now: a connected host-events subscriber (or
// whatever m.responders adds) or a held responder.
func (m *Module) anyResponder() bool {
	return m.responderHolds.Load() > 0 || m.responders.Any()
}

func sameSession(a, b string) bool { return a != "" && a == b } // exact: session ids are opaque

var _ team.ApprovalFeed = (*Module)(nil)
