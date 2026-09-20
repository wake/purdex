package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func TestLoadDefaultsWhenFileNotExist(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Errorf("bind: want 127.0.0.1, got %s", cfg.Bind)
	}
	if cfg.Port != 7860 {
		t.Errorf("port: want 7860, got %d", cfg.Port)
	}
}

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
bind = "100.64.0.2"
port = 9090
token = "secret123"
allow = ["10.0.0.0/8"]
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "100.64.0.2" {
		t.Errorf("bind: want 100.64.0.2, got %s", cfg.Bind)
	}
	if cfg.Port != 9090 {
		t.Errorf("port: want 9090, got %d", cfg.Port)
	}
	if cfg.Token != "secret123" {
		t.Errorf("token: want secret123, got %s", cfg.Token)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "10.0.0.0/8" {
		t.Errorf("allow: want [10.0.0.0/8], got %v", cfg.Allow)
	}
}

func TestLoadAutoDefaultPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7860 {
		t.Errorf("port: want 7860, got %d", cfg.Port)
	}
}

// TestLoadIgnoresUnknownStreamTable pins the P-D.2 compatibility guarantee:
// a config.toml that still carries the removed `[[stream.presets]]` table
// must keep loading (toml.Unmarshal ignores unknown tables) and known keys
// must still parse.
func TestLoadIgnoresUnknownStreamTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
bind = "0.0.0.0"
port = 8080

[[stream.presets]]
name = "cc"
command = "claude -p --input-format stream-json --output-format stream-json"

[[stream.presets]]
name = "dangerous"
command = "claude -p --input-format stream-json --output-format stream-json --dangerously-skip-permissions"

[detect]
cc_commands = ["claude", "cld"]
poll_interval = 3
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load with stale [[stream.presets]] table: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("port: want 8080, got %d", cfg.Port)
	}
	if len(cfg.Detect.CCCommands) != 2 {
		t.Fatalf("expected 2 cc_commands, got %d", len(cfg.Detect.CCCommands))
	}
	if cfg.Detect.PollInterval != 3 {
		t.Fatalf("expected poll_interval 3, got %d", cfg.Detect.PollInterval)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Load(filepath.Join(dir, "missing.toml"))
	if cfg.Detect.PollInterval != 2 {
		t.Fatalf("expected default poll_interval 2, got %d", cfg.Detect.PollInterval)
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	os.WriteFile(path, []byte(`not valid toml {{{{`), 0644)

	_, err := config.Load(path)
	if err == nil {
		t.Error("want error for invalid TOML")
	}
}

func TestDefaultsHaveUploadDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	// Point at a missing file so the real ~/.config/pdx/config.toml (which
	// may override upload_dir) cannot leak into the defaults assertion.
	cfg, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "pdx", "uploads")
	if cfg.UploadDir != want {
		t.Errorf("UploadDir = %q, want %q", cfg.UploadDir, want)
	}
}

func TestGetSizingModeDefault(t *testing.T) {
	tc := config.TerminalConfig{}
	if tc.GetSizingMode() != "auto" {
		t.Errorf("expected default 'auto', got %q", tc.GetSizingMode())
	}
}

func TestGetSizingModeExplicit(t *testing.T) {
	tc := config.TerminalConfig{SizingMode: "terminal-first"}
	if tc.GetSizingMode() != "terminal-first" {
		t.Errorf("expected 'terminal-first', got %q", tc.GetSizingMode())
	}
}

func TestPeerAliasUnsetWithColonInHostID(t *testing.T) {
	cfg := config.Config{HostID: "mini-lab:278cbm"}
	got := cfg.PeerAlias()
	want := "mini-lab"
	if got != want {
		t.Errorf("PeerAlias: want %q, got %q", want, got)
	}
}

func TestPeerAliasSetAlias(t *testing.T) {
	cfg := config.Config{
		HostID: "mini-lab:278cbm",
		Peers:  config.PeersConfig{Alias: "my-peer"},
	}
	got := cfg.PeerAlias()
	want := "my-peer"
	if got != want {
		t.Errorf("PeerAlias: want %q, got %q", want, got)
	}
}

func TestPeerAliasHostIDWithoutColon(t *testing.T) {
	cfg := config.Config{HostID: "standalone"}
	got := cfg.PeerAlias()
	want := "standalone"
	if got != want {
		t.Errorf("PeerAlias: want %q, got %q", want, got)
	}
}

func TestPeerAliasEmptyHostID(t *testing.T) {
	cfg := config.Config{HostID: ""}
	got := cfg.PeerAlias()
	want := ""
	if got != want {
		t.Errorf("PeerAlias: want %q, got %q", want, got)
	}
}

func TestPeerAliasRoundTripWriteFileAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write a config with peers.alias
	originalCfg := config.Config{
		HostID: "mini-lab:278cbm",
		Bind:   "127.0.0.1",
		Port:   7860,
		Peers: config.PeersConfig{
			Alias: "test-peer",
		},
	}
	if err := config.WriteFile(path, originalCfg); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load it back
	loadedCfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify all fields round-tripped correctly
	if loadedCfg.HostID != originalCfg.HostID {
		t.Errorf("HostID: want %q, got %q", originalCfg.HostID, loadedCfg.HostID)
	}
	if loadedCfg.Peers.Alias != originalCfg.Peers.Alias {
		t.Errorf("Peers.Alias: want %q, got %q", originalCfg.Peers.Alias, loadedCfg.Peers.Alias)
	}
	if loadedCfg.PeerAlias() != "test-peer" {
		t.Errorf("PeerAlias after load: want %q, got %q", "test-peer", loadedCfg.PeerAlias())
	}
}

// TestLoadIgnoresUnknownDispatchTable pins the P-D.1 compatibility guarantee:
// a config.toml that still carries the removed `[dispatch]` table must keep
// loading (toml.Unmarshal ignores unknown tables) and known keys must still
// parse.
func TestLoadIgnoresUnknownDispatchTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
port = 7861

[dispatch]
allowed_repo_roots = ["/x"]
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load with stale [dispatch] table: %v", err)
	}
	if cfg.Port != 7861 {
		t.Errorf("port: want 7861, got %d", cfg.Port)
	}
}
