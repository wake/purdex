package teammod

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// halfClient is the decider of an idle half: the daemon itself, but not "unattended" (that kind is the quota-spending auto-approve).
func halfClient() team.Client { return team.Client{Kind: "daemon", Label: "閒置寫接力檔"} }

// beginHalf is handleRelayBegin's write for an idle half (RT-3a1 T29): the op (mode half) and its row, approved by halfClient, in one
// transaction and claimed - nothing is spent (Auto false), no card, no pause. The mod then writes the handoff file only. Caller holds
// createMu; the switch refusals and the member refusal were answered before.
func (m *Module) beginHalf(w http.ResponseWriter, op team.RelayOp, row team.Approval, hash string) {
	by := halfClient()
	c := Close{State: team.StateApproved, DecidedAt: row.CreatedAt, DecidedBy: &by}
	after, claimed, err := m.store.CreateSelfRelayApproved(op, row, hash, c)
	switch {
	case errors.Is(err, ErrMemberRelayIsLeads):
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
		return
	case errors.Is(err, ErrRelayOpOpen) && m.writeRelayOpen(w, op.SessionID):
		return
	case err != nil:
		m.logf("[team] half begin %s: %v", op.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] relay op %s claimed: self half, origin=%s (%s), request %s approved by the daemon", op.ID, op.Ref, op.SessionID, after.ID)
	m.announceClosed(after, nil)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: claimed, RequestID: after.ID})
}

// isIdleCard says whether an open self_relay row is an idle card (reason idle), not a reask's.
func isIdleCard(a team.Approval) bool {
	if a.Kind != team.KindSelfRelay {
		return false
	}
	var p team.SelfRelayPayload
	return json.Unmarshal(a.Payload, &p) == nil && p.Reason == team.RelayBeginReasonIdle && !p.Reask
}
