package teammod

// Boot settlement of a kill that died between its claim and its end (#2152 point 2). A kill claims its member (active →
// killing) before it signals and ends the row (killed, gone) or gives the claim back (active) after; a daemon that dies in
// between leaves a killing row. The registry decides, and nothing is signalled here:
//   - the session is still listed → active again: the lead can kill it again. A pid read at boot may be stale, and signalling
//     the wrong process is the one thing that cannot be undone, so the boot never finishes a kill on the lead's behalf;
//   - the session is gone → gone: whatever the dead call was about to end has ended.
//
// A registry that cannot answer leaves the row for the next boot. Idempotent.

// recoverKillingMembers settles every local killing row. It runs before the first request is served.
func (m *Module) recoverKillingMembers() {
	rows, err := m.store.LocalKillingMembers()
	if err != nil {
		m.logf("[team] boot: killing members: %v", err)
		return
	}
	for _, mr := range rows {
		_, live, err := m.origins.ResolveOriginBySession(mr.SessionID)
		if err != nil {
			m.logf("[team] boot: killing member %s: registry: %v; left for the next boot", mr.SpawnOp, err)
			continue
		}
		if live {
			if ok, err := m.store.GiveBackMemberKilling(mr.SpawnOp, mr.SessionID, m.now()); err != nil {
				m.logf("[team] boot: killing member %s: give back: %v", mr.SpawnOp, err)
			} else if ok {
				m.logf("[team] boot: member %s was being killed when the daemon stopped; its session is still there, so it is active again", mr.Ref)
			}
			continue
		}
		if ok, err := m.store.MarkMemberGone(mr.SpawnOp, mr.SessionID, m.now()); err != nil {
			m.logf("[team] boot: killing member %s: mark gone: %v", mr.SpawnOp, err)
		} else if ok {
			m.logf("[team] boot: member %s was being killed when the daemon stopped and its session is gone; marked gone", mr.Ref)
		}
	}
	if len(rows) > 0 {
		m.rosterChanged()
	}
}
