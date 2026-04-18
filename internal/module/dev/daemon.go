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
	Type    string `json:"type"` // "log" | "error" | "success"
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

	writeEvent := func(ev daemonRebuildEvent) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	binDir := filepath.Join(m.repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
	newPath := filepath.Join(binDir, "pdx.new")

	cmd := exec.CommandContext(ctx, "go", "build", "-o", newPath, "./cmd/pdx")
	cmd.Dir = m.repoRoot
	// Inherit env so GOCACHE / PATH / HOME work; do not scrub.
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		writeEvent(daemonRebuildEvent{Type: "error", Message: err.Error()})
		return
	}

	if err := cmd.Start(); err != nil {
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

	// Capture new hash for client UI.
	newHash := ""
	if out, hashErr := exec.Command("git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output(); hashErr == nil {
		newHash = strings.TrimSpace(string(out))
	}
	writeEvent(daemonRebuildEvent{Type: "success", NewHash: newHash})

	// TODO Task 5: atomic rename + syscall.Exec self
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
