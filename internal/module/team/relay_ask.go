package teammod

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/team"
)

// A member asks its lead to relay it (spec 2026-10-10-member-relay-ask §3.1). The member's mod calls POST
// /api/relay/ask at a turn boundary once its usage is over the threshold; the daemon records the ask, tells the lead
// (`pdx relay _<ref>` approves), and closes it after five minutes. The member is not paused; the daemon decides nothing.

// handleRelayAsk is POST /api/relay/ask.
func (m *Module) handleRelayAsk(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.RelayAskRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if u, err := uuid.Parse(req.RequestID); err != nil || u.Version() != 4 || u.Variant() != uuid.RFC4122 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "request_id must be a UUID v4", nil)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || req.UsedPct < 0 || req.UsedPct > 100 || req.Window < 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required; used_pct must be 0–100 and window non-negative", nil)
		return
	}
	// The same mutex as the lead's relay create: an ask and a `pdx relay` for the same member are strictly ordered
	// (ask first → the relay accepts it; relay first → the ask is refused relay_open).
	m.createMu.Lock()
	now := m.now()
	ask, replay, err := m.store.CreateRelayAsk(RelayAsk{ID: req.RequestID, SessionID: req.SessionID, UsedPct: req.UsedPct, Window: req.Window,
		CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000})
	m.createMu.Unlock()
	switch {
	case errors.Is(err, ErrAskNotMember):
		m.writeErr(w, http.StatusConflict, team.ErrNotMember, "the session is not an active member of a live team", nil)
		return
	case errors.Is(err, ErrAskRemote):
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "the member lives on another host; a cross-host member relay is not supported", nil)
		return
	case errors.Is(err, ErrAskRelayOpen):
		if !m.writeRelayOpen(w, req.SessionID) {
			m.writeErr(w, http.StatusConflict, team.ErrRelayOpen, "this session already has a relay in progress", nil)
		}
		return
	case err != nil:
		m.logf("[team] relay ask %s: %v", req.RequestID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if ask.SessionID != req.SessionID { // the id belongs to another session's ask
		m.writeErr(w, http.StatusConflict, team.ErrBadRequest, "request_id belongs to another session's ask", nil)
		return
	}
	if !replay {
		m.notifyAskAsync(ask)
	}
	m.writeJSON(w, http.StatusOK, team.RelayAskResponse{ID: ask.ID, State: ask.State, ExpiresAt: ask.ExpiresAt, Replay: replay})
}

// notifyAskAsync sends the lead's notice off the request's path; false when the daemon is stopping (notified_at stays 0,
// and the next start's sweeper sends it).
func (m *Module) notifyAskAsync(a RelayAsk) bool {
	return m.goTracked(func() { m.sendAskNotice(a) })
}

// sendAskNotice tells the lead about an open, unnotified ask, with the minutes left of its window (rounded up), and
// records the delivery. Safe to call again and from two places: one send per ask at a time, and nothing once the ask is
// notified, closed or past its window.
func (m *Module) sendAskNotice(a RelayAsk) {
	if m.sender == nil || m.stopping() {
		return
	}
	m.askMu.Lock()
	if _, busy := m.askSending[a.ID]; busy {
		m.askMu.Unlock()
		return
	}
	if m.askSending == nil {
		m.askSending = map[string]struct{}{}
	}
	m.askSending[a.ID] = struct{}{}
	m.askMu.Unlock()
	defer func() {
		m.askMu.Lock()
		delete(m.askSending, a.ID)
		m.askMu.Unlock()
	}()

	cur, ok, err := m.store.GetRelayAsk(a.ID)
	if err != nil {
		m.logf("[team] relay ask notice %s: %v", a.ID, err)
		return
	}
	now := m.now()
	if !ok || cur.State != team.RelayAskOpen || cur.NotifiedAt != 0 || cur.ExpiresAt <= now {
		return
	}
	mr, t, ok, err := m.store.ActiveMemberInLiveTeam(cur.SessionID)
	if err != nil {
		m.logf("[team] relay ask notice %s: %v", a.ID, err)
		return
	}
	if !ok {
		return // the member left: the sweeper withdraws the ask
	}
	address, title := m.memberNoticeName(mr)
	ref := strings.TrimPrefix(mr.Ref, "_")
	minutes := int((cur.ExpiresAt - now + 59_999) / 60_000)
	if !m.noticeToLead(mr, t, fmt.Sprintf(team.RelayAskNoticeFmt, address, ref, title, cur.UsedPct, minutes, ref), "relay ask") {
		return
	}
	if _, err := m.store.MarkAskNotified(cur.ID, m.now()); err != nil {
		m.logf("[team] relay ask notice %s: %v", a.ID, err)
	}
}

// settleAsks is the sweeper's liveness-tick step for relay asks (§3.3), in this order: an ask whose window has passed is
// expired; an ask whose member is no longer an active member of a live team is withdrawn (member_left); then every
// open ask still owing its notice is sent again. Nobody is told about an expiry or a withdrawal.
func (m *Module) settleAsks() {
	now := m.now()
	if n, err := m.store.ExpireRelayAsks(now); err != nil {
		m.logf("[team] sweep relay asks: %v", err)
	} else if n > 0 {
		m.logf("[team] %d relay ask(s) expired", n)
	}
	if n, err := m.store.WithdrawAsksOfInactiveMembers(now); err != nil {
		m.logf("[team] sweep relay asks: %v", err)
	} else if n > 0 {
		m.logf("[team] %d relay ask(s) withdrawn: the member left", n)
	}
	m.retryAskNotices()
}

// retryAskNotices is the sweeper's step (liveness tick): every open ask whose notice has not been delivered is sent
// again, with the minutes left. The window is not extended.
func (m *Module) retryAskNotices() {
	if m.sender == nil || m.stopping() {
		return
	}
	asks, err := m.store.ListUnnotifiedAsks(m.now())
	if err != nil {
		m.logf("[team] relay ask retry: %v", err)
		return
	}
	for _, a := range asks {
		m.notifyAskAsync(a)
	}
}
