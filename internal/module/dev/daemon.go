package dev

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

// BakedInHash is the short git commit hash injected at build time via
// -ldflags "-X github.com/wake/purdex/internal/module/dev.BakedInHash=<sha>".
// Defaults to "unknown" for dev-mode `go run` without the flag.
var BakedInHash = "unknown"

// daemonRebuildMu serializes concurrent rebuild requests. Used by Task 4.
var daemonRebuildMu sync.Mutex

type daemonCheckResponse struct {
	CurrentHash string `json:"current_hash"`
	LatestHash  string `json:"latest_hash"`
	Available   bool   `json:"available"`
}

func (m *DevModule) handleDaemonCheck(w http.ResponseWriter, _ *http.Request) {
	latest := ""
	cmd := exec.Command("git", "log", "-1", "--format=%h")
	cmd.Dir = m.repoRoot
	if out, err := cmd.Output(); err == nil {
		latest = strings.TrimSpace(string(out))
	}
	resp := daemonCheckResponse{
		CurrentHash: BakedInHash,
		LatestHash:  latest,
		Available:   latest != "" && latest != BakedInHash,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
