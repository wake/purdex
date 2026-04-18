package dev

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleDaemonCheck_ReturnsHashes(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	m := New(".") // repoRoot = cwd (git worktree)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body daemonCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.LatestHash == "" {
		t.Error("latest_hash empty — expected a git commit short hash")
	}
}

func TestHandleDaemonRebuild_BuildsInTempRepo(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	dir := t.TempDir()
	// Minimal go module that builds at ./cmd/pdx.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte("package main\nfunc main(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &DevModule{repoRoot: dir}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/dev/daemon/rebuild", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, `"type":"success"`) {
		t.Errorf("expected success event, got:\n%s", got)
	}
	if !strings.Contains(got, "data: ") {
		t.Errorf("expected SSE framing, got:\n%s", got)
	}
	// After Task 5: rename succeeded, so bin/pdx should exist (pdx.new is gone).
	if _, err := os.Stat(filepath.Join(dir, "bin", "pdx")); err != nil {
		t.Errorf("bin/pdx not present after rename: %v", err)
	}
}

func TestHandleDaemonRebuild_ConcurrentReturns409(t *testing.T) {
	daemonRebuildMu.Lock()
	defer daemonRebuildMu.Unlock()

	m := &DevModule{repoRoot: "."}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/dev/daemon/rebuild", nil)
	m.handleDaemonRebuild(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestHandleDaemonRebuild_BuildFailureEmitsError(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	// Deliberate syntax error to trigger go build failure.
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte("package main\nfunc main(){ broken syntax }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &DevModule{repoRoot: dir}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/dev/daemon/rebuild", nil)
	m.handleDaemonRebuild(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("expected error event, got:\n%s", body)
	}
}

func TestHandleDaemonRebuild_RenameFailureEmitsError(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "pdx", "main.go"), []byte("package main\nfunc main(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Make bin/pdx a non-empty directory so rename(file -> dir) fails.
	if err := os.MkdirAll(filepath.Join(dir, "bin", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "pdx", "blocker"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	m := &DevModule{repoRoot: dir}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/dev/daemon/rebuild", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, `"type":"error"`) {
		t.Errorf("expected error event after rename failure, got:\n%s", got)
	}
	if !strings.Contains(got, "rename failed") {
		t.Errorf("expected rename-failed message, got:\n%s", got)
	}
}

func TestHandleDaemonCheck_AvailableFlag(t *testing.T) {
	old := BakedInHash
	defer func() { BakedInHash = old }()

	BakedInHash = "definitely-not-a-real-hash-0000"
	t.Setenv("PDX_DEV_UPDATE", "1")
	m := New(".")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body daemonCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Available {
		t.Errorf("expected Available=true when BakedInHash differs, got %+v", body)
	}
}
