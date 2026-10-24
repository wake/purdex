package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// While the host is unattended the daemon answers a member's relay ask itself (RT-2b T22, S-4, U-7.2): it opens the relay the way
// the lead's `pdx relay _ref` would, so the member pool is spent or the member is paused for quota, exactly as for the lead's
// own relay. Attended, the ask is the lead's to answer, as before.

// listOpenAsks is every open ask whose window has not passed.
func (s *Store) listOpenAsks(now int64) ([]RelayAsk, error) {
	rows, err := s.db.Query(`SELECT `+relayAskCols+` FROM relay_asks WHERE state = 'open' AND expires_at > ? ORDER BY created_at, id`, now)
	if err != nil {
		return nil, fmt.Errorf("list open relay asks: %w", err)
	}
	defer rows.Close()
	var out []RelayAsk
	for rows.Next() {
		a, err := scanRelayAsk(rows)
		if err != nil {
			return nil, fmt.Errorf("list open relay asks: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// acceptAskUnattended opens the relay for one open ask, as the lead would. accepted says an op was opened (the ask is then closed
// accepted by the op's own transaction); an ask whose member was paused for quota is withdrawn as paused. Anything else leaves the
// ask open for the lead. Caller holds createMu and has read the switch on.
func (m *Module) acceptAskUnattended(a RelayAsk) (accepted bool) {
	if m.stopping() {
		return false
	}
	mr, t, ok, err := m.store.ActiveMemberInLiveTeam(a.SessionID)
	if err == nil && !ok {
		mr, t, ok, err = m.store.remoteAskMember(a)
	}
	if err != nil {
		m.logf("[team] unattended ask %s: %v", a.ID, err)
		return false
	}
	if !ok {
		return false // the member left: the sweeper withdraws the ask
	}
	req := team.RelayCreateRequest{ID: m.newID(), Target: mr.Ref}
	cw := newCapture()
	m.createMemberRelayFor(cw, req, t, mr, false)
	switch cw.status {
	case http.StatusCreated:
	case http.StatusConflict:
		var ae team.APIError
		if json.Unmarshal(cw.body, &ae) == nil && ae.Error == team.ErrMemberPausedNoQuota {
			if _, err := m.store.WithdrawRelayAsk(a.SessionID, team.RelayAskWithdrawPaused, m.now()); err != nil {
				m.logf("[team] unattended ask %s: withdraw after the pause: %v", a.ID, err)
			}
			return false
		}
		fallthrough
	default:
		m.logf("[team] unattended ask %s not accepted (%d): %s", a.ID, cw.status, string(cw.body))
		return false
	}
	m.logf("[team] unattended: ask %s of member %s accepted", a.ID, mr.Ref)
	left := 0
	if q, _, err := m.store.RelayQuotaOf(t.LeadSessionID); err == nil {
		left = q.MemberPoolLeft
	}
	address, title := m.memberNoticeName(mr)
	m.goTracked(func() {
		m.noticeToLead(mr, t, fmt.Sprintf(team.UnattendedAskAcceptedFmt, address, title, a.UsedPct, left), "unattended ask accepted")
	})
	return true
}

// acceptOpenAsksUnattended accepts every open ask while the switch is on: the switch-on sweep and the liveness tick (an ask that a
// transient error left open is tried again). Caller holds createMu.
func (m *Module) acceptOpenAsksUnattended() int {
	if !m.unattendedOn() || !m.memberAutoOn() {
		return 0
	}
	asks, err := m.store.listOpenAsks(m.now())
	if err != nil {
		m.logf("[team] unattended asks: %v", err)
		return 0
	}
	n := 0
	for _, a := range asks {
		m.askMu.Lock() // behind an ask notice on the wire
		cur, ok, err := m.store.GetRelayAsk(a.ID)
		m.askMu.Unlock()
		if err != nil || !ok || cur.State != team.RelayAskOpen {
			continue
		}
		if m.acceptAskUnattended(cur) {
			n++
		}
	}
	return n
}
