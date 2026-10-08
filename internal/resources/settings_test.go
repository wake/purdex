package resources

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An empty Settings reads as the documented defaults, through Effective and
// through every accessor; 0 stays a real value for warmup_s and floor_pct.
func TestSettings_EffectiveDefaults(t *testing.T) {
	d := DefaultSettings()
	assert.Equal(t, "lease", d.Mode)
	assert.Equal(t, map[string]int{"test-full": 35, "build": 35, "test-pkg": 15, "lint-full": 10}, d.Kinds)
	require.NotNil(t, d.DeadlineS)
	assert.Equal(t, 300, *d.DeadlineS)
	assert.Equal(t, 20, *d.WarmupS)
	assert.Equal(t, 50, *d.FloorPct)
	assert.Equal(t, 3600, *d.MaxHoldS)
	assert.Equal(t, 15, *d.EWMAHalfLifeS)

	var zero Settings
	assert.Equal(t, 300*time.Second, zero.Deadline())
	assert.Equal(t, 20*time.Second, zero.Warmup())
	assert.Equal(t, 0.5, zero.Floor())
	assert.Equal(t, time.Hour, zero.MaxHold())
	assert.Equal(t, 15*time.Second, zero.HalfLife())

	zeros := Settings{WarmupS: intp(0), FloorPct: intp(0)}
	assert.Equal(t, time.Duration(0), zeros.Warmup())
	assert.Equal(t, 0.0, zeros.Floor())
	assert.Equal(t, 0, *zeros.Effective().WarmupS)
	assert.Equal(t, 0, *zeros.Effective().FloorPct)
}

// Effective merges kinds over the built-ins, clamps what is out of range and
// shares nothing with its receiver or with DefaultKinds.
func TestSettings_EffectiveMergesClampsAndCopies(t *testing.T) {
	s := Settings{Mode: "bogus", Kinds: map[string]int{"build": 20, "custom": 7, "huge": 500}, DeadlineS: intp(9999)}
	e := s.Effective()
	assert.Equal(t, "lease", e.Mode)
	assert.Equal(t, map[string]int{"test-full": 35, "build": 20, "test-pkg": 15, "lint-full": 10, "custom": 7, "huge": 100}, e.Kinds)
	assert.Equal(t, 590, *e.DeadlineS)

	e.Kinds["test-full"] = 1
	assert.Equal(t, 35, DefaultKinds["test-full"], "the default table is not shared")
	assert.Equal(t, 20, s.Kinds["build"])

	w, ok := s.Weight("custom")
	assert.True(t, ok)
	assert.Equal(t, 7, w)
	w, ok = s.Weight("test-pkg")
	assert.True(t, ok)
	assert.Equal(t, 15, w)
	_, ok = s.Weight("nope")
	assert.False(t, ok)
}

// Codex attack (high): the limit counts the kinds an operator adds. Effective
// puts the four built-ins on top, so counting the merged set would accept 32
// custom kinds and then refuse the stored result on the next read.
func TestSettings_CustomKindLimitSurvivesEffective(t *testing.T) {
	custom := func(n int) map[string]int {
		m := map[string]int{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("kind-%02d", i)] = 10
		}
		return m
	}
	ok := Settings{Kinds: custom(maxKinds)}
	require.NoError(t, ok.Validate())
	require.NoError(t, ok.Effective().Validate(), "what Effective stores must validate again")
	assert.Equal(t, maxKinds+len(DefaultKinds), len(ok.Effective().Kinds))

	tooMany := Settings{Kinds: custom(maxKinds + 1)}
	assert.ErrorIs(t, tooMany.Validate(), ErrSettings)

	// Overriding built-ins costs nothing against the limit.
	withBuiltins := custom(maxKinds)
	for k := range DefaultKinds {
		withBuiltins[k] = 20
	}
	require.NoError(t, Settings{Kinds: withBuiltins}.Validate())
}

func TestSettings_ValidateNamesTheField(t *testing.T) {
	assert.NoError(t, Settings{}.Validate())
	assert.NoError(t, DefaultSettings().Validate())
	for want, s := range map[string]Settings{
		"mode":             {Mode: "on"},
		"deadline_s":       {DeadlineS: intp(9)},
		"warmup_s":         {WarmupS: intp(301)},
		"floor_pct":        {FloorPct: intp(-1)},
		"max_hold_s":       {MaxHoldS: intp(59)},
		"ewma_half_life_s": {EWMAHalfLifeS: intp(4)},
		"kinds.build":      {Kinds: map[string]int{"build": 0}},
		"kind name":        {Kinds: map[string]int{"Bad Name": 5}},
	} {
		err := s.Validate()
		require.ErrorIs(t, err, ErrSettings, want)
		assert.Contains(t, err.Error(), want)
	}
}

func TestSettings_JSONOmitsUnset(t *testing.T) {
	b, err := json.Marshal(Settings{})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(b))
	b, err = json.Marshal(Settings{WarmupS: intp(0)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"warmup_s":0}`, string(b))
}
