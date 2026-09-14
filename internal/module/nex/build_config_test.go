package nex

import (
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lab.protype.tw/wake/nexen/api"
	"lab.protype.tw/wake/nexen/sandbox"

	pdxconfig "github.com/wake/purdex/internal/config"
)

func TestRoutePrefixIsValidPublicPrefix(t *testing.T) {
	if err := api.ValidatePublicPrefix(RoutePrefix); err != nil {
		t.Errorf("RoutePrefix %q is not a valid PublicPrefix: %v", RoutePrefix, err)
	}
}

func TestBuildOptionsFullMapping(t *testing.T) {
	n := pdxconfig.NexConfig{
		RepoRoots:    []string{"/repo/a", "/repo/b"},
		ServiceRoots: []string{"/svc/a"},
		ClaudeBin:    "/usr/local/bin/claude",
		CswapBin:     "/usr/local/bin/cswap",
		Sandbox: pdxconfig.NexSandboxConfig{
			MaxProfile:     "trusted",
			DefaultProfile: "readonly",
		},
		Timeouts: pdxconfig.NexTimeoutsConfig{
			LeaseTTL:  "30s",
			Interrupt: "5s",
			Turn:      "10m",
		},
	}

	opts, err := buildOptions("host1", "/data", n, 7*time.Second)
	if err != nil {
		t.Fatalf("buildOptions() error = %v, want nil", err)
	}

	cfg := opts.Config
	if cfg == nil {
		t.Fatal("opts.Config is nil")
	}

	if cfg.HostID != "host1" {
		t.Errorf("Config.HostID = %q, want %q", cfg.HostID, "host1")
	}
	wantDataDir := filepath.Join("/data", "nex")
	if cfg.DataDir != wantDataDir {
		t.Errorf("Config.DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if !reflect.DeepEqual(cfg.RepoRoots, n.RepoRoots) {
		t.Errorf("Config.RepoRoots = %v, want %v", cfg.RepoRoots, n.RepoRoots)
	}
	if !reflect.DeepEqual(cfg.ServiceRoots, n.ServiceRoots) {
		t.Errorf("Config.ServiceRoots = %v, want %v", cfg.ServiceRoots, n.ServiceRoots)
	}
	if cfg.CswapBin != n.CswapBin {
		t.Errorf("Config.CswapBin = %q, want %q", cfg.CswapBin, n.CswapBin)
	}
	wantSandbox := sandbox.Policy{MaxProfile: "trusted", DefaultProfile: "readonly"}
	if cfg.Sandbox != wantSandbox {
		t.Errorf("Config.Sandbox = %+v, want %+v", cfg.Sandbox, wantSandbox)
	}
	if time.Duration(cfg.LeaseTTL) != 30*time.Second {
		t.Errorf("Config.LeaseTTL = %v, want %v", time.Duration(cfg.LeaseTTL), 30*time.Second)
	}
	if time.Duration(cfg.InterruptTimeout) != 5*time.Second {
		t.Errorf("Config.InterruptTimeout = %v, want %v", time.Duration(cfg.InterruptTimeout), 5*time.Second)
	}
	if time.Duration(cfg.TurnTimeout) != 10*time.Minute {
		t.Errorf("Config.TurnTimeout = %v, want %v", time.Duration(cfg.TurnTimeout), 10*time.Minute)
	}
	if time.Duration(cfg.ShutdownTimeout) != 7*time.Second {
		t.Errorf("Config.ShutdownTimeout = %v, want %v", time.Duration(cfg.ShutdownTimeout), 7*time.Second)
	}

	if opts.PublicPrefix != RoutePrefix {
		t.Errorf("opts.PublicPrefix = %q, want %q", opts.PublicPrefix, RoutePrefix)
	}
	if opts.ClaudeBin != n.ClaudeBin {
		t.Errorf("opts.ClaudeBin = %q, want %q", opts.ClaudeBin, n.ClaudeBin)
	}

	if opts.Auth == nil {
		t.Fatal("opts.Auth is nil")
	}
	req := httptest.NewRequest("GET", "/api/nex/v1/capabilities", nil)
	principal, err := opts.Auth.Authenticate(req)
	if err != nil {
		t.Fatalf("opts.Auth.Authenticate() error = %v, want nil", err)
	}
	if principal != "pdx:host1" {
		t.Errorf("opts.Auth.Authenticate() principal = %q, want %q", principal, "pdx:host1")
	}
}

// TestBuildOptionsEmptyTimeoutsGetNexenDefaults is spec I13: when every
// [nex.timeouts] string is "", buildOptions still returns a Config whose
// defaultable timeouts are non-zero, because it ran
// (*nexconfig.Config).Validate before returning rather than leaving that
// to Assemble.
func TestBuildOptionsEmptyTimeoutsGetNexenDefaults(t *testing.T) {
	n := pdxconfig.NexConfig{
		RepoRoots: []string{"/repo/a"},
	}

	opts, err := buildOptions("host1", "/data", n, 0)
	if err != nil {
		t.Fatalf("buildOptions() error = %v, want nil", err)
	}

	cfg := opts.Config
	if time.Duration(cfg.LeaseTTL) == 0 {
		t.Error("Config.LeaseTTL is zero, want a Nexen-applied default")
	}
	if time.Duration(cfg.InterruptTimeout) == 0 {
		t.Error("Config.InterruptTimeout is zero, want a Nexen-applied default")
	}
	if time.Duration(cfg.ShutdownTimeout) == 0 {
		t.Error("Config.ShutdownTimeout is zero, want a Nexen-applied default")
	}
}

func TestBuildOptionsEmptyHostIDErrors(t *testing.T) {
	n := pdxconfig.NexConfig{RepoRoots: []string{"/repo/a"}}

	_, err := buildOptions("", "/data", n, 5*time.Second)
	if err == nil {
		t.Fatal("buildOptions() error = nil, want non-nil")
	}
	const want = "nex: host_id is empty; cannot derive principal"
	if err.Error() != want {
		t.Errorf("buildOptions() error = %q, want %q", err.Error(), want)
	}
}

// TestBuildOptionsValidateErrorIsWrapped is spec I13's counterpart: a
// negative duration is shape-valid to pdx's own NexConfig.Validate (it
// only checks the string parses), but Nexen's Config.Validate rejects it.
// buildOptions must surface that with a single "nex: config:" prefix —
// Nexen's own error already starts with "config:", so wrapping with just
// "nex:" must not double it into "nex: config: config:".
func TestBuildOptionsValidateErrorIsWrapped(t *testing.T) {
	n := pdxconfig.NexConfig{
		RepoRoots: []string{"/repo/a"},
		Timeouts: pdxconfig.NexTimeoutsConfig{
			LeaseTTL: "-1s",
		},
	}

	_, err := buildOptions("host1", "/data", n, 5*time.Second)
	if err == nil {
		t.Fatal("buildOptions() error = nil, want non-nil")
	}
	if !strings.HasPrefix(err.Error(), "nex: config: ") {
		t.Errorf("buildOptions() error = %q, want prefix %q", err.Error(), "nex: config: ")
	}
	if strings.Contains(err.Error(), "config: config:") {
		t.Errorf("buildOptions() error = %q, prefix doubled to \"config: config:\"", err.Error())
	}
	if !strings.Contains(err.Error(), "lease_ttl") {
		t.Errorf("buildOptions() error = %q, want it to mention lease_ttl", err.Error())
	}
}
