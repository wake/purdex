package config_test

import (
	"errors"
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
			Alias:   "mini-lab",
			Deliver: true,
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

	if loadedCfg.Peers.Deliver != true {
		t.Errorf("Peers.Deliver: want true, got %v", loadedCfg.Peers.Deliver)
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
		Peers: config.PeersConfig{
			Deliver: true,
			Hosts:   []config.PeerHost{{Alias: "air", Token: "t1"}},
		},
	}

	clone := cfg.Clone()

	if clone.Peers.Deliver != true {
		t.Errorf("clone.Peers.Deliver = %v, want true (whole-struct value copy)", clone.Peers.Deliver)
	}

	// Mutate the clone in every dimension the brief calls out.
	clone.Peers.Hosts = append(clone.Peers.Hosts, config.PeerHost{Alias: "iphone"})
	clone.Detect.CCCommands[0] = "mutated"
	clone.Allow = append(clone.Allow, "192.168.0.0/16")
	clone.AllowedPaths = append(clone.AllowedPaths, "/etc")

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

// TestValidateSelfAlias pins spec S-1 (#1196): the self alias clears the
// same shape/reserved rules as an entry alias, plus it must not
// case-insensitively equal any configured peer host's alias. The empty
// string is not this function's business (the handler clears without
// validating), so it is deliberately absent from the table.
func TestValidateSelfAlias(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air26", URL: "http://air:7860"}}

	cases := []struct {
		name      string
		alias     string
		hosts     []config.PeerHost
		wantErr   bool
		collision bool
		wantText  string
	}{
		{name: "collides with a host alias", alias: "air26", hosts: hosts, wantErr: true, collision: true, wantText: `alias "air26" is already used by a peer host`},
		{name: "collides case-insensitively", alias: "AIR26", hosts: hosts, wantErr: true, collision: true, wantText: `alias "AIR26" is already used by a peer host`},
		{name: "distinct from every host", alias: "mlab", hosts: hosts},
		{name: "reserved dot-dot", alias: "..", hosts: hosts, wantErr: true, wantText: "reserved"},
		{name: "reserved dot", alias: ".", hosts: hosts, wantErr: true, wantText: "reserved"},
		{name: "bad pattern", alias: "bad alias!", hosts: hosts, wantErr: true, wantText: "must match"},
		{name: "no hosts", alias: "x", hosts: nil},
		{name: "too long", alias: strings.Repeat("a", 65), hosts: nil, wantErr: true, wantText: "must match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateSelfAlias(tc.alias, tc.hosts)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("ValidateSelfAlias(%q): want accept, got %v", tc.alias, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateSelfAlias(%q): want reject, got nil", tc.alias)
			}
			if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantText)
			}
			if got := errors.Is(err, config.ErrSelfAliasCollision); got != tc.collision {
				t.Errorf("errors.Is(err, ErrSelfAliasCollision) = %v, want %v (err=%v)", got, tc.collision, err)
			}
		})
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

	host, _, ok := peers.MatchInboundToken("pdxp_cccccccccccccccccccccccccccccccc")
	if !ok {
		t.Fatal("expected match for office's token")
	}
	if host.Alias != "office" {
		t.Errorf("MatchInboundToken: want alias %q, got %q", "office", host.Alias)
	}

	if _, _, ok := peers.MatchInboundToken(""); ok {
		t.Error("empty bearer must never match")
	}

	if _, _, ok := peers.MatchInboundToken(""); ok {
		t.Error("empty bearer must never match (host with empty InboundToken)")
	}

	if _, _, ok := peers.MatchInboundToken("pdxp_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); ok {
		t.Error("unknown bearer must not match")
	}
}

// TestMatchInboundToken_PendingRotation_BothTokensSameAlias pins spec §6.2:
// while a rotation is pending, the old token (prev) and the new one
// (current) both authenticate as the same alias, and usedPrev is true only
// for the old one.
func TestMatchInboundToken_PendingRotation_BothTokensSameAlias(t *testing.T) {
	peers := config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "air", InboundToken: "pdxp_11111111111111111111111111111111", InboundTokenPrev: "pdxp_00000000000000000000000000000000"},
	}}
	h, usedPrev, ok := peers.MatchInboundToken("pdxp_11111111111111111111111111111111")
	if !ok || h.Alias != "air" || usedPrev {
		t.Fatalf("current: ok=%v alias=%q usedPrev=%v; want ok air false", ok, h.Alias, usedPrev)
	}
	h, usedPrev, ok = peers.MatchInboundToken("pdxp_00000000000000000000000000000000")
	if !ok || h.Alias != "air" || !usedPrev {
		t.Fatalf("prev: ok=%v alias=%q usedPrev=%v; want ok air true", ok, h.Alias, usedPrev)
	}
}

// TestMatchInboundToken_LastMatchWinsAcrossPrev pins "no early exit across
// configured non-empty token fields" without a comparison seam (spec §8.3):
// the bearer equals entry 1's current AND entry 3's prev; the result must
// be entry 3 with usedPrev — an implementation that returns on the first
// match, or that never looks at prev, yields entry 1.
func TestMatchInboundToken_LastMatchWinsAcrossPrev(t *testing.T) {
	const bearer = "pdxp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	peers := config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "one", InboundToken: bearer},
		{Alias: "two", InboundToken: "pdxp_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", InboundTokenPrev: "pdxp_cccccccccccccccccccccccccccccccc"},
		{Alias: "three", InboundToken: "pdxp_dddddddddddddddddddddddddddddddd", InboundTokenPrev: bearer},
	}}
	h, usedPrev, ok := peers.MatchInboundToken(bearer)
	if !ok || h.Alias != "three" || !usedPrev {
		t.Fatalf("got ok=%v alias=%q usedPrev=%v; want ok three true", ok, h.Alias, usedPrev)
	}
}

// A prev with no current is not a state the API produces, but a hand-edited
// config may hold one; the field is a token, not a flag, so it authenticates.
func TestMatchInboundToken_PrevOnlyStillAuthenticates(t *testing.T) {
	peers := config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "air", InboundToken: "", InboundTokenPrev: "pdxp_00000000000000000000000000000000"},
	}}
	h, usedPrev, ok := peers.MatchInboundToken("pdxp_00000000000000000000000000000000")
	if !ok || h.Alias != "air" || !usedPrev {
		t.Fatalf("got ok=%v alias=%q usedPrev=%v; want ok air true", ok, h.Alias, usedPrev)
	}
	if _, _, ok := peers.MatchInboundToken(""); ok {
		t.Fatal("empty bearer must never match an empty current")
	}
}

func TestRedacted_BlanksInboundTokenPrev(t *testing.T) {
	cfg := config.Config{Peers: config.PeersConfig{Hosts: []config.PeerHost{
		{Alias: "air", Token: "out", InboundToken: "in", InboundTokenPrev: "in-prev"},
	}}}
	got := cfg.Redacted().Peers.Hosts[0]
	if got.Token != "" || got.InboundToken != "" || got.InboundTokenPrev != "" {
		t.Fatalf("Redacted left a token value: %+v", got)
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

// TokenFingerprint is the non-reversible identity the peers module keys its
// rotation record on: deterministic, short, never the token, and "" for "".
func TestTokenFingerprint(t *testing.T) {
	const tok = "pdxp_00000000000000000000000000000000"
	fp := config.TokenFingerprint(tok)
	if fp != config.TokenFingerprint(tok) {
		t.Fatalf("not deterministic: %q vs %q", fp, config.TokenFingerprint(tok))
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(fp) {
		t.Fatalf("fingerprint %q is not 16 lowercase hex chars", fp)
	}
	if strings.Contains(tok, fp) || strings.Contains(fp, "pdxp") {
		t.Fatalf("fingerprint %q is a substring of the token, or carries its prefix", fp)
	}
	if got := config.TokenFingerprint(""); got != "" {
		t.Fatalf("TokenFingerprint(\"\") = %q, want \"\"", got)
	}
	if other := config.TokenFingerprint("pdxp_00000000000000000000000000000001"); other == fp {
		t.Fatalf("two different tokens share fingerprint %q", fp)
	}
}
