package convfeed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newTestCache(max int) (*Cache, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	return NewCache(CacheOptions{Max: max, Now: clk.Now}), clk
}

func acquire(t *testing.T, c *Cache, id string) (*Entry, func()) {
	t.Helper()
	e, release, err := c.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("Acquire(%s): %v", id, err)
	}
	return e, release
}

func TestCache_SameSessionConcurrentlyIsOneEntry(t *testing.T) {
	c, _ := newTestCache(16)
	const n = 32
	got := make([]*Entry, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			e, release, err := c.Acquire(context.Background(), "s1")
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			got[i] = e
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("caller %d got another entry: two normalizers for one conversation", i)
		}
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestCache_PinsAreCountedAndReleaseIsIdempotent(t *testing.T) {
	c, clk := newTestCache(16)
	_, r1 := acquire(t, c, "s1")
	_, r2 := acquire(t, c, "s1")
	r1()
	r1() // a second call must not drop the other holder's pin
	clk.Advance(DefaultIdle + time.Minute)
	acquire2, rOther := acquire(t, c, "other") // sweeps idle entries
	_ = acquire2
	defer rOther()
	if c.Len() != 2 {
		t.Fatalf("Len = %d: s1 is still pinned by its second holder and must stay", c.Len())
	}
	r2()
	clk.Advance(DefaultIdle)
	_, r3 := acquire(t, c, "third")
	defer r3()
	if c.Len() != 2 { // s1 swept (unpinned, idle); other still pinned; third new
		t.Fatalf("Len = %d, want s1 gone after its last release and the idle time", c.Len())
	}
}

func TestCache_PinnedNeverEvictedIdleEvictedAfterTenMinutes(t *testing.T) {
	c, clk := newTestCache(16)
	pinned, rp := acquire(t, c, "pinned")
	defer rp()
	_, ri := acquire(t, c, "idle")
	ri()
	clk.Advance(9 * time.Minute)
	_, r := acquire(t, c, "probe")
	r()
	if c.Len() != 3 {
		t.Fatalf("Len = %d: nothing is idle for 10 minutes yet", c.Len())
	}
	clk.Advance(2 * time.Minute) // idle: 11, pinned: also 11 but pinned, probe: 2
	_, r = acquire(t, c, "probe")
	r()
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want the idle entry gone and the pinned one kept", c.Len())
	}
	again, ra := acquire(t, c, "pinned")
	defer ra()
	if again != pinned {
		t.Fatal("the pinned entry was replaced")
	}
}

func TestCache_SeventeenthWithSixteenPinnedIsBusy(t *testing.T) {
	c, _ := newTestCache(16)
	var releases []func()
	for i := 0; i < 16; i++ {
		_, r := acquire(t, c, fmt.Sprintf("s%d", i))
		releases = append(releases, r)
	}
	if _, _, err := c.Acquire(context.Background(), "s16"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if c.Len() != 16 {
		t.Fatalf("Len = %d: a busy refusal must not evict", c.Len())
	}
	releases[3]()
	if _, r, err := c.Acquire(context.Background(), "s16"); err != nil {
		t.Fatalf("after one release: %v", err)
	} else {
		r()
	}
}

func TestCache_FullEvictsTheOldestUnpinned(t *testing.T) {
	c, clk := newTestCache(3)
	for _, id := range []string{"a", "b", "c"} {
		_, r := acquire(t, c, id)
		r()
		clk.Advance(time.Second)
	}
	_, ra := acquire(t, c, "a") // a is now the most recently used, and pinned
	defer ra()
	_, rd := acquire(t, c, "d") // room needed: b is the oldest unpinned
	defer rd()
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
	if _, ok := c.entries["b"]; ok {
		t.Fatal("b (the oldest unpinned) was kept")
	}
	if _, ok := c.entries["c"]; !ok {
		t.Fatal("c (newer than b) was evicted instead of b")
	}
}

// An entry pinned for a refresh survives the eviction pressure that arrives while it runs.
func TestCache_RefreshRacingEvictionKeepsTheEntry(t *testing.T) {
	c, clk := newTestCache(2)
	e, release := acquire(t, c, "s1")
	m := newMem(idle(2)...)
	m.onRead = func(int) {
		clk.Advance(time.Hour)
		for i := 0; i < 4; i++ { // other sessions come and go during the read
			_, r, err := c.Acquire(context.Background(), fmt.Sprintf("x%d", i))
			if err == nil {
				r()
			}
		}
	}
	refresh(t, e, src(m, "f1", false))
	release()
	again, r := acquire(t, c, "s1")
	defer r()
	if again != e || turnsOf(again) != 2 {
		t.Fatal("the entry in use was evicted and replaced during its refresh")
	}
}

func TestCache_CancelledContextAcquiresNothing(t *testing.T) {
	c, _ := newTestCache(16)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Acquire(ctx, "s1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if c.Len() != 0 {
		t.Fatal("a cancelled Acquire created an entry")
	}
}

// The clock does not move between uses (coarse clock, a burst): the least recently used entry is still the one to go.
func TestCache_EqualTimestampsStillEvictTheLeastRecentlyUsed(t *testing.T) {
	for i := 0; i < 20; i++ { // map iteration order is random: repeat
		c, _ := newTestCache(2)
		for _, id := range []string{"a", "b", "a"} {
			_, r := acquire(t, c, id)
			r()
		}
		_, r := acquire(t, c, "c")
		r()
		if _, ok := c.entries["a"]; !ok {
			t.Fatalf("round %d: the entry used last was evicted", i)
		}
		if _, ok := c.entries["b"]; ok {
			t.Fatalf("round %d: b (least recently used) was kept", i)
		}
	}
}

// A caller cancelled while it waits for the cache lock must not evict, create or pin anything.
func TestCache_CancelledWhileWaitingForTheLockChangesNothing(t *testing.T) {
	c, _ := newTestCache(1)
	_, r := acquire(t, c, "keep")
	r()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, _, err := c.Acquire(ctx, "new")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond) // the goroutine is parked on c.mu
	cancel()
	c.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, ok := c.entries["keep"]; !ok || c.Len() != 1 {
		t.Fatal("a cancelled Acquire evicted the entry that was there")
	}
}

// With no further traffic, Sweep (run on a timer by the owner) still reclaims an idle entry.
func TestCache_SweepReclaimsWithoutTraffic(t *testing.T) {
	c, clk := newTestCache(16)
	_, r := acquire(t, c, "s1")
	r()
	_, rp := acquire(t, c, "pinned")
	defer rp()
	clk.Advance(DefaultIdle)
	c.Sweep()
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want only the pinned entry", c.Len())
	}
}

func TestCache_RunSweepsUntilCancelled(t *testing.T) {
	c, clk := newTestCache(16)
	_, r := acquire(t, c, "s1")
	r()
	clk.Advance(DefaultIdle)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for c.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if c.Len() != 0 {
		t.Fatal("Run did not reclaim the idle entry")
	}
}

func TestCache_RunWithANonPositiveIntervalDoesNotPanic(t *testing.T) {
	c, _ := newTestCache(16)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Run(ctx, 0) // returns at once: the context is done
}
