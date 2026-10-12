// internal/module/team/pause_resume.go — leaving a pause: the switch off and the reconciliation (unattended quota pause spec
// 2026-10-12 §3.4, UP-3a). Leaving is not behind the release gate: with it closed there are no pause rows and these are no-ops.
package teammod

import (
	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// pauseReconcileEveryMs is the reconciliation's period (spec §3.4: boot and every 30 s).
const pauseReconcileEveryMs = 30_000

// resumePauses runs one resume group (Store.ResumePauses) and wakes what it fed: the roster and the outbox loop. all takes every row
// (the switch just went off); otherwise the rows of another on-period than the switch's current Since — or every row while the
// switch is off. It returns the number of sessions no longer paused. Caller holds createMu.
func (m *Module) resumePauses(why string, all bool) int {
	st, err := m.unattended.Unattended()
	if err != nil {
		m.logf("[team] resume pauses (%s): switch unreadable: %v", why, err)
		return 0
	}
	scope := ResumeScope{All: all || !st.On, OnSinceNot: st.Since}
	res, err := m.store.ResumePauses(scope, m.now(), m.speaksReask, uuid.NewString)
	if err != nil {
		m.logf("[team] resume pauses (%s): %v", why, err)
		return 0
	}
	if res.Rows > 0 {
		m.logf("[team] resume pauses (%s): %d row(s) deleted, %d session(s) resumed", why, res.Rows, res.Sessions)
		m.rosterChanged()
		m.kickPauseOutbox()
		m.kickCommands() // a lead's remote members are told (UP-3c)
	}
	return res.Sessions
}

// reconcilePauses is the reconciliation: resume the pause rows the switch no longer covers (it is off, or they are of an earlier
// on-period — a crash between the off and its resume group, even if the switch was turned on again), and delete the release tokens
// older than 24 h. It runs at boot (force) and on the tick at most every 30 s. It takes createMu itself.
func (m *Module) reconcilePauses(force bool) {
	now := m.now()
	if !force && now-m.pauseReconcileAt.Load() < pauseReconcileEveryMs {
		return
	}
	m.pauseReconcileAt.Store(now)
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		return
	}
	m.resumePauses("reconcile", false)
	if n, err := m.store.PruneReleaseTokens(now - team.PauseTokenTTLMs); err != nil {
		m.logf("[team] %v", err)
	} else if n > 0 {
		m.logf("[team] pruned %d release token(s) older than 24 h", n)
	}
}

// speaksReask: the session's mod (its persisted hello) speaks the reask protocol.
func (m *Module) speaksReask(sid string) bool {
	return m.modProtocolAtLeast(sid, team.MinPauseReaskModVersion)
}

// resumeOne is the release of one session (spec §3.4 "Release one"): the resume group for sid and the `lead`-cause members it paused
// with it. It returns the number of sessions no longer paused. Caller holds createMu.
func (m *Module) resumeOne(sid string) (int, error) {
	res, err := m.store.ResumePauses(ResumeScope{Release: sid}, m.now(), m.speaksReask, uuid.NewString)
	if err != nil {
		return 0, err
	}
	if res.Rows > 0 {
		m.rosterChanged()
		m.kickPauseOutbox()
		m.kickCommands()
	}
	return res.Sessions, nil
}
