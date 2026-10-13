// internal/module/team/pause_release.go — POST /api/team/paused/{session_id}/release (unattended quota pause spec 2026-10-12 §3.4, UP-3b).
package teammod

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/team"
)

// handlePausedRelease clears one session's pause: the resume group for that session — a lead takes the members it paused with it
// (their `lead` rows) — with the release text; unattended stays on. The body's client is an audit claim like PUT /api/team/roots's
// (kind "app", a label): told, not enforced. A session that holds no pause row is 404 not_paused (not 409: the resource does not
// exist, and a second click on 「解除」 has nothing left to do).
func (m *Module) handlePausedRelease(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.PauseReleaseRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `client must be {"kind":"app","label":…}`, nil)
		return
	}
	sid := r.PathValue("session_id")
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	held, err := m.store.PausesAmong([]string{sid})
	if err != nil {
		m.logf("[team] release %s: %v", sid, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if _, paused := held[sid]; !paused {
		m.writeErr(w, http.StatusNotFound, team.ErrNotPaused, "this session is not paused", nil)
		return
	}
	client := req.Client
	client.Addr = r.RemoteAddr
	n, err := m.releaseOne(sid, client)
	if err != nil {
		m.logf("[team] release %s: %v", sid, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] pause of %s released by app %q from %s (%d session(s) resumed)", sid, strings.TrimSpace(req.Client.Label), r.RemoteAddr, n)
	m.writeJSON(w, http.StatusOK, team.PauseReleaseResponse{Released: n})
}

// releaseOne is a person's release of sid. A session that also stopped because its final relay card went unanswered (RT-2a2): the
// person chose not to relay it, so the card closes denied by the request's client (which removes that pause row and, when it was
// the only cause, resumes the session with the denial), and the other causes are released without a reask - asking again would
// undo what the person just decided. Caller holds createMu.
func (m *Module) releaseOne(sid string, client team.Client) (int, error) {
	id, err := m.store.OpenUnansweredCardOf(sid)
	if err != nil {
		return 0, err
	}
	if id != "" {
		return m.releaseCard(sid, id, client)
	}
	if ask, ok, err := m.store.OpenAskOf(sid); err != nil {
		return 0, err
	} else if ok && ask.UnansweredAt > 0 {
		return m.releaseAsk(sid)
	}
	return m.resumeOne(sid)
}

// releaseAsk is a person's release of a member its lead's silence paused (#2816): the final ask closes withdrawn / denied - the
// person decided - and the pause row goes with it, and the other causes are released without a reask, all in ONE transaction (a
// failure leaves the ask, the pauses and the outbox as they were, so a retry cannot turn the person's refusal into a reask); the lead
// is told. A member that has left, or an ask another close took first, is an ordinary release. Caller holds createMu.
func (m *Module) releaseAsk(sid string) (int, error) {
	// what the lead's notice needs is read BEFORE the ask closes: a read that fails then fails the release with the ask still open
	// (codex R1); whether the member is still active is decided again inside the transaction (codex attack)
	mr, t, isMember, err := m.store.ActiveMemberInLiveTeam(sid)
	if err != nil {
		return 0, err
	}
	m.askMu.Lock() // behind an ask notice on the wire
	ask, closed, res, err := m.store.ReleaseDeniedAsk(sid, m.now(), m.speaksReask, uuid.NewString)
	m.askMu.Unlock()
	if err != nil {
		return 0, err
	}
	if !closed {
		return m.resumeOne(sid)
	}
	m.logf("[team] relay ask %s of %s denied by a person's release", ask.ID, sid)
	if isMember {
		address, _ := m.memberNoticeName(mr)
		text := fmt.Sprintf(team.RelayAskDeniedByUserLeadFmt, address)
		m.goTracked(func() { m.noticeToLead(mr, t, text, "relay ask denied by a person") })
	}
	m.askAnswered()
	m.kickCommands()
	n := res.Sessions
	if n == 0 { // the ask's close was the whole release
		n = 1
	}
	return n, nil
}

// releaseCard denies the card id of sid for the person and releases the other causes in the SAME transaction (a failure rolls the
// deny back, so a retry cannot turn the refusal into a reask, #2821); when the deny lost, it is an ordinary release.
func (m *Module) releaseCard(sid, id string, client team.Client) (int, error) {
	var res ResumeResult
	now := m.now()
	_, won, err := m.closeAs(id, Close{State: team.StateDenied, DecidedAt: now, DecidedBy: &client, AfterWon: func(tx *sql.Tx) error {
		if m.store.afterAskDeny != nil { // test seam: a failure between the deny and the release
			if err := m.store.afterAskDeny(); err != nil {
				return err
			}
		}
		var err error
		res, err = m.store.resumePausesIn(tx, ResumeScope{Release: sid, NoReask: true, NoReaskText: team.PauseResumedUnansweredDenied}, now, m.speaksReask, uuid.NewString)
		return err
	}})
	if err != nil {
		return 0, err
	}
	if !won {
		// Another close (an approve, the session's own cancel) took the card first and told the session how it ended: this click did
		// not decide it, so the rest is an ordinary release (codex R1).
		return m.resumeOne(sid)
	}
	if res.Rows > 0 {
		m.rosterChanged()
		m.kickPauseOutbox()
		m.kickCommands()
	}
	n := res.Sessions
	if n == 0 { // the card's close was the whole release
		n = 1
	}
	return n, nil
}
