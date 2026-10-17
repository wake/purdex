package gate

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- GC.0 (#3058): the take publishes the command ----
//
// A slot's record carries the argv of the command that took it from the moment the take returns: the next gate that gets the admit
// lock must never see a held slot with an empty record. The argv is the one the gate queued with and keeps in memory, not what it
// reads back from the ticket file (whose write may fail).

func takeInfo(name string) WaiterInfo {
	return WaiterInfo{PID: os.Getpid(), Argv: []string{"tsc", "-b", name}, Cwd: "/work/" + name, Session: "sess-" + name}
}

// Right after TryTake returns, Held() shows the argv, cwd and session. Mutation gate: the placeholder without the info → red.
func TestTake_TheSlotRecordCarriesTheCommandAtOnce(t *testing.T) {
	rp := newRankPool(t, 2)
	tk, err := rp.EnqueueFor(takeInfo("a"))
	require.NoError(t, err)
	defer tk.Drop()
	got, err := rp.TryTakeRanked(tk)
	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	defer got.Slot.Release(nil)
	held := rp.Held()
	require.Len(t, held, 1)
	assert.Equal(t, []string{"tsc", "-b", "a"}, held[0].Argv)
	assert.Equal(t, "/work/a", held[0].Cwd)
	assert.Equal(t, "sess-a", held[0].Session)
	assert.False(t, held[0].Late)
}

// The take uses the ticket's in-memory info: a ticket file that was left empty or damaged does not matter.
// Mutation gate: the take reads the ticket file back → empty argv (red).
func TestTake_UsesTheTicketsInMemoryInfoNotTheFile(t *testing.T) {
	rp := newRankPool(t, 1)
	tk, err := rp.EnqueueFor(takeInfo("b"))
	require.NoError(t, err)
	defer tk.Drop()
	require.NoError(t, os.WriteFile(filepath.Join(rp.queueDir(), tk.Name), []byte("{broken"), 0o644))
	got, err := rp.TryTakeRanked(tk)
	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	defer got.Slot.Release(nil)
	assert.Equal(t, []string{"tsc", "-b", "b"}, rp.Held()[0].Argv)
}

// A late claim writes its command the same way, and its record says it is late.
func TestTakeLate_TheSlotRecordCarriesTheCommandAtOnce(t *testing.T) {
	rp := newRankPool(t, 1)
	s, err := rp.TakeLate(takeInfo("c"))
	require.NoError(t, err)
	require.NotNil(t, s)
	defer s.Release(nil)
	held := rp.Held()
	require.Len(t, held, 1)
	assert.Equal(t, []string{"tsc", "-b", "c"}, held[0].Argv)
	assert.True(t, held[0].Late)
}

// A placeholder that cannot be written fails the take and lets the lock go: no slot is handed out without its argv on disk.
func TestTake_APlaceholderThatCannotBeWrittenGivesNoSlot(t *testing.T) {
	rp := newRankPool(t, 1)
	tk, err := rp.EnqueueFor(takeInfo("d"))
	require.NoError(t, err)
	defer tk.Drop()
	require.NoError(t, os.Mkdir(rp.recordPath(1), 0o755)) // a directory where the record file goes: the rename onto it fails
	got, err := rp.TryTakeRanked(tk)
	assert.Error(t, err)
	assert.Nil(t, got.Slot)
	require.NoError(t, os.Remove(rp.recordPath(1)))
	free, err := rp.TryTakeRanked(tk)
	require.NoError(t, err)
	require.NotNil(t, free.Slot, "the lock was let go: the slot can be taken once the record can be written")
	free.Slot.Release(nil)
}

// A reader polling Held() while takes happen never meets a locked slot with an empty record. Run under -race.
func TestTake_NoWindowWithAnEmptyRecordWhileTakesRace(t *testing.T) {
	rp := newRankPool(t, 4)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, h := range rp.Held() {
				if len(h.Argv) == 0 && !h.NoRecord {
					t.Errorf("a held slot with no argv: %+v", h)
					return
				}
			}
		}
	}()
	for i := 0; i < 40; i++ {
		tk, err := rp.EnqueueFor(takeInfo("x"))
		require.NoError(t, err)
		got, err := rp.TryTakeRanked(tk)
		require.NoError(t, err)
		tk.Drop()
		if got.Slot != nil {
			time.Sleep(time.Millisecond)
			got.Slot.Release(nil)
		}
	}
	close(stop)
	wg.Wait()
}
