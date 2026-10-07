package modevents

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const (
	sidA = "11111111-1111-4111-8111-111111111111"
	sidB = "22222222-2222-4222-8222-222222222222"
)

func mkEvent(seq int64, typ string) Event {
	return Event{Seq: seq, At: 1000 + seq, SID: sidA, Type: typ, Data: json.RawMessage(`{}`)}
}

func mkBatch(stream string, dropped int64, events ...Event) Batch {
	return Batch{V: 1, Stream: stream, Agent: "cc", CCVersion: "2.1.293", ModVersion: "1.0.0-alpha.596", DroppedTotal: dropped, Events: events}
}

// recorder subscribes and records "<stream>#<seq>" per delivery.
type recorder struct {
	mu  sync.Mutex
	got []string
}

func (r *recorder) fn(info StreamInfo, e Event) {
	r.mu.Lock()
	r.got = append(r.got, fmt.Sprintf("%s#%d", info.Stream, e.Seq))
	r.mu.Unlock()
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func streamInfo(t *testing.T, reg *Registry, stream string) StreamInfo {
	t.Helper()
	for _, s := range reg.Streams() {
		if s.Stream == stream {
			return s
		}
	}
	t.Fatalf("stream %s not in the registry", stream)
	return StreamInfo{}
}

func TestApply_DedupesRetriedEvents(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	rec := &recorder{}
	reg.Subscribe(rec.fn)
	const s = "streamAAA"

	b := mkBatch(s, 0, mkEvent(1, TypeTurnStart), mkEvent(2, TypeToolStart), mkEvent(3, TypeToolEnd))
	if ack := reg.Apply(b); ack != 3 {
		t.Fatalf("ack = %d, want 3", ack)
	}
	if ack := reg.Apply(b); ack != 3 {
		t.Fatalf("resent batch: ack = %d, want 3", ack)
	}
	want := []string{s + "#1", s + "#2", s + "#3"}
	if got := rec.list(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %v, want %v (a resent batch is delivered once)", got, want)
	}
	// A batch overlapping the applied ones delivers only the new tail.
	if ack := reg.Apply(mkBatch(s, 0, mkEvent(2, TypeToolStart), mkEvent(3, TypeToolEnd), mkEvent(4, TypeTurnComplete))); ack != 4 {
		t.Fatalf("overlap: ack = %d, want 4", ack)
	}
	if got := rec.list(); !reflect.DeepEqual(got, append(want, s+"#4")) {
		t.Fatalf("deliveries = %v", got)
	}
	info := streamInfo(t, reg, s)
	if info.Counts[TypeToolStart] != 1 || info.Counts[TypeTurnStart] != 1 || info.Gaps != 0 || info.LastSeq != 4 {
		t.Fatalf("info = %+v (duplicates are not counted)", info)
	}
	if evs, ok := reg.Events(s, 0); !ok || len(evs) != 4 {
		t.Fatalf("ring = %v, %v", evs, ok)
	}
}

func TestApply_DroppedTotalIsIdempotent(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	const s = "streamAAA"
	b := mkBatch(s, 5, mkEvent(1, TypeHeartbeat))
	reg.Apply(b)
	reg.Apply(b) // the 200 was lost; the mod resends the same batch
	if got := streamInfo(t, reg, s).DroppedTotal; got != 5 {
		t.Fatalf("dropped_total = %d after a resend, want 5", got)
	}
	// A batch built before more losses arrives late with a lower total.
	reg.Apply(mkBatch(s, 3, mkEvent(2, TypeHeartbeat)))
	if got := streamInfo(t, reg, s).DroppedTotal; got != 5 {
		t.Fatalf("dropped_total = %d after a lower total, want 5", got)
	}
	reg.Apply(mkBatch(s, 8, mkEvent(3, TypeHeartbeat)))
	if got := streamInfo(t, reg, s).DroppedTotal; got != 8 {
		t.Fatalf("dropped_total = %d, want 8", got)
	}
}

func TestApply_FirstSeenFixed(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	const s = "streamAAA"
	t0 := clk.Now()
	reg.Apply(mkBatch(s, 0, mkEvent(1, TypeSessionStart)))
	clk.Advance(time.Minute)
	reg.Apply(mkBatch(s, 0, mkEvent(2, TypeHeartbeat)))
	info := streamInfo(t, reg, s)
	if !info.FirstSeen.Equal(t0) || !info.LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("first_seen = %v, last_seen = %v; want %v, %v", info.FirstSeen, info.LastSeen, t0, t0.Add(time.Minute))
	}
	if info.Agent != "cc" || info.CCVersion != "2.1.293" || info.ModVersion != "1.0.0-alpha.596" || info.SID != sidA {
		t.Fatalf("info = %+v", info)
	}
}

func TestApply_CountsGaps(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	rec := &recorder{}
	reg.Subscribe(rec.fn)
	const s = "streamAAA"
	reg.Apply(mkBatch(s, 0, mkEvent(1, TypeTurnStart), mkEvent(2, TypeTurnComplete)))
	if ack := reg.Apply(mkBatch(s, 0, mkEvent(5, TypeHeartbeat))); ack != 5 {
		t.Fatalf("ack = %d, want 5 (a gap is applied)", ack)
	}
	reg.Apply(mkBatch(s, 0, mkEvent(6, TypeHeartbeat)))
	reg.Apply(mkBatch(s, 0, mkEvent(9, TypeHeartbeat), mkEvent(10, TypeHeartbeat)))
	if info := streamInfo(t, reg, s); info.Gaps != 2 || info.LastSeq != 10 {
		t.Fatalf("gaps = %d, last_seq = %d; want 2, 10", info.Gaps, info.LastSeq)
	}
	if got := rec.list(); len(got) != 6 {
		t.Fatalf("deliveries = %v (events after a gap are delivered)", got)
	}

	// A stream this registry has never seen (the daemon restarted while the
	// mod kept running) starts wherever the mod's queue starts: events the
	// previous daemon acknowledged are not a gap (spec §6.7).
	const r = "streamRRR"
	reg.Apply(mkBatch(r, 0, mkEvent(41, TypeHeartbeat), mkEvent(42, TypeHeartbeat)))
	if info := streamInfo(t, reg, r); info.Gaps != 0 || info.LastSeq != 42 {
		t.Fatalf("restarted registry: gaps = %d, last_seq = %d; want 0, 42", info.Gaps, info.LastSeq)
	}
}

func TestApply_UnknownTypeCountedNotDelivered(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	rec := &recorder{}
	reg.Subscribe(rec.fn)
	const s = "streamAAA"
	ack := reg.Apply(mkBatch(s, 0, mkEvent(1, TypeTurnStart), mkEvent(2, "turn.step"), mkEvent(3, TypeHeartbeat)))
	if ack != 3 {
		t.Fatalf("ack = %d, want 3 (an unknown type is applied)", ack)
	}
	if got, want := rec.list(), []string{s + "#1", s + "#3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %v, want %v", got, want)
	}
	info := streamInfo(t, reg, s)
	if info.Counts[CountUnknown] != 1 || info.Counts["turn.step"] != 0 || info.Counts[TypeTurnStart] != 1 || info.Counts[TypeHeartbeat] != 1 {
		t.Fatalf("counts = %v", info.Counts)
	}
	evs, _ := reg.Events(s, 0)
	if len(evs) != 2 || evs[0].Seq != 1 || evs[1].Seq != 3 {
		t.Fatalf("ring = %+v (unknown types stay out of it)", evs)
	}
}

func TestApply_DeliveryHoldsTheStream(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	const a, b = "streamAAA", "streamBBB"
	enteredA1 := make(chan struct{})
	release := make(chan struct{})
	delivered := make(chan string, 8)
	var mu sync.Mutex
	var order []string
	reg.Subscribe(func(info StreamInfo, e Event) {
		key := fmt.Sprintf("%s#%d", info.Stream, e.Seq)
		if key == a+"#1" {
			close(enteredA1)
			<-release
		}
		mu.Lock()
		order = append(order, key)
		mu.Unlock()
		delivered <- key
	})

	done := make(chan struct{}, 3)
	go func() { reg.Apply(mkBatch(a, 0, mkEvent(1, TypeTurnStart))); done <- struct{}{} }()
	<-enteredA1
	go func() { reg.Apply(mkBatch(a, 0, mkEvent(2, TypeTurnComplete))); done <- struct{}{} }()
	go func() { reg.Apply(mkBatch(b, 0, mkEvent(1, TypeTurnStart))); done <- struct{}{} }()

	// Another stream is not held by A's delivery.
	select {
	case k := <-delivered:
		if k != b+"#1" {
			t.Fatalf("delivered %s while A#1 is held", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream B was not delivered while stream A's subscriber blocked")
	}
	// A#2 waits for A#1's delivery to return.
	select {
	case k := <-delivered:
		t.Fatalf("delivered %s while A#1 is held", k)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 3 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Apply did not return after the release")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{b + "#1", a + "#1", a + "#2"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("deliveries = %v, want %v", order, want)
	}
}

func TestReject_CountsAndCaps(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	const s = "streamAAA"
	t0 := clk.Now()
	reg.Reject(s)
	clk.Advance(time.Second)
	reg.Reject(s)
	info := streamInfo(t, reg, s)
	if info.Rejected != 2 || !info.FirstSeen.Equal(t0) || info.LastSeq != 0 {
		t.Fatalf("info = %+v; want rejected 2, first_seen %v", info, t0)
	}
	// A later valid batch keeps the count.
	reg.Apply(mkBatch(s, 0, mkEvent(1, TypeHeartbeat)))
	if info := streamInfo(t, reg, s); info.Rejected != 2 || info.LastSeq != 1 {
		t.Fatalf("info = %+v", info)
	}
	reg.Reject("bad id")
	if n := len(reg.Streams()); n != 1 {
		t.Fatalf("an invalid stream id must not create an entry: %d streams", n)
	}

	// Reject creates entries under the same cap as Apply: the stream with the
	// oldest last_seen goes first.
	for i := range MaxStreams - 1 {
		clk.Advance(time.Second)
		reg.Apply(mkBatch(fmt.Sprintf("stream%04d", i), 0, mkEvent(1, TypeHeartbeat)))
	}
	if n := len(reg.Streams()); n != MaxStreams {
		t.Fatalf("%d streams, want %d", n, MaxStreams)
	}
	clk.Advance(time.Second)
	reg.Reject("streamNEW")
	streams := reg.Streams()
	if len(streams) != MaxStreams {
		t.Fatalf("%d streams after a Reject at the cap, want %d", len(streams), MaxStreams)
	}
	seen := map[string]bool{}
	for _, st := range streams {
		seen[st.Stream] = true
	}
	if seen[s] || !seen["streamNEW"] || !seen["stream0000"] {
		t.Fatalf("the oldest stream (%s) must be evicted and the new one kept", s)
	}
}

func TestSubscribe_PanicIsContained(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	reg.Subscribe(func(StreamInfo, Event) { panic("subscriber bug") })
	rec := &recorder{}
	cancel := reg.Subscribe(rec.fn)
	const s = "streamAAA"
	if ack := reg.Apply(mkBatch(s, 0, mkEvent(1, TypeTurnStart), mkEvent(2, TypeTurnComplete))); ack != 2 {
		t.Fatalf("ack = %d, want 2", ack)
	}
	if got := rec.list(); len(got) != 2 {
		t.Fatalf("deliveries = %v: a panicking subscriber must not stop the others", got)
	}
	cancel()
	cancel() // idempotent
	reg.Apply(mkBatch(s, 0, mkEvent(3, TypeHeartbeat)))
	if got := rec.list(); len(got) != 2 {
		t.Fatalf("deliveries after cancel = %v", got)
	}
}

func TestEviction_EndedAndIdleAndCap(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	const ended, idle, live = "streamEND", "streamIDL", "streamLIV"
	reg.Apply(mkBatch(ended, 0, mkEvent(1, TypeSessionStart), mkEvent(2, TypeSessionEnd)))
	reg.Apply(mkBatch(idle, 0, mkEvent(1, TypeSessionStart)))
	if info := streamInfo(t, reg, ended); !info.Ended || !info.EndedAt.Equal(clk.Now()) {
		t.Fatalf("ended info = %+v", info)
	}

	keepAlive := func(d time.Duration) {
		for step := time.Duration(0); step < d; step += 10 * time.Minute {
			clk.Advance(10 * time.Minute)
			reg.Apply(mkBatch(live, 0, mkEvent(int64(clk.Now().Unix()), TypeHeartbeat)))
		}
	}
	has := func(id string) bool {
		_, ok := reg.Events(id, 0)
		return ok
	}

	keepAlive(20 * time.Minute)
	reg.Evict()
	if !has(ended) {
		t.Fatal("an ended stream stays 30 min")
	}
	keepAlive(20 * time.Minute) // 40 min after session.end
	reg.Evict()
	if has(ended) {
		t.Fatal("an ended stream is evicted 30 min after session.end")
	}
	if !has(idle) {
		t.Fatal("a stream idle for 40 min stays")
	}
	keepAlive(90 * time.Minute) // 2 h 10 min without events from idle; Apply evicts too
	if has(idle) {
		t.Fatal("a stream is evicted after 2 h without events")
	}
	if !has(live) {
		t.Fatal("a live stream stays")
	}

	// Above MaxStreams the oldest last_seen goes first.
	for i := range MaxStreams {
		clk.Advance(time.Second)
		reg.Apply(mkBatch(fmt.Sprintf("stream%04d", i), 0, mkEvent(1, TypeHeartbeat)))
	}
	if n := len(reg.Streams()); n != MaxStreams {
		t.Fatalf("%d streams, want %d", n, MaxStreams)
	}
	if has(live) || !has("stream0000") || !has(fmt.Sprintf("stream%04d", MaxStreams-1)) {
		t.Fatal("the stream with the oldest last_seen must be the one evicted")
	}
}

// session.end also fires on /clear and /resume, after which the same mod
// load keeps reporting: a later applied event reopens the stream so it is
// not evicted 30 min later while alive.
func TestApply_EventAfterEndReopens(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	const s = "streamAAA"
	reg.Apply(mkBatch(s, 0, mkEvent(1, TypeSessionEnd)))
	reg.Apply(mkBatch(s, 0, mkEvent(1, TypeSessionEnd))) // a resend does not reopen
	if info := streamInfo(t, reg, s); !info.Ended {
		t.Fatalf("info = %+v", info)
	}
	reg.Apply(mkBatch(s, 0, mkEvent(2, TypeSessionClear)))
	if info := streamInfo(t, reg, s); info.Ended || !info.EndedAt.IsZero() {
		t.Fatalf("a later event must reopen the stream: %+v", info)
	}
	clk.Advance(40 * time.Minute)
	reg.Evict()
	if _, ok := reg.Events(s, 0); !ok {
		t.Fatal("a reopened stream is not evicted as ended")
	}
}

func TestBySID_FollowsClear(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	const s = "streamAAA"
	start := mkEvent(1, TypeSessionStart)
	start.Data = json.RawMessage(`{"cwd":"/work/repo","surface":"tui"}`)
	reg.Apply(mkBatch(s, 0, start))
	info, ok := reg.BySID(sidA)
	if !ok || info.Stream != s || info.CWD != "/work/repo" || !info.Interactive {
		t.Fatalf("BySID(A) = %+v, %v", info, ok)
	}

	clr := mkEvent(2, TypeSessionClear)
	clr.SID = sidB
	clr.Data = json.RawMessage(`{"prev_sid":"` + sidA + `"}`)
	reg.Apply(mkBatch(s, 0, clr))
	if info, ok := reg.BySID(sidB); !ok || info.Stream != s || info.SID != sidB {
		t.Fatalf("BySID(new sid) = %+v, %v", info, ok)
	}
	if _, ok := reg.BySID(sidA); ok {
		t.Fatal("the old sid must no longer find the stream")
	}

	// Two streams on one sid (a resumed session): the newest wins.
	clk.Advance(time.Minute)
	other := mkEvent(1, TypeHeartbeat)
	other.SID = sidB
	reg.Apply(mkBatch("streamOTH", 0, other))
	if info, _ := reg.BySID(sidB); info.Stream != "streamOTH" {
		t.Fatalf("BySID = %s, want the stream seen last", info.Stream)
	}
}

func TestStreams_SortedByLastSeen(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	for _, s := range []string{"streamAAA", "streamBBB", "streamCCC"} {
		clk.Advance(time.Second)
		reg.Apply(mkBatch(s, 0, mkEvent(1, TypeHeartbeat)))
	}
	clk.Advance(time.Second)
	reg.Apply(mkBatch("streamAAA", 0, mkEvent(2, TypeHeartbeat)))
	var got []string
	for _, s := range reg.Streams() {
		got = append(got, s.Stream)
	}
	if want := []string{"streamAAA", "streamCCC", "streamBBB"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Streams() = %v, want %v", got, want)
	}
	// Snapshots are copies.
	reg.Streams()[0].Counts[TypeHeartbeat] = 99
	if streamInfo(t, reg, "streamAAA").Counts[TypeHeartbeat] != 2 {
		t.Fatal("Streams() must return copies of counts")
	}
	if _, ok := reg.Events("streamZZZ", 0); ok {
		t.Fatal("unknown stream")
	}
	if evs, _ := reg.Events("streamAAA", 1); len(evs) != 1 || evs[0].Seq != 2 {
		t.Fatalf("Events after 1 = %+v", evs)
	}
}
