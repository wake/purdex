package teammod

import (
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// Settling a kill that never reached its end (#2152 point 2). A kill claims its member (active → killing) before it signals and
// ends the row (killed, gone) or gives the claim back (active) after. Two things leave a killing row behind: the daemon dying
// between the claim and the end, and a store error after the signal went out. Nothing here signals: a pid read later may be
// stale, and signalling the wrong process is the one thing that cannot be undone, so a stuck kill is never finished on the
// lead's behalf — the lead kills again.

// killClaimStuckAfter is how long a claim may live before the sweeper takes it for lost. A kill is a registry read, a process
// check and one signal (or one tmux kill): seconds at the very most.
const killClaimStuckAfter = 30 * time.Second

// recoverKillingMembers is the boot's part, run before the first request is served (so no live kill can exist): a killing row
// whose session the registry lists goes back to active. A row it does not list is NOT judged here — at boot a live session may
// not have registered again yet, which is why every other clean-up waits out the boot grace; the sweeper settles it after.
func (m *Module) recoverKillingMembers() {
	rows, err := m.store.LocalKillingMembers()
	if err != nil {
		m.logf("[team] boot: killing members: %v", err)
		return
	}
	for _, mr := range rows {
		_, live, err := m.origins.ResolveOriginBySession(mr.SessionID)
		if err != nil {
			m.logf("[team] boot: killing member %s: registry: %v; left for the sweeper", mr.SpawnOp, err)
			continue
		}
		if !live {
			continue
		}
		if ok, err := m.store.GiveBackMemberKilling(mr.SpawnOp, mr.SessionID, m.now()); err != nil {
			m.logf("[team] boot: killing member %s: give back: %v", mr.SpawnOp, err)
		} else if ok {
			m.logf("[team] boot: member %s was being killed when the daemon stopped; its session is still there, so it is active again", mr.Ref)
		}
	}
	if len(rows) > 0 {
		m.rosterChanged()
	}
}

// settleStuckKillingMembers is the sweeper's part, after the boot grace and on the liveness cadence: a killing row older than
// killClaimStuckAfter is settled by what the process table says, the way markGoneMembers decides a member is gone. PresenceGone
// → gone (the signal did its work, or the session ended on its own); anything else → active again, so the lead can kill it
// again or release it. A presence the registry cannot tell keeps the claim to the next tick.
func (m *Module) settleStuckKillingMembers() {
	if m.now() < m.bootAt+team.BootGraceS*1000 {
		return
	}
	rows, err := m.store.StaleKillingMembers(m.now() - killClaimStuckAfter.Milliseconds())
	if err != nil {
		m.logf("[team] sweep killing members: %v", err)
		return
	}
	for _, mr := range rows {
		switch m.origins.LeadPresence(mr.SessionID, mr.PID, mr.ProcStart) {
		case peersmod.PresenceGone:
			if ok, err := m.store.MarkMemberGone(mr.SpawnOp, mr.SessionID, m.now()); err != nil {
				m.logf("[team] sweep killing member %s: mark gone: %v", mr.SpawnOp, err)
			} else if ok {
				m.logf("[team] member %s (%s) was killing and its process is gone; marked gone", mr.Ref, mr.SessionID)
				m.rosterChanged()
			}
		case peersmod.PresenceLive:
			if ok, err := m.store.GiveBackMemberKilling(mr.SpawnOp, mr.SessionID, m.now()); err != nil {
				m.logf("[team] sweep killing member %s: give back: %v", mr.SpawnOp, err)
			} else if ok {
				m.logf("[team] member %s (%s) was killing for too long and its process is still there; active again", mr.Ref, mr.SessionID)
				m.rosterChanged()
			}
		}
	}
}
