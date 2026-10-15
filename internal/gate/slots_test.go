package gate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPool(t *testing.T, class Class, size int) *Pool {
	t.Helper()
	return &Pool{Dir: t.TempDir(), Class: class, Size: size, Now: time.Now}
}

// two tickets with a chosen order, whatever the clock says
func ticketAt(t *testing.T, p *Pool, ns int64) *Ticket {
	t.Helper()
	p2 := *p
	p2.Now = func() time.Time { return time.Unix(0, ns) }
	tk, err := p2.Enqueue()
	require.NoError(t, err)
	return tk
}

func TestPool_TakesUpToSizeThenWaits(t *testing.T) {
	p := testPool(t, ClassHeavy, 2)
	var slots []*Slot
	for i := 0; i < 2; i++ {
		tk := ticketAt(t, p, int64(100+i))
		s, ahead, err := p.TryTake(tk)
		require.NoError(t, err)
		require.NotNil(t, s, "slot %d", i+1)
		assert.Zero(t, ahead)
		tk.Drop()
		slots = append(slots, s)
	}
	assert.Equal(t, 1, slots[0].K)
	assert.Equal(t, 2, slots[1].K)
	tk := ticketAt(t, p, 200)
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	assert.Nil(t, s, "both slots are held")
	slots[0].Release()
	s, _, err = p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s, "a released slot is free again")
	assert.Equal(t, 1, s.K)
}

// The head of the queue gets the freed slot even when a later arrival looks first (nq order).
// (M: take a free slot without looking at the tickets before it.)
func TestPool_TheOlderTicketGetsTheFreedSlot(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	holder, err := p.Enqueue()
	require.NoError(t, err)
	hs, _, err := p.TryTake(holder)
	require.NoError(t, err)
	require.NotNil(t, hs)
	holder.Drop()

	first := ticketAt(t, p, 1000)
	second := ticketAt(t, p, 2000)
	hs.Release()

	s, ahead, err := p.TryTake(second)
	require.NoError(t, err)
	assert.Nil(t, s, "the younger one finds a free slot and still waits for the one before it")
	assert.Equal(t, 1, ahead)

	s, ahead, err = p.TryTake(first)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Zero(t, ahead)
	first.Drop()

	// with the head gone and the slot taken, the second still waits
	s2, _, err := p.TryTake(second)
	require.NoError(t, err)
	assert.Nil(t, s2)
	s.Release()
	s2, _, err = p.TryTake(second)
	require.NoError(t, err)
	assert.NotNil(t, s2)
}

// Two free slots, two older waiters: the third arrival waits; one older waiter gone, it may go.
func TestPool_FreeSlotsAreCountedAgainstTheTicketsBefore(t *testing.T) {
	p := testPool(t, ClassHeavy, 2)
	a := ticketAt(t, p, 10)
	b := ticketAt(t, p, 20)
	c := ticketAt(t, p, 30)
	s, ahead, err := p.TryTake(c)
	require.NoError(t, err)
	assert.Nil(t, s, "2 tickets before it, 2 free slots: not yet")
	assert.Equal(t, 2, ahead)
	a.Drop()
	s, _, err = p.TryTake(c)
	require.NoError(t, err)
	assert.NotNil(t, s, "1 ticket before it, 2 free slots")
	_ = b
}

// A waiter that died leaves its ticket; its lock is free, so it is not counted and is removed.
// (M: count every ticket file as a waiter.)
func TestPool_ADeadWaitersTicketIsNotAWaiter(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	require.NoError(t, os.MkdirAll(p.queueDir(), 0o755))
	dead := filepath.Join(p.queueDir(), "00000000000000000001-99999")
	require.NoError(t, os.WriteFile(dead, nil, 0o644)) // nobody holds a lock on it
	tk := ticketAt(t, p, 500)
	s, ahead, err := p.TryTake(tk)
	require.NoError(t, err)
	assert.NotNil(t, s)
	assert.Zero(t, ahead)
	_, err = os.Stat(dead)
	assert.True(t, os.IsNotExist(err), "the stale ticket is removed")
}

func TestPool_ALiveTicketBeforeCounts(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	live := ticketAt(t, p, 1)
	tk := ticketAt(t, p, 2)
	s, ahead, err := p.TryTake(tk)
	require.NoError(t, err)
	assert.Nil(t, s)
	assert.Equal(t, 1, ahead)
	assert.Equal(t, 2, p.Waiting())
	live.Drop()
	assert.Equal(t, 1, p.Waiting())
}

func TestPool_HoldersAreTheLockedSlotsOnly(t *testing.T) {
	p := testPool(t, ClassHeavy, 2)
	tk := ticketAt(t, p, 1)
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s)
	tk.Drop()
	require.NoError(t, s.SetHolder(Holder{PID: 42, PGID: 43, Argv: []string{"go", "test", "./..."}, Cwd: "/work", Started: time.Unix(1700000000, 0)}))

	// a record left beside a free lock (its gate was killed) is stale
	stale := Holder{Class: ClassHeavy, Slot: 2, PID: 7}
	require.NoError(t, os.WriteFile(p.recordPath(2), mustJSON(t, stale), 0o644))

	held := p.Held()
	require.Len(t, held, 1)
	assert.Equal(t, 1, held[0].Slot)
	assert.Equal(t, 42, held[0].PID)
	assert.Equal(t, []string{"go", "test", "./..."}, held[0].Argv)
	assert.Equal(t, ClassHeavy, held[0].Class)

	s.Release()
	assert.Empty(t, p.Held())
	_, err = os.Stat(p.recordPath(1))
	assert.True(t, os.IsNotExist(err), "the record goes with the slot")
}

// The kernel drops a lock when its holder is gone, with no help: close the file without releasing.
func TestPool_ALockLostWithItsHolderIsFree(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	tk := ticketAt(t, p, 1)
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s)
	tk.Drop()
	_ = s.f.Close() // what dying does to the lock
	tk2 := ticketAt(t, p, 2)
	s2, _, err := p.TryTake(tk2)
	require.NoError(t, err)
	assert.NotNil(t, s2)
}

func TestPool_TheTwoClassesAreSeparate(t *testing.T) {
	dir := t.TempDir()
	heavy := &Pool{Dir: dir, Class: ClassHeavy, Size: 1, Now: time.Now}
	light := &Pool{Dir: dir, Class: ClassLight, Size: 1, Now: time.Now}
	hs, _, err := heavy.TryTake(ticketAt(t, heavy, 1))
	require.NoError(t, err)
	require.NotNil(t, hs)
	ls, _, err := light.TryTake(ticketAt(t, light, 1))
	require.NoError(t, err)
	assert.NotNil(t, ls, "a full heavy pool does not hold a light job")
}

// A file where the directory should be is a damaged install (broken, the command is not run); a directory the process
// may not write is a sandbox (unavailable, the command runs without a slot).
func TestPool_AFileInPlaceOfTheDirectoryIsBroken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	p := &Pool{Dir: filepath.Join(file, "gate"), Class: ClassHeavy, Size: 1, Now: time.Now}
	_, err := p.Enqueue()
	assert.ErrorIs(t, err, ErrBroken)
	assert.NotErrorIs(t, err, ErrUnavailable)
}

func TestPool_ADirectoryTheProcessMayNotWriteIsUnavailable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may write anywhere")
	}
	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0o555))
	defer os.Chmod(parent, 0o755)
	p := &Pool{Dir: filepath.Join(parent, "gate"), Class: ClassHeavy, Size: 1, Now: time.Now}
	_, err := p.Enqueue()
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestSlot_Label(t *testing.T) {
	assert.Equal(t, "heavy-2", (&Slot{Class: ClassHeavy, K: 2}).Label())
	assert.Equal(t, "light-1", (&Slot{Class: ClassLight, K: 1}).Label())
}

var _ = syscall.LOCK_EX
