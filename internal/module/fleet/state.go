package fleetmod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// persisted is state.json (spec §4): the role, the highest term seen, when the other was last heard.
type persisted struct {
	Role      string    `json:"role"`
	Term      int64     `json:"term"`
	LastHeard time.Time `json:"last_heard,omitempty"`
}

// loadState reads path; a missing or torn file is standby, term 0.
func loadState(path string) persisted {
	def := persisted{Role: "standby"}
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	var p persisted
	if json.Unmarshal(b, &p) != nil || p.Term < 0 {
		return def
	}
	if p.Role != "primary" && p.Role != "standby" && p.Role != "off" {
		p.Role = "standby"
	}
	return p
}

// saveState writes a temp file then renames it (no fsync, spec §4).
func saveState(path string, p persisted) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
