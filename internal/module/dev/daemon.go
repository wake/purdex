package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/execstat"
)

// daemonRebuildMu serializes concurrent rebuild requests. Used by Task 4.
var daemonRebuildMu sync.Mutex

type daemonCheckResponse struct {
	CurrentHash string `json:"current_hash"`
	LatestHash  string `json:"latest_hash"`
	Available   bool   `json:"available"`
}

type daemonRebuildEvent struct {
	Type    string `json:"type"` // "log" | "error" | "success" | "restarting"
	Line    string `json:"line,omitempty"`
	Message string `json:"message,omitempty"`
	NewHash string `json:"new_hash,omitempty"`
}

func (m *DevModule) handleDaemonRebuild(w http.ResponseWriter, r *http.Request) {
	if !daemonRebuildMu.TryLock() {
		http.Error(w, `{"error":"rebuild in progress"}`, http.StatusConflict)
		return
	}
	defer daemonRebuildMu.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var writeMu sync.Mutex
	writeEvent := func(ev daemonRebuildEvent) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		writeMu.Lock()
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		writeMu.Unlock()
	}

	// Fix 4: derive build context from m.stopCtx so SIGTERM cancels in-flight
	// builds; propagate client disconnect via a side goroutine.
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()

	// Propagate client disconnect to the build too.
	go func() {
		select {
		case <-r.Context().Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	log.Printf("dev rebuild requested by %s (current %s)", r.RemoteAddr, buildinfo.Hash)
	// The target is the binary this daemon runs from, found before anything is built: when it cannot be found the answer is an error, never
	// the repo's bin/pdx, which is the move away from the running binary this exists to prevent.
	target, err := m.rebuildTarget()
	if err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: "cannot find the running pdx binary: " + err.Error()})
		return
	}
	// The build goes next to the target so the rename below stays on one volume and is atomic.
	binDir := filepath.Dir(target)
	if err := os.MkdirAll(binDir, 0755); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
	newPath := target + ".new"

	// Fix 1: Inject the current git hash via -ldflags so the rebuilt binary
	// reports the correct buildinfo.Hash through /api/dev/daemon/check. Without
	// this, the new binary would start with buildinfo.Hash="unknown" and the UI
	// would permanently show "update available" after every rebuild.
	// Bound the git query with a short timeout to avoid blocking the mutex.
	hashCtx, hashCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer hashCancel()
	hashOut, _ := execstat.CommandContext(hashCtx, "dev.git-hash", "git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output()
	hash := strings.TrimSpace(string(hashOut))

	if err := m.buildBinary(ctx, buildTarget{}, hash, m.readVersionFile(), newPath, func(line string) {
		writeEvent(daemonRebuildEvent{Type: "log", Line: line})
	}); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	// Fix 3: send success AFTER atomic rename so we don't emit success-then-
	// error if rename fails. hash was captured before the build (Fix 1/5).

	// Atomic swap: <target>.new -> <target>, where target is the running binary (bin/pdx only when the daemon runs from the repo).
	finalPath := target
	if err := os.Rename(newPath, finalPath); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: "rename failed: " + err.Error()})
		return
	}

	writeEvent(daemonRebuildEvent{Type: "success", NewHash: hash})

	// Hand the swap to core's restart flow (#3572): modules stop in order,
	// the log shows "shutdown: ... done, restarting", and core refuses a
	// second restart. It execs finalPath, the binary just built, not the
	// one this daemon booted from. The call does not block; the HTTP
	// shutdown waits for this handler to return, so the events below are
	// still flushed. In tests restartFn is nil, so nothing restarts.
	if m.restartFn != nil {
		reason := fmt.Sprintf("dev rebuild from %s (%s -> %s)", r.RemoteAddr, buildinfo.Hash, hash)
		if err := m.restartFn(reason, finalPath, nil); err != nil {
			log.Printf("dev rebuild: restart refused: %v", err)
			writeEvent(daemonRebuildEvent{Type: "error", Message: "restart: " + err.Error()})
			return
		}
	}
	writeEvent(daemonRebuildEvent{Type: "restarting"})
}

func (m *DevModule) handleDaemonCheck(w http.ResponseWriter, _ *http.Request) {
	latest := ""
	cmd := execstat.Command("dev.git-log", "git", "log", "-1", "--format=%h")
	cmd.Dir = m.repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err == nil {
		latest = strings.TrimSpace(string(out))
		if latest == "" {
			log.Printf("dev: git log -1 in %q printed nothing (stderr %q)", m.repoRoot, strings.TrimSpace(stderr.String()))
		}
	} else {
		// the answer is "no latest hash" either way; the reason is what a person looking at an empty one needs
		log.Printf("dev: git log -1 in %q: %v (stderr %q)", m.repoRoot, err, strings.TrimSpace(stderr.String()))
	}
	resp := daemonCheckResponse{
		CurrentHash: buildinfo.Hash,
		LatestHash:  latest,
		Available:   latest != "" && latest != buildinfo.Hash,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// readVersionFile is readVersion with "unknown" for a missing or empty file,
// the form the build identity wants.
func (m *DevModule) readVersionFile() string {
	if v := m.readVersion(); v != "" {
		return v
	}
	return "unknown"
}
