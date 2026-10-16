package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type peekPressure struct {
	fakePressure
	peek Pressure
	have bool
}

func (p *peekPressure) Peek() (Pressure, bool) { return p.peek, p.have }

func TestWouldWait_FreeSlotMeansNoWait(t *testing.T) {
	g, _ := testGate(t)
	w := g.WouldWait(ClassHeavy)
	assert.False(t, w.Wait)
	assert.Empty(t, w.Note)
	assert.Equal(t, 0, w.Ahead)
}

func TestWouldWait_AllSlotsTakenSaysWhoAndHowLong(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	hold, release := holdSlot(t, g, ClassHeavy, "a")
	eventually(t, "a held slot", func() bool { return len(g.Status().Heavy) == 1 })
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "slots", w.Reason)
	assert.Equal(t, 1, w.Ahead)
	assert.Contains(t, w.HeldBy, "started-a")
	assert.Contains(t, w.Note, "waiting for a heavy slot")
	assert.Contains(t, w.Note, "do not interrupt it")
	// the other class is not affected
	assert.False(t, g.WouldWait(ClassLight).Wait)
	release()
	<-hold
	assert.False(t, g.WouldWait(ClassHeavy).Wait)
}

// A slot is free but someone queued is entitled to it: the caller would wait behind them.
func TestWouldWait_AQueueInFrontOfAFreeSlotIsAWait(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	p := g.pool(g.Config(), ClassHeavy)
	tk, err := p.Enqueue()
	require.NoError(t, err)
	defer tk.Drop()
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "queue", w.Reason)
	assert.Equal(t, 1, w.Ahead)
}

func TestWouldWait_PressureOverTheLimitIsAWaitAndNotesTheNumbers(t *testing.T) {
	g, _ := testGate(t)
	pp := &peekPressure{peek: Pressure{Level: 2, PageRate: 10}, have: true}
	g.Pressure = pp
	w := g.WouldWait(ClassHeavy)
	assert.True(t, w.Wait)
	assert.Equal(t, "pressure", w.Reason)
	assert.Contains(t, w.Note, "memory pressure (level 2")
	pp.peek = Pressure{Level: 1}
	assert.False(t, g.WouldWait(ClassHeavy).Wait)
	pp.have = false
	pp.peek = Pressure{Level: 4}
	assert.False(t, g.WouldWait(ClassHeavy).Wait, "no reading is no reason to wait")
	g.SetConfig(func(c *Config) { c.Admission.Enabled = false })
	pp.have, pp.peek = true, Pressure{Level: 4}
	assert.False(t, g.WouldWait(ClassHeavy).Wait, "admission off")
}

func TestWouldWait_OffOrUnknownClassNeverWaits(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	hold, release := holdSlot(t, g, ClassHeavy, "a")
	eventually(t, "a held slot", func() bool { return len(g.Status().Heavy) == 1 })
	require.True(t, g.WouldWait(ClassHeavy).Wait)
	g.SetConfig(func(c *Config) { c.Enabled = false })
	assert.False(t, g.WouldWait(ClassHeavy).Wait, "the gate is off: nothing waits")
	g.SetConfig(func(c *Config) { c.Enabled = true })
	assert.False(t, g.WouldWait(Class("none")).Wait, "a class the gate does not hold never waits")
	release()
	<-hold
}

func TestWouldWait_TakesNothingAndWritesNothing(t *testing.T) {
	g, _ := testGate(t)
	g.WouldWait(ClassHeavy)
	ents, _ := os.ReadDir(g.Config().Dir)
	for _, e := range ents {
		assert.NotContains(t, e.Name(), ".lock", "no slot lock is created or kept: %s", e.Name())
	}
	assert.Empty(t, g.Status().Heavy)
}

func TestHostReader_PeekNeverSamplesAndNeverWrites(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	r := &HostReader{Dir: dir, Now: func() time.Time { return now }, Level: func() (int, error) { return 2, nil },
		Pages: func() (uint64, uint64, error) { t.Fatal("Peek must not run vm_stat"); return 0, 0, nil }}
	p, have := r.Peek()
	assert.True(t, have, "the level alone is a reading")
	assert.Equal(t, Pressure{Level: 2}, p)

	samples := sampleFile{Samples: []cachedSample{
		{At: now.Add(-18 * time.Second).UnixMilli(), Level: 1, Pageouts: 100},
		{At: now.Add(-12 * time.Second).UnixMilli(), Level: 1, Pageouts: 700},
	}}
	b, _ := json.Marshal(samples)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pressure.json"), b, 0o644))
	before, _ := os.ReadFile(filepath.Join(dir, "pressure.json"))
	p, have = r.Peek()
	assert.True(t, have)
	assert.Equal(t, 2, p.Level, "the level is the kernel's now")
	assert.InDelta(t, 100, p.PageRate, 0.01, "the rate comes from the cached samples")
	after, _ := os.ReadFile(filepath.Join(dir, "pressure.json"))
	assert.Equal(t, before, after)

	r.Now = func() time.Time { return now.Add(10 * time.Minute) } // the cache is stale: no rate from it
	p, _ = r.Peek()
	assert.Equal(t, Pressure{Level: 2}, p)
	r.Level = func() (int, error) { return 0, os.ErrInvalid }
	_, have = r.Peek()
	assert.False(t, have)
}
