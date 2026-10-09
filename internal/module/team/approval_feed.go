package teammod

import (
	"strings"
	"sync"

	"github.com/wake/purdex/internal/team"
)

// sessionSub is one SubscribeSession subscription, kept under eventMu.
type sessionSub struct {
	sessionID string
	fn        func(op string, a team.Approval)
}

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
		m.sessionSubs = map[uint64]sessionSub{}
	}
	m.sessionSubs[id] = sessionSub{sessionID: sessionID, fn: fn}

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			m.eventMu.Lock()
			defer m.eventMu.Unlock()
			delete(m.sessionSubs, id)
		})
	}
	return open, cancel, nil
}

// deliverToSessionSubs hands one op to the subscriptions of the approval's session. The caller holds eventMu.
func (m *Module) deliverToSessionSubs(op string, a *team.Approval) {
	for _, s := range m.sessionSubs {
		if sameSession(a.Origin.SessionID, s.sessionID) {
			s.fn(op, *a)
		}
	}
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

func sameSession(a, b string) bool { return a != "" && strings.EqualFold(a, b) }

var _ team.ApprovalFeed = (*Module)(nil)
