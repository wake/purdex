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
// lock. The two gates read the in-memory record (rotation.go) inside that
// closure and HOLD rotMu from the check through the write, so a dial that
// authenticates concurrently is recorded only after the write: it is future
// evidence, visible on the next read. Neither gate is a proof about the
// future; each is a proof that the operation is not ALREADY known to be a
// lock-out (D-7). Lock order CfgMu → rotMu (the closure runs under CfgMu).
//
// Cancel's record rewrite (confirmCancelledRotation: a "prev" note becomes
// "current" because that token IS current again) happens only AFTER
// UpdateConfig has returned nil, i.e. only after config.WriteFile has
// persisted the swap. Doing it inside the closure would make the record
// describe a write that might still fail (config.WriteFile, core.go): if
// the write errors, UpdateConfig leaves the config untouched (still
// pending, current = new token) but a rewrite done before the write would
// have already turned the record "current" — passing the very commit gate
// that exists to stop the peer, still on the OLD token, from being locked
// out. A "prev" note landing between the persisted write and the
// after-the-fact rewrite is still correctly rewritten to "current" (that
// token really is current by then); a "current" note cannot land in that
// window because the new token no longer authenticates once the write has
// landed. (`rotate`'s `resetInboundAuth` runs before its write too, but
// that ordering fails CLOSED — a failed rotate just leaves the reset
// record wanting a fresh dial — so it is left as-is.)

// rotateResponse is the second and last response that carries a live
// inbound-token value (the first is POST 201).
type rotateResponse struct {
	Alias        string `json:"alias"`
	InboundToken string `json:"inbound_token"`
}

type rotateGateRequest struct {
	Force bool `json:"force"`
}

// decodeGateRequest reads an optional {force} body: empty body = no force,
// invalid JSON = 400.
func decodeGateRequest(r *http.Request) (rotateGateRequest, error) {
	var req rotateGateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return req, err
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
		last := m.lastInboundAuthLocked(h.Alias)
		if h.InboundTokenPrev == "" {
			row = m.hostRowLocked(*h, last)
			return nil
		}
		if !req.Force && last != inboundAuthCurrent {
			return &apiError{http.StatusConflict, "rotation unconfirmed"}
		}
		if m.rotateAfterGate != nil {
			m.rotateAfterGate()
		}
		h.InboundTokenPrev = ""
		row = m.hostRowLocked(*h, last)
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

	var committed config.PeerHost
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
		if !req.Force && m.lastInboundAuthLocked(h.Alias) != inboundAuthPrev {
			return &apiError{http.StatusConflict, "rotation unconfirmed"}
		}
		if m.rotateAfterGate != nil {
			m.rotateAfterGate()
		}
		h.InboundToken = h.InboundTokenPrev
		h.InboundTokenPrev = ""
		committed = *h
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	// The swap is now persisted (UpdateConfig returned nil). Only now is it
	// true that the peer's old token IS the current one, so only now does a
	// "prev" record become "current".
	m.rotMu.Lock()
	m.confirmCancelledRotation(committed.Alias)
	last := m.lastInboundAuthLocked(committed.Alias)
	m.rotMu.Unlock()
	row := m.hostRowLocked(committed, last)
	_ = json.NewEncoder(w).Encode(row)
}
