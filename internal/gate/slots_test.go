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

// Only EWOULDBLOCK means a lock is held; a lock that cannot be probed is broken, not busy, or a command would wait forever
// on a slot nobody holds. (M: read every flock error as held.)
func TestFlockNB_HeldIsNotBroken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l")
	a, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	b, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	st, err := flockNB(a)
	assert.Equal(t, lockGot, st)
	assert.NoError(t, err)
	st, err = flockNB(b)
	assert.Equal(t, lockHeld, st, "another descriptor holds it")
	assert.NoError(t, err)
	require.NoError(t, a.Close())
	st, _ = flockNB(b)
	assert.Equal(t, lockGot, st, "released when the holder closes")
	require.NoError(t, b.Close())
	st, err = flockNB(b)
	assert.Equal(t, lockBroken, st, "a closed descriptor cannot be locked: broken, not busy")
	assert.Error(t, err)
}

func TestPool_ABrokenSlotLockIsBrokenNotBusy(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	tk := ticketAt(t, p, 1)
	old := lockProbe
	lockProbe = func(*os.File) (lockState, error) { return lockBroken, syscall.ENOLCK }
	defer func() { lockProbe = old }()
	s, _, err := p.TryTake(tk)
	assert.Nil(t, s)
	assert.ErrorIs(t, err, ErrBroken, "the caller fails closed instead of queueing for ever")
}

// Two tickets made in the same nanosecond by one process are two tickets: the second must not open, lock-fail and remove
// the first one's file. (M: name a ticket by time and pid alone, without O_EXCL.)
func TestPool_TicketsOfOneProcessInOneNanosecondAreDistinct(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	p.Now = func() time.Time { return time.Unix(0, 12345) }
	a, err := p.Enqueue()
	require.NoError(t, err)
	b, err := p.Enqueue()
	require.NoError(t, err)
	assert.NotEqual(t, a.Name, b.Name)
	assert.Less(t, a.Name, b.Name, "made later, ordered later")
	assert.Equal(t, 2, p.Waiting(), "both are live")
	_, err = os.Stat(a.path)
	assert.NoError(t, err, "the first ticket's file is still there")
	s, ahead, err := p.TryTake(b)
	require.NoError(t, err)
	assert.Nil(t, s, "the second waits behind the first")
	assert.Equal(t, 1, ahead)
}

// A queue that cannot be read is not an empty queue: the oldest waiter must not be passed. (M: an unreadable queue counts
// as nobody waiting.)
func TestPool_AQueueThatCannotBeReadFailsClosed(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	tk := ticketAt(t, p, 1)
	defer tk.Drop()
	require.NoError(t, os.RemoveAll(p.queueDir()))
	require.NoError(t, os.WriteFile(p.queueDir(), nil, 0o644)) // a file where the queue directory was: ENOTDIR
	s, _, err := p.TryTake(tk)
	assert.Nil(t, s)
	assert.ErrorIs(t, err, ErrBroken)

	// a missing queue is an empty one
	require.NoError(t, os.Remove(p.queueDir()))
	s, _, err = p.TryTake(tk)
	require.NoError(t, err)
	assert.NotNil(t, s)
}

// A ticket that can be seen in the queue is already locked: a scan by another gate between "created" and "locked" must not
// find a free lock and remove it. (M: make the ticket under its real name.)
func TestPool_ATicketIsNotSeenBeforeItIsLocked(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	scanned := false
	p.afterCreate = func() {
		scanned = true
		// another gate, younger than the new ticket, takes the count of the tickets before it
		other := &Ticket{Name: "99999999999999999999-1-000000"}
		before, err := p.liveBefore(other)
		require.NoError(t, err)
		assert.Empty(t, before, "the ticket is not in the queue yet")
	}
	tk, err := p.Enqueue()
	require.NoError(t, err)
	require.True(t, scanned)
	_, err = os.Stat(tk.path)
	assert.NoError(t, err, "the ticket was not removed by the scan")
	assert.Equal(t, 1, p.Waiting(), "and it is in the queue now, locked")

	p.afterCreate = nil
	later := ticketAt(t, p, 1<<62)
	s, ahead, err := p.TryTake(later)
	require.NoError(t, err)
	assert.Nil(t, s, "a later arrival is not ahead of it")
	assert.Equal(t, 1, ahead)
}

// A failed Enqueue leaves nothing behind in the queue directory.
func TestPool_AFailedEnqueueLeavesNoFile(t *testing.T) {
	p := testPool(t, ClassHeavy, 1)
	p.afterCreate = func() {
		ents, _ := os.ReadDir(p.queueDir())
		for _, e := range ents {
			require.Contains(t, e.Name(), ".new", "the ticket is under a name the scans skip")
		}
	}
	tk, err := p.Enqueue()
	require.NoError(t, err)
	tk.Drop()
	ents, _ := os.ReadDir(p.queueDir())
	assert.Empty(t, ents)
}
