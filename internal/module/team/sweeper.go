package teammod

import (
	"math"
	"slices"
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
	// sweepReadMax is the longest the sweeper goes without reading the open requests (G3b): it reads them when the store was
	// written to, when one is due, on the liveness tick, and at the latest this often (a write nothing counted is found then).
	sweepReadMax = 5 * time.Second
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
// "unknown" (SessionPresence), and so does an unverifiable file of a live
// process: only a session that is positively gone is abandoned here. The deadline and lease paths close through CloseIfExpired:
// the decision here is made on a copy, and a poll that renewed the lease
// since the read must win, so the CAS re-checks the expiry in the same
// statement and a lost CAS leaves the row open for the next tick. Every
// close goes through closeWith, so it competes fairly with decide and
// cancel and broadcasts once.
func (m *Module) tick() {
	m.tickN++
	m.reconcilePauses(false) // pause rows the switch no longer covers, at most every 30 s (UP-3a)
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
		// One registry read answers every presence question of this pass (#3092).
		if snap, ok := m.origins.(presenceSnapshotter); ok {
			m.passPresence.Store(&presencePass{snap.PresenceSnapshot()})
		}
		m.endGoneTeams()
		m.markGoneMembers()
		m.sweepFreshHalves()          // a fresh half of a session that is gone (RT-3a1)
		m.settleStuckKillingMembers() // a kill that claimed its member and never ended (#2152)
		m.markGoneRemoteMembers()     // sessions here that a lead on another host adopted (X2c)
		m.passPresence.Store(nil)
		m.endUnpairedRemoteMembers()  // a lead host that is no longer paired (§3.2)
		m.scanUnpaired()              // a host whose pairing is gone: its remote members are gone, its commands dropped (X3a)
		m.noticeUsage()               // after the gone teams and members are settled: a member at the threshold and idle tells its lead once (P7-1)
		m.noticeIdle()                // a member idle for RelayIdleMinutes with a large context tells its lead once per idle period (idle relay §3.3)
		m.settleAsks()                // expire / withdraw relay asks after the settlements above, then resend unsent notices (member relay ask §3.3)
		// Titles and names change in the registry without a write of ours:
		// the hash gate in rosterChanged makes the unchanged case a read.
		m.rosterChanged()
		m.kickNotices() // a notice whose send failed, or a lead whose inbox was not up yet
	}
	m.expireCommands() // a spawn or adopt not delivered within 10 minutes is void (X3a)
	open, ok := m.openForTick(checkLive)
	if !ok {
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
		case a.DeadlineAt <= now && isFinalCard(a):
			// the last ask of the ladder is not timed out: the card stays open and the session stops (RT-2a1)
			m.markUnanswered(a, now)
			continue
		case a.DeadlineAt <= now:
			after, won, err = m.closeExpired(a.ID, now, team.StateTimeout)
		case a.LeaseUntil <= now && a.UnansweredAt == 0:
			// an unanswered card is never abandoned for its lease (S-13): it ends by an answer, the session's end or a compaction
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
		case checkLive && m.sessionGone("approval "+a.ID, a.Origin.SessionID):
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

// openForTick is the open requests for this tick, and false when the tick has nothing to do with them. They are read when
// the store counted a write since the last read, when one of them falls due (deadline or lease) by now, on the liveness
// tick (which asks about their origins) and at the latest every sweepReadMax; otherwise nothing can have changed since the
// last read (and no request is waiting for the unattended switch's per-tick retry), and the tick returns without touching the database (G3b: it read every open request every second). A failed
// read is logged and the tick ends, as before.
func (m *Module) openForTick(checkLive bool) ([]team.Approval, bool) {
	now := m.now()
	gen := approvalWrites.Load()
	// SQLite's data_version, from a connection of ours, moves when another connection COMMITS: read before ListOpen, so a commit
	// after it is seen by the next tick whatever the write's duration. Where there is none (a memory store) the write count
	// stands in for it, with one more read after it moves (openSettle).
	dv, dvOk := m.store.DataVersion()
	same := gen == m.openGen && dvOk == m.openDVOk && dv == m.openDV
	if m.openRead && !checkLive && !m.openSettle && !m.unattendedRetry() && m.notAutoApprovedN.Load() == 0 && same && now < m.openNext && now-m.openAt < sweepReadMax.Milliseconds() {
		return nil, false
	}
	open, err := m.store.ListOpen()
	if err != nil {
		m.openRead = false
		m.logf("[team] sweep: %v", err)
		return nil, false
	}
	// Without data_version, a write counts before it commits, so a read made just after the count may have seen the state from
	// before it: when the count moved, one more read follows on the next tick (openSettle), by which time the write has
	// usually committed (a transaction that outlasts a tick is found by the periodic read).
	m.openSettle = !dvOk && gen != m.openGen
	m.openRead, m.openGen, m.openAt, m.openDV, m.openDVOk = true, gen, now, dv, dvOk
	m.openAutoApprovable = slices.ContainsFunc(open, func(a team.Approval) bool { return autoApprovableRow(a) || team.UnattendedRefusable(a.Kind) })
	// the next moment a request falls due: only deadlines and leases still ahead count; one already past that stays open (a
	// final card, an unanswered one) is looked at again by the periodic read, not on every tick
	m.openNext = int64(math.MaxInt64)
	for _, a := range open {
		if a.DeadlineAt > now && a.DeadlineAt < m.openNext {
			m.openNext = a.DeadlineAt
		}
		if a.UnansweredAt == 0 && a.LeaseUntil > now && a.LeaseUntil < m.openNext {
			m.openNext = a.LeaseUntil
		}
	}
	return open, true
}

// unattendedRetry is whether the unattended switch is on and the last read held a request it may approve: such a request is
// retried on every tick while it stays open (a transient store error must not wait for sweepReadMax), so those ticks read.
// The switch is read without createMu: a stale answer costs one tick of lateness, not a wrong approval (the sweep itself
// re-reads it under the lock).
func (m *Module) unattendedRetry() bool {
	if !m.openAutoApprovable || m.unattended == nil {
		return false
	}
	st, err := m.unattended.Unattended()
	return err == nil && st.On
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
		if m.leadPresence(t.LeadSessionID, req.Origin.PID, req.Origin.ProcStart) != peersmod.PresenceGone {
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
		if m.leadPresence(mr.SessionID, mr.PID, mr.ProcStart) != peersmod.PresenceGone {
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
