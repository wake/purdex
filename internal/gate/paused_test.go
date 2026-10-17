package gate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A slot count of 0 pauses the class (user decision 10-16, W3c): the count is read as a setting, set by `pdx gate set`, and
// accepted in a profile.
func TestPaused_ZeroIsAcceptedByLoadSetAndProfile(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, "[gate.slots]\nheavy = 0\nlight = 0\n"))
	require.NoError(t, err)
	assert.Empty(t, cfg.Warnings)
	assert.Equal(t, 0, cfg.Slots.Heavy)
	assert.Equal(t, 0, cfg.Slots.Light)
	assert.True(t, cfg.Slots.Paused(ClassHeavy))
	assert.True(t, cfg.Slots.Paused(ClassLight))
	assert.Equal(t, SrcConfig, cfg.Sources["slots.heavy"], "the 0 is shown as set, not turned into the default")
	for _, e := range cfg.Effective() {
		if e.Key == "slots.heavy" {
			assert.Equal(t, "0", e.Value)
		}
	}

	p := cfgFile(t, "[gate]\nenabled = true\n")
	res, err := Set(p, "", "slots.heavy", "0")
	require.NoError(t, err)
	assert.Equal(t, "0", res.New)
	loaded, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, 0, loaded.Slots.Heavy)

	p = cfgFile(t, "[gate]\nprofile = \"night\"\n[gate.profiles.night.slots]\nheavy = 0\n")
	loaded, err = Load(p)
	require.NoError(t, err)
	assert.Empty(t, loaded.Warnings)
	assert.Equal(t, 0, loaded.Slots.Heavy)
	assert.Equal(t, SrcProfile, loaded.Sources["slots.heavy"])
}

func TestPaused_AboveTheLimitOrNegativeIsStillRefused(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, "[gate.slots]\nheavy = -1\nlight = 65\n"))
	require.NoError(t, err)
	assert.Equal(t, Defaults().Slots, cfg.Slots)
	assert.Len(t, cfg.Warnings, 2)
}

func pausedGate(t *testing.T, heavy, light int) *Gate {
	t.Helper()
	g, _ := testGate(t)
	g.Holders = nil
	g.SetConfig(func(c *Config) { c.Slots.Heavy, c.Slots.Light = heavy, light })
	return g
}

// A heavy command at slots.heavy=0 waits (never fails), says paused, and starts by itself once the count is raised; light is
// not touched meanwhile.
func TestPaused_AHeavyCommandWaitsThenStartsWhenTheCountIsRaised(t *testing.T) {
	g := pausedGate(t, 0, 1)
	dir := t.TempDir()
	heavy := runAsync(g, context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	eventually(t, "the command to queue", func() bool { return g.StatusWith(HoldersOrphaned).WaitHeavy == 1 })

	st := g.StatusWith(HoldersOrphaned)
	assert.True(t, st.PausedHeavy)
	assert.False(t, st.PausedLight)
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "paused", w.Reason)
	assert.Contains(t, w.Note, "paused")
	assert.False(t, g.WouldWait(ClassLight).Wait, "the other class is not paused")

	// light goes through at once
	ld := t.TempDir()
	res := runSync(t, g, Options{Argv: sh("touch " + ld + "/ran"), Class: ClassLight})
	assert.Equal(t, 0, res.code)
	assert.True(t, exists(ld+"/ran"))

	time.Sleep(150 * time.Millisecond)
	assert.False(t, exists(dir+"/ran"), "paused: it has not started")
	select {
	case r := <-heavy:
		t.Fatalf("a paused command must wait, it ended with %d", r.code)
	default:
	}

	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	select {
	case r := <-heavy:
		assert.Equal(t, 0, r.code)
	case <-time.After(5 * time.Second):
		t.Fatal("the command did not start after the count was raised")
	}
	assert.True(t, exists(dir+"/ran"))
	rec := lastDecision(t, g)
	assert.Contains(t, rec["wait_reasons"], "paused")
}

// The waiter says so in its own line.
func TestPaused_TheWaitersLineSaysPaused(t *testing.T) {
	g, buf := testGate(t)
	g.Holders = nil
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 0 })
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(g, ctx, Options{Argv: sh("true"), Class: ClassHeavy})
	eventually(t, "the line", func() bool { return strings.Contains(buf.String(), "paused") })
	cancel()
	assert.Equal(t, 130, (<-done).code)
	assert.Contains(t, buf.String(), "slots.heavy = 0")
}

// Lowering the count to 0 does not touch a command that runs.
func TestPaused_ARunningCommandIsNotTouchedByTheChangeToZero(t *testing.T) {
	g := pausedGate(t, 1, 1)
	done, release := holdSlot(t, g, ClassHeavy, "runner")
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 0 })
	time.Sleep(200 * time.Millisecond)
	assert.Len(t, g.StatusWith(HoldersOrphaned).Heavy, 1, "still holding its slot")
	select {
	case r := <-done:
		t.Fatalf("the running command was stopped (%d)", r.code)
	default:
	}
	release()
	assert.Equal(t, 0, (<-done).code)
}

// A paused class takes no claim at all: no late one, no over one.
func TestPaused_NoLateClaimWhilePaused(t *testing.T) {
	p := lateTestPool(t, 0)
	s, err := p.TakeLate()
	require.NoError(t, err)
	assert.Nil(t, s)
	assert.Empty(t, p.Held())
	p.Size = 1
	s, err = p.TakeLate()
	require.NoError(t, err)
	require.NotNil(t, s, "and it takes one as soon as the count is raised")
	s.Release(nil)
}

// The class is paused AFTER the script started: the late claim reads the settings as they are when the timer fires, so it takes
// nothing. Mutation gate: the pool built from the settings the command started under → a late slot is taken (red).
func TestPaused_AClassPausedAfterTheStartTakesNoLateSlot(t *testing.T) {
	g := packagingGate(t, 600*time.Millisecond)
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runPackaging(g, dir)
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	g.SetConfig(func(c *Config) { c.Slots.Light = 0 })
	time.Sleep(1200 * time.Millisecond)
	assert.Empty(t, lightHeld(g), "no late slot for a class paused since the start")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go"), nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.NotContains(t, lastDecision(t, g), "late_slot")
}

// A packaging script of a paused class gets no late slot; it is not stopped either.
func TestPaused_APackagingScriptGetsNoLateSlotWhileItsClassIsPaused(t *testing.T) {
	g := packagingGate(t, 150*time.Millisecond)
	g.SetConfig(func(c *Config) { c.Slots.Light = 0 })
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runPackaging(g, dir)
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	time.Sleep(500 * time.Millisecond)
	assert.Empty(t, lightHeld(g))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go"), nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.NotContains(t, lastDecision(t, g), "late_slot")
}
