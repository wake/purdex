package gate

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lateTestPool(t *testing.T, size int) *Pool {
	t.Helper()
	return &Pool{Dir: t.TempDir(), Class: ClassLight, Size: size, Now: time.Now}
}

// A late holder takes the lowest free slot of the pool; when the pool is full it takes one beyond its size, and the slot says so.
func TestTakeLate_UsesAFreeSlotElseGoesOver(t *testing.T) {
	p := lateTestPool(t, 2)
	a, err := p.TakeLate()
	require.NoError(t, err)
	require.NotNil(t, a)
	assert.Equal(t, 1, a.K, "a free slot of the pool first")
	assert.False(t, a.Over(p.Size))

	tk, err := p.Enqueue()
	require.NoError(t, err)
	b, _, err := p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, b)
	tk.Drop()
	assert.Equal(t, 2, b.K)

	c, err := p.TakeLate()
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, 3, c.K, "the pool is full: the late holder is over its size")
	assert.True(t, c.Over(p.Size))
	require.NoError(t, c.SetHolder(Holder{PID: os.Getpid(), Late: true, Argv: []string{"pnpm", "run", "package:win"}}))

	held := p.Held()
	require.Len(t, held, 3)
	assert.False(t, held[0].Over)
	assert.True(t, held[2].Over, "status can tell it is over")
	assert.True(t, held[2].Late)

	for _, s := range []*Slot{a, b, c} {
		s.Release(nil)
	}
	assert.Empty(t, p.Held())
}

// Only overSlots late holders fit beyond the pool; the next gets nil, not an error and not a wait.
func TestTakeLate_RunsOutOfOverSlots(t *testing.T) {
	p := lateTestPool(t, 1)
	var got []*Slot
	for i := 0; i < 1+overSlots; i++ {
		s, err := p.TakeLate()
		require.NoError(t, err)
		require.NotNil(t, s, "late holder %d", i)
		got = append(got, s)
	}
	s, err := p.TakeLate()
	require.NoError(t, err)
	assert.Nil(t, s)
	for _, s := range got {
		s.Release(nil)
	}
}

// A slot held beyond the pool uses up a free one: the people behind a late packaging wait for it, as for any holder.
func TestTryTake_AnOverSlotTakesAPlaceFromTheFreeOnes(t *testing.T) {
	p := lateTestPool(t, 2)
	take := func() *Slot {
		tk, err := p.Enqueue()
		require.NoError(t, err)
		defer tk.Drop()
		s, _, err := p.TryTake(tk)
		require.NoError(t, err)
		return s
	}
	a, b := take(), take()
	require.NotNil(t, a)
	require.NotNil(t, b)
	late, err := p.TakeLate()
	require.NoError(t, err)
	require.True(t, late.Over(p.Size))

	a.Release(nil)
	assert.Nil(t, take(), "one real slot is free, and the late holder has the place of it: the pool is full")
	b.Release(nil)
	again := take()
	require.NotNil(t, again, "with both real slots free and one late holder, there is room for one")
	assert.Nil(t, take(), "and not for two")
	again.Release(nil)
	late.Release(nil)
	assert.NotNil(t, take(), "when the late holder ends the pool is whole again")
}

// WouldWait counts a late holder like any other.
func TestWouldWait_CountsAnOverSlot(t *testing.T) {
	g, _ := testGate(t)
	g.Holders = nil
	g.SetConfig(func(c *Config) { c.Slots.Light = 1 })
	pool := g.pool(g.Config(), ClassLight)
	s, err := pool.TakeLate()
	require.NoError(t, err)
	require.NotNil(t, s)
	require.NoError(t, s.SetHolder(Holder{PID: os.Getpid(), Started: time.Now(), Late: true}))
	w := g.WouldWait(ClassLight)
	assert.True(t, w.Wait, "the only light slot is a late holder's")
	s.Release(nil)
	assert.False(t, g.WouldWait(ClassLight).Wait)
}

// What names a packaging script: the five bases, each with a colon suffix, and electron:build; not an unknown script, and not
// a word that merely contains one.
func TestIsPackagingScript(t *testing.T) {
	for _, n := range []string{"build", "package", "pack", "dist", "bundle", "electron:build", "build:mac", "package:win:dev", "dist:linux", "bundle:prod", "pack:all"} {
		assert.True(t, isPackagingScript(n), n)
	}
	for _, n := range []string{"", "dev", "start", "serve", "storybook", "test", "check", "lint", "builder", "rebuild", "unpack", "electron", "electron:dev", "dev:build", "test:build", ":build", "build-all", "builds"} {
		assert.False(t, isPackagingScript(n), n)
	}
}

func TestClassify_PackagingNamesAreNoneWithTheirReason(t *testing.T) {
	for _, cmd := range []string{"pnpm run package:win:dev", "pnpm run build:mac", "npm run dist", "yarn run bundle", "pnpm package:win", "pnpm -r run build", "pnpm run electron:build"} {
		c, why := ClassifyReason(words(cmd), Env{})
		assert.Equal(t, ClassNone, c, cmd)
		assert.Equal(t, ReasonPackaging, why, cmd)
	}
	for _, cmd := range []string{"pnpm run dev", "npm run start", "pnpm run storybook", "yarn run serve", "pnpm run e2e"} {
		c, why := ClassifyReason(words(cmd), Env{})
		assert.Equal(t, ClassNone, c, cmd)
		assert.Equal(t, ReasonUnknown, why, cmd+": an unknown script is only timed, never put under a slot")
	}
}

func TestPackagingConfig(t *testing.T) {
	d := Defaults()
	assert.Equal(t, 60*time.Second, d.Packaging.LateSlotAfter)
	assert.Equal(t, ClassLight, d.Packaging.LateSlotClass)
	assert.Equal(t, 10*time.Second, d.Packaging.RecordUnknown)
	assert.Equal(t, "default", string(d.Sources["packaging.late_slot_after_s"]))

	c := FromTable(map[string]any{"packaging": map[string]any{"late_slot_after_s": int64(0), "late_slot_class": "heavy", "record_unknown_s": int64(30)}})
	assert.Equal(t, time.Duration(0), c.Packaging.LateSlotAfter, "0 turns it off")
	assert.Equal(t, ClassHeavy, c.Packaging.LateSlotClass)
	assert.Equal(t, 30*time.Second, c.Packaging.RecordUnknown)
	assert.Empty(t, c.Warnings)

	bad := FromTable(map[string]any{"packaging": map[string]any{"late_slot_after_s": int64(-5), "late_slot_class": "none"}})
	assert.Equal(t, 60*time.Second, bad.Packaging.LateSlotAfter, "out of range keeps the default")
	assert.Equal(t, ClassLight, bad.Packaging.LateSlotClass)
	assert.Len(t, bad.Warnings, 2)
}
