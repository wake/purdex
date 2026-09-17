package config_test

import (
	"encoding/json"
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
	want := "nex.repo_roots / nex.service_roots: at least one root required when nex.enabled"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestNexConfigEnabledWithServiceRootsOnlyIsValid: the roots rule is "at
// least one of repo_roots / service_roots", not "repo_roots non-empty".
func TestNexConfigEnabledWithServiceRootsOnlyIsValid(t *testing.T) {
	n := config.DefaultNexConfig()
	n.Enabled = true
	n.ServiceRoots = []string{"/svc"}
	if err := n.Validate("/home/u"); err != nil {
		t.Errorf("enabled with service_roots only should be valid, got %v", err)
	}
}

// TestNexConfigValidateNoHomeDisabledSkipsTildeEntries: with home == ""
// (os.UserHomeDir failed) and the section not enabled, the DEFAULT
// "~/.local/bin" must not be rejected as non-absolute — otherwise every
// pdx subcommand fails on a host that never opted into nex. Non-"~"
// relative entries are still rejected.
func TestNexConfigValidateNoHomeDisabledSkipsTildeEntries(t *testing.T) {
	n := config.DefaultNexConfig()
	if err := n.Validate(""); err != nil {
		t.Errorf("disabled default section with home=\"\" should validate, got %v", err)
	}

	n2 := config.DefaultNexConfig()
	n2.ClaudeBin = "~/bin/claude"
	n2.RepoRoots = []string{"~", "~/x"}
	n2.ServiceRoots = []string{"~/svc"}
	if err := n2.Validate(""); err != nil {
		t.Errorf("disabled section with only ~ entries and home=\"\" should validate, got %v", err)
	}

	n3 := config.DefaultNexConfig()
	n3.PathPrepend = []string{"~/.local/bin", "rel"}
	err := n3.Validate("")
	if err == nil {
		t.Fatal("expected error for relative non-~ entry, got nil")
	}
	if !strings.Contains(err.Error(), "nex.path_prepend[1]") || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %q, want it to name nex.path_prepend[1] as non-absolute", err.Error())
	}
}

// TestNexConfigValidateNoHomeEnabledRejectsTildeEntries: enabled + a "~"
// entry + no home is a hard error that names HOME, not a misleading
// "must be an absolute path".
func TestNexConfigValidateNoHomeEnabledRejectsTildeEntries(t *testing.T) {
	n := config.DefaultNexConfig()
	n.Enabled = true
	n.RepoRoots = []string{"/repo"}
	err := n.Validate("")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := `nex.path_prepend[0]: HOME is not set, cannot expand "~/.local/bin"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	n2 := config.DefaultNexConfig()
	n2.Enabled = true
	n2.PathPrepend = nil
	n2.RepoRoots = []string{"~/Workspace"}
	err = n2.Validate("")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want = `nex.repo_roots[0]: HOME is not set, cannot expand "~/Workspace"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestLoadWithoutHome is C1 end to end: Load with HOME unset succeeds for
// a config with no [nex] section (the default path_prepend contains
// "~/.local/bin") and fails, naming HOME, only when [nex] is enabled.
func TestLoadWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir() still resolves with HOME unset on this platform")
	}

	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.toml")
	if err := os.WriteFile(plain, []byte("bind = \"127.0.0.1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(plain); err != nil {
		t.Errorf("Load without HOME and without [nex]: got %v, want nil", err)
	}

	enabled := filepath.Join(dir, "enabled.toml")
	if err := os.WriteFile(enabled, []byte("[nex]\nenabled = true\nrepo_roots = [\"/repo\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(enabled)
	if err == nil {
		t.Fatal("Load without HOME with [nex] enabled: got nil error, want HOME error")
	}
	if !strings.Contains(err.Error(), "HOME is not set") {
		t.Errorf("error = %q, want it to name HOME", err.Error())
	}
	if !strings.Contains(err.Error(), "nex.path_prepend[0]") {
		t.Errorf("error = %q, want it to name nex.path_prepend[0]", err.Error())
	}
	if strings.Count(err.Error(), "config:") > 1 {
		t.Errorf("error = %q, \"config:\" prefix doubled", err.Error())
	}
}

// TestLoadValidateErrorIsNotDoublePrefixed: Load's Validate error is
// returned so that main's `log.Fatalf("config: %v", err)` prints
// "config: nex.<key>: …" exactly once.
func TestLoadValidateErrorIsNotDoublePrefixed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[nex]\nsandbox = { max_profile = \"yolo\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "nex.sandbox.max_profile:") {
		t.Errorf("error = %q, want prefix %q", err.Error(), "nex.sandbox.max_profile:")
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

// TestNexConfigRedactedJSONListsNeverNull pins that GET /api/config never
// carries `null` for a nex list: a TOML that omits repo_roots/service_roots
// decodes nil, and the SPA list editors call .map on them.
func TestNexConfigRedactedJSONListsNeverNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bind = \"127.0.0.1\"\n\n[nex]\nenabled = false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Nex.PathPrepend = nil // as a JSON PUT with "path_prepend": null leaves it

	data, err := json.Marshal(cfg.Redacted())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, key := range []string{"repo_roots", "service_roots", "path_prepend"} {
		if !strings.Contains(s, `"`+key+`":[]`) {
			t.Errorf("Redacted JSON %s is not [] in %s", key, s)
		}
	}
}
