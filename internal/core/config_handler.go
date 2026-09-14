// internal/core/config_handler.go
package core

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/config"
)

// handleGetConfig returns the current config as JSON with sensitive fields redacted.
func (c *Core) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	cfg := c.Cfg.Redacted()
	c.CfgMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// configUpdateRequest defines the fields that can be updated via PUT /api/config.
type configUpdateRequest struct {
	Stream    *config.StreamConfig   `json:"stream,omitempty"`
	Detect    *detectUpdateRequest   `json:"detect,omitempty"`
	Terminal  *config.TerminalConfig `json:"terminal,omitempty"`
	UploadDir *string                `json:"upload_dir,omitempty"`
	Nex       json.RawMessage        `json:"nex"`
}

// detectUpdateRequest allows partial updates to detect config.
// Using pointers so we can distinguish "not provided" from "zero value".
type detectUpdateRequest struct {
	CCCommands   *[]string `json:"cc_commands,omitempty"`
	PollInterval *int      `json:"poll_interval,omitempty"`
}

// handlePutConfig accepts a partial config update, persists it to disk, and returns the updated config.
func (c *Core) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var req configUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Validate before mutating
	if len(req.Nex) > 0 {
		http.Error(w, "nex is not editable via API in this version; edit config.toml and restart", http.StatusBadRequest)
		return
	}

	if req.Terminal != nil && req.Terminal.SizingMode != "" {
		switch req.Terminal.SizingMode {
		case "auto", "terminal-first", "minimal-first":
			// valid
		default:
			http.Error(w, "invalid sizing_mode: must be auto, terminal-first, or minimal-first", http.StatusBadRequest)
			return
		}
	}

	if req.UploadDir != nil {
		if *req.UploadDir == "" || !filepath.IsAbs(*req.UploadDir) {
			http.Error(w, "upload_dir must be a non-empty absolute path", http.StatusBadRequest)
			return
		}
	}

	err := c.UpdateConfig(func(cfg *config.Config) error {
		if req.Stream != nil {
			cfg.Stream = *req.Stream
		}
		if req.Detect != nil {
			if req.Detect.CCCommands != nil {
				cfg.Detect.CCCommands = *req.Detect.CCCommands
			}
			if req.Detect.PollInterval != nil && *req.Detect.PollInterval > 0 {
				cfg.Detect.PollInterval = *req.Detect.PollInterval
			}
		}
		if req.Terminal != nil && req.Terminal.SizingMode != "" {
			cfg.Terminal.SizingMode = req.Terminal.SizingMode
		}
		if req.UploadDir != nil {
			cfg.UploadDir = *req.UploadDir
		}
		return nil
	})
	if err != nil {
		http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	c.CfgMu.RLock()
	cfg := c.Cfg.Redacted()
	c.CfgMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
