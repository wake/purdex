package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cfgFile(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if text != "" {
		require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func TestSet_ChangesAKeyInPlaceAndKeepsTheRest(t *testing.T) {
	p := cfgFile(t, "# mine\n[other]\nx = 1\n\n[gate.slots]\n# how many\nheavy = 1\nlight = 3\n\n[gate.priority]\nnice = 5\n")
	res, err := Set(p, "", "slots.heavy", "2")
	require.NoError(t, err)
	assert.Equal(t, "1", res.Old)
	assert.Equal(t, "2", res.New)
	assert.Equal(t, SrcConfig, res.NewSrc)
	assert.Equal(t, "# mine\n[other]\nx = 1\n\n[gate.slots]\n# how many\nheavy = 2\nlight = 3\n\n[gate.priority]\nnice = 5\n", read(t, p))
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.Slots.Heavy)
	st, _ := os.Stat(p)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "the file's mode is kept")
}

func TestSet_AddsAKeyATableAndAFile(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 1\n")
	_, err := Set(p, "", "slots.light", "4")
	require.NoError(t, err)
	assert.Equal(t, "[gate.slots]\nlight = 4\nheavy = 1\n", read(t, p))
	_, err = Set(p, "", "priority.mode", "none") // a bare word is a string
	require.NoError(t, err)
	assert.Contains(t, read(t, p), "\n[gate.priority]\nmode = \"none\"\n")

	fresh := cfgFile(t, "")
	_, err = Set(fresh, "", "parallelism.go_p", "auto")
	require.NoError(t, err)
	assert.Equal(t, "[gate.parallelism]\ngo_p = \"auto\"\n", read(t, fresh))
	_, err = Set(fresh, "", "enabled", "false")
	require.NoError(t, err)
	cfg, _ := Load(fresh)
	assert.False(t, cfg.Enabled)
}

func TestSet_RefusesWhatTheGateWouldNotAccept(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 1\n")
	before := read(t, p)
	for _, c := range []struct{ key, val, want string }{
		{"slots.heavy", "0", "slots.heavy"},         // out of range
		{"slots.heavy", "many", "slots.heavy"},      // a string where a number is wanted
		{"slots.nope", "1", "not a setting"},        // unknown key
		{"nope.heavy", "1", "not a setting"},        // unknown module
		{"heavy", "1", "not a setting"},             // no module
		{"Slots.Heavy", "1", "not a setting name"},  // spelling
		{"slots.heavy", "1 +", "not a TOML value"},  // junk
		{"priority.mode", "turbo", "priority.mode"}, // not in the enum
	} {
		_, err := Set(p, "", c.key, c.val)
		require.Error(t, err, "%s=%s", c.key, c.val)
		assert.Contains(t, err.Error(), c.want, "%s=%s", c.key, c.val)
	}
	assert.Equal(t, before, read(t, p), "a refused change leaves the file alone")
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		assert.False(t, strings.Contains(e.Name(), ".tmp"), "no temp file is left: %s", e.Name())
	}
}

func TestSet_ADottedKeyOfTheFileIsRefusedNotDuplicated(t *testing.T) {
	p := cfgFile(t, "[gate]\nslots.heavy = 1\n")
	before := read(t, p)
	_, err := Set(p, "", "slots.heavy", "2")
	require.Error(t, err)
	assert.Equal(t, before, read(t, p))
}

func TestSet_AProfileHoldsTheValueAndWinsWhenActive(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 1\n")
	res, err := Set(p, "night", "slots.heavy", "2")
	require.NoError(t, err)
	assert.Equal(t, SrcProfile, res.NewSrc)
	assert.Contains(t, read(t, p), "[gate.profiles.night.slots]\nheavy = 2\n")
	cfg, _ := Load(p)
	assert.Equal(t, 1, cfg.Slots.Heavy, "the profile is not active yet")
	assert.Equal(t, []string{"night"}, cfg.Profiles)

	known, err := UseProfile(p, "night")
	require.NoError(t, err)
	assert.True(t, known)
	cfg, _ = Load(p)
	assert.Equal(t, "night", cfg.Profile)
	assert.Equal(t, 2, cfg.Slots.Heavy)
	assert.Equal(t, SrcProfile, cfg.Sources["slots.heavy"])
	assert.Equal(t, "2", cfg.Values["slots.heavy"])

	known, err = UseProfile(p, "default")
	require.NoError(t, err)
	assert.True(t, known)
	cfg, _ = Load(p)
	assert.Equal(t, 1, cfg.Slots.Heavy)
	assert.Equal(t, SrcConfig, cfg.Sources["slots.heavy"])

	known, err = UseProfile(p, "ghost")
	require.NoError(t, err)
	assert.False(t, known, "a profile that is not defined is accepted and changes nothing")
	_, err = UseProfile(p, "Bad Name")
	assert.Error(t, err)
	_, err = Set(p, "night", "enabled", "false")
	assert.Error(t, err, "a profile holds module settings only")
}

func TestProfile_CannotChangeTheProfileOrTheDirectory(t *testing.T) {
	p := cfgFile(t, "[gate]\nprofile = \"a\"\n[gate.profiles.a]\nprofile = \"b\"\ndir = \"/elsewhere\"\n[gate.profiles.a.slots]\nlight = 7\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "a", cfg.Profile)
	assert.NotEqual(t, "/elsewhere", cfg.Dir)
	assert.Equal(t, 7, cfg.Slots.Light)
}

func TestConfig_EffectiveListsEveryValueWithItsSource(t *testing.T) {
	p := cfgFile(t, "[gate.slots]\nheavy = 2\n[gate.parallelism]\ngo_p = \"auto\"\n")
	cfg, err := Load(p)
	require.NoError(t, err)
	got := map[string]Setting{}
	for _, s := range cfg.Effective() {
		got[s.Key] = s
	}
	assert.Equal(t, Setting{Key: "slots.heavy", Value: "2", Source: SrcConfig}, got["slots.heavy"])
	assert.Equal(t, Setting{Key: "slots.light", Value: "3", Source: SrcDefault}, got["slots.light"])
	assert.Equal(t, "auto", got["parallelism.go_p"].Value)
	assert.Equal(t, "nice", got["priority.mode"].Value)
	assert.Equal(t, "go,pnpm,npx,vitest,tsc", got["intercept.tools"].Value)
	assert.Equal(t, "10", got["admission.disk_min_gb"].Value)
	assert.Equal(t, "auto", got["admission.max_page_rate"].Value)
}
