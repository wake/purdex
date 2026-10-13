package teammod

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"sync"

	"github.com/wake/purdex/internal/team"
)

// personToken is one minted token (RT-1a). The table lives in memory: a restart drops it and the person types the command again.
type personToken struct {
	token    string
	session  string
	command  string
	expires  int64 // unix ms
	minted   int64
	consumed bool
}

// personTokens is the module's table of live person tokens, under its own mutex (never held with createMu's work).
type personTokens struct {
	mu   sync.Mutex
	live []*personToken
}

// mint adds a token for (session, command) good for ttlS seconds from now and returns it. At most team.PersonMaxLive unspent
// tokens of one session stay: the oldest is dropped. Spent and expired ones are swept here.
func (p *personTokens) mint(session, command string, ttlS int, now int64) personToken {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("person token: " + err.Error()) // crypto/rand does not fail on a working system
	}
	t := &personToken{token: hex.EncodeToString(raw[:]), session: session, command: command, expires: now + int64(ttlS)*1000, minted: now}
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.live[:0]
	for _, o := range p.live {
		if !o.consumed && o.expires > now {
			kept = append(kept, o)
		}
	}
	p.live = kept
	var mine []*personToken
	for _, o := range p.live {
		if o.session == session {
			mine = append(mine, o)
		}
	}
	if len(mine) >= team.PersonMaxLive {
		sort.SliceStable(mine, func(i, j int) bool { return mine[i].minted < mine[j].minted })
		drop := map[*personToken]bool{}
		for _, o := range mine[:len(mine)-team.PersonMaxLive+1] {
			drop[o] = true
		}
		kept = p.live[:0]
		for _, o := range p.live {
			if !drop[o] {
				kept = append(kept, o)
			}
		}
		p.live = kept
	}
	p.live = append(p.live, t)
	return *t
}

// consume is true for exactly one caller holding a token minted for this session and command that has not expired: it marks
// it spent. The comparison of the secret is constant-time.
func (p *personTokens) consume(session, command, token string, now int64) bool {
	if token == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, o := range p.live {
		if subtle.ConstantTimeCompare([]byte(o.token), []byte(token)) != 1 {
			continue
		}
		if o.consumed || o.expires <= now || o.session != session || o.command != command {
			return false
		}
		o.consumed = true
		return true
	}
	return false
}

// consumePerson is the module's use of the table.
func (m *Module) consumePerson(session, command, token string) bool {
	return m.persons.consume(session, command, token, m.now())
}

// handlePerson is POST /api/relay/person: mint a token for the mod of a live session of this host.
func (m *Module) handlePerson(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.PersonRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	ttl := team.PersonTTLS(req.Command)
	if req.SessionID == "" || ttl == 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id and a command of relay, adopt, lead or spawn are required", nil)
		return
	}
	_, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrUnknownSession, "session_id is not a live Claude Code session on this host", nil)
		return
	}
	t := m.persons.mint(req.SessionID, req.Command, ttl, m.now())
	m.writeJSON(w, http.StatusOK, team.PersonResponse{Token: t.token, ExpiresAt: t.expires})
}

// personClose is the close of a request the person approved by a typed command: the person decides, the daemon spends nothing.
func (m *Module) personClose(at int64) Close {
	by := team.PersonClient()
	return Close{State: team.StateApproved, DecidedAt: at, DecidedBy: &by}
}

// beginPerson is the person's /relay when the session has no open request: the op and its row are written approved at once
// (no quota read or spent, the member check passes for a manual payload), announced closed only, then the session's pauses
// end without a reask — the relay is the answer. Caller holds createMu.
func (m *Module) beginPerson(w http.ResponseWriter, op team.RelayOp, row team.Approval, hash string) {
	after, claimed, err := m.store.CreateSelfRelayApproved(op, row, hash, m.personClose(row.CreatedAt))
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
	m.logf("[team] relay op %s claimed: self, origin=%s (%s), request %s approved by the person at begin", op.ID, op.Ref, op.SessionID, after.ID)
	m.announceClosed(after, nil)
	m.releasePausesForRelay(op.SessionID)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: claimed, RequestID: after.ID})
}

// personApproveOpen approves the session's open self-relay card (awaiting approval) as the person: the card is answered by the
// person's own /relay, not refused as "a relay is open". Caller holds createMu.
func (m *Module) personApproveOpen(w http.ResponseWriter, sessionID string, op team.RelayOp) {
	row, ok, err := m.store.Get(op.RequestID)
	if err != nil || !ok {
		m.logf("[team] relay begin %s: card %s: ok=%v err=%v", sessionID, op.RequestID, ok, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	after, won, memberCancelled, err := m.approve(row, m.personClose(m.now()))
	if err != nil {
		m.logf("[team] relay begin %s: approve card %s: %v", sessionID, row.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if memberCancelled || (!won && after.State != team.StateApproved) {
		m.writeRelayOpen(w, sessionID) // the card closed another way first: a relay (or its end) is what the session has
		return
	}
	m.releasePausesForRelay(sessionID)
	cur, found, err := m.store.RelayOpByRequest(row.ID)
	if err != nil || !found {
		cur = op
	}
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: cur, RequestID: row.ID})
}

// releasePausesForRelay ends the pauses of a session its person relayed by hand, asking nothing again. Caller holds createMu.
func (m *Module) releasePausesForRelay(sessionID string) {
	if _, err := m.resumeScope(ResumeScope{Release: sessionID, NoReask: true}); err != nil {
		m.logf("[team] release pauses of %s after its person's relay: %v", sessionID, err)
	}
}
