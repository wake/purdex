package nex

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1a (spec 2026-10-08 §3.1, §3.8): the read slot that orders every
// client-visible nex read and stamps successful ones with ver.

// readSlotFree reports whether nobody holds s, leaving it free either way.
func readSlotFree(s *readSlot) bool {
	select {
	case s.sem <- struct{}{}:
		<-s.sem
		return true
	default:
		return false
	}
}

// okRead is a read body that succeeds without doing anything.
func okRead(context.Context) error { return nil }

// steppedClock returns a clock that answers each call with the next offset
// from base, and keeps answering the last one once they run out. It makes
// wait/hold durations exact without sleeping.
func steppedClock(offsets ...time.Duration) func() time.Time {
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	i := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		d := offsets[i]
		if i < len(offsets)-1 {
			i++
		}
		return base.Add(d)
	}
}

// logRecorder collects formatted log lines.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logRecorder) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func TestReadSlot_EpochIsSixteenLowercaseHexAndStable(t *testing.T) {
	s := newReadSlot(discardLogf)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{16}$`), s.epoch)

	first, err := s.read(context.Background(), "row", 0, okRead)
	require.NoError(t, err)
	second, err := s.read(context.Background(), "list", 0, okRead)
	require.NoError(t, err)
	assert.Equal(t, s.epoch, first.Epoch)
	assert.Equal(t, first.Epoch, second.Epoch, "the epoch changed between two reads of one slot")

	// Two slots (two processes, as far as a client can tell) draw two
	// different epochs.
	assert.NotEqual(t, s.epoch, newReadSlot(discardLogf).epoch)
}

func TestReadSlot_VerIncrementsOnlyOnSuccess(t *testing.T) {
	s := newReadSlot(discardLogf)
	ctx := context.Background()

	st, err := s.read(ctx, "row", 0, okRead)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), st.Ver)
	assert.Equal(t, uint64(0), st.Bseq, "a read alone never advances bseq")

	boom := errors.New("engine said 500")
	st, err = s.read(ctx, "row", 0, func(context.Context) error { return boom })
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, slotStamp{}, st, "a failed read must return no stamp")

	st, err = s.read(ctx, "list", 0, okRead)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), st.Ver, "the failed read consumed a ver")
	assert.True(t, readSlotFree(s), "the slot is still held after the reads returned")
}

func TestReadSlot_FnGetsTheCallersContext(t *testing.T) {
	type key struct{}
	s := newReadSlot(discardLogf)
	ctx := context.WithValue(context.Background(), key{}, "caller")
	var got any
	_, err := s.read(ctx, "row", 0, func(ctx context.Context) error {
		got = ctx.Value(key{})
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "caller", got)
}

// Many goroutines read at once: no two bodies ever run together, and the
// vers handed out follow the order in which the bodies finished.
func TestReadSlot_ConcurrentReadsNeverOverlapAndVerFollowsCompletion(t *testing.T) {
	s := newReadSlot(discardLogf)
	const workers, perWorker = 16, 25

	var inside, maxInside atomic.Int32
	var mu sync.Mutex
	var finished []int // read ids in the order their bodies finished
	stamps := map[int]uint64{}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := w*perWorker + i
				fail := id%7 == 0 // some reads fail and must consume nothing
				st, err := s.read(context.Background(), "row", 0, func(context.Context) error {
					n := inside.Add(1)
					for {
						m := maxInside.Load()
						if n <= m || maxInside.CompareAndSwap(m, n) {
							break
						}
					}
					runtime.Gosched()
					inside.Add(-1)
					if fail {
						return errors.New("failed read")
					}
					mu.Lock()
					finished = append(finished, id)
					mu.Unlock()
					return nil
				})
				if fail {
					if err == nil {
						t.Errorf("read %d: want an error", id)
					}
					continue
				}
				if err != nil {
					t.Errorf("read %d: %v", id, err)
					continue
				}
				mu.Lock()
				stamps[id] = st.Ver
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	assert.Equal(t, int32(1), maxInside.Load(), "two read bodies ran at the same time")
	require.Len(t, stamps, len(finished))
	for i, id := range finished {
		assert.Equal(t, uint64(i+1), stamps[id], "read %d finished #%d but was stamped %d", id, i+1, stamps[id])
	}
}

func TestReadSlot_WaitPastMaxWaitIsBusyAndSlotStaysUsable(t *testing.T) {
	s := newReadSlot(discardLogf)
	require.NoError(t, s.acquire(context.Background(), 0))

	called := false
	start := time.Now()
	st, err := s.read(context.Background(), "list", 20*time.Millisecond, func(context.Context) error {
		called = true
		return nil
	})
	assert.ErrorIs(t, err, errSlotBusy)
	assert.NotErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, called, "a read that never got the slot ran its body")
	assert.Equal(t, slotStamp{}, st)
	assert.Less(t, time.Since(start), 2*time.Second, "maxWait was not honoured")

	s.release()
	assert.True(t, readSlotFree(s), "the busy waiter left the slot held")
	st, err = s.read(context.Background(), "list", 20*time.Millisecond, okRead)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), st.Ver, "the busy read consumed a ver")
}

func TestReadSlot_CancelledWhileWaitingReturnsPromptlyWithoutTheSlot(t *testing.T) {
	s := newReadSlot(discardLogf)
	require.NoError(t, s.acquire(context.Background(), 0))

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		st  slotStamp
		err error
	}
	done := make(chan result, 1)
	called := atomic.Bool{}
	go func() {
		st, err := s.read(ctx, "list", 0, func(context.Context) error {
			called.Store(true)
			return nil
		})
		done <- result{st, err}
	}()
	runtime.Gosched()
	cancel()

	select {
	case r := <-done:
		assert.ErrorIs(t, r.err, context.Canceled)
		assert.NotErrorIs(t, r.err, errSlotBusy)
		assert.Equal(t, slotStamp{}, r.st)
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter whose context ended did not return")
	}
	assert.False(t, called.Load(), "a cancelled waiter ran its body")

	s.release()
	assert.True(t, readSlotFree(s), "the cancelled waiter took the slot")
}

// With the slot free and the context already ended, both select cases are
// ready; repeated, a missing post-send check shows up at once (the session
// slot's #1293 R1 case).
func TestReadSlot_EndedContextNeverTakesAFreeSlot(t *testing.T) {
	s := newReadSlot(discardLogf)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 1000; i++ {
		if err := s.acquire(ctx, time.Second); err == nil {
			t.Fatalf("iteration %d: acquire with an ended context returned nil", i)
		}
		if !readSlotFree(s) {
			t.Fatalf("iteration %d: an ended context left the slot held", i)
		}
	}
}

func TestReadSlot_PanickingBodyReleasesTheSlot(t *testing.T) {
	s := newReadSlot(discardLogf)
	func() {
		defer func() {
			assert.Equal(t, "body exploded", recover())
		}()
		_, _ = s.read(context.Background(), "row", 0, func(context.Context) error {
			panic("body exploded")
		})
	}()
	require.True(t, readSlotFree(s), "a panicking body left the slot held")

	st, err := s.read(context.Background(), "row", 0, okRead)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), st.Ver, "the panicking read consumed a ver")
}

// The clock is read three times per read: before waiting, once the slot is
// taken, and when it is released. Each case steps those three readings.
func TestReadSlot_LogsWaitsAndHoldsAboveTheThreshold(t *testing.T) {
	cases := []struct {
		name               string
		who                string
		start, got, done   time.Duration
		want               []string
		wantHold, wantWait int64
	}{
		{"both below", "list", 0, 100 * time.Millisecond, 200 * time.Millisecond, nil, 100, 100},
		{"exactly at threshold is not logged", "list", 0, 250 * time.Millisecond, 500 * time.Millisecond, nil, 250, 250},
		{"long wait", "list", 0, 300 * time.Millisecond, 400 * time.Millisecond,
			[]string{"nex-delta: slot wait 300ms for list"}, 100, 300},
		{"long hold", "row", 0, 10 * time.Millisecond, 410 * time.Millisecond,
			[]string{"nex-delta: slot held 400ms by row"}, 400, 10},
		{"both long", "reconcile", 0, 260 * time.Millisecond, 1260 * time.Millisecond,
			[]string{"nex-delta: slot wait 260ms for reconcile", "nex-delta: slot held 1000ms by reconcile"}, 1000, 260},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logRecorder{}
			s := newReadSlot(logs.logf)
			s.now = steppedClock(tc.start, tc.got, tc.done)
			_, err := s.read(context.Background(), tc.who, 0, okRead)
			require.NoError(t, err)
			assert.Equal(t, tc.want, logs.all())
			hold, wait := s.maxima()
			assert.Equal(t, tc.wantHold, hold, "max hold")
			assert.Equal(t, tc.wantWait, wait, "max wait")
		})
	}
}

// A hold is logged even when the body failed: the slot was held all the
// same, and a slow failing page is exactly what the log is for.
func TestReadSlot_LogsALongHoldOfAFailedRead(t *testing.T) {
	logs := &logRecorder{}
	s := newReadSlot(logs.logf)
	s.now = steppedClock(0, 0, 500*time.Millisecond)
	_, err := s.read(context.Background(), "list", 0, func(context.Context) error { return errors.New("500") })
	require.Error(t, err)
	assert.Equal(t, []string{"nex-delta: slot held 500ms by list"}, logs.all())
}

// A wait that gave up is logged too (and counted in the max wait), with
// why it gave up: a 2 s busy timeout is the case worth seeing.
func TestReadSlot_LogsALongWaitThatGaveUp(t *testing.T) {
	logs := &logRecorder{}
	s := newReadSlot(logs.logf)
	require.NoError(t, s.acquire(context.Background(), 0))
	s.now = steppedClock(0, 2*time.Second)

	_, err := s.read(context.Background(), "list", 5*time.Millisecond, okRead)
	require.ErrorIs(t, err, errSlotBusy)
	assert.Equal(t, []string{"nex-delta: slot wait 2000ms for list (gave up: nex read slot busy)"}, logs.all())
	_, wait := s.maxima()
	assert.Equal(t, int64(2000), wait)
	s.release()
}

// The maxima keep the largest value seen, not the latest.
func TestReadSlot_MaximaKeepTheLargest(t *testing.T) {
	s := newReadSlot(discardLogf)
	s.threshold = time.Hour // keep the log quiet; only the maxima matter here
	s.now = steppedClock(0, 50*time.Millisecond, 250*time.Millisecond)
	_, err := s.read(context.Background(), "row", 0, okRead)
	require.NoError(t, err)
	s.now = steppedClock(0, 10*time.Millisecond, 20*time.Millisecond)
	_, err = s.read(context.Background(), "row", 0, okRead)
	require.NoError(t, err)

	hold, wait := s.maxima()
	assert.Equal(t, int64(200), hold)
	assert.Equal(t, int64(50), wait)
}

// #1866 PR1b (spec 2026-10-08 §3.3, §3.5): readThen runs its continuation
// inside the same hold, right after the read succeeded and its ver was
// taken — where the projector takes its bseq and broadcasts.
func TestReadSlot_ReadThenRunsInsideTheHoldAfterTheVer(t *testing.T) {
	s := newReadSlot(discardLogf)
	ctx := context.Background()
	var seen slotStamp
	var held bool
	st, err := s.readThen(ctx, "row", 0, okRead, func(in slotStamp) {
		seen, held = in, !readSlotFree(s)
		assert.Equal(t, uint64(1), s.nextBseq())
	})
	require.NoError(t, err)
	assert.True(t, held, "the continuation ran outside the slot")
	assert.Equal(t, slotStamp{Epoch: s.epoch, Ver: 1, Bseq: 0}, seen)
	assert.Equal(t, seen, st)

	// Every later stamp carries the advanced high-water mark (§8 R3-1).
	st, err = s.read(ctx, "list", 0, okRead)
	require.NoError(t, err)
	assert.Equal(t, slotStamp{Epoch: s.epoch, Ver: 2, Bseq: 1}, st)
}

func TestReadSlot_ReadThenSkipsTheContinuationOfAFailedRead(t *testing.T) {
	s := newReadSlot(discardLogf)
	ran := false
	_, err := s.readThen(context.Background(), "row", 0,
		func(context.Context) error { return errors.New("500") },
		func(slotStamp) { ran = true })
	require.Error(t, err)
	assert.False(t, ran, "a failed read ran its continuation")
	assert.Equal(t, uint64(0), s.ver)
	assert.Equal(t, uint64(0), s.bseq)
}
