// internal/module/team/pause_held.go — the sweep's pause of a held card (unattended quota pause spec 2026-10-12 §3.3, UP-2b).
package teammod

import "github.com/wake/purdex/internal/team"

// pauseHeldCard is the sweep's pause of a held card whose auto-approve found no quota (spec §3.3, "switch on with open held cards"):
// the row and its op are cancelled and the session paused in one transaction (Store.PauseHeldRow). It says whether it did. A row a
// decide, a timeout or a gone member got to first is not paused and falls back to the held handling; a storage error is logged and
// does the same, so the next tick tries again. Caller holds createMu, with the switch read on.
func (m *Module) pauseHeldCard(a team.Approval) bool {
	st, _ := m.unattended.Unattended()
	after, res, err := m.store.PauseHeldRow(a.ID, st.Since, m.now())
	if err != nil {
		m.logf("[team] approval %s: pause at the sweep: %v", a.ID, err)
		return false
	}
	if res != PauseHeldPaused {
		return false
	}
	m.forgetRefusal(a.ID)
	m.logf("[team] approval %s (%s, origin %s): no relay quota while unattended: cancelled, the session is paused", a.ID, a.Kind, a.Origin.Ref)
	m.announceClosed(after, nil)
	m.rosterChanged()
	m.kickPauseOutbox()
	m.kickCommands() // a lead's remote members are told (UP-3c)
	return true
}
