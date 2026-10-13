package teammod

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/team"
)

// A person's own /relay (RT-1a): the begin the mod makes with a valid person token. The relay, the end of the session's pauses
// and (last) the spending of the token are one outcome: the pauses end in the SAME transaction that writes the relay, so a
// failure or a crash leaves neither half; the token is spent only after that transaction committed, so a refusal or a storage
// failure costs the person nothing. Everything here runs under createMu.

// personClose is the close of a request the person approved by a typed command: the person decides, the daemon spends nothing.
func (m *Module) personClose(at int64) Close {
	by := team.PersonClient()
	return Close{State: team.StateApproved, DecidedAt: at, DecidedBy: &by}
}

// personResume is the statements that end sessionID's pauses on the relay's transaction, asking nothing again; res receives the
// result for what must follow the commit.
func (m *Module) personResume(sessionID string, at int64, res *ResumeResult) func(tx *sql.Tx) error {
	return func(tx *sql.Tx) error {
		r, err := m.store.resumePausesIn(tx, ResumeScope{Release: sessionID, NoReask: true}, at, m.speaksReask, uuid.NewString)
		*res = r
		return err
	}
}

// afterPersonResume wakes what follows the commit of an ended pause.
func (m *Module) afterPersonResume(res ResumeResult) {
	if res.Rows > 0 {
		m.rosterChanged()
		m.kickPauseOutbox()
		m.kickCommands()
	}
}

// beginPerson is the person's /relay when the session has no open request: the op and its row are written approved at once
// (no quota read or spent, the member check passes for a manual payload), announced closed only.
func (m *Module) beginPerson(w http.ResponseWriter, op team.RelayOp, row team.Approval, hash, token string) {
	var res ResumeResult
	after, claimed, err := m.store.CreateSelfRelayApprovedThen(op, row, hash, m.personClose(row.CreatedAt), m.personResume(op.SessionID, row.CreatedAt, &res))
	switch {
	case errors.Is(err, ErrMemberRelayIsLeads):
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
		return
	case errors.Is(err, ErrRelayOpOpen) && m.writeRelayOpen(w, op.SessionID):
		return
	case err != nil:
		m.logf("[team] relay begin %s: %v", op.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.persons.spend(token)
	m.logf("[team] relay op %s claimed: self, origin=%s (%s), request %s approved by the person at begin", op.ID, op.Ref, op.SessionID, after.ID)
	m.announceClosed(after, nil)
	m.afterPersonResume(res)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: claimed, RequestID: after.ID})
}

// personApproveOpen approves the session's open self-relay card (awaiting approval) as the person: the card is answered by the
// person's own /relay, not refused as "a relay is open". The close is a CAS on the open row, with the pauses ended in its
// transaction; a card that another request closed at the same moment is not ours to approve and is answered from what it became.
func (m *Module) personApproveOpen(w http.ResponseWriter, sessionID string, op team.RelayOp, token string) {
	row, ok, err := m.store.Get(op.RequestID)
	if err != nil || !ok {
		m.logf("[team] relay begin %s: card %s: ok=%v err=%v", sessionID, op.RequestID, ok, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if m.afterPersonCardRead != nil {
		m.afterPersonCardRead()
	}
	var res ResumeResult
	var memberCancelled bool
	at := m.now()
	after, won, err := m.closeWith(row.ID, func() (team.Approval, bool, error) {
		a, won, mc, err := m.store.closeSelfRelayApprovedThen(row.ID, m.personClose(at), row.Origin.SessionID, m.personResume(sessionID, at, &res))
		memberCancelled = mc
		return a, won, err
	})
	if err != nil {
		m.logf("[team] relay begin %s: approve card %s: %v", sessionID, row.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	cur, found, err := m.store.RelayOpByRequest(row.ID)
	if err != nil || !found {
		cur = op
	}
	if !won || memberCancelled {
		// The card closed another way at the same moment (a click, the sweeper, the member rule). The person's token stays
		// unspent. If it was approved the relay is on its way and that is the answer; otherwise it says what became of it.
		if after.State == team.StateApproved && !memberCancelled {
			m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: cur, RequestID: row.ID})
			return
		}
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRelayOpen, Detail: "the open relay request was decided at the same moment", Op: &cur})
		return
	}
	m.persons.spend(token)
	m.afterPersonResume(res)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: cur, RequestID: row.ID})
}
