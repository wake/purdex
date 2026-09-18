package peers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// Inbound-token rotation routes (spec §6.3). All three are admin-only by
// construction (HostRoutePolicy) and by requireAdmin; all three mutate
// inside one UpdateConfig closure, re-finding the entry by alias under the
// lock. The two gates derive the state from the in-memory record
// (rotation.go) inside that closure and HOLD rotMu from the check through
// the write. A host-token match and its observation happen together under
// CfgMu.RLock (the matcher in cmd/pdx/http_chain.go calls noteInboundFP
// there), and the closure runs under CfgMu.Lock, so every authentication
// that completed before the gate is visible to it; one that has not yet
// matched when the write lands authenticates against the new state and is
// future evidence, judged on the next read by the token it carries.
// Neither gate is a proof about the future; each is a proof that the
// operation is not ALREADY known to be a lock-out (D-7).
// Lock order CfgMu → rotMu (the closure runs under CfgMu).
//
// The record stores WHICH token the peer presented (by fingerprint) and
// "current"/"prev" is derived against the entry's tokens at read time, so
// the state is bound to the epoch by construction and no handler ever
// rewrites it:
//   - a note that raced a rotate (matched by PeerAuth before the rotate,
//     landed after its reset) reads "prev" — that token IS now the prev,
//     and the commit gate refuses on it;
//   - after a cancel the peer's token IS current, so a "prev" note reads
//     "current" by itself — there is no post-persist rewrite, and hence no
//     window in which a failed write or a racing rotate could see a record
//     that describes a swap that did not happen;
//   - after a commit or cancel, a note for the token that was dropped
//     reads "" — it names a token the entry no longer has.
// `rotate`'s `resetInboundAuth` runs before its write, but that ordering
// fails CLOSED — a failed rotate just leaves the reset record wanting a
// fresh dial — so it is left as-is.

// rotateResponse is the second and last response that carries a live
// inbound-token value (the first is POST 201).
type rotateResponse struct {
	Alias        string `json:"alias"`
	InboundToken string `json:"inbound_token"`
}

type rotateGateRequest struct {
	Force bool `json:"force"`
}

// decodeGateRequest reads an optional {force} body: empty body = no force;
// otherwise the body must be EXACTLY one JSON value — truncated JSON,
// trailing bytes after the value (`{"force":true} garbage`) and a second
// value (`{}{}`) are all errors (400). A decoder stops at the end of the
// first value, so the second Decode must report io.EOF for the body to
// have been a single value (codex F1).
func decodeGateRequest(r *http.Request) (rotateGateRequest, error) {
	var req rotateGateRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return rotateGateRequest{}, nil // empty body
		}
		return req, err
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing data after json value")
		}
		return rotateGateRequest{}, err
	}
	return req, nil
}

// handleRotateHost: prev := current; current := mint(); record := "".
func (m *Module) handleRotateHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")

	m.core.CfgMu.RLock()
	adminToken := m.core.Cfg.Token
	m.core.CfgMu.RUnlock()
	tok, err := mintInboundToken(adminToken)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var resp rotateResponse
	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		if h.InboundTokenPrev != "" {
			return &apiError{http.StatusConflict, "rotation already pending"}
		}
		h.InboundTokenPrev = h.InboundToken
		h.InboundToken = tok
		// New epoch: a "current" seen before this rotation must never
		// satisfy this rotation's commit gate.
		m.resetInboundAuth(h.Alias)
		resp = rotateResponse{Alias: h.Alias, InboundToken: tok}
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleRotateCommit: prev := "". Idempotent (no prev → 200 no-op); refused
// while the peer has not been seen on the NEW token in this epoch, unless
// force.
func (m *Module) handleRotateCommit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")
	req, err := decodeGateRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var row hostRow
	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		m.rotMu.Lock()
		defer m.rotMu.Unlock()
		if h.InboundTokenPrev == "" {
			row = m.hostRowLocked(*h)
			return nil
		}
		if !req.Force && m.lastInboundAuthLocked(*h) != inboundAuthCurrent {
			return &apiError{http.StatusConflict, "rotation unconfirmed"}
		}
		if m.rotateAfterGate != nil {
			m.rotateAfterGate()
		}
		h.InboundTokenPrev = ""
		row = m.hostRowLocked(*h)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(row)
}

// handleRotateCancel: current := prev; prev := "". Refused when nothing is
// pending (there is nothing safe to restore) and, unless force, when the
// peer was not last seen on the OLD token (it may already hold the new one).
func (m *Module) handleRotateCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")
	req, err := decodeGateRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var row hostRow
	err = m.core.UpdateConfig(func(cfg *config.Config) error {
		i := cfg.Peers.FindPeerHostByAlias(alias)
		if i == -1 {
			return &apiError{http.StatusNotFound, "unknown alias"}
		}
		h := &cfg.Peers.Hosts[i]
		m.rotMu.Lock()
		defer m.rotMu.Unlock()
		if h.InboundTokenPrev == "" {
			return &apiError{http.StatusConflict, "no rotation pending"}
		}
		if !req.Force && m.lastInboundAuthLocked(*h) != inboundAuthPrev {
			return &apiError{http.StatusConflict, "rotation unconfirmed"}
		}
		if m.rotateAfterGate != nil {
			m.rotateAfterGate()
		}
		h.InboundToken = h.InboundTokenPrev
		h.InboundTokenPrev = ""
		// The record is untouched: the peer's token is now the entry's
		// current one, so its note derives "current" from here on. If the
		// write fails, UpdateConfig restores the pending entry and the
		// same note derives "prev" again — nothing to undo.
		row = m.hostRowLocked(*h)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(row)
}
