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
	// A member whose lead is on another host asks through the lead host's capabilities first (MR-4, D8).
	if row, ok, err := m.store.ActiveRemoteMemberBySession(req.SessionID); err != nil {
		m.logf("[team] relay ask %s: %v", req.RequestID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	} else if ok {
		if !m.memberAutoOn() {
			m.writeMemberAutoOff(w)
			return
		}
		m.askRemote(w, req, row)
		return
	}
	// The same mutex as the lead's relay create: an ask and a `pdx relay` for the same member are strictly ordered
	// (ask first → the relay accepts it; relay first → the ask is refused relay_open).
	m.createMu.Lock()
	// member_auto is read again under createMu (an adopt takes the same lock, so this read and the member's state are ordered):
	// off, an active member of a live team is refused; anything else keeps the answer it always had (not_member).
	if !m.memberAutoOn() {
		_, _, isMember, merr := m.store.ActiveMemberInLiveTeam(req.SessionID)
		if merr != nil || isMember {
			m.createMu.Unlock()
			if merr != nil {
				m.logf("[team] relay ask %s: %v", req.RequestID, merr)
				m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
				return
			}
			m.writeMemberAutoOff(w)
			return
		}
	}
	now := m.now()
	ask, replay, err := m.store.CreateRelayAsk(RelayAsk{ID: req.RequestID, SessionID: req.SessionID, UsedPct: req.UsedPct, Window: req.Window,
		CreatedAt: now, ExpiresAt: now + team.RelayAskHoldS*1000})
	// Unattended, the daemon answers a new ask itself (RT-2b T22), still under createMu so no `pdx relay` slips between.
	if err == nil && !replay && m.unattendedOn() && m.acceptAskUnattended(ask) {
		if cur, ok, gerr := m.store.GetRelayAsk(ask.ID); gerr == nil && ok {
			ask = cur
		}
		m.createMu.Unlock()
		m.writeJSON(w, http.StatusOK, team.RelayAskResponse{ID: ask.ID, State: ask.State, ExpiresAt: ask.ExpiresAt})
		return
	}
	if err == nil && !replay { // an ask that was paused or withdrawn above is not the lead's to answer: re-read for the response and the notice
		if cur, ok, gerr := m.store.GetRelayAsk(ask.ID); gerr == nil && ok {
			ask = cur
		}
	}
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
	if !replay && ask.State == team.RelayAskOpen {
		m.notifyAskAsync(ask)
	}
	m.writeJSON(w, http.StatusOK, team.RelayAskResponse{ID: ask.ID, State: ask.State, ExpiresAt: ask.ExpiresAt, Replay: replay})
}

// writeMemberAutoOff is the answer to an ask while relay.member_auto is off. relay_unsupported is the code the mod already
// treats as "do not ask again for another 10 points" (spec member-relay-switch §2-3).
func (m *Module) writeMemberAutoOff(w http.ResponseWriter) {
	m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "member 自動接力已在主機設定關閉 (member automatic relay is off in the host config)", nil)
}

// notifyAskAsync sends the lead's notice off the request's path; false when the daemon is stopping (notified_at stays 0,
// and the next start's sweeper sends it).
func (m *Module) notifyAskAsync(a RelayAsk) bool {
	return m.goTracked(func() { m.sendAskNotice(a) })
}

// sendAskNotice tells the lead about an open, unnotified ask, with the minutes left of its window (rounded up), and
// records the delivery. Safe to call again and from two places: one send at a time, and nothing once the ask is
// notified, closed or past its window. The notice is at-least-once, like the 70% one: a crash between the send and the
// mark sends it once more after the restart (with the minutes then left), which is harmless — the lead's answer is the
// same command; an outbox is not worth it for an advisory.
func (m *Module) sendAskNotice(a RelayAsk) {
	if m.sender == nil || m.stopping() {
		return
	}
	// One send at a time, from the state check to the delivery mark; a second sender waits and then finds the ask
	// notified. A compaction takes the same lock to withdraw (handleRelayCompacted), so "approve within N minutes" is
	// never sent after the compaction notice's withdrawal.
	m.askMu.Lock()
	defer m.askMu.Unlock()

	cur, ok, err := m.store.GetRelayAsk(a.ID)
	if err != nil {
		m.logf("[team] relay ask notice %s: %v", a.ID, err)
		return
	}
	now := m.now()
	if !ok || cur.State != team.RelayAskOpen || cur.NotifiedAt != 0 || cur.ExpiresAt <= now {
		return
	}
	// The final gate (member_auto, spec member-relay-switch §2): read right before the send, after the state re-check, so a
	// switch turned off since the ask was created (or the mirror applied) sends nothing. The ask stays unnotified.
	if !m.memberAutoOn() {
		return
	}
	mr, t, ok, err := m.store.ActiveMemberInLiveTeam(cur.SessionID)
	if err != nil {
		m.logf("[team] relay ask notice %s: %v", a.ID, err)
		return
	}
	minutes := int((cur.ExpiresAt - now + 59_999) / 60_000)
	var text string
	if ok {
		address, title := m.memberNoticeName(mr)
		ref := strings.TrimPrefix(mr.Ref, "_")
		text = fmt.Sprintf(team.RelayAskNoticeFmt, address, ref, title, cur.UsedPct, minutes, ref, ref)
		if cur.UsedPct >= team.RelayLastAskPct {
			text += team.RelayAskFinalSuffix
		}
	} else {
		// a mirror of a remote member's ask (MR-4): the member is a row of another host, named as the lead can address it
		var rok bool
		if mr, t, rok, err = m.store.remoteAskMember(cur); err != nil {
			m.logf("[team] relay ask notice %s: %v", a.ID, err)
			return
		} else if !rok {
			return // the member left: the sweeper withdraws the ask
		}
		if text = m.remoteAskNoticeText(mr, minutes, cur.UsedPct); text == "" {
			m.logf("[team] relay ask notice %s: host %s has no alias here; nothing the lead could type", a.ID, mr.HostID)
			return
		}
	}
	if !m.noticeToLead(mr, t, text, "relay ask") {
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
	// Behind a notice that is on the wire (askMu, see sendAskNotice): an ask is not closed between a notice's state
	// check and its delivery. Released before the retry below, whose senders take it themselves. The time is read after
	// the wait, so the decisions and the closed_at stamps are those of the moment the sweep runs.
	m.askMu.Lock()
	now := m.now()
	expired, errE := m.store.ExpireRelayAsks(now)
	left, errW := m.store.WithdrawAsksOfInactiveMembers(now)
	m.askMu.Unlock()
	m.markFinalAsks()
	for _, err := range []error{errE, errW} {
		if err != nil {
			m.logf("[team] sweep relay asks: %v", err)
		}
	}
	if expired > 0 {
		m.logf("[team] %d relay ask(s) expired", expired)
	}
	if left > 0 {
		m.askAnswered()
		m.logf("[team] %d relay ask(s) withdrawn: the member left", left)
	}
	m.acceptOpenAsksOnTick()
	m.retryAskNotices()
}

// acceptOpenAsksOnTick is the liveness tick's accept of the asks still open while the switch is on. It takes createMu, so it only
// reads the store first: a tick with no open ask costs one query.
func (m *Module) acceptOpenAsksOnTick() {
	asks, err := m.store.listOpenAsks(m.now())
	if err != nil || len(asks) == 0 {
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	m.acceptOpenAsksUnattended()
}

// retryAskNotices is the sweeper's step (liveness tick): every open ask whose notice has not been delivered is sent
// again, with the minutes left. The window is not extended.
func (m *Module) retryAskNotices() {
	if m.sender == nil || m.stopping() || !m.memberAutoOn() {
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
