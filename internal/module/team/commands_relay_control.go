// internal/module/team/commands_relay_control.go
package teammod

import (
	"context"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// sendRemoteRelayControlAsync tells the remote member's mod to claim the op a relay command opened (D4, §3.3): M's own template
// (`[pdx-relay:control] op=<id>`), through the remote-notice seam with the lead host's lead as the sender and the member's session
// re-validated by the seam (the mod verifies a control by the daemon's `seen`, not by its sender). Off the request's path; safe to
// repeat for an op id — the claim is a compare-and-set — and it sends nothing for an op that is no longer requested. A failure is
// logged: the claim timeout covers a message that never arrived.
func (m *Module) sendRemoteRelayControlAsync(leadHostID, mk, opID string) {
	if m.teamNotices == nil {
		return
	}
	m.goTracked(func() { m.sendRemoteRelayControl(leadHostID, mk, opID) })
}

func (m *Module) sendRemoteRelayControl(leadHostID, mk, opID string) {
	op, ok, err := m.store.GetRelayOp(opID)
	if err != nil || !ok || op.State != team.RelayRequested {
		if err != nil {
			m.logf("[team] control for op %s: %v", opID, err)
		}
		return
	}
	member, found, err := m.store.RemoteMember(mk)
	if err != nil || !found || member.State != remoteActive || member.LeadHostID != leadHostID || member.MemberSessionID != op.SessionID {
		m.logf("[team] control for op %s: member %s is not an active member of that lead host's here (%v)", opID, mk, err)
		return
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, controlSendTimeout)
	defer cancel()
	if _, err := m.teamNotices.DeliverTeamNotice(ctx, peersmod.TeamNotice{
		MsgID:      m.newID(),
		LeadHostID: member.LeadHostID,
		Lead: peersmod.TeamNoticeLead{SessionID: member.LeadSessionID, Ref: member.LeadRef, Address: member.LeadAddress,
			PID: member.LeadPID, ProcStart: member.LeadProcStart},
		Target: ipeers.WireTo{AgentSessionID: member.MemberSessionID, PID: member.PID, ProcStart: member.ProcStart},
		Text:   team.RelayControlPrefix + opID,
	}); err != nil {
		m.logf("[team] control for op %s to %s: %v", opID, member.Ref, err)
	}
}
