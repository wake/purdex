package notifylabel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fakeSources struct {
	codes    map[string]string // session id -> persona code
	groups   map[string]string // session id -> team display name
	display  map[string]string // code -> display name
	mu       sync.Mutex
	groupErr error
	codeErr  error
	reads    atomic.Int64
	gate     chan struct{} // when set, a read waits for it
}

func (f *fakeSources) sources() Sources {
	return Sources{
		Code: func(ctx context.Context, sid string) (string, bool, error) {
			f.reads.Add(1)
			f.wait(ctx)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.codeErr != nil {
				return "", false, f.codeErr
			}
			c, ok := f.codes[sid]
			return c, ok, nil
		},
		Group: func(ctx context.Context, sid string) (string, error) {
			f.wait(ctx)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.groupErr != nil {
				return "", f.groupErr
			}
			return f.groups[sid], nil
		},
		Display: func(code string) (string, bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			n, ok := f.display[code]
			return n, ok
		},
	}
}

func (f *fakeSources) wait(ctx context.Context) {
	if f.gate == nil {
		return
	}
	select {
	case <-f.gate:
	case <-ctx.Done():
	}
}

func (f *fakeSources) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

func newTest(t *testing.T, f *fakeSources, opts ...Option) (*Labeler, *clock) {
	t.Helper()
	clk := &clock{t: time.Unix(1_000_000, 0)}
	l := New(f.sources(), append([]Option{WithClock(clk.Now)}, opts...)...)
	l.Start()
	t.Cleanup(l.Stop)
	return l, clk
}

func settle(t *testing.T, l *Labeler) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l.idle() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the background read did not finish")
}

func baseSources() *fakeSources {
	return &fakeSources{
		codes:   map[string]string{"s1": "mei"},
		groups:  map[string]string{"s1": "Purdex A team"},
		display: map[string]string{"mei": "Mei"},
	}
}

// A cold cache answers with what it knows (the tmux name), reads once in the background however many events come, and
// answers fully afterwards.
func TestLabeler_ColdThenWarm(t *testing.T) {
	f := baseSources()
	l, _ := newTest(t, f)
	for i := 0; i < 10; i++ {
		if got := l.Label("s1", "dev"); got != (Label{Tmux: "dev"}) {
			t.Fatalf("cold label = %+v", got)
		}
	}
	settle(t, l)
	if got := f.reads.Load(); got != 1 {
		t.Fatalf("reads = %d, want exactly one for ten cold events", got)
	}
	if got := l.Label("s1", "dev"); got != (Label{Name: "Mei", Group: "Purdex A team", Tmux: "dev"}) {
		t.Fatalf("warm label = %+v", got)
	}
	if got := f.reads.Load(); got != 1 {
		t.Fatalf("a warm label read again: %d", got)
	}
}

// The display name is looked up on every Label (a memory lookup), so a persona renamed in the collection shows at once.
func TestLabeler_DisplayNameIsLive(t *testing.T) {
	f := baseSources()
	l, _ := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	f.set(func() { f.display["mei"] = "Mei Lin" })
	if got := l.Label("s1", "dev"); got.Name != "Mei Lin" {
		t.Fatalf("name = %q", got.Name)
	}
	f.set(func() { delete(f.display, "mei") }) // a code the collection no longer knows: no name, not the raw code
	if got := l.Label("s1", "dev"); got.Name != "" || got.Group != "Purdex A team" {
		t.Fatalf("label = %+v", got)
	}
}

// After the ttl the next Label asks for a reread (and still answers from the old entry); a team move or a rename shows
// within one ttl.
func TestLabeler_StaleEntryIsReread(t *testing.T) {
	f := baseSources()
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	clk.advance(29 * time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	if got := f.reads.Load(); got != 1 {
		t.Fatalf("reads inside the ttl = %d", got)
	}
	f.set(func() { f.groups["s1"] = "Other team"; f.codes["s1"] = "kai"; f.display["kai"] = "Kai" })
	clk.advance(2 * time.Second)
	if got := l.Label("s1", "dev"); got.Group != "Purdex A team" || got.Name != "Mei" {
		t.Fatalf("the stale entry answers while the reread runs: %+v", got)
	}
	settle(t, l)
	if got := f.reads.Load(); got != 2 {
		t.Fatalf("reads = %d, want 2", got)
	}
	if got := l.Label("s1", "dev"); got != (Label{Name: "Kai", Group: "Other team", Tmux: "dev"}) {
		t.Fatalf("reread label = %+v", got)
	}
}

// A slow source never holds a Label (nor the broadcast that calls it): Label returns while the read is parked.
func TestLabeler_SlowSourceDoesNotBlockLabel(t *testing.T) {
	f := baseSources()
	f.gate = make(chan struct{})
	l, _ := newTest(t, f)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			l.Label("s1", "dev")
			l.Label("s2", "dev") // a second session queues behind the parked read
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Label waited for a slow source")
	}
	close(f.gate)
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Name != "Mei" {
		t.Fatalf("label after the read = %+v", got)
	}
}

// A failed read keeps what was known and is not retried until the ttl is over (no read per event while the store is down).
func TestLabeler_ReadErrorKeepsLastKnownAndBacksOff(t *testing.T) {
	f := baseSources()
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	f.set(func() { f.groupErr = errors.New("db busy") })
	clk.advance(31 * time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	reads := f.reads.Load()
	for i := 0; i < 5; i++ {
		if got := l.Label("s1", "dev"); got != (Label{Name: "Mei", Group: "Purdex A team", Tmux: "dev"}) {
			t.Fatalf("label after a failed read = %+v", got)
		}
	}
	settle(t, l)
	if got := f.reads.Load(); got != reads {
		t.Fatalf("a failed read was retried per event: %d -> %d", reads, got)
	}
	f.set(func() { f.groupErr = nil })
	clk.advance(31 * time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Name != "Mei" {
		t.Fatalf("label after recovery = %+v", got)
	}
}

// A session that has no persona has no name, and the group alone is still carried.
func TestLabeler_NoPersona(t *testing.T) {
	f := baseSources()
	delete(f.codes, "s1")
	l, _ := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got != (Label{Group: "Purdex A team", Tmux: "dev"}) {
		t.Fatalf("label = %+v", got)
	}
}

type overlay map[string]string

func (o overlay) GroupOf(sid string) string { return o[sid] }

// The group written back by the App (N-2) fills in only where no team does.
func TestLabeler_TeamBeatsTheOverlay(t *testing.T) {
	f := baseSources()
	delete(f.groups, "s1")
	f.codes["s2"] = "mei"
	f.groups["s2"] = "Real team"
	l, _ := newTest(t, f, WithOverlay(overlay{"s1": "My group", "s2": "My group"}))
	l.Label("s1", "dev")
	l.Label("s2", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Group != "My group" {
		t.Fatalf("no team: group = %q", got.Group)
	}
	if got := l.Label("s2", "dev"); got.Group != "Real team" {
		t.Fatalf("a team wins: group = %q", got.Group)
	}
}

// A session id that is empty has nothing to look up and starts no read.
func TestLabeler_EmptySessionID(t *testing.T) {
	f := baseSources()
	l, _ := newTest(t, f)
	if got := l.Label("", "dev"); got != (Label{Tmux: "dev"}) {
		t.Fatalf("label = %+v", got)
	}
	settle(t, l)
	if f.reads.Load() != 0 {
		t.Fatal("an empty session id started a read")
	}
}

// After Stop a Label still answers and starts nothing; Stop twice is harmless.
func TestLabeler_StopIsFinal(t *testing.T) {
	f := baseSources()
	l, _ := newTest(t, f)
	l.Stop()
	l.Stop()
	if got := l.Label("s1", "dev"); got != (Label{Tmux: "dev"}) {
		t.Fatalf("label = %+v", got)
	}
	if f.reads.Load() != 0 {
		t.Fatal("a stopped labeler read")
	}
}

// The cache is bounded: a flood of distinct sessions cannot grow it without limit.
func TestLabeler_CacheIsBounded(t *testing.T) {
	f := baseSources()
	l, _ := newTest(t, f, WithMaxEntries(8))
	for i := 0; i < 100; i++ {
		l.Label(string(rune('a'+i%26))+string(rune('A'+i/26)), "dev")
		settle(t, l)
	}
	if n := l.size(); n > 8 {
		t.Fatalf("cache holds %d entries, cap 8", n)
	}
}

func BenchmarkLabeler_WarmLabel(b *testing.B) {
	f := baseSources()
	clk := &clock{t: time.Unix(1_000_000, 0)}
	l := New(f.sources(), WithClock(clk.Now))
	l.Start()
	defer l.Stop()
	l.Label("s1", "dev")
	for !l.idle() {
		time.Sleep(time.Millisecond)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Label("s1", "dev")
	}
}

// A source that ignores its context (a store call with no deadline) must not hold the daemon's shutdown: Stop gives the
// reader a moment, then lets go of it.
func TestLabeler_StopDoesNotWaitForeverOnAStuckSource(t *testing.T) {
	f := baseSources()
	stuck := make(chan struct{})
	started := make(chan struct{}, 1)
	src := f.sources()
	src.Group = func(context.Context, string) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-stuck // never looks at ctx
		return "", nil
	}
	l := New(src, WithStopWait(50*time.Millisecond))
	l.Start()
	l.Label("s1", "dev")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the read did not start")
	}
	done := make(chan struct{})
	go func() { l.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited for a source that ignores its context")
	}
	close(stuck)
}

// A failed persona read keeps the name that was known (a busy names db must not blank the title for a ttl).
func TestLabeler_CodeReadFailureKeepsTheKnownName(t *testing.T) {
	f := baseSources()
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	f.set(func() { f.codeErr = errors.New("db busy") })
	clk.advance(31 * time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Name != "Mei" {
		t.Fatalf("label after a failed persona read = %+v", got)
	}
	f.set(func() { f.codeErr = nil; delete(f.codes, "s1") }) // the session really lost its persona
	clk.advance(31 * time.Second)
	l.Label("s1", "dev")
	settle(t, l)
	if got := l.Label("s1", "dev"); got.Name != "" {
		t.Fatalf("a read that succeeded with no persona must clear the name: %+v", got)
	}
}

// A session that is quiet for a long task is reread on its own, so the notification that ends it carries the current
// name (spec §3.1): no Label call between the change and the sweep.
func TestLabeler_SweepRereadsSessionsInUse(t *testing.T) {
	f := baseSources()
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	f.set(func() { f.groups["s1"] = "Other team"; f.codes["s1"] = "kai"; f.display["kai"] = "Kai" })
	clk.advance(31 * time.Second)
	l.sweep()
	settle(t, l)
	if got := l.Label("s1", "dev"); got != (Label{Name: "Kai", Group: "Other team", Tmux: "dev"}) {
		t.Fatalf("the first label after a quiet stretch = %+v", got)
	}
}

// An entry that was not asked for within the retention is dropped, and not reread.
func TestLabeler_SweepDropsSessionsNotAskedFor(t *testing.T) {
	f := baseSources()
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	settle(t, l)
	reads := f.reads.Load()
	clk.advance(61 * time.Minute)
	l.sweep()
	settle(t, l)
	if l.size() != 0 {
		t.Fatalf("cache holds %d entries after the retention", l.size())
	}
	if f.reads.Load() != reads {
		t.Fatal("an unused session was reread")
	}
}

// A sweep that finds nothing stale reads nothing; one whose read is parked on I/O holds neither the sweep nor Label.
func TestLabeler_SweepDoesIOOutsideTheLock(t *testing.T) {
	f := baseSources()
	f.codes["s2"], f.groups["s2"] = "mei", "T2"
	l, clk := newTest(t, f)
	l.Label("s1", "dev")
	l.Label("s2", "dev")
	settle(t, l)
	l.sweep()
	settle(t, l)
	if got := f.reads.Load(); got != 2 {
		t.Fatalf("a sweep of fresh entries read: %d", got)
	}
	f.gate = make(chan struct{})
	clk.advance(31 * time.Second)
	done := make(chan struct{})
	go func() { l.sweep(); l.Label("s1", "dev"); l.Label("s3", "dev"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep or Label waited for a parked read")
	}
	close(f.gate)
	settle(t, l)
}
