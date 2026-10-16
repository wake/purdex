package gate

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pool size is read again for every command. A slot taken under a bigger size, or a late one numbered above the size of that
// time, is still seen after the size is lowered: it counts against the capacity, is listed, and does not let more late holders in
// than there is room for (R1 of #3011).
func TestPool_ASlotAboveALoweredSizeIsStillSeen(t *testing.T) {
	big := lateTestPool(t, 4)
	var slots []*Slot
	for i := 0; i < 4; i++ {
		tk, err := big.Enqueue()
		require.NoError(t, err)
		s, _, err := big.TryTake(tk)
		require.NoError(t, err)
		tk.Drop()
		require.NotNil(t, s)
		slots = append(slots, s)
	}
	require.NoError(t, slots[3].SetHolder(Holder{PID: os.Getpid(), Argv: []string{"go", "test", "./..."}}))
	for _, s := range slots[:3] {
		s.Release(nil)
	}

	small := &Pool{Dir: big.Dir, Class: ClassLight, Size: 1, Now: time.Now}
	held := small.Held()
	require.Len(t, held, 1, "slot 4 is held, whatever the size is now")
	assert.Equal(t, 4, held[0].Slot)
	assert.True(t, held[0].Over, "above the size of now")

	tk, err := small.Enqueue()
	require.NoError(t, err)
	s, _, err := small.TryTake(tk)
	require.NoError(t, err)
	tk.Drop()
	assert.Nil(t, s, "the one slot of the pool is free, and the job above it still runs: the pool is full")

	slots[3].Release(nil)
	tk, err = small.Enqueue()
	require.NoError(t, err)
	s, _, err = small.TryTake(tk)
	require.NoError(t, err)
	tk.Drop()
	assert.NotNil(t, s, "and once it ends, there is room")
	s.Release(nil)
}

// Late holders numbered above a lowered size still count toward the limit on late holders.
func TestTakeLate_TheLimitCountsLateHoldersAboveALoweredSize(t *testing.T) {
	p := lateTestPool(t, 2)
	var got []*Slot
	for i := 0; i < 2+overSlots; i++ { // fills the pool and every over slot
		s, err := p.TakeLate()
		require.NoError(t, err)
		require.NotNil(t, s)
		got = append(got, s)
	}
	small := &Pool{Dir: p.Dir, Class: ClassLight, Size: 1, Now: time.Now}
	s, err := small.TakeLate()
	require.NoError(t, err)
	assert.Nil(t, s, "the slots above the lowered size are still held: no room for another late holder")
	for _, s := range got {
		s.Release(nil)
	}
}
