package teammod

import (
	"time"

	"github.com/wake/purdex/internal/module/agent"
	peersmod "github.com/wake/purdex/internal/module/peers"
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
// gone (spec §7.1, endGoneTeams), stores the members' and leads' readings
// (persistUsage) and marks gone members (markGoneMembers), open approvals
// or not. A resolver that
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
		m.reconcileAfterBootGrace()
		m.sweepRelayTimeouts()
		m.pruneHookLocks()
		m.pruneAskFlags()
		m.persistUsage()
		m.endGoneTeams()
		m.markGoneMembers()
		m.settleStuckKillingMembers() // a kill that claimed its member and never ended (#2152)
		m.markGoneRemoteMembers()     // sessions here that a lead on another host adopted (X2c)
		m.endUnpairedRemoteMembers()  // a lead host that is no longer paired (§3.2)
		m.scanUnpaired()              // a host whose pairing is gone: its remote members are gone, its commands dropped (X3a)
		m.noticeUsage()               // after the gone teams and members are settled: a member at the threshold and idle tells its lead once (P7-1)
		// Titles and names change in the registry without a write of ours:
		// the hash gate in rosterChanged makes the unchanged case a read.
		m.rosterChanged()
		m.kickNotices() // a notice whose send failed, or a lead whose inbox was not up yet
	}
	m.expireCommands() // a spawn or adopt not delivered within 10 minutes is void (X3a)
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] sweep: %v", err)
		return
	}
	if len(open) == 0 {
		m.reconcileUnattended(open) // forgets refusals of rows closed since
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
		case a.Kind == team.KindMemberRelay:
			// Liveness is the TEAM's (RQ-2 §4.2): the origin is the lead at create, which the lead's own relay ends while
			// the row is rightly still open. A gone member is the approve's re-check, not the sweeper's.
			if live, err := m.memberRelayTeamLive(a); err != nil || live {
				if err != nil {
					m.logf("[team] sweep %s: %v", a.ID, err)
				}
				continue
			}
			after, won, err = m.closeAs(a.ID, Close{State: team.StateAbandoned, DecidedAt: now})
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
	m.reconcileUnattended(open)
}

// closeExpired is the sweeper's close for a passed deadline or lease: the
// store re-checks the expiry at now inside the CAS (CloseIfExpired).
func (m *Module) closeExpired(id string, now int64, state team.State) (team.Approval, bool, error) {
	if m.beforeCloseExpired != nil {
		if err := m.beforeCloseExpired(id); err != nil {
			return team.Approval{}, false, err
		}
	}
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
// end. Only PresenceGone ends a team (peers/origin_resolver.go), and it is
// tied to the lead's own process — the pid and start time its request
// recorded, which a relay's /clear keeps: that process dead or reused, or
// alive in another conversation. Anything the registry cannot tell, and
// any store error, skips the team. Nothing ends within BootGraceS of
// Start: right after a restart a lead may not be listed yet (P4-2
// review). Members are untouched (D4).
func (m *Module) endGoneTeams() {
	if m.now() < m.bootAt+team.BootGraceS*1000 {
		return
	}
	teams, err := m.store.ListLiveTeams()
	if err != nil {
		m.logf("[team] sweep teams: %v", err)
		return
	}
	for _, t := range teams {
		// The request row commits with the team (CloseLeadApproved) and is
		// never deleted; without it the lead's process is unknown: skip.
		req, ok, err := m.store.Get(t.RequestID)
		if err != nil || !ok {
			m.logf("[team] sweep team %s: its request row: ok=%v err=%v", t.ID, ok, err)
			continue
		}
		if m.origins.LeadPresence(t.LeadSessionID, req.Origin.PID, req.Origin.ProcStart) != peersmod.PresenceGone {
			continue // live, or nothing proves the lead's conversation ended
		}
		if m.beforeEndTeam != nil {
			m.beforeEndTeam(t)
		}
		ended, err := m.store.EndTeamWithCommands(t, team.TeamEndLeadGone, m.now(), m.leadTuple(t), m.newID)
		if err != nil {
			m.logf("[team] sweep team %s: %v", t.ID, err)
			continue
		}
		if ended {
			m.logf("[team] team %s ended (%s): its lead %s (%s) is gone", t.ID, team.TeamEndLeadGone, t.LeadRef, t.LeadSessionID)
			m.kickCommands()
			m.rosterChanged()
		}
	}
}

// persistUsage copies the agent module's last statusline reading of each
// live team's lead, and of each active member of one, onto its row when it
// is newer than the stored one (spec §8.5: it survives a restart; GET
// /api/team serves it until the next reading). A store error is logged and
// the next liveness tick tries again.
func (m *Module) persistUsage() {
	if m.usage == nil {
		return
	}
	teams, err := m.store.ListLiveTeams()
	if err != nil {
		m.logf("[team] sweep usage: %v", err)
		return
	}
	for _, t := range teams {
		if u, ok := m.usage.ContextUsage(t.LeadSessionID); ok {
			if _, err := m.store.SetLeadUsage(t.ID, t.LeadSessionID, contextOf(u)); err != nil {
				m.logf("[team] sweep usage: %v", err)
			}
		}
	}
	members, err := m.store.ActiveMembersOfLiveTeams()
	if err != nil {
		m.logf("[team] sweep usage: %v", err)
		return
	}
	for _, mr := range members {
		if u, ok := m.usage.ContextUsage(mr.SessionID); ok {
			if _, err := m.store.SetMemberUsage(mr.SpawnOp, mr.SessionID, contextOf(u)); err != nil {
				m.logf("[team] sweep usage: %v", err)
			}
		}
	}
}

// contextOf is a statusline reading in the wire's shape, its percentage copied.
func contextOf(u agent.ContextUsage) team.MemberContext {
	c := team.MemberContext{Window: u.WindowSize, ModelID: u.ModelID, Effort: u.Effort, At: u.At}
	if u.UsedPercentage != nil {
		v := *u.UsedPercentage
		c.UsedPercentage = &v
	}
	return c
}

// markGoneMembers marks gone every active member of a live team whose
// conversation ended (spec §7.3), which frees its place: the spawn limit
// counts active rows of team.db alone (P4-5 review). It is as conservative
// as endGoneTeams, by the same rule: only PresenceGone of the member's own
// process — the pid and start time its row recorded at registration, which
// a relay's /clear keeps — dead or reused, or alive in another conversation
// (a manual /clear). Anything the registry cannot tell keeps the member, as
// does a relay op of its session in flight (in MarkMemberGone's statement)
// and the boot grace. A row stored with no process (spawnFinish resumed
// after the registry lost the session) is never confirmed gone: it holds
// its place until pdx kill. A store error is logged.
func (m *Module) markGoneMembers() {
	if m.now() < m.bootAt+team.BootGraceS*1000 {
		return
	}
	members, err := m.store.ActiveMembersOfLiveTeams()
	if err != nil {
		m.logf("[team] sweep members: %v", err)
		return
	}
	for _, mr := range members {
		if m.origins.LeadPresence(mr.SessionID, mr.PID, mr.ProcStart) != peersmod.PresenceGone {
			continue
		}
		if m.beforeMarkGone != nil {
			m.beforeMarkGone(mr)
		}
		gone, err := m.store.MarkMemberGone(mr.SpawnOp, mr.SessionID, m.now())
		if err != nil {
			m.logf("[team] sweep member %s: %v", mr.SpawnOp, err)
			continue
		}
		if gone {
			m.logf("[team] member %s (%s) of team %s is gone", mr.Ref, mr.SessionID, mr.TeamID)
			m.rosterChanged()
		}
	}
}
