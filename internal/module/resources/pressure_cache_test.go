package resourcesmod

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/hostmem"
)

// Each tick adds the reading it took (level and paging counters) to the gate's cache, stamped with the moment the tick began;
// a reading that lacks the level or the counters adds nothing; a module that was not wired to the host writes nowhere.
func TestSampler_WritesTheGatesPressureCache(t *testing.T) {
	r := newZoneRig(t)
	dir := t.TempDir()
	cfg := gate.Defaults()
	cfg.Dir = dir
	r.m.gateConfig = func() gate.Config { return cfg }
	c := hostmem.Cache{Dir: dir}

	r.tick(t, withPages(zoneRaw(40, 1, 2), 1000))
	got := c.Load().Samples
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Level)
	assert.EqualValues(t, 1000, got[0].Pageouts)
	assert.Equal(t, r.m.now().UnixMilli(), got[0].At, "the clock is the module's own, at the tick")

	r.tick(t, withPages(zoneRaw(40, 1, 4), 1600))
	got = c.Load().Samples
	require.Len(t, got, 2)
	assert.Equal(t, 4, got[1].Level)
	assert.EqualValues(t, 1600, got[1].Pageouts)
	assert.Less(t, got[0].At, got[1].At)

	r.tick(t, zoneRaw(40, 1, 4))                  // no counters
	r.tick(t, withPages(zoneRaw(40, 1, 0), 2000)) // the level is unknown
	assert.Len(t, c.Load().Samples, 2, "neither reading is written")
}

func TestSampler_AnUnwiredModuleWritesNoPressureCache(t *testing.T) {
	r := newZoneRig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	r.tick(t, withPages(zoneRaw(40, 1, 2), 1000))
	entries, _ := os.ReadDir(home)
	assert.Empty(t, entries, "a test's module never writes the home it runs in")
}
