package teammod

// NoticeLeadOf implements team.LeadNoticer (lease v2 amendment A, P2d-2): the notice goes to the lead of the member's
// team through noticeToLead, the one route that also reaches the lead for a member of another host.
func (m *Module) NoticeLeadOf(sessionID, text string) (member, delivered bool) {
	if m == nil || m.store == nil || sessionID == "" {
		return false, false
	}
	mr, t, ok, err := m.store.ActiveMemberInLiveTeam(sessionID)
	if err != nil {
		m.logf("[team] lease notice for %s: %v", sessionID, err)
		return true, false // unknown: not a reason to tell the person, try again
	}
	if !ok {
		return false, false
	}
	if m.sender == nil || m.stopping() {
		return true, false
	}
	return true, m.noticeToLead(mr, t, text, "lease notice")
}
