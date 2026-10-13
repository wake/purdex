// internal/module/team/pause_unanswered.go — the final self-relay card (relay/team rules v2, RT-2a1, spec §4.2).
package teammod

import (
	"encoding/json"

	"github.com/wake/purdex/internal/team"
)

// isFinalCard says whether a is the LAST ask of its session's ladder (S-2, S-20): a self-relay card at ≥ team.RelayLastAskPct that
// is not an idle ask and not an older mod's manual /relay card; a re-ask card (opened after a pause) is final whatever the use.
// Whether the host's PDX_RELAY_THRESHOLD is lower does not matter: the 90 is the daemon's.
func isFinalCard(a team.Approval) bool {
	if a.Kind != team.KindSelfRelay {
		return false
	}
	var p team.SelfRelayPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return false
	}
	if p.Reason == team.RelayBeginReasonIdle || p.Manual {
		return false
	}
	return p.Reask || p.UsedPercentage >= team.RelayLastAskPct
}

// markUnanswered is the sweeper's step for a final card at its deadline: the card stays open with no deadline and the session
// stops working (cause relay_unanswered) — one transaction, once. A member's session is not paused with its lead (S-6), so
// nothing else is touched. After the commit the long-poll waiters of the card are woken (they return `open` with unanswered_at set,
// which is how the mod learns) and the roster, the notice and the push follow. No new approval event op: the App learns it from
// the pause and from the row.
func (m *Module) markUnanswered(a team.Approval, now int64) {
	marked, err := m.store.MarkUnanswered(a, now)
	if err != nil {
		m.logf("[team] sweep %s: mark unanswered: %v", a.ID, err)
		return
	}
	if !marked {
		return
	}
	m.logf("[team] approval %s unanswered at its deadline: %s (%s) is paused until a person answers", a.ID, a.Origin.Ref, a.Origin.SessionID)
	m.wake(a.ID)
	m.rosterChanged()
	m.kickPauseOutbox()
}
