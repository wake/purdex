package gate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// holdsFake is the wwd's answer the test steers, and how often it was asked.
type holdsFake struct {
	list  atomic.Pointer[[]wire.Exception]
	err   atomic.Pointer[error]
	delay time.Duration
	calls atomic.Int32
}

func (f *holdsFake) fetch(ctx context.Context) ([]wire.Exception, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if e := f.err.Load(); e != nil {
		return nil, *e
	}
	if l := f.list.Load(); l != nil {
		return *l, nil
	}
	return nil, nil
}

func (f *holdsFake) set(l ...wire.Exception) { f.list.Store(&l) }

func hold(name string, release bool, holds ...wire.Hold) wire.Exception {
	e := wire.Exception{Name: name, Holds: holds, Allow: &wire.HoldAllow{}}
	if release {
		e.Flags = []wire.HoldFlag{wire.HoldFlagRelease}
	}
	return e
}

func holdGate(t *testing.T) (*Gate, *holdsFake) {
	g, _ := testGate(t)
	f := &holdsFake{}
	g.HoldFetch = f.fetch
	return g, f
}

func TestHold_HeavyWaitsForAnyHoldAndSaysWhich(t *testing.T) {
	g, f := holdGate(t)
	f.set(hold("hold-x", false, wire.HoldHeavy))
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "hold:hold-x", w.Reason)
	assert.Contains(t, w.Note, "hold-x")
	assert.False(t, g.WouldWait(ClassLight).Wait, "a hold that lists only heavy leaves light alone")
}

func TestHold_LightWaitsOnlyForAHoldThatListsLight(t *testing.T) {
	g, f := holdGate(t)
	f.set(hold("hold-l", false, wire.HoldHeavy, wire.HoldLight))
	w := g.WouldWait(ClassLight)
	assert.True(t, w.Wait)
	assert.Equal(t, "hold:hold-l", w.Reason)
}

// A plain `pdx wwd hold` (no allow, no flags) is the same on the wire as an ordinary exception, and both are what the headroom is decided by
// (spec §3, §13.4): heavy waits for it, light only when it lists light.
func TestHold_APlainHoldOrExceptionMakesHeavyWait(t *testing.T) {
	g, f := holdGate(t)
	f.set(wire.Exception{Name: "hold-plain", Holds: []wire.Hold{wire.HoldHeavy}}) // Allow nil, no flags
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "hold:hold-plain", w.Reason)
}

func TestHold_APlainHoldThatListsOnlyHeavyLeavesLight(t *testing.T) {
	g, f := holdGate(t)
	f.set(wire.Exception{Name: "hold-plain", Holds: []wire.Hold{wire.HoldHeavy}})
	assert.False(t, g.WouldWait(ClassLight).Wait)
}

func TestHold_APlainHoldThatListsLightMakesLightWait(t *testing.T) {
	g, f := holdGate(t)
	f.set(wire.Exception{Name: "hold-plain", Holds: []wire.Hold{wire.HoldHeavy, wire.HoldLight}})
	w := g.WouldWait(ClassLight)
	assert.True(t, w.Wait)
	assert.Equal(t, "hold:hold-plain", w.Reason)
}

func TestHold_ReleaseTakesTheLightSlotsDownToOne(t *testing.T) {
	g, f := holdGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Light = 3 })
	f.set(hold("hold-r", true, wire.HoldHeavy))
	assert.Equal(t, 1, g.Config().Slots.Light, "effective")
	assert.Equal(t, 3, g.st.cfg.Slots.Light, "the stored setting is not changed")
	assert.Equal(t, 1, g.refresh().Slots.Light)
	// one light command takes the slot, the next waits for it
	done, release := holdSlot(t, g, ClassLight, "a")
	defer func() { release(); <-done }()
	w := g.WouldWait(ClassLight)
	assert.True(t, w.Wait)
	assert.Equal(t, "slots", w.Reason)
	// a setting below 1 stays where it is
	g.SetConfig(func(c *Config) { c.Slots.Light = 0 })
	assert.Equal(t, 0, g.Config().Slots.Light)
}

func TestHold_SlotsComeBackWhenTheHoldEnds(t *testing.T) {
	g, f := holdGate(t)
	now := time.Now()
	g.Now = func() time.Time { return now }
	g.SetConfig(func(c *Config) { c.Slots.Light = 3 })
	f.set(hold("hold-r", true, wire.HoldHeavy))
	require.Equal(t, 1, g.Config().Slots.Light)
	f.set()
	now = now.Add(holdTTL + time.Second)
	assert.Equal(t, 3, g.Config().Slots.Light)
}

func TestHold_ARunningGateWaitsForTheHoldAndStartsWhenItEnds(t *testing.T) {
	g, f := holdGate(t)
	now := time.Now()
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	g.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	f.set(hold("hold-x", false, wire.HoldHeavy))
	done := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy})
	select {
	case r := <-done:
		t.Fatalf("started under a hold: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	f.set()
	clock.Add(int64(holdTTL + time.Second))
	select {
	case r := <-done:
		assert.Equal(t, 0, r.code)
	case <-time.After(5 * time.Second):
		t.Fatal("did not start after the hold ended")
	}
}

func TestHold_FailOpen(t *testing.T) {
	t.Run("an error", func(t *testing.T) {
		g, f := holdGate(t)
		e := errors.New("wwd is down")
		f.err.Store(&e)
		assert.False(t, g.WouldWait(ClassHeavy).Wait)
	})
	t.Run("too slow", func(t *testing.T) {
		g, f := holdGate(t)
		f.delay = 5 * time.Second
		f.set(hold("hold-x", true, wire.HoldHeavy, wire.HoldLight))
		start := time.Now()
		assert.False(t, g.WouldWait(ClassHeavy).Wait)
		assert.Less(t, time.Since(start), 2*time.Second, "the read is bounded")
		g.SetConfig(func(c *Config) { c.Slots.Light = 3 })
		assert.Equal(t, 3, g.Config().Slots.Light, "light stays at the setting")
	})
	t.Run("no fetch at all", func(t *testing.T) {
		g, _ := testGate(t)
		assert.False(t, g.WouldWait(ClassHeavy).Wait)
	})
}

func TestHold_TheAnswerIsKeptFiveSecondsAcrossProcesses(t *testing.T) {
	g, f := holdGate(t)
	now := time.Now()
	g.Now = func() time.Time { return now }
	f.set(hold("hold-x", false, wire.HoldHeavy))
	require.True(t, g.WouldWait(ClassHeavy).Wait)
	require.True(t, g.WouldWait(ClassHeavy).Wait)
	assert.EqualValues(t, 1, f.calls.Load(), "the same process asks once")

	// another gate process: its own memory is empty, the file in the gate directory is what it reads
	g2 := New(g.Config())
	g2.Now = g.Now
	g2.HoldFetch = f.fetch
	f.set() // the hold is gone, but nobody has looked since
	assert.True(t, g2.WouldWait(ClassHeavy).Wait, "from the file")
	assert.EqualValues(t, 1, f.calls.Load())
	_, err := os.Stat(filepath.Join(g.Config().Dir, holdCacheFile))
	assert.NoError(t, err)

	now = now.Add(holdTTL - time.Millisecond)
	g3 := New(g.Config())
	g3.Now = g.Now
	g3.HoldFetch = f.fetch
	assert.True(t, g3.WouldWait(ClassHeavy).Wait, "still inside five seconds")
	assert.EqualValues(t, 1, f.calls.Load())

	now = now.Add(2 * time.Millisecond)
	g4 := New(g.Config())
	g4.Now = g.Now
	g4.HoldFetch = f.fetch
	assert.False(t, g4.WouldWait(ClassHeavy).Wait, "after five seconds it asks again")
	assert.EqualValues(t, 2, f.calls.Load())
}

func TestHold_ABrokenCacheFileIsAskedAgain(t *testing.T) {
	g, f := holdGate(t)
	f.set(hold("hold-x", false, wire.HoldHeavy))
	require.NoError(t, os.WriteFile(filepath.Join(g.Config().Dir, holdCacheFile), []byte("{not json"), 0o600))
	assert.True(t, g.WouldWait(ClassHeavy).Wait)
	assert.EqualValues(t, 1, f.calls.Load())
}

// R2: a look in a gate directory that does not exist yet (would-wait and status are read-only) still leaves the answer for the next process.
func TestHold_TheGateDirectoryIsMadeForTheCache(t *testing.T) {
	g, f := holdGate(t)
	dir := filepath.Join(t.TempDir(), "not-yet")
	g.SetConfig(func(c *Config) { c.Dir = dir })
	f.set(hold("hold-x", false, wire.HoldHeavy))
	require.True(t, g.WouldWait(ClassHeavy).Wait)
	g2 := New(g.Config())
	g2.HoldFetch = f.fetch
	require.True(t, g2.WouldWait(ClassHeavy).Wait)
	assert.EqualValues(t, 1, f.calls.Load(), "the second process read the file")
}

// R2: a slow failure that ends after another process wrote a good answer must not turn that answer into "no hold".
func TestHold_ALateFailureDoesNotOverwriteAFreshAnswer(t *testing.T) {
	g, _ := holdGate(t)
	now := time.Now()
	g.Now = func() time.Time { return now }
	dir := g.st.cfg.Dir // (Config() would already ask the fetch of the rig)
	g.HoldFetch = func(ctx context.Context) ([]wire.Exception, error) {
		b, _ := json.Marshal(holdFile{At: now.UnixMilli(), Holds: []HoldInfo{{Name: "hold-other"}}}) // the other process finishes first
		require.NoError(t, os.WriteFile(filepath.Join(dir, holdCacheFile), b, 0o600))
		return nil, errors.New("timed out")
	}
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "hold:hold-other", w.Reason)
	b, err := os.ReadFile(filepath.Join(dir, holdCacheFile))
	require.NoError(t, err)
	assert.Contains(t, string(b), "hold-other")
}
