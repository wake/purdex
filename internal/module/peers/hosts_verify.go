// internal/module/peers/hosts_verify.go
package peers

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// verifyHostResponse is POST /api/peers/hosts/{alias}/verify's 200 body:
// one scope=all fan-out row for this entry, minus its peer rows. Every
// probe outcome is a 200 with ok — "no outbound token", a transport
// error, "peer: <bounded>", "peer reported ok=false", "host_id mismatch:
// got <bounded>", "peer returned an invalid host_id" — the exact strings
// fetchHostResult produces, so this route and `pdx peers --all` can never
// disagree about one host. host_id is the configured value, or the learned
// one when the entry had none. self_alias and daemon_version are the
// peer's own words, bounded for display and NOT validated (v4 spec §7.1).
// Never a token value.
type verifyHostResponse struct {
	Alias         string `json:"alias"`
	HostID        string `json:"host_id"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	SelfAlias     string `json:"self_alias"`
	DaemonVersion string `json:"daemon_version"`
}

// handleVerifyHost serves POST /api/peers/hosts/{alias}/verify (spec
// §4.1): admin-only, body ignored, reads the entry under the lock, then
// runs fetchHostResult on the copy OUTSIDE the lock — the same probe one
// scope=all row is built by. It never writes: an entry that lacks a
// host_id has the learned one REPORTED, not stored (spec D-6; that state
// is only reachable by hand-editing the config, and a probe that is
// idempotent and side-effect-free is worth more than closing it).
func (m *Module) handleVerifyHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	alias := r.PathValue("alias")

	m.core.CfgMu.RLock()
	idx := m.core.Cfg.Peers.FindPeerHostByAlias(alias)
	var h config.PeerHost
	if idx != -1 {
		h = m.core.Cfg.Peers.Hosts[idx]
	}
	m.core.CfgMu.RUnlock()

	if idx == -1 {
		writeJSONError(w, http.StatusNotFound, "unknown alias")
		return
	}

	res := m.fetchHostResult(r.Context(), h)
	_ = json.NewEncoder(w).Encode(verifyHostResponse{
		Alias:         res.Alias,
		HostID:        res.HostID,
		OK:            res.OK,
		Error:         res.Error,
		SelfAlias:     res.SelfAlias,
		DaemonVersion: res.DaemonVersion,
	})
}
