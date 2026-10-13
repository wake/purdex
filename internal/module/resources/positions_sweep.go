package resourcesmod

import (
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The sweeper's part of amendment A: the windows of an offered or idle position
// (§3.1, plan §2.8), the liveness of a position nobody polls (F4), and the cost
// of the windows while someone waits (#10). All of it runs under stateMu, in
// lease mode, from sweepLeases.

// sessionSwap counts, per position, the consecutive snapshots in which the
// position's agent pid is listed under another session id (a /clear keeps the
// pid and changes the session).
type sessionSwap struct {
	count    int
	lastSeen time.Time // the sampled_at of the last snapshot counted
}

// windowCost is the time a position held the host in a window while someone
// waited, not yet written (flushed when the position leaves the state).
type windowCost struct {
	state    string
	lastTick int64
	ms       int64
}

// anyLivePosition says whether a position that has not ended exists: a hint that
// decides whether the process table is worth reading.
func (m *Module) anyLivePosition() bool {
	ps, err := m.store.LivePositions()
	return err == nil && len(ps) > 0
}

// agentGone says whether the position's agent process is gone, or has been listed
// under another session in two consecutive snapshots (the /clear case, Q9). An
// unreadable view, an unknown agent, or a position made after the view was read
// count as alive. stateMu is held.
func (m *Module) agentGone(p positionRow, view procView, viewAt time.Time) bool {
	if p.AgentPID <= 0 {
		return false
	}
	if view != nil && p.CreatedAt < viewAt.UnixMilli() &&
		holderGone(view, leaseRow{HolderPID: p.AgentPID, HolderStart: p.AgentStart}) {
		return true
	}
	snap := m.latest.Load()
	if snap == nil || !snap.Available {
		return false
	}
	other := false
	for _, s := range snap.Sessions {
		if s.PID == p.AgentPID && s.SessionID != p.SessionID {
			other = true
		}
	}
	if m.swaps == nil {
		m.swaps = map[string]*sessionSwap{}
	}
	st := m.swaps[p.ID]
	switch {
	case !other:
		delete(m.swaps, p.ID)
		return false
	case st == nil:
		m.swaps[p.ID] = &sessionSwap{count: 1, lastSeen: snap.SampledAt}
		return false
	case snap.SampledAt.After(st.lastSeen):
		st.count++
		st.lastSeen = snap.SampledAt
	}
	return st.count >= 2
}

// sweepPositions ends what is overdue among the live positions and counts the
// cost of the windows. It fills m.goneAgents for the held-row ends that follow.
func (m *Module) sweepPositions(now time.Time, view procView, viewAt time.Time) {
	nowMS := now.UnixMilli()
	m.goneAgents = map[string]bool{}
	positions, err := m.store.LivePositions()
	if err != nil {
		m.logf("[resources] sweeper: list positions: %v", err)
		return
	}
	live := make(map[string]bool, len(positions))
	for _, p := range positions {
		live[p.ID] = true
		if m.agentGone(p, view, viewAt) {
			m.goneAgents[p.ID] = true
		}
	}
	for id := range m.swaps {
		if !live[id] {
			delete(m.swaps, id)
		}
	}
	for id := range m.costs {
		if !live[id] {
			m.flushWindowCost(id) // a position that ended some way the sweeper did not see
		}
	}
	for _, p := range positions {
		m.accrueWindowCost(p, nowMS)
		switch {
		case m.goneAgents[p.ID] && p.State != resources.PosRunning:
			m.endPosition(p, nowMS, endPosition{Reason: resources.EndAbandoned,
				From: []string{resources.PosQueued, resources.PosOffered, resources.PosIdle}})
		case p.State == resources.PosOffered && p.DeliveredAt > 0 && p.WindowUntil <= nowMS:
			// The window counts from delivery (Q18): a delivered offer nobody used lapses.
			m.endPosition(p, nowMS, endPosition{Reason: resources.EndNoAnswer, From: []string{resources.PosOffered},
				WindowGuard: p.WindowUntil, Notice: m.cancelNotice(p)})
		case p.State == resources.PosOffered && p.DeliveredAt == 0 && nowMS >= p.OfferedAt+resources.PositionWindow.Milliseconds():
			// Never delivered in 3 minutes: the offer passes to the next waiter, the
			// position keeps its place and is not ended (P15).
			won, err := m.store.PassOffer(p.ID, p.OfferedAt, nowMS)
			if err != nil {
				m.logf("[resources] sweeper: pass offer %s: %v", p.ID, err)
			} else if won {
				m.flushWindowCost(p.ID) // charged up to this moment; the position no longer holds the host
				m.logf("[resources] position %s: the offer was not delivered in time and passes", p.ID)
				m.wake()
			}
		case p.State == resources.PosIdle && p.WindowUntil <= nowMS:
			m.endPosition(p, nowMS, endPosition{Reason: resources.EndIdleTimeout, AskedReason: resources.EndNoAnswer,
				From: []string{resources.PosIdle}, WindowGuard: p.WindowUntil, Notice: m.cancelNotice(p)})
		}
	}
	m.writePendingCosts() // once per sweep, after every settlement of this tick
}

// endPosition ends a position through the store and logs a win.
func (m *Module) endPosition(p positionRow, nowMS int64, o endPosition) {
	won, err := m.store.EndPositionWithRows(p.ID, nowMS, o)
	switch {
	case err != nil:
		m.logf("[resources] sweeper: end position %s (%s): %v", p.ID, o.Reason, err)
	case won:
		m.flushWindowCost(p.ID) // only a win settles the cost: a failed end is tried again
		m.logf("[resources] position %s ended: %s", p.ID, o.Reason)
		m.wake()
	}
}

// cancelNotice is the notice slot a lapsing window arms (Q7): only when the
// session has something pending, an asked ask row it did not answer or a named
// queue row it did not re-run.
func (m *Module) cancelNotice(p positionRow) string {
	rows, err := m.store.RowsOf(p.ID)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.State == resources.StateWaiting && r.OfferedAt > 0 {
			return "cancelled"
		}
	}
	return ""
}

// accrueWindowCost adds the time since the last tick to the position's window
// cost when it is offered or idle and someone waits; it writes the total when the
// position leaves the state (#10: the cost of V16 / V17).
func (m *Module) accrueWindowCost(p positionRow, nowMS int64) {
	if m.costs == nil {
		m.costs = map[string]*windowCost{}
	}
	c := m.costs[p.ID]
	if c != nil && c.state != p.State {
		// The position left the state since the last tick: count up to the moment
		// it did where that is known (the first start of an offered position), else
		// up to this tick, so the time between the last tick and the change is not lost.
		end := nowMS
		if c.state == resources.PosOffered && p.FirstStartedAt >= c.lastTick && p.FirstStartedAt < nowMS {
			end = p.FirstStartedAt
		}
		if waiting, err := m.someoneWaitingFor(p.ID, nowMS); err == nil && waiting {
			c.ms += max(0, end-c.lastTick)
		}
		m.flushWindowCost(p.ID)
		c = nil
	}
	if p.State != resources.PosOffered && p.State != resources.PosIdle {
		return
	}
	if c == nil {
		c = &windowCost{state: p.State, lastTick: nowMS}
		switch {
		case p.State == resources.PosOffered && p.OfferedAt > 0:
			c.lastTick = p.OfferedAt // the host has been reserved since the offer
		case p.State == resources.PosIdle && p.WindowUntil > 0:
			c.lastTick = p.WindowUntil - resources.PositionWindow.Milliseconds() // since the command ended
		}
		m.costs[p.ID] = c
	}
	waiting, err := m.someoneWaitingFor(p.ID, nowMS)
	if err == nil && waiting {
		end := nowMS
		if p.WindowUntil > 0 && p.WindowUntil < end {
			end = p.WindowUntil // the window ended before this tick noticed
		}
		c.ms += max(0, end-c.lastTick)
	}
	c.lastTick = nowMS
}

// pendingCost is a window cost that is final but not yet written.
type pendingCost struct {
	id, state string
	ms        int64
}

// flushWindowCost settles the cost kept in memory for a position: it is final,
// and is written at the end of this sweep or, if the write fails, on a later one.
func (m *Module) flushWindowCost(id string) {
	c := m.costs[id]
	if c == nil {
		return
	}
	delete(m.costs, id)
	if c.ms == 0 {
		return
	}
	// Merged by position and state, and capped (maxPendingCosts) if the writes keep
	// failing.
	for i := range m.pendingCosts {
		if m.pendingCosts[i].id == id && m.pendingCosts[i].state == c.state {
			m.pendingCosts[i].ms += c.ms
			return
		}
	}
	m.pendingCosts = append(m.pendingCosts, pendingCost{id: id, state: c.state, ms: c.ms})
	if len(m.pendingCosts) > maxPendingCosts {
		// The store has refused writes for a long time: the cost is only a report, so
		// the oldest are given up rather than letting the list grow without bound.
		m.logf("[resources] sweeper: dropping the window cost of %s: %d writes pending", m.pendingCosts[0].id, len(m.pendingCosts))
		m.pendingCosts = m.pendingCosts[1:]
	}
}

// maxPendingCosts bounds the window costs waiting for a write that keeps failing.
const maxPendingCosts = 64

// writePendingCosts writes the settled costs, once per sweep; one whose write
// fails stays for the next tick, one that was written is gone, so nothing is
// lost or written twice.
func (m *Module) writePendingCosts() {
	keep := m.pendingCosts[:0]
	for _, pc := range m.pendingCosts {
		var offer, idle int64
		if pc.state == resources.PosOffered {
			offer = pc.ms
		} else {
			idle = pc.ms
		}
		if err := m.store.AddWindowWait(pc.id, offer, idle); err != nil {
			m.logf("[resources] sweeper: window cost of %s: %v (kept, tried again next tick)", pc.id, err)
			keep = append(keep, pc)
		}
	}
	m.pendingCosts = keep
}
