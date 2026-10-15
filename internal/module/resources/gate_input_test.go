package resourcesmod

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

// The slot records of a real gate directory: one heavy slot held, one command waiting, a light slot held.
func TestSlotCounts_ReadsTheGateDirectory(t *testing.T) {
	cfg := gate.Defaults()
	cfg.Dir = t.TempDir()
	m := newTestModule(idleSampler(), nil)
	m.gateConfig = func() gate.Config { return cfg }

	heavy, light, waiting, ok := m.slotCounts()
	require.True(t, ok)
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{heavy, light, waiting}, "an empty directory")

	hp := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassHeavy, Size: 1, Now: time.Now}
	lp := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassLight, Size: 3, Now: time.Now}
	take := func(p *gate.Pool) *gate.Slot {
		tk, err := p.Enqueue()
		require.NoError(t, err)
		s, _, err := p.TryTake(tk)
		require.NoError(t, err)
		require.NotNil(t, s)
		tk.Drop()
		return s
	}
	hs, ls1, ls2 := take(hp), take(lp), take(lp)
	defer hs.Release(nil)
	defer ls1.Release(nil)
	defer ls2.Release(nil)
	// a ticket whose gate is gone is not a waiter, and reading the counts does not remove it (the daemon only observes)
	dead := filepath.Join(cfg.Dir, "queue", "heavy", "00000000000000000001-999999-000001")
	require.NoError(t, os.WriteFile(dead, nil, 0o644))
	w, err := hp.EnqueueFor(gate.WaiterInfo{PID: os.Getpid(), Argv: []string{"go", "test"}})
	require.NoError(t, err)
	defer w.Drop()

	heavy, light, waiting, ok = m.slotCounts()
	require.True(t, ok)
	assert.Equal(t, [3]int{1, 2, 1}, [3]int{heavy, light, waiting})
	_, err = os.Stat(dead)
	assert.NoError(t, err, "the dead ticket is still there")
}

// A module not wired to the host reads nothing (and never the home directory it runs in).
func TestSlotCounts_UnwiredModuleReadsNothing(t *testing.T) {
	m := newTestModule(idleSampler(), nil)
	_, _, _, ok := m.slotCounts()
	assert.False(t, ok)
	assert.Equal(t, gate.Defaults().Slots, m.gateCfg().Slots)
}
