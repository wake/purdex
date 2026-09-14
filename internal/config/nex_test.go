package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func TestNexConfigDefaultsOnAbsentSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bind = \"127.0.0.1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	want := config.DefaultNexConfig()
	if !reflect.DeepEqual(cfg.Nex, want) {
		t.Errorf("Nex = %+v, want %+v", cfg.Nex, want)
	}
}

func TestNexConfigDisabledWithNoRootsIsValid(t *testing.T) {
	n := config.DefaultNexConfig()
	if n.Enabled {
		t.Fatalf("default Enabled should be false")
	}
	if err := n.Validate("/home/u"); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestNexConfigEnabledWithNoRootsIsInvalid(t *testing.T) {
	n := config.DefaultNexConfig()
	n.Enabled = true

	err := n.Validate("/home/u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nex.repo_roots") {
		t.Errorf("error %q does not mention nex.repo_roots", err.Error())
	}
	want := "nex.repo_roots: at least one root required when nex.enabled"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestNexConfigSandboxMaxProfileValidation(t *testing.T) {
	n := config.DefaultNexConfig()
	n.Sandbox.MaxProfile = "handoff"
	if err := n.Validate("/home/u"); err != nil {
		t.Errorf("max_profile=handoff should be valid, got %v", err)
	}

	n2 := config.DefaultNexConfig()
	n2.Sandbox.MaxProfile = "yolo"
	err := n2.Validate("/home/u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nex.sandbox.max_profile") {
		t.Errorf("error %q does not mention nex.sandbox.max_profile", err.Error())
	}
}

func TestNexConfigClaudeBinExpansionAndValidation(t *testing.T) {
	home := "/home/u"

	n := config.DefaultNexConfig()
	n.ClaudeBin = "~/bin/claude"
	expanded := n.Expanded(home)
	want := filepath.Join(home, "bin", "claude")
	if expanded.ClaudeBin != want {
		t.Errorf("Expanded().ClaudeBin = %q, want %q", expanded.ClaudeBin, want)
	}

	n2 := config.DefaultNexConfig()
	n2.ClaudeBin = "bin/claude"
	err := n2.Validate(home)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nex.claude_bin") {
		t.Errorf("error %q does not mention nex.claude_bin", err.Error())
	}
}

func TestNexConfigPathPrependValidation(t *testing.T) {
	n := config.DefaultNexConfig()
	n.PathPrepend = []string{"~/.local/bin", "rel"}

	err := n.Validate("/home/u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nex.path_prepend[1]") {
		t.Errorf("error %q does not mention nex.path_prepend[1]", err.Error())
	}
}

func TestNexConfigRepoRootsValidation(t *testing.T) {
	n := config.DefaultNexConfig()
	n.RepoRoots = []string{"rel/x"}

	err := n.Validate("/home/u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nex.repo_roots[0]") {
		t.Errorf("error %q does not mention nex.repo_roots[0]", err.Error())
	}
}

func TestNexConfigTimeoutsValidation(t *testing.T) {
	n := config.DefaultNexConfig()
	n.Timeouts.LeaseTTL = "12x"

	err := n.Validate("/home/u")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nex.timeouts.lease_ttl") {
		t.Errorf("error %q does not mention nex.timeouts.lease_ttl", err.Error())
	}
}

func TestNexConfigTomlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg, err := config.Load(filepath.Join(dir, "nonexistent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Nex = config.NexConfig{
		Enabled:      true,
		RepoRoots:    []string{"/repo1", "/repo2"},
		ServiceRoots: []string{"/svc1"},
		ClaudeBin:    "/usr/local/bin/claude",
		CswapBin:     "/usr/local/bin/cswap",
		PathPrepend:  []string{"/opt/homebrew/bin"},
		Sandbox:      config.NexSandboxConfig{MaxProfile: "handoff", DefaultProfile: "trusted"},
		Timeouts:     config.NexTimeoutsConfig{LeaseTTL: "5m", Interrupt: "10s", Turn: "30m"},
	}

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(loaded.Nex, cfg.Nex) {
		t.Errorf("round trip mismatch:\ngot  %+v\nwant %+v", loaded.Nex, cfg.Nex)
	}
}

func TestNexConfigCloneIndependence(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(filepath.Join(dir, "nonexistent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Nex.RepoRoots = []string{"/repo1"}
	cfg.Nex.ServiceRoots = []string{"/svc1"}
	cfg.Nex.PathPrepend = []string{"/opt/homebrew/bin"}

	clone := cfg.Clone()
	clone.Nex.RepoRoots[0] = "/mutated"
	clone.Nex.ServiceRoots[0] = "/mutated"
	clone.Nex.PathPrepend[0] = "/mutated"

	if cfg.Nex.RepoRoots[0] != "/repo1" {
		t.Errorf("RepoRoots leaked into original: %v", cfg.Nex.RepoRoots)
	}
	if cfg.Nex.ServiceRoots[0] != "/svc1" {
		t.Errorf("ServiceRoots leaked into original: %v", cfg.Nex.ServiceRoots)
	}
	if cfg.Nex.PathPrepend[0] != "/opt/homebrew/bin" {
		t.Errorf("PathPrepend leaked into original: %v", cfg.Nex.PathPrepend)
	}
}
