package gate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- GQ: queue order, front / back / hold (#3034, spec §5.2) ----

// rankPool is a pool of the given size whose clock the test sets: tickets made one millisecond apart, ranks stamped at the clock.
type rankPool struct {
	*Pool
	now atomic.Int64 // unix ns
}

func newRankPool(t *testing.T, size int) *rankPool {
	t.Helper()
	rp := &rankPool{}
	rp.now.Store(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC).UnixNano())
	rp.Pool = &Pool{Dir: t.TempDir(), Class: ClassHeavy, Size: size, Now: func() time.Time { return time.Unix(0, rp.now.Load()) }}
	return rp
}

func (rp *rankPool) advance(d time.Duration) { rp.now.Add(int64(d)) }

// enqueue makes n tickets, a millisecond apart, and returns them in arrival order.
func (rp *rankPool) enqueue(t *testing.T, n int) []*Ticket {
	t.Helper()
	var out []*Ticket
	for i := 0; i < n; i++ {
		rp.advance(time.Millisecond)
		tk, err := rp.EnqueueFor(WaiterInfo{PID: 1000 + i, Argv: []string{"cmd"}})
		require.NoError(t, err)
		t.Cleanup(tk.Drop)
		out = append(out, tk)
	}
	return out
}

func (rp *rankPool) order(t *testing.T) []string {
	t.Helper()
	snap, err := rp.Snapshot()
	require.NoError(t, err)
	var names []string
	for _, e := range snap.Queue {
		names = append(names, e.Name)
	}
	return names
}

func nameOf(ts []*Ticket, idx ...int) []string {
	var out []string
	for _, i := range idx {
		out = append(out, ts[i].Name)
	}
	return out
}

func (rp *rankPool) set(t *testing.T, tk *Ticket, r Rank, until time.Time) {
	t.Helper()
	_, err := rp.SetRank(tk.Name, r, "sess-1", "because", until)
	require.NoError(t, err)
}

// front before normal before back; fronts in the order they were moved there; a tie by ticket name; a hold last.
// Mutation gates: the group order changed → red; fronts by arrival instead of by the time moved → red; no tie-break by name → red.
func TestRank_ServingOrder(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 6) // 0..5 in arrival order
	rp.set(t, ts[4], RankFront, time.Time{})
	rp.advance(time.Second)
	rp.set(t, ts[3], RankFront, time.Time{}) // moved later: behind ts[4], though it arrived before
	rp.set(t, ts[1], RankBack, time.Time{})
	rp.set(t, ts[5], RankHold, time.Time{})
	old, err := rp.SetRank(ts[1].Name, RankBack, "sess-2", "again", time.Time{})
	require.NoError(t, err)
	assert.Equal(t, RankBack, old.Rank, "SetRank answers the rank the ticket had")
	assert.Equal(t, "sess-1", old.By)
	assert.Equal(t, nameOf(ts, 4, 3, 0, 2, 1, 5), rp.order(t), "front by the time moved, normal, back, the hold last")

	// two fronts moved at the same instant go by ticket name
	rp2 := newRankPool(t, 1)
	us := rp2.enqueue(t, 3)
	rp2.set(t, us[2], RankFront, time.Time{})
	rp2.set(t, us[1], RankFront, time.Time{}) // the same instant
	assert.Equal(t, nameOf(us, 1, 2, 0), rp2.order(t))
}

// A hold is not served and does not count as ahead of anybody; the others pass it.
// Mutation gates: a hold counted ahead → red; a hold served when a slot is free → red.
func TestRank_AHoldIsPassedAndNotServed(t *testing.T) {
	rp := newRankPool(t, 2)
	ts := rp.enqueue(t, 3)
	rp.set(t, ts[1], RankHold, time.Time{})

	tk, err := rp.TryTakeRanked(ts[1])
	require.NoError(t, err)
	assert.True(t, tk.Held)
	assert.Nil(t, tk.Slot, "a held ticket takes no slot though two are free")
	assert.Equal(t, 0, tk.Ahead, "nothing is served before a held ticket: it is not in the line")
	assert.Equal(t, RankHold, tk.Rank.Rank)
	assert.Equal(t, "sess-1", tk.Rank.By)
	assert.Equal(t, "because", tk.Rank.Reason)

	a, err := rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	require.NotNil(t, a.Slot)
	defer a.Slot.Release(nil)
	ts[0].Drop()
	c, err := rp.TryTakeRanked(ts[2])
	require.NoError(t, err)
	assert.Equal(t, 0, c.Ahead, "the hold ticket is not ahead of the one behind it")
	require.NotNil(t, c.Slot, "the second slot goes to the one behind the hold")
	c.Slot.Release(nil)
}

// A hold with an until that has passed reads as normal, in its arrival place; setting it back to normal (ClearRank) does too.
// Mutation gate: an expired hold stays held → red.
func TestRank_ExpiredAndClearedHoldsAreNormal(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 2)
	rp.set(t, ts[0], RankHold, time.Unix(0, rp.now.Load()).Add(time.Minute))
	assert.Equal(t, nameOf(ts, 1, 0), rp.order(t))
	rp.advance(2 * time.Minute)
	assert.Equal(t, nameOf(ts, 0, 1), rp.order(t), "after until the ticket is normal again, in its arrival place")

	rp.set(t, ts[1], RankHold, time.Time{})
	old, err := rp.ClearRank(ts[1].Name)
	require.NoError(t, err)
	assert.Equal(t, RankHold, old.Rank)
	assert.Equal(t, nameOf(ts, 0, 1), rp.order(t))
}

// A rank file that cannot be read or makes no sense reads as normal and says why; it never strands a waiter.
func TestRank_BrokenFilesReadAsNormalWithAnError(t *testing.T) {
	for name, content := range map[string]string{
		"not json":     "{nope",
		"unknown rank": `{"rank":"sideways","at":1}`,
		"empty":        "",
		"no time":      `{"rank":"front"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rp := newRankPool(t, 1)
			ts := rp.enqueue(t, 2)
			require.NoError(t, os.WriteFile(rp.rankPath(ts[1].Name), []byte(content), 0o644))
			snap, err := rp.Snapshot()
			require.NoError(t, err)
			require.Len(t, snap.Queue, 2)
			assert.Equal(t, ts[1].Name, snap.Queue[1].Name, "arrival order")
			assert.Equal(t, RankNormal, snap.Queue[1].Rank.Rank)
			assert.NotEmpty(t, snap.Queue[1].Rank.Error)
			tk, err := rp.TryTakeRanked(ts[0])
			require.NoError(t, err)
			require.NotNil(t, tk.Slot)
			tk.Slot.Release(nil)
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		rp := newRankPool(t, 1)
		ts := rp.enqueue(t, 1)
		require.NoError(t, os.Mkdir(rp.rankPath(ts[0].Name), 0o755)) // a directory where the file should be
		snap, err := rp.Snapshot()
		require.NoError(t, err)
		require.Len(t, snap.Queue, 1)
		assert.Equal(t, RankNormal, snap.Queue[0].Rank.Rank)
		assert.NotEmpty(t, snap.Queue[0].Rank.Error)
	})
}

// A front ticket is served before the older normal ones; a rank written between two attempts is honoured by the second.
// Mutation gate: the rank not read by TryTake → the front waiter does not pass (red).
func TestRank_FrontPassesAndARankBetweenTwoTriesIsHonoured(t *testing.T) {
	rp := newRankPool(t, 1)
	holder, err := rp.TakeLate(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, holder)
	ts := rp.enqueue(t, 2) // ts[0] older, ts[1] behind it

	first, err := rp.TryTakeRanked(ts[1])
	require.NoError(t, err)
	assert.Nil(t, first.Slot)
	assert.Equal(t, 1, first.Ahead)

	rp.set(t, ts[1], RankFront, time.Time{}) // moved between the two tries
	holder.Release(nil)
	second, err := rp.TryTakeRanked(ts[1])
	require.NoError(t, err)
	require.NotNil(t, second.Slot, "the front ticket passes the older one")
	assert.Equal(t, RankFront, second.Rank.Rank)
	second.Slot.Release(nil)

	behind, err := rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	assert.Equal(t, 1, behind.Ahead, "the older one is behind the front ticket while it waits")
	assert.Equal(t, 1, behind.AheadFront)
}

// A late claim (a command already running) takes its slot whatever the ranks say; the front waiter is next once it ends.
func TestRank_ALateClaimIgnoresRanksAndTheFrontWaiterIsNext(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 1)
	rp.set(t, ts[0], RankFront, time.Time{})
	late, err := rp.TakeLate(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, late, "the late claim takes the free slot though a front waiter queues")
	tk, err := rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	assert.Nil(t, tk.Slot)
	late.Release(nil)
	tk, err = rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	require.NotNil(t, tk.Slot)
	tk.Slot.Release(nil)
}

// A rank never outlives its ticket, and the CLI's answer for a ticket that has gone is "no longer waiting".
// Mutation gates: Drop leaves the rank → red; SetRank on a gone ticket succeeds → red.
func TestRank_NeverOutlivesItsTicket(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 2)
	rp.set(t, ts[0], RankFront, time.Time{})
	_, err := os.Stat(rp.rankPath(ts[0].Name))
	require.NoError(t, err)
	gone := ts[0].Name
	ts[0].Drop()
	_, err = os.Stat(rp.rankPath(gone))
	assert.True(t, os.IsNotExist(err), "Drop removes the rank with the ticket")

	_, err = rp.SetRank(gone, RankFront, "x", "y", time.Time{})
	assert.ErrorIs(t, err, ErrNotWaiting)
	_, err = os.Stat(rp.rankPath(gone))
	assert.True(t, os.IsNotExist(err), "no rank is left for a ticket that is gone")
	_, err = rp.ClearRank(gone)
	assert.ErrorIs(t, err, ErrNotWaiting)

	// a rank left by a crash is cleaned by the next look
	require.NoError(t, os.WriteFile(rp.rankPath("00000000000000000001-1-000001"), []byte(`{"rank":"front","at":1}`), 0o644))
	assert.Equal(t, nameOf(ts, 1), rp.order(t))
	_, err = os.Stat(rp.rankPath("00000000000000000001-1-000001"))
	assert.True(t, os.IsNotExist(err))

	_, err = rp.SetRank("../etc", RankFront, "x", "y", time.Time{})
	assert.ErrorIs(t, err, ErrNotWaiting, "a name with a path is no ticket")
	_, err = rp.SetRank(ts[1].Name, Rank("sideways"), "x", "y", time.Time{})
	assert.Error(t, err)
}

// A ticket whose file was taken from under it stands where its name puts it, behind the older live tickets: never at the head of the line.
// Mutation gate: ahead 0 for a ticket that is not listed → the newer ticket takes the slot (red).
func TestRank_ATicketThatIsNotListedStaysBehindTheOlderOnes(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 2)
	require.NoError(t, os.Remove(filepath.Join(rp.queueDir(), ts[1].Name))) // the newer ticket's file is gone
	tk, err := rp.TryTakeRanked(ts[1])
	require.NoError(t, err)
	assert.Nil(t, tk.Slot, "it does not pass the older waiter")
	assert.Equal(t, 1, tk.Ahead)
}

// ...and an OLDER missing ticket stands behind a newer front ticket: a front ticket precedes every normal one. Back and held tickets do not count.
// Mutation gate: only older names counted → the older missing ticket takes the slot ahead of the front one (red).
func TestRank_AnOlderMissingTicketStaysBehindAFrontOne(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 4) // ts[0] will go missing; ts[1] front, ts[2] back, ts[3] hold
	rp.set(t, ts[1], RankFront, time.Time{})
	rp.set(t, ts[2], RankBack, time.Time{})
	rp.set(t, ts[3], RankHold, time.Time{})
	require.NoError(t, os.Remove(filepath.Join(rp.queueDir(), ts[0].Name)))
	tk, err := rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	assert.Nil(t, tk.Slot, "the front ticket goes first")
	assert.Equal(t, 1, tk.Ahead, "only the front ticket: back and held ones are passed")
	assert.Equal(t, 1, tk.AheadFront)
}

// A negative until is a nonsense value: the ticket reads as normal with an error, not as a hold that never ends.
func TestRank_ANegativeUntilReadsAsNormal(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 1)
	require.NoError(t, os.WriteFile(rp.rankPath(ts[0].Name), []byte(`{"rank":"hold","at":1,"until":-1}`), 0o644))
	snap, err := rp.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, RankNormal, snap.Queue[0].Rank.Rank)
	assert.NotEmpty(t, snap.Queue[0].Rank.Error)
	tk, err := rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	require.NotNil(t, tk.Slot)
	tk.Slot.Release(nil)
}

// Giving up a place does not wait forever for the admit lock: after the bound the ticket and its rank are removed anyway.
// Mutation gate: an unbounded wait → the Drop never returns (red by timeout).
func TestRank_DropIsBoundedByAnAdmitLockThatIsHeld(t *testing.T) {
	old := dropLockWait
	dropLockWait = 50 * time.Millisecond
	defer func() { dropLockWait = old }()
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 1)
	rp.set(t, ts[0], RankFront, time.Time{})
	unlock, err := rp.lockAdmit() // somebody holds the lock and does not let go
	require.NoError(t, err)
	defer unlock()
	done := make(chan struct{})
	go func() { ts[0].Drop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Drop is waiting for the admit lock without end")
	}
	_, err = os.Stat(filepath.Join(rp.queueDir(), ts[0].Name))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(rp.rankPath(ts[0].Name))
	assert.True(t, os.IsNotExist(err), "the rank goes with the ticket")
}

// The status's count removes a dead gate's ticket together with its rank.
func TestRank_WaitingCleansTheRankOfADeadTicket(t *testing.T) {
	rp := newRankPool(t, 1)
	name := "00000000000000000001-1-000001"
	require.NoError(t, os.MkdirAll(rp.queueDir(), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rp.queueDir(), name), nil, 0o644)) // nobody holds its lock: a dead gate's
	require.NoError(t, os.WriteFile(rp.rankPath(name), []byte(`{"rank":"front","at":1}`), 0o644))
	assert.Equal(t, 0, rp.Waiting())
	_, err := os.Stat(rp.rankPath(name))
	assert.True(t, os.IsNotExist(err))
}

// Would-wait: a new normal command waits behind the front and normal tickets only; back and hold never make it wait.
// Mutation gate: back or hold counted → red; front not counted → red.
func TestRank_AheadOfANewTicketCountsFrontAndNormalOnly(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 5)
	rp.set(t, ts[1], RankBack, time.Time{})
	rp.set(t, ts[2], RankHold, time.Time{})
	rp.set(t, ts[3], RankFront, time.Time{})
	snap, err := rp.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, 3, snap.AheadOfNew(), "ts[0] normal, ts[3] front, ts[4] normal; ts[1] back and ts[2] hold are passed")
}

// SetRank, Drop and Snapshot together: no panic, no orphan rank, and every snapshot lists each live ticket once. Run under -race.
func TestRank_RacesBetweenSetRankDropAndSnapshot(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 8)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap, err := rp.Snapshot()
				if err != nil {
					t.Error(err)
					return
				}
				seen := map[string]bool{}
				for _, e := range snap.Queue {
					if seen[e.Name] {
						t.Errorf("%s listed twice", e.Name)
					}
					seen[e.Name] = true
				}
			}
		}()
	}
	for i, tk := range ts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = rp.SetRank(tk.Name, []Rank{RankFront, RankBack, RankHold, RankNormal}[(i+j)%4], "x", "y", time.Time{})
			}
			tk.Drop()
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	ents, err := os.ReadDir(rp.queueDir())
	require.NoError(t, err)
	for _, e := range ents {
		assert.False(t, strings.HasSuffix(e.Name(), ".rank"), "an orphan rank: %s", filepath.Join(rp.queueDir(), e.Name()))
	}
}
