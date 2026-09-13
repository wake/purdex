// internal/module/peers/settings.go
package peers

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// settingsResponse is the body of both GET and PUT /api/peers/settings.
type settingsResponse struct {
	Deliver bool   `json:"deliver"`
	Alias   string `json:"alias"`
}

// putSettingsRequest is PUT /api/peers/settings' body. Deliver is a
// pointer: absent (nil) means "leave unchanged", present sets the value —
// mirroring putHostRequest.AllowBypass's convention in hosts.go.
type putSettingsRequest struct {
	Deliver *bool `json:"deliver"`
}

// handleGetSettings serves GET /api/peers/settings: this host's current
// deliver toggle and its own alias. Admin-only (policy.go's HostRoutePolicy
// already refuses every host principal on this path; requireAdmin is the
// defense-in-depth check for when the handler is exercised directly).
func (m *Module) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}

	m.core.CfgMu.RLock()
	resp := settingsResponse{
		Deliver: m.core.Cfg.Peers.Deliver,
		Alias:   m.core.Cfg.PeerAlias(),
	}
	m.core.CfgMu.RUnlock()

	_ = json.NewEncoder(w).Encode(resp)
}

// handlePutSettings serves PUT /api/peers/settings: an optional Deliver
// (when non-nil) sets Peers.Deliver, persisted via Core.UpdateConfig; the
// response always reflects the settings actually committed. Admin-only,
// same defense-in-depth as handleGetSettings.
func (m *Module) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}

	var req putSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var resp settingsResponse
	err := m.core.UpdateConfig(func(cfg *config.Config) error {
		if req.Deliver != nil {
			cfg.Peers.Deliver = *req.Deliver
		}
		resp = settingsResponse{
			Deliver: cfg.Peers.Deliver,
			Alias:   cfg.PeerAlias(),
		}
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	_ = json.NewEncoder(w).Encode(resp)
}
