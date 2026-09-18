package peers

import (
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
)

// Rotation bookkeeping (spec §6.2). lastInbound remembers, per alias, WHICH
// token the peer MOST RECENTLY authenticated with — by non-reversible
// fingerprint (config.TokenFingerprint), never the value. The served state
// ("" | "current" | "prev") is not stored; it is DERIVED at read time by
// comparing that fingerprint against the entry's InboundToken and
// InboundTokenPrev as they are at that moment. So the state is bound to
// the rotation epoch by construction, not by when the note landed:
//
//   - a note for a dial that authenticated just BEFORE a rotate but landed
//     after it (the handler's note, below, is a separate critical section
//     from the match) reads "prev" — that token is now the entry's prev,
//     and a commit on it would lock the peer out;
//   - after a cancel, a note for the old token reads "current" by itself —
//     no rewrite step, nothing to order against the persist;
//   - after a commit (or cancel), a note for the token that was dropped
//     reads "" — it names a token the entry no longer has.
//
// WHO NOTES. The authoritative observation is the middleware one: Init
// installs noteInboundFP as core.HostAuthObserver, and the peer auth
// matcher (cmd/pdx/http_chain.go) calls it for every successful host-token
// match INSIDE the same CfgMu.RLock hold as the match. The gates run under
// CfgMu.Lock (UpdateConfig), so every authentication that completed before
// a commit/cancel is visible to its gate — a request that has already
// matched the old token but not yet reached its handler can no longer be
// invisible to a commit that then drops that token (codex F2). The handlers'
// noteInboundAuth(principal) is an idempotent second note of the same fact
// (kept so handler tests that inject principals without the middleware
// still exercise the record).
//
// It is in memory on purpose: after a daemon restart it reads "" and both
// commit and cancel are refused until the peer dials again — fail-closed,
// in the state (both tokens valid) the daemon was already in. Persisting it
// would buy nothing (a stored value is older than a fresh dial, and the
// gates want the freshest) and would cost a write on every host-principal
// request.
//
// Lock order: core.CfgMu → rotMu. The gates read the record inside an
// UpdateConfig closure (CfgMu held) and take rotMu there; noteInboundFP
// takes rotMu only and never CfgMu (it is called with CfgMu.RLock already
// held by the matcher), so the two can never deadlock.

const (
	inboundAuthCurrent = "current"
	inboundAuthPrev    = "prev"
)

// inboundAuth is one observation (spec §6.2): which token (fingerprint),
// and when. `at` is m.now() at the note; it is kept for logs/debugging and
// never served — "most recent" is the write order under rotMu, which is
// the order the daemon observed the dials in.
type inboundAuth struct {
	fp string
	at time.Time
}

// noteInboundFP records that the host entry alias was authenticated by the
// token with fingerprint fp. It is the core.HostAuthObserver (installed by
// Init) and takes rotMu only — never CfgMu, which the caller already holds
// for reading. Empty inputs mean there is nothing to bind and are ignored.
func (m *Module) noteInboundFP(alias, fp string) {
	if alias == "" || fp == "" {
		return
	}
	m.rotMu.Lock()
	if m.lastInbound == nil {
		m.lastInbound = map[string]inboundAuth{}
	}
	m.lastInbound[alias] = inboundAuth{fp: fp, at: m.now()}
	m.rotMu.Unlock()
}

// noteInboundAuth is the handlers' second, idempotent note of the fact the
// matcher already observed (see the header). Called at the two places a
// host principal is served — handlePeers and handleDeliver — BEFORE any
// policy or rate-limit refusal can return: the fact recorded is "this
// bearer authenticated", which is true whether or not the request is then
// refused. Admin principals are not peer dials and are ignored, as is a
// host principal that carries no fingerprint (there is nothing to bind).
func (m *Module) noteInboundAuth(p middleware.Principal) {
	if p.Kind != middleware.PrincipalHost {
		return
	}
	m.noteInboundFP(p.Alias, p.TokenFingerprint)
}

// lastInboundAuthLocked derives "" | "current" | "prev" for the entry h
// from its record: the noted fingerprint against h's tokens as h is NOW.
// "" means no note, or a note for a token h no longer has. The caller
// holds rotMu — the gates do, across their check AND their write, so the
// record cannot change between the two.
func (m *Module) lastInboundAuthLocked(h config.PeerHost) string {
	a, ok := m.lastInbound[h.Alias]
	if !ok || a.fp == "" {
		return ""
	}
	if a.fp == config.TokenFingerprint(h.InboundToken) {
		return inboundAuthCurrent
	}
	if h.InboundTokenPrev != "" && a.fp == config.TokenFingerprint(h.InboundTokenPrev) {
		return inboundAuthPrev
	}
	return ""
}

func (m *Module) lastInboundAuth(h config.PeerHost) string {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	return m.lastInboundAuthLocked(h)
}

// renameInboundAuth moves the record with the entry (PUT rename, spec §4.2):
// the evidence is about the peer behind the entry, not about its name.
// oldAlias must be the entry's STORED alias (the map key), not the request
// path's spelling — FindPeerHostByAlias is case-insensitive, the map is not.
func (m *Module) renameInboundAuth(oldAlias, newAlias string) {
	m.rotMu.Lock()
	if a, ok := m.lastInbound[oldAlias]; ok {
		delete(m.lastInbound, oldAlias)
		m.lastInbound[newAlias] = a
	}
	m.rotMu.Unlock()
}

// resetInboundAuth clears the record. rotate calls it so the row reads ""
// right after a rotate (spec §6.2) — a note that lands afterwards for a
// pre-rotate dial still derives "prev", because it is judged by its token.
// delete calls it so a re-created entry does not inherit a stranger's
// evidence. alias must be the entry's stored alias.
func (m *Module) resetInboundAuth(alias string) {
	m.rotMu.Lock()
	delete(m.lastInbound, alias)
	m.rotMu.Unlock()
}

// hostRowLocked is toHostRow plus the two rotation fields, derived from the
// record under rotMu, which the caller holds. Never a token value.
func (m *Module) hostRowLocked(h config.PeerHost) hostRow {
	row := toHostRow(h)
	row.RotationPending = h.InboundTokenPrev != ""
	row.LastInboundAuth = m.lastInboundAuthLocked(h)
	return row
}

// hostRowFor is hostRowLocked for callers that do not hold rotMu.
func (m *Module) hostRowFor(h config.PeerHost) hostRow {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	return m.hostRowLocked(h)
}
