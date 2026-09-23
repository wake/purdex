package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/wake/purdex/internal/config"
)

func TestWriteFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Config{Bind: "127.0.0.1", Port: 7860, Token: "secret"}

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		t.Errorf("config file should not be group/other readable, got %o", perm)
	}
}

func TestEnsureHostID_GeneratesWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Config{DataDir: dir}

	id, err := config.EnsureHostID(&cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("expected hostname:code format, got %q", id)
	}
	if len(parts[1]) != 6 {
		t.Fatalf("expected 6-char code, got %q (len %d)", parts[1], len(parts[1]))
	}
	if cfg.HostID != id {
		t.Fatalf("cfg.HostID not updated: want %q, got %q", id, cfg.HostID)
	}

	// Verify persisted to file
	var saved config.Config
	data, _ := os.ReadFile(path)
	toml.Unmarshal(data, &saved)
	if saved.HostID != id {
		t.Fatalf("persisted HostID: want %q, got %q", id, saved.HostID)
	}
}

func TestEnsureHostID_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Config{HostID: "mlab:abc123", DataDir: dir}

	id, err := config.EnsureHostID(&cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if id != "mlab:abc123" {
		t.Fatalf("want preserved mlab:abc123, got %q", id)
	}

	// File should NOT be written (no change)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should not be created when HostID already set")
	}
}

func TestEnsureHostID_RollbackOnPersistFailure(t *testing.T) {
	cfg := config.Config{DataDir: "/nonexistent"}
	// Use a path that will fail (unwritable root-level directory)
	id, err := config.EnsureHostID(&cfg, "/nonexistent/deep/nested/config.toml")
	if err == nil {
		t.Fatal("expected error for unwritable path")
	}
	if id != "" {
		t.Fatalf("expected empty id on failure, got %q", id)
	}
	if cfg.HostID != "" {
		t.Fatalf("expected HostID rolled back to empty, got %q", cfg.HostID)
	}
}

func TestEnsureHostID_AppendsToExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("bind = \"0.0.0.0\"\nport = 9090\n"), 0644)

	cfg, _ := config.Load(path)
	_, err := config.EnsureHostID(&cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	var saved config.Config
	data, _ := os.ReadFile(path)
	toml.Unmarshal(data, &saved)
	if saved.Bind != "0.0.0.0" {
		t.Fatalf("bind lost: want 0.0.0.0, got %q", saved.Bind)
	}
	if saved.Port != 9090 {
		t.Fatalf("port lost: want 9090, got %d", saved.Port)
	}
	if saved.HostID == "" {
		t.Fatal("HostID should be set")
	}
}

// #1293 §3.2: the tmux-instance probe on the session-list path runs under the
// list's budget — min(parent deadline, its own 3 s). A parent deadline far
// shorter than 3 s must end a hung probe at the parent's deadline.
func TestGetTmuxInstanceContext_HonoursShorterParentDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := config.GetTmuxInstanceContext(ctx)
	elapsed := time.Since(start)
	if got != "" {
		t.Fatalf("hung probe = %q, want \"\"", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("probe returned after %v; the parent's 200ms deadline must bound it (not the 3 s default)", elapsed)
	}
}

func TestGetTmuxInstanceContext_ReadsInstance(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nprintf '4471:1788740000\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := config.GetTmuxInstanceContext(context.Background()); got != "4471:1788740000" {
		t.Fatalf("GetTmuxInstanceContext() = %q", got)
	}
}
