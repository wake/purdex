// internal/core/config_handler.go
package core

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/config"
)

// handleGetConfig returns the current config as JSON with sensitive fields redacted.
func (c *Core) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	defer c.CfgMu.RUnlock()

	// Shallow copy to redact sensitive fields; encode under lock to avoid slice race
	cfg := *c.Cfg
	cfg.Token = ""
	cfg.HostID = ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// configUpdateRequest defines the fields that can be updated via PUT /api/config.
type configUpdateRequest struct {
	Stream   *config.StreamConfig   `json:"stream,omitempty"`
	Detect   *detectUpdateRequest   `json:"detect,omitempty"`
	Terminal *config.TerminalConfig `json:"terminal,omitempty"`
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

	c.CfgMu.Lock()

	// Validate before mutating
	if req.Terminal != nil && req.Terminal.SizingMode != "" {
		switch req.Terminal.SizingMode {
		case "auto", "terminal-first", "minimal-first":
			// valid
		default:
			c.CfgMu.Unlock()
			http.Error(w, "invalid sizing_mode: must be auto, terminal-first, or minimal-first", http.StatusBadRequest)
			return
		}
	}

	// Snapshot before any mutation so writeConfig failure can roll back the
	// in-memory state, keeping c.Cfg and disk consistent. Shallow copy is
	// sufficient because every mutation below replaces fields wholesale
	// (slice headers reassigned, never appended into). Future fields must
	// follow the same whole-field-assignment rule or rollback will break.
	snapshot := *c.Cfg

	detectChanged := false

	// Invariant: every mutation below must replace the field wholesale.
	// Do NOT append into c.Cfg slices or write into c.Cfg maps — rollback
	// relies on whole-field assignment to restore `snapshot` correctly.
	if req.Stream != nil {
		c.Cfg.Stream = *req.Stream
	}
	if req.Detect != nil {
		if req.Detect.CCCommands != nil {
			c.Cfg.Detect.CCCommands = *req.Detect.CCCommands
			detectChanged = true
		}
		if req.Detect.PollInterval != nil && *req.Detect.PollInterval > 0 {
			c.Cfg.Detect.PollInterval = *req.Detect.PollInterval
			detectChanged = true
		}
	}

	if req.Terminal != nil && req.Terminal.SizingMode != "" {
		c.Cfg.Terminal.SizingMode = req.Terminal.SizingMode
	}

	// Write back to config file
	if c.CfgPath != "" {
		if err := config.WriteFile(c.CfgPath, *c.Cfg); err != nil {
			*c.Cfg = snapshot // rollback: preserve pointer identity for other goroutines
			c.CfgMu.Unlock()
			http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Return updated config (redacted)
	cfg := *c.Cfg
	c.CfgMu.Unlock()

	// Notify registered callbacks about config changes (outside lock)
	if detectChanged {
		c.NotifyConfigChange()
	}

	cfg.Token = ""
	cfg.HostID = ""
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
