// internal/module/peers/settings.go
package peers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
)

// maxSettingsBodyBytes bounds the PUT /api/peers/settings body, like
// /send and /deliver bound theirs.
const maxSettingsBodyBytes = 1 << 20

// The wire shapes — ipeers.SettingsResponse (GET and PUT) and
// ipeers.PutSettingsRequest (PUT; Deliver / Alias nil means "leave
// unchanged", mirroring putHostRequest.AllowBypass's convention in
// hosts.go) — live in internal/peers, shared with cmd/pdx.

// settingsView builds the response both GET and PUT answer from a config
// snapshot: the effective alias plus where it comes from (self-alias spec
// S-3, #1196) — "config" when [peers] alias is set, "host_id" when
// PeerAlias derived it from host_id.
func settingsView(cfg *config.Config) ipeers.SettingsResponse {
	source := "host_id"
	if cfg.Peers.Alias != "" {
		source = "config"
	}
	return ipeers.SettingsResponse{
		Deliver:     cfg.Peers.Deliver,
		Alias:       cfg.PeerAlias(),
		AliasSource: source,
	}
}

// applySelfAlias applies a PUT {alias} to cfg under the config write lock
// (self-alias spec S-2, S-6, #1196). "" clears Peers.Alias back to the
// host_id default — but the alias that WOULD become effective has to
// clear the same bar a typed one does (codex plan review F1): a peer entry
// may carry exactly the host_id-derived name because a different self
// alias allowed it, and clearing would then make <alias>/<name>
// ambiguous. Anything else must clear config.ValidateSelfAlias against
// the peer hosts configured at this moment (S-1) and is stored verbatim.
// Shape / reserved errors are 400, a collision with a peer host alias is
// 409, as *apiError for writeAPIError.
func applySelfAlias(cfg *config.Config, alias string) error {
	if alias == "" {
		derived := config.Config{HostID: cfg.HostID}.PeerAlias()
		if err := config.ValidateSelfAlias(derived, cfg.Peers.Hosts); err != nil {
			if errors.Is(err, config.ErrSelfAliasCollision) {
				return &apiError{http.StatusConflict, fmt.Sprintf(
					"clearing the alias would make it %q, which is already used by a peer host", derived)}
			}
			return &apiError{http.StatusBadRequest, fmt.Sprintf(
				"clearing the alias would make it %q: %s", derived, err.Error())}
		}
		cfg.Peers.Alias = ""
		return nil
	}
	if err := config.ValidateSelfAlias(alias, cfg.Peers.Hosts); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, config.ErrSelfAliasCollision) {
			status = http.StatusConflict
		}
		return &apiError{status, err.Error()}
	}
	cfg.Peers.Alias = alias
	return nil
}

// handleGetSettings serves GET /api/peers/settings: this host's current
// deliver toggle, its own alias and the alias' source. Admin-only
// (policy.go's HostRoutePolicy already refuses every host principal on
// this path; requireAdmin is the defense-in-depth check for when the
// handler is exercised directly).
func (m *Module) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}

	m.core.CfgMu.RLock()
	resp := settingsView(m.core.Cfg)
	m.core.CfgMu.RUnlock()

	_ = json.NewEncoder(w).Encode(resp)
}

// handlePutSettings serves PUT /api/peers/settings: an optional Deliver
// (when non-nil) sets Peers.Deliver; an optional Alias (when non-nil —
// absent and JSON null both decode to nil = unchanged) sets this host's
// own alias via applySelfAlias (self-alias spec S-2 / S-6). Both writes
// happen in the one Core.UpdateConfig closure, so a rejected alias aborts
// the deliver change sent in the same body: nothing is persisted and the
// response is the error, not a partial commit. The 200 response always
// reflects the settings actually committed. Admin-only, same
// defense-in-depth as handleGetSettings.
func (m *Module) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}

	// The body must be EXACTLY one JSON value (codex A1, same rule as the
	// rotate gates' decodeGateRequest): a second value or trailing bytes
	// after the first is invalid JSON (spec S-6), not "first value wins" —
	// a concatenated or truncated body must not persist a setting the
	// sender did not mean.
	var req ipeers.PutSettingsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingsBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}

	var resp ipeers.SettingsResponse
	err := m.core.UpdateConfig(func(cfg *config.Config) error {
		if req.Deliver != nil {
			cfg.Peers.Deliver = *req.Deliver
		}
		if req.Alias != nil {
			if err := applySelfAlias(cfg, *req.Alias); err != nil {
				return err
			}
		}
		resp = settingsView(cfg)
		return nil
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	_ = json.NewEncoder(w).Encode(resp)
}
