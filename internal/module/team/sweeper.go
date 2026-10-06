package teammod

import (
	"time"

	"github.com/wake/purdex/internal/team"
)

// sweepInterval is the sweeper's tick; livenessEvery is the tick multiple
// on which it also asks the origin resolver whether each requesting
// session is still alive (PD5: a registry read forks ps per entry, so not
// every second).
const (
	sweepInterval = time.Second
	livenessEvery = 10
)

// runSweeper ticks until Stop.
func (m *Module) runSweeper() {
	defer m.sweepWG.Done()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.tick()
		}
	}
}

// tick closes what is overdue (spec §6.2, §9.2): a passed deadline is a
// timeout (U7), an expired lease is an abandonment, and — every
// livenessEvery-th tick, only while something is open — a vanished origin
// session is one too. A resolver that cannot read the registry answers
// "live" (peers/origin_resolver.go), so a read error never abandons
// anything. Each close goes through closeAs, so it competes fairly with
// decide and cancel and broadcasts once.
func (m *Module) tick() {
	m.tickN++
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] sweep: %v", err)
		return
	}
	if len(open) == 0 {
		return
	}
	now := m.now()
	checkLive := m.tickN%livenessEvery == 0
	for _, a := range open {
		var state team.State
		switch {
		case a.DeadlineAt <= now:
			state = team.StateTimeout
		case a.LeaseUntil <= now:
			state = team.StateAbandoned
		case checkLive && !m.origins.LiveSession(a.Origin.SessionID):
			state = team.StateAbandoned
		default:
			continue
		}
		after, won, err := m.closeAs(a.ID, Close{State: state, DecidedAt: now})
		if err != nil {
			m.logf("[team] sweep %s: %v", a.ID, err)
			continue
		}
		if won {
			m.logf("[team] approval %s %s by the sweeper (origin %s)", a.ID, after.State, a.Origin.Ref)
		}
	}
}
