package dev

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

	binDir := filepath.Join(m.repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
	newPath := filepath.Join(binDir, "pdx.new")

	// Fix 1: Inject the current git hash via -ldflags so the rebuilt binary
	// reports the correct BakedInHash through /api/dev/daemon/check. Without
	// this, the new binary would start with BakedInHash="unknown" and the UI
	// would permanently show "update available" after every rebuild.
	// Bound the git query with a short timeout to avoid blocking the mutex.
	hashCtx, hashCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer hashCancel()
	hashOut, _ := exec.CommandContext(hashCtx, "git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output()
	hash := strings.TrimSpace(string(hashOut))
	ldflags := "-X github.com/wake/purdex/internal/module/dev.BakedInHash=" + hash
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags", ldflags, "-o", newPath, "./cmd/pdx")
	cmd.Dir = m.repoRoot
	// Inherit env so GOCACHE / PATH / HOME work; do not scrub.
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
	// Fix 2: close stdout pipe on StderrPipe failure to avoid FD leak.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	// Fix 2: close both pipes when cmd.Start fails.
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	streamLines := func(src io.Reader, done chan<- struct{}) {
		defer close(done)
		scanner := bufio.NewScanner(src)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			writeEvent(daemonRebuildEvent{Type: "log", Line: scanner.Text()})
		}
	}
	doneOut := make(chan struct{})
	doneErr := make(chan struct{})
	go streamLines(stdout, doneOut)
	go streamLines(stderr, doneErr)
	<-doneOut
	<-doneErr

	if err := cmd.Wait(); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	// Fix 3: send success AFTER atomic rename so we don't emit success-then-
	// error if rename fails. hash was captured before the build (Fix 1/5).

	// Atomic swap: bin/pdx.new -> bin/pdx
	finalPath := filepath.Join(binDir, "pdx")
	if err := os.Rename(newPath, finalPath); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: "rename failed: " + err.Error()})
		return
	}

	writeEvent(daemonRebuildEvent{Type: "success", NewHash: hash})
	writeEvent(daemonRebuildEvent{Type: "restarting"})
	// Give SSE a moment to flush to the client before we vanish into Exec.
	time.Sleep(200 * time.Millisecond)

	self, err := os.Executable()
	if err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: "Executable: " + err.Error()})
		return
	}
	// execSelf replaces this process; the function only returns on failure.
	// In tests execSelf is nil (DevModule constructed directly without Init),
	// so we skip exec entirely — safe, no risk of replacing the test binary.
	if m.execSelf != nil {
		if err := m.execSelf(self, os.Args, os.Environ()); err != nil {
			writeEvent(daemonRebuildEvent{Type: "error", Message: "exec: " + err.Error()})
		}
	}
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
