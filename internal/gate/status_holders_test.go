package gate

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lock-holder lookup is an lsof over the whole host (about 0.3 s of CPU); the light status asks it only for a slot whose gate
// is gone (#2933).
func TestStatusWith_OrphanedAsksOnlyForSlotsWhoseGateIsGone(t *testing.T) {
	g, _ := testGate(t)
	var asked atomic.Int32
	g.Holders = func(string) []Straggler { asked.Add(1); return []Straggler{{PID: 5, Cmd: "x"}} }
	hold := runAsync(g, context.Background(), Options{Argv: sh("sleep 2"), Class: ClassHeavy})
	eventually(t, "a held slot", func() bool { return len(g.StatusWith(HoldersOrphaned).Heavy) == 1 })

	asked.Store(0)
	st := g.StatusWith(HoldersOrphaned)
	time.Sleep(100 * time.Millisecond)
	require.Len(t, st.Heavy, 1)
	assert.Zero(t, asked.Load(), "a live gate's slot is not looked up")
	assert.Empty(t, st.Heavy[0].Locked)
	assert.NotZero(t, st.Heavy[0].PID, "the slot record is still there")

	st = g.Status()
	assert.NotZero(t, asked.Load())
	assert.NotEmpty(t, st.Heavy[0].Locked, "the full status looks every slot up")
	<-hold
}

func TestFillLocked_OrphanedModeLooksUpOnlyTheOrphans(t *testing.T) {
	g, _ := testGate(t)
	cfg := g.Config()
	hp, lp := g.pool(cfg, ClassHeavy), g.pool(cfg, ClassLight)
	var asked atomic.Int32
	g.Holders = func(string) []Straggler { asked.Add(1); return []Straggler{{PID: 9}} }
	heavy := []Holder{{Slot: 1, Orphaned: true}, {Slot: 2}}
	light := []Holder{{Slot: 1}, {Slot: 2, Orphaned: true}}
	g.fillLocked(hp, heavy, lp, light, HoldersOrphaned)
	time.Sleep(100 * time.Millisecond) // a lookup that should not have been made would have started by now
	assert.EqualValues(t, 2, asked.Load())
	assert.NotEmpty(t, heavy[0].Locked)
	assert.Empty(t, heavy[1].Locked)
	assert.Empty(t, light[0].Locked)
	assert.NotEmpty(t, light[1].Locked)
	// nothing orphaned: no lookup and no wait
	asked.Store(0)
	g.fillLocked(hp, []Holder{{Slot: 1}}, lp, nil, HoldersOrphaned)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, asked.Load())
}
