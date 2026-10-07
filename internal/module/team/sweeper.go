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
// session is one too. The liveness tick also ends the teams whose lead is
// gone (spec §7.1, endGoneTeams), open approvals or not. A resolver that
// cannot read the registry answers
// "live" (peers/origin_resolver.go), so a read error never abandons
// anything. The deadline and lease paths close through CloseIfExpired:
// the decision here is made on a copy, and a poll that renewed the lease
// since the read must win, so the CAS re-checks the expiry in the same
// statement and a lost CAS leaves the row open for the next tick. Every
// close goes through closeWith, so it competes fairly with decide and
// cancel and broadcasts once.
func (m *Module) tick() {
	m.tickN++
	checkLive := m.tickN%livenessEvery == 0
	if checkLive {
		// Flags outlive their request (spec §6.6): prune them on the same
		// cadence as the liveness check, whether or not anything is open.
		// With no flag on disk this is one ReadDir that answers ENOENT.
		m.pruneHookLocks()
		m.pruneAskFlags()
		m.endGoneTeams()
	}
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] sweep: %v", err)
		return
	}
	if len(open) == 0 {
		return
	}
	if m.afterListOpen != nil {
		m.afterListOpen()
	}
	now := m.now()
	for _, a := range open {
		var after team.Approval
		var won bool
		var err error
		switch {
		case a.DeadlineAt <= now:
			after, won, err = m.closeExpired(a.ID, now, team.StateTimeout)
		case a.LeaseUntil <= now:
			after, won, err = m.closeExpired(a.ID, now, team.StateAbandoned)
		case checkLive && !m.origins.LiveSession(a.Origin.SessionID):
			after, won, err = m.closeAs(a.ID, Close{State: team.StateAbandoned, DecidedAt: now})
		default:
			continue
		}
		if err != nil {
			m.logf("[team] sweep %s: %v", a.ID, err)
			continue
		}
		if won {
			m.logf("[team] approval %s %s by the sweeper (origin %s)", a.ID, after.State, a.Origin.Ref)
		}
	}
}

// closeExpired is the sweeper's close for a passed deadline or lease: the
// store re-checks the expiry at now inside the CAS (CloseIfExpired).
func (m *Module) closeExpired(id string, now int64, state team.State) (team.Approval, bool, error) {
	return m.closeWith(id, func() (team.Approval, bool, error) {
		return m.store.CloseIfExpired(id, now, Close{State: state, DecidedAt: now})
	})
}

// endGoneTeams ends every live team whose lead's conversation ended (spec
// §7.1): its session is gone from the registry and it is not mid-relay.
// The relay guard is what keeps a relay's own /clear from ending the team:
// the old session id leaves the registry about 0.6 s after /clear while
// the op is still written, and the cleared report then moves the lead
// (P4-3). A manual /clear has no op, so it ends the team. The guard lives
// in EndTeam's UPDATE, with the check that the lead is still the one read
// here, so neither a relay claimed nor a lead moved since loses to the
// end. A registry read error answers "live" (peers/origin_resolver.go) and
// a store error skips the team, so neither ever ends one. Members are
// untouched (D4).
func (m *Module) endGoneTeams() {
	teams, err := m.store.ListLiveTeams()
	if err != nil {
		m.logf("[team] sweep teams: %v", err)
		return
	}
	for _, t := range teams {
		if m.origins.LiveSession(t.LeadSessionID) {
			continue
		}
		if m.beforeEndTeam != nil {
			m.beforeEndTeam(t)
		}
		ended, err := m.store.EndTeam(t.ID, t.LeadSessionID, team.TeamEndLeadGone, m.now())
		if err != nil {
			m.logf("[team] sweep team %s: %v", t.ID, err)
			continue
		}
		if ended {
			m.logf("[team] team %s ended (%s): its lead %s (%s) is gone", t.ID, team.TeamEndLeadGone, t.LeadRef, t.LeadSessionID)
		}
	}
}
