package teammod

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
	p.prune(now)
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
		kept := p.live[:0]
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

// prune drops the spent-and-expired tokens; caller holds p.mu.
func (p *personTokens) prune(now int64) {
	kept := p.live[:0]
	for _, o := range p.live {
		if !o.consumed && o.expires > now {
			kept = append(kept, o)
		}
	}
	for i := len(kept); i < len(p.live); i++ {
		p.live[i] = nil // do not keep the dropped ones reachable through the backing array
	}
	p.live = kept
}

// find is the live token that is this exact secret for this session and command, nil when there is none. The comparison of the
// secret is constant-time. Caller holds p.mu.
func (p *personTokens) find(session, command, token string, now int64) *personToken {
	if token == "" {
		return nil
	}
	p.prune(now)
	for _, o := range p.live {
		if subtle.ConstantTimeCompare([]byte(o.token), []byte(token)) != 1 {
			continue
		}
		if o.session != session || o.command != command {
			return nil
		}
		return o
	}
	return nil
}

// valid says whether token is a live token for this session and command, without spending it: a request that uses it spends it
// only once its effect is committed (spend), so a refusal or a storage failure costs the person nothing. The callers that do
// this hold createMu, which is what makes check-then-spend one step against another request with the same token.
func (p *personTokens) valid(session, command, token string, now int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.find(session, command, token, now) != nil
}

// spend removes the token (it is used). Unknown tokens are ignored.
func (p *personTokens) spend(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, o := range p.live {
		if subtle.ConstantTimeCompare([]byte(o.token), []byte(token)) == 1 {
			p.live = append(p.live[:i], p.live[i+1:]...)
			return
		}
	}
}

// consume is true for exactly one caller holding a live token for this session and command: it spends it in the same step.
func (p *personTokens) consume(session, command, token string, now int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	o := p.find(session, command, token, now)
	if o == nil {
		return false
	}
	o.consumed = true
	p.prune(now)
	return true
}

// consumePerson is the module's use of the table.
func (m *Module) consumePerson(session, command, token string) bool {
	return m.persons.consume(session, command, token, m.now())
}

// validPerson is valid() at the module's clock.
func (m *Module) validPerson(session, command, token string) bool {
	return m.persons.valid(session, command, token, m.now())
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
