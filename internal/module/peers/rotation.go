package peers

import (
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
)

// Rotation bookkeeping (spec §6.2). lastInbound remembers, per alias, which
// of the entry's two inbound tokens the peer MOST RECENTLY authenticated
// with: "current" or "prev". It is in memory on purpose: after a daemon
// restart it reads "" and both commit and cancel are refused until the peer
// dials again — fail-closed, in the state (both tokens valid) the daemon was
// already in. Persisting it would buy nothing (a stored value is older than
// a fresh dial, and the gates want the freshest) and would cost a write on
// every host-principal request.
//
// Lock order: core.CfgMu → rotMu. The gates read the record inside an
// UpdateConfig closure (CfgMu held) and take rotMu there; noteInboundAuth
// takes rotMu only and never CfgMu, so the two can never deadlock.

const (
	inboundAuthCurrent = "current"
	inboundAuthPrev    = "prev"
)

// inboundAuth is one observation (spec §6.2): which token, and when. `at`
// is m.now() at the note; it is kept for logs/debugging and never served —
// "most recent" is the write order under rotMu, which is the order the
// daemon observed the dials in.
type inboundAuth struct {
	usedPrev bool
	at       time.Time
}

func (a inboundAuth) String() string {
	if a.usedPrev {
		return inboundAuthPrev
	}
	return inboundAuthCurrent
}

// noteInboundAuth records which token a host principal presented. Called at
// the two places a host principal is served — handlePeers and handleDeliver
// — BEFORE any policy or rate-limit refusal can return: the fact recorded is
// "this bearer authenticated", which is true whether or not the request is
// then refused. Admin principals are not peer dials and are ignored.
func (m *Module) noteInboundAuth(p middleware.Principal) {
	if p.Kind != middleware.PrincipalHost || p.Alias == "" {
		return
	}
	m.rotMu.Lock()
	if m.lastInbound == nil {
		m.lastInbound = map[string]inboundAuth{}
	}
	m.lastInbound[p.Alias] = inboundAuth{usedPrev: p.UsedPrevToken, at: m.now()}
	m.rotMu.Unlock()
}

// lastInboundAuthLocked is "" | "current" | "prev" for alias in the current
// rotation epoch. The caller holds rotMu — the gates do, across their check
// AND their write, so the record cannot change between the two.
func (m *Module) lastInboundAuthLocked(alias string) string {
	a, ok := m.lastInbound[alias]
	if !ok {
		return ""
	}
	return a.String()
}

func (m *Module) lastInboundAuth(alias string) string {
	m.rotMu.Lock()
	defer m.rotMu.Unlock()
	return m.lastInboundAuthLocked(alias)
}

// renameInboundAuth moves the record with the entry (PUT rename, spec §4.2):
// the evidence is about the peer behind the entry, not about its name.
func (m *Module) renameInboundAuth(oldAlias, newAlias string) {
	m.rotMu.Lock()
	if a, ok := m.lastInbound[oldAlias]; ok {
		delete(m.lastInbound, oldAlias)
		m.lastInbound[newAlias] = a
	}
	m.rotMu.Unlock()
}

// resetInboundAuth starts a new epoch: rotate calls it so a "current" seen
// before this rotation can never satisfy this rotation's commit gate.
func (m *Module) resetInboundAuth(alias string) {
	m.rotMu.Lock()
	delete(m.lastInbound, alias)
	m.rotMu.Unlock()
}

// confirmCancelledRotation keeps the record truthful after a cancel: the
// token the peer was last seen on (prev) is the current one again, so a
// record of "prev" becomes "current". After a cancel there is no prev, so
// the record can only read "" or "current" (spec §6.2). Caller holds rotMu.
func (m *Module) confirmCancelledRotation(alias string) {
	if a, ok := m.lastInbound[alias]; ok && a.usedPrev {
		a.usedPrev = false
		m.lastInbound[alias] = a
	}
}

// hostRowLocked is toHostRow plus the two rotation fields, with the record
// value the caller already read under rotMu. Never a token value.
func (m *Module) hostRowLocked(h config.PeerHost, last string) hostRow {
	row := toHostRow(h)
	row.RotationPending = h.InboundTokenPrev != ""
	row.LastInboundAuth = last
	return row
}

// hostRowFor is hostRowLocked for callers that do not hold rotMu.
func (m *Module) hostRowFor(h config.PeerHost) hostRow {
	return m.hostRowLocked(h, m.lastInboundAuth(h.Alias))
}
