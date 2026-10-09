// internal/module/team/facts_sweep.go
package teammod

import (
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// markGoneRemoteMembers is markGoneMembers for the sessions of this host that a lead on another host adopted
// (cross-host team spec §5.2): an active remote row whose session is no longer live goes gone, with the `ended`
// fact for the lead host queued in the same transaction. The same rules as the local sweep: nothing before the
// boot grace, and only a verdict of PresenceGone — an unreadable registry or an unverifiable file never abandons
// a member. The facts are sent by the facts pump (X2c-2).
func (m *Module) markGoneRemoteMembers() {
	if m.stopping() || m.now() < m.bootAt+team.BootGraceS*1000 {
		return
	}
	members, err := m.store.ActiveRemoteMembers()
	if err != nil {
		m.logf("[team] sweep remote members: %v", err)
		return
	}
	for _, r := range members {
		if m.origins.LeadPresence(r.MemberSessionID, r.PID, r.ProcStart) != peersmod.PresenceGone {
			continue
		}
		gone, err := m.store.MarkRemoteMemberGone(r.MK, r.MemberSessionID, m.newID(), m.now())
		if err != nil {
			m.logf("[team] sweep remote member %s: %v", r.MK, err)
			continue
		}
		if gone {
			m.logf("[team] remote member %s (%s) of team %s is gone", r.Ref, r.MemberSessionID, r.TeamID)
		}
	}
}

// endUnpairedRemoteMembers is spec §3.2 on this host: a lead host whose peer entry is gone (deleted, or its alias
// re-created for another host) is no longer bound, so its live remote members end locally and its queued facts
// are dropped. The other side learns it only when it next calls (§3.2's accepted residual).
func (m *Module) endUnpairedRemoteMembers() {
	if m.stopping() {
		return
	}
	m.core.CfgMu.RLock()
	paired := make([]string, 0, len(m.core.Cfg.Peers.Hosts))
	for _, h := range m.core.Cfg.Peers.Hosts {
		if h.HostID != "" {
			paired = append(paired, h.HostID)
		}
	}
	m.core.CfgMu.RUnlock()
	n, err := m.store.EndUnpairedRemoteMembers(paired, m.now())
	if err != nil {
		m.logf("[team] end unpaired remote members: %v", err)
		return
	}
	if n > 0 {
		m.logf("[team] %d remote member(s) ended: their lead host is no longer paired", n)
	}
}
