package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func TestPeerHostRoundTripWriteFileAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	originalCfg := config.Config{
		HostID: "mini-lab:278cbm",
		Bind:   "127.0.0.1",
		Port:   7860,
		Peers: config.PeersConfig{
			Alias: "mini-lab",
			Hosts: []config.PeerHost{
				{
					Alias:        "air",
					URL:          "https://air.mlab.host",
					HostID:       "air-2019:abc123",
					Token:        "pdxp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					InboundToken: "pdxp_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					AllowBypass:  true,
				},
				{
					Alias:        "iphone",
					URL:          "https://iphone.mlab.host",
					HostID:       "",
					Token:        "pdxp_cccccccccccccccccccccccccccccccc",
					InboundToken: "pdxp_dddddddddddddddddddddddddddddddd",
					AllowBypass:  false,
				},
			},
		},
	}

	if err := config.WriteFile(path, originalCfg); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "[[peers.hosts]]") {
		t.Errorf("expected TOML to contain [[peers.hosts]] tables, got:\n%s", raw)
	}

	loadedCfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loadedCfg.Peers.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(loadedCfg.Peers.Hosts))
	}
	for i, want := range originalCfg.Peers.Hosts {
		got := loadedCfg.Peers.Hosts[i]
		if got.Alias != want.Alias {
			t.Errorf("host[%d].Alias: want %q, got %q", i, want.Alias, got.Alias)
		}
		if got.URL != want.URL {
			t.Errorf("host[%d].URL: want %q, got %q", i, want.URL, got.URL)
		}
		if got.HostID != want.HostID {
			t.Errorf("host[%d].HostID: want %q, got %q", i, want.HostID, got.HostID)
		}
		if got.Token != want.Token {
			t.Errorf("host[%d].Token: want %q, got %q", i, want.Token, got.Token)
		}
		if got.InboundToken != want.InboundToken {
			t.Errorf("host[%d].InboundToken: want %q, got %q", i, want.InboundToken, got.InboundToken)
		}
		if got.AllowBypass != want.AllowBypass {
			t.Errorf("host[%d].AllowBypass: want %v, got %v", i, want.AllowBypass, got.AllowBypass)
		}
	}
}

func TestRedactedBlanksSecretsAndLeavesOriginalIntact(t *testing.T) {
	cfg := config.Config{
		HostID: "mini-lab:278cbm",
		Token:  "top-level-secret",
		Peers: config.PeersConfig{
			Alias: "mini-lab",
			Hosts: []config.PeerHost{
				{
					Alias:        "air",
					URL:          "https://air.mlab.host",
					HostID:       "air-2019:abc123",
					Token:        "outbound-secret",
					InboundToken: "inbound-secret",
					AllowBypass:  true,
				},
			},
		},
	}

	redacted := cfg.Redacted()

	if redacted.Token != "" {
		t.Errorf("redacted.Token: want empty, got %q", redacted.Token)
	}
	if redacted.HostID != "" {
		t.Errorf("redacted.HostID: want empty, got %q", redacted.HostID)
	}
	if len(redacted.Peers.Hosts) != 1 {
		t.Fatalf("expected 1 host in redacted copy, got %d", len(redacted.Peers.Hosts))
	}
	if redacted.Peers.Hosts[0].Token != "" {
		t.Errorf("redacted host Token: want empty, got %q", redacted.Peers.Hosts[0].Token)
	}
	if redacted.Peers.Hosts[0].InboundToken != "" {
		t.Errorf("redacted host InboundToken: want empty, got %q", redacted.Peers.Hosts[0].InboundToken)
	}
	// Non-secret fields must survive redaction.
	if redacted.Peers.Hosts[0].Alias != "air" {
		t.Errorf("redacted host Alias: want %q, got %q", "air", redacted.Peers.Hosts[0].Alias)
	}
	if redacted.Peers.Hosts[0].HostID != "air-2019:abc123" {
		t.Errorf("redacted host HostID: want %q, got %q", "air-2019:abc123", redacted.Peers.Hosts[0].HostID)
	}

	// Mutate the copy's Hosts slice and confirm the original is untouched.
	redacted.Peers.Hosts[0].Alias = "mutated"
	redacted.Peers.Hosts = append(redacted.Peers.Hosts, config.PeerHost{Alias: "extra"})

	if cfg.Token != "top-level-secret" {
		t.Errorf("original Token mutated: got %q", cfg.Token)
	}
	if cfg.HostID != "mini-lab:278cbm" {
		t.Errorf("original HostID mutated: got %q", cfg.HostID)
	}
	if len(cfg.Peers.Hosts) != 1 {
		t.Fatalf("original Peers.Hosts length mutated: got %d", len(cfg.Peers.Hosts))
	}
	if cfg.Peers.Hosts[0].Alias != "air" {
		t.Errorf("original host Alias mutated: got %q", cfg.Peers.Hosts[0].Alias)
	}
	if cfg.Peers.Hosts[0].Token != "outbound-secret" {
		t.Errorf("original host Token mutated: got %q", cfg.Peers.Hosts[0].Token)
	}
	if cfg.Peers.Hosts[0].InboundToken != "inbound-secret" {
		t.Errorf("original host InboundToken mutated: got %q", cfg.Peers.Hosts[0].InboundToken)
	}
}

func TestCloneDeepCopiesSlices(t *testing.T) {
	cfg := config.Config{
		Allow:        []string{"10.0.0.0/8"},
		AllowedPaths: []string{"/tmp"},
		Detect: config.DetectConfig{
			CCCommands: []string{"claude"},
		},
		Stream: config.StreamConfig{
			Presets: []config.Preset{{Name: "cc", Command: "claude -p"}},
		},
		Dispatch: config.DispatchConfig{
			AllowedRepoRoots: []string{"/repos"},
		},
		Peers: config.PeersConfig{
			Hosts: []config.PeerHost{{Alias: "air", Token: "t1"}},
		},
	}

	clone := cfg.Clone()

	// Mutate the clone in every dimension the brief calls out.
	clone.Peers.Hosts = append(clone.Peers.Hosts, config.PeerHost{Alias: "iphone"})
	clone.Detect.CCCommands[0] = "mutated"
	clone.Allow = append(clone.Allow, "192.168.0.0/16")
	clone.AllowedPaths = append(clone.AllowedPaths, "/etc")
	clone.Stream.Presets[0].Name = "mutated"
	clone.Dispatch.AllowedRepoRoots = append(clone.Dispatch.AllowedRepoRoots, "/more")

	if len(cfg.Peers.Hosts) != 1 {
		t.Errorf("original Peers.Hosts length mutated: got %d", len(cfg.Peers.Hosts))
	}
	if cfg.Detect.CCCommands[0] != "claude" {
		t.Errorf("original Detect.CCCommands[0] mutated: got %q", cfg.Detect.CCCommands[0])
	}
	if len(cfg.Allow) != 1 {
		t.Errorf("original Allow length mutated: got %d", len(cfg.Allow))
	}
	if len(cfg.AllowedPaths) != 1 {
		t.Errorf("original AllowedPaths length mutated: got %d", len(cfg.AllowedPaths))
	}
	if cfg.Stream.Presets[0].Name != "cc" {
		t.Errorf("original Stream.Presets[0].Name mutated: got %q", cfg.Stream.Presets[0].Name)
	}
	if len(cfg.Dispatch.AllowedRepoRoots) != 1 {
		t.Errorf("original Dispatch.AllowedRepoRoots length mutated: got %d", len(cfg.Dispatch.AllowedRepoRoots))
	}
}

// TestCloneEmptySliceStaysEmptyNilStaysNil pins Item 1: Clone must not
// collapse a non-nil empty slice to nil (append(nil, src...) does exactly
// that), since a config that explicitly has an empty list (e.g.
// detect.cc_commands = []) must survive a Clone as still non-nil, or it
// re-encodes without the key and silently re-applies defaults on the next
// Load. A field left at its nil zero value must stay nil.
func TestCloneEmptySliceStaysEmptyNilStaysNil(t *testing.T) {
	cfg := config.Config{
		Detect: config.DetectConfig{
			CCCommands: []string{},
		},
	}

	clone := cfg.Clone()

	if clone.Detect.CCCommands == nil {
		t.Errorf("Detect.CCCommands = nil, want non-nil empty slice")
	}
	if len(clone.Detect.CCCommands) != 0 {
		t.Errorf("Detect.CCCommands = %v, want empty", clone.Detect.CCCommands)
	}

	if clone.Allow != nil {
		t.Errorf("Allow = %v, want nil (source was nil)", clone.Allow)
	}
	if clone.AllowedPaths != nil {
		t.Errorf("AllowedPaths = %v, want nil (source was nil)", clone.AllowedPaths)
	}
	if clone.Stream.Presets != nil {
		t.Errorf("Stream.Presets = %v, want nil (source was nil)", clone.Stream.Presets)
	}
	if clone.Dispatch.AllowedRepoRoots != nil {
		t.Errorf("Dispatch.AllowedRepoRoots = %v, want nil (source was nil)", clone.Dispatch.AllowedRepoRoots)
	}
	if clone.Peers.Hosts != nil {
		t.Errorf("Peers.Hosts = %v, want nil (source was nil)", clone.Peers.Hosts)
	}
}

func TestValidateAlias(t *testing.T) {
	const localAlias = "mini-lab"

	accept := []string{"air", "air.2026", "air-2_x"}
	for _, alias := range accept {
		if err := config.ValidateAlias(alias, localAlias); err != nil {
			t.Errorf("ValidateAlias(%q): want accept, got error %v", alias, err)
		}
	}

	reject := []string{
		"",
		".",
		"..",
		"a/b",
		"a b",
		"?x",
		"#",
		"%41",
		strings.Repeat("a", 65),
	}
	for _, alias := range reject {
		if err := config.ValidateAlias(alias, localAlias); err == nil {
			t.Errorf("ValidateAlias(%q): want reject, got nil error", alias)
		}
	}

	// Local alias, in any case, must be rejected.
	for _, alias := range []string{"mini-lab", "Mini-Lab", "MINI-LAB"} {
		if err := config.ValidateAlias(alias, localAlias); err == nil {
			t.Errorf("ValidateAlias(%q) vs local %q: want reject, got nil error", alias, localAlias)
		}
	}
}

func TestNewPeerTokenShapeAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^pdxp_[0-9a-f]{32}$`)

	tok1, err := config.NewPeerToken()
	if err != nil {
		t.Fatalf("NewPeerToken: %v", err)
	}
	if !re.MatchString(tok1) {
		t.Errorf("NewPeerToken shape: got %q", tok1)
	}

	tok2, err := config.NewPeerToken()
	if err != nil {
		t.Fatalf("NewPeerToken: %v", err)
	}
	if !re.MatchString(tok2) {
		t.Errorf("NewPeerToken shape: got %q", tok2)
	}

	if tok1 == tok2 {
		t.Errorf("NewPeerToken: two calls produced the same token %q", tok1)
	}
}

func TestMatchInboundToken(t *testing.T) {
	peers := config.PeersConfig{
		Hosts: []config.PeerHost{
			{Alias: "air", InboundToken: "pdxp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{Alias: "iphone", InboundToken: ""},
			{Alias: "office", InboundToken: "pdxp_cccccccccccccccccccccccccccccccc"},
		},
	}

	host, ok := peers.MatchInboundToken("pdxp_cccccccccccccccccccccccccccccccc")
	if !ok {
		t.Fatal("expected match for office's token")
	}
	if host.Alias != "office" {
		t.Errorf("MatchInboundToken: want alias %q, got %q", "office", host.Alias)
	}

	if _, ok := peers.MatchInboundToken(""); ok {
		t.Error("empty bearer must never match")
	}

	if _, ok := peers.MatchInboundToken(""); ok {
		t.Error("empty bearer must never match (host with empty InboundToken)")
	}

	if _, ok := peers.MatchInboundToken("pdxp_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); ok {
		t.Error("unknown bearer must not match")
	}
}

func TestFindPeerHostByAliasCaseInsensitive(t *testing.T) {
	peers := config.PeersConfig{
		Hosts: []config.PeerHost{
			{Alias: "air"},
			{Alias: "iphone"},
		},
	}

	if idx := peers.FindPeerHostByAlias("AIR"); idx != 0 {
		t.Errorf("FindPeerHostByAlias(AIR): want 0, got %d", idx)
	}
	if idx := peers.FindPeerHostByAlias("iPhone"); idx != 1 {
		t.Errorf("FindPeerHostByAlias(iPhone): want 1, got %d", idx)
	}
	if idx := peers.FindPeerHostByAlias("nope"); idx != -1 {
		t.Errorf("FindPeerHostByAlias(nope): want -1, got %d", idx)
	}
}
