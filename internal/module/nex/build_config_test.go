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
	// No "nex:" prefix here: Module.Init wraps as "nex: init: %w", and a
	// prefix on both sides would read "nex: init: nex: host_id …".
	const want = "host_id is empty; cannot derive principal"
	if err.Error() != want {
		t.Errorf("buildOptions() error = %q, want %q", err.Error(), want)
	}
}

// TestBuildOptionsWhitespaceHostIDErrors: a host_id that is only
// whitespace is as empty as "" for principal purposes, and a host_id with
// surrounding whitespace is trimmed before it becomes the principal and
// Nexen's HostID.
func TestBuildOptionsWhitespaceHostIDErrors(t *testing.T) {
	n := pdxconfig.NexConfig{RepoRoots: []string{"/repo/a"}}

	_, err := buildOptions("  ", "/data", n, 5*time.Second)
	if err == nil {
		t.Fatal("buildOptions(\"  \") error = nil, want non-nil")
	}
	const want = "host_id is empty; cannot derive principal"
	if err.Error() != want {
		t.Errorf("buildOptions() error = %q, want %q", err.Error(), want)
	}

	opts, err := buildOptions(" h1 ", "/data", n, 5*time.Second)
	if err != nil {
		t.Fatalf("buildOptions(\" h1 \") error = %v, want nil", err)
	}
	if opts.Config.HostID != "h1" {
		t.Errorf("Config.HostID = %q, want trimmed %q", opts.Config.HostID, "h1")
	}
	req := httptest.NewRequest("GET", "/api/nex/v1/capabilities", nil)
	principal, err := opts.Auth.Authenticate(req)
	if err != nil || principal != "pdx:h1" {
		t.Errorf("Authenticate() = %q, %v; want %q, nil", principal, err, "pdx:h1")
	}
}

// TestBuildOptionsPrincipalClientID is spec §4.3's X-Pdx-Client rule:
// an optional request header naming the client is appended to the
// principal so two clients sharing the host token do not present as the
// same writer (and silently re-mint each other's control lease). Absent
// or malformed values are ignored, not rejected.
func TestBuildOptionsPrincipalClientID(t *testing.T) {
	n := pdxconfig.NexConfig{RepoRoots: []string{"/repo/a"}}
	opts, err := buildOptions("h", "/data", n, 5*time.Second)
	if err != nil {
		t.Fatalf("buildOptions() error = %v", err)
	}

	tests := []struct {
		name   string
		header string
		set    bool
		want   string
	}{
		{name: "absent", want: "pdx:h"},
		{name: "valid", header: "tab-1", set: true, want: "pdx:h/tab-1"},
		{name: "valid with dot and underscore", header: "spa_1.2", set: true, want: "pdx:h/spa_1.2"},
		{name: "invalid characters", header: "bad value!", set: true, want: "pdx:h"},
		{name: "empty value", header: "", set: true, want: "pdx:h"},
		{name: "64 chars", header: strings.Repeat("a", 64), set: true, want: "pdx:h/" + strings.Repeat("a", 64)},
		{name: "65 chars", header: strings.Repeat("a", 65), set: true, want: "pdx:h"},
		{name: "slash", header: "a/b", set: true, want: "pdx:h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/nex/v1/capabilities", nil)
			if tt.set {
				req.Header.Set("X-Pdx-Client", tt.header)
			}
			got, err := opts.Auth.Authenticate(req)
			if err != nil {
				t.Fatalf("Authenticate() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("Authenticate() principal = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildOptionsValidateErrorIsWrapped is spec I13's counterpart: a
// negative duration is shape-valid to pdx's own NexConfig.Validate (it
// only checks the string parses), but Nexen's Config.Validate rejects it.
// buildOptions must surface that with a single "config:" prefix (Nexen's
// own) and no "nex:" of its own — Module.Init adds "nex: init:" — so the
// final message reads "nex: init: config: …" rather than
// "nex: init: nex: config: config: …".
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
	if !strings.HasPrefix(err.Error(), "config: ") {
		t.Errorf("buildOptions() error = %q, want prefix %q", err.Error(), "config: ")
	}
	if strings.HasPrefix(err.Error(), "nex:") {
		t.Errorf("buildOptions() error = %q, must not carry its own \"nex:\" prefix (Init adds it)", err.Error())
	}
	if strings.Contains(err.Error(), "config: config:") {
		t.Errorf("buildOptions() error = %q, prefix doubled to \"config: config:\"", err.Error())
	}
	if !strings.Contains(err.Error(), "lease_ttl") {
		t.Errorf("buildOptions() error = %q, want it to mention lease_ttl", err.Error())
	}
}
