package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
)

// c933b0-122: [nex.sandbox] extra_profiles.

func TestNexExtraProfilesValidate(t *testing.T) {
	ok := config.DefaultNexConfig()
	ok.Sandbox.ExtraProfiles = []string{"confined"}
	if err := ok.Validate("/home/u"); err != nil {
		t.Errorf("extra_profiles=[confined] should be valid: %v", err)
	}
	for _, bad := range [][]string{{"trusted"}, {"yolo"}, {"confined", "nope"}, {""}} {
		n := config.DefaultNexConfig()
		n.Sandbox.ExtraProfiles = bad
		err := n.Validate("/home/u")
		if err == nil {
			t.Errorf("extra_profiles=%q accepted", bad)
			continue
		}
		if !strings.Contains(err.Error(), "nex.sandbox.extra_profiles") {
			t.Errorf("error %q does not name nex.sandbox.extra_profiles", err)
		}
	}
}

func TestNexExtraProfilesEqual(t *testing.T) {
	a, b := config.DefaultNexConfig(), config.DefaultNexConfig()
	if !a.Equal(b) {
		t.Fatal("defaults differ")
	}
	b.Sandbox.ExtraProfiles = []string{"confined"}
	if a.Equal(b) || b.Equal(a) {
		t.Error("a change to extra_profiles is not a change (restart_required would stay false)")
	}
	a.Sandbox.ExtraProfiles = []string{}
	c := config.DefaultNexConfig()
	if !a.Equal(c) {
		t.Error("nil and empty extra_profiles must be equal")
	}
}

func TestNexExtraProfilesTomlRoundTripAndOmitted(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := writeAndRead(t, cfg)
	if strings.Contains(text, "extra_profiles") {
		t.Errorf("an empty extra_profiles was written:\n%s", text)
	}
	cfg.Nex.Sandbox.ExtraProfiles = []string{"confined"}
	text, got := writeAndRead(t, cfg)
	if !strings.Contains(text, "extra_profiles") || !got.Nex.Equal(cfg.Nex) {
		t.Errorf("extra_profiles lost in a round trip:\n%s\ngot %+v", text, got.Nex.Sandbox)
	}
}

func TestConfigCloneAndRedactedExtraProfiles(t *testing.T) {
	cfg := config.Config{}
	cfg.Nex.Sandbox.ExtraProfiles = []string{"confined"}
	clone := cfg.Clone()
	clone.Nex.Sandbox.ExtraProfiles[0] = "mutated"
	if cfg.Nex.Sandbox.ExtraProfiles[0] != "confined" {
		t.Error("Clone shares the extra_profiles backing array")
	}
	if r := (config.Config{}).Redacted(); r.Nex.Sandbox.ExtraProfiles == nil {
		t.Error("Redacted leaves extra_profiles nil: GET /api/config would send null")
	}
}
