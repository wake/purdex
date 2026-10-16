package gate

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #3049: a slot that is locked but has no record read as a holder with no pid and no start time, which the watcher turned into a
// slot_long of 292 years. The gate has work to do between taking a slot and writing the holder (the host's facts, the disk floor,
// the settings), and a record that was removed at the take left the slot recordless for all of it.

// A command that waits for the host's facts after it was given its slot: until it has written its holder, the slot already has a
// record that says who took it and when. Reproduction: the Facts hook is where the gate is when the status is read.
func TestHeld_ASlotJustTakenIsNeverWithoutARecord(t *testing.T) {
	g, _ := testGate(t)
	g.Holders = nil
	inFacts, proceed := make(chan struct{}), make(chan struct{})
	g.Facts = func() HostFacts {
		close(inFacts)
		<-proceed
		return HostFacts{NCPU: 10, AvailMemMB: 8000}
	}
	done := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy})
	select {
	case <-inFacts:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate did not get to the host's facts")
	}
	held := g.pool(g.Config(), ClassHeavy).Held()
	require.Len(t, held, 1, "the slot is taken")
	assert.False(t, held[0].NoRecord)
	assert.Equal(t, os.Getpid(), held[0].PID, "it names the gate that took it")
	assert.False(t, held[0].Started.IsZero(), "and when")
	close(proceed)
	assert.Equal(t, 0, (<-done).code)
	assert.Empty(t, g.pool(g.Config(), ClassHeavy).Held())
}

// The same for a late slot, between TakeLate and SetHolder.
func TestHeld_ALateSlotJustTakenIsNeverWithoutARecord(t *testing.T) {
	p := lateTestPool(t, 2)
	s, err := p.TakeLate()
	require.NoError(t, err)
	require.NotNil(t, s)
	held := p.Held()
	require.Len(t, held, 1)
	assert.False(t, held[0].NoRecord)
	assert.Equal(t, os.Getpid(), held[0].PID)
	assert.False(t, held[0].Started.IsZero())
	s.Release(nil)
}

// The placeholder carries no token: a command with an old token cannot pass the nesting check against it (the rule of the removal
// it replaces).
func TestTryTake_ThePlaceholderRecordHasNoToken(t *testing.T) {
	p := lateTestPool(t, 1)
	tk, err := p.Enqueue()
	require.NoError(t, err)
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s)
	tk.Drop()
	h, locked, ok := p.Record(1)
	require.True(t, ok)
	assert.True(t, locked)
	assert.Empty(t, h.Token)
	s.Release(nil)
}

// A record that is not there while the lock is held (what is left after the gate released the slot and a process that inherited its
// lock lives on) is listed, and says so: it has no pid and no start, and is not to be measured.
func TestHeld_ALockedSlotWithNoRecordSaysSo(t *testing.T) {
	p := lateTestPool(t, 1)
	tk, err := p.Enqueue()
	require.NoError(t, err)
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s)
	tk.Drop()
	require.NoError(t, os.Remove(p.recordPath(1)))
	held := p.Held()
	require.Len(t, held, 1)
	assert.True(t, held[0].NoRecord)
	assert.True(t, held[0].Started.IsZero())
	assert.Equal(t, 0, held[0].PID)
	s.Release(nil)
}
