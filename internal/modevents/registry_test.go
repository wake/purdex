package modevents

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	if ack, err := reg.Apply(b); err != nil || ack != 3 {
		t.Fatalf("ack = %d, err = %v; want 3", ack, err)
	}
	if ack, err := reg.Apply(b); err != nil || ack != 3 {
		t.Fatalf("resent batch: ack = %d, err = %v; want 3", ack, err)
	}
	want := []string{s + "#1", s + "#2", s + "#3"}
	if got := rec.list(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %v, want %v (a resent batch is delivered once)", got, want)
	}
	// A batch overlapping the applied ones delivers only the new tail.
	if ack, err := reg.Apply(mkBatch(s, 0, mkEvent(2, TypeToolStart), mkEvent(3, TypeToolEnd), mkEvent(4, TypeTurnComplete))); err != nil || ack != 4 {
		t.Fatalf("overlap: ack = %d, err = %v; want 4", ack, err)
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
	if ack, err := reg.Apply(mkBatch(s, 0, mkEvent(5, TypeHeartbeat))); err != nil || ack != 5 {
		t.Fatalf("ack = %d, err = %v; want 5 (a gap is applied)", ack, err)
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
	ack, err := reg.Apply(mkBatch(s, 0, mkEvent(1, TypeTurnStart), mkEvent(2, "turn.step"), mkEvent(3, TypeHeartbeat)))
	if err != nil || ack != 3 {
		t.Fatalf("ack = %d, err = %v; want 3 (an unknown type is applied)", ack, err)
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

// tool.approved (U1-2a-1) is a known type: counted under its own name,
// kept in the ring and delivered.
func TestApply_ToolApprovedIsDelivered(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	rec := &recorder{}
	reg.Subscribe(rec.fn)
	const s = "streamAPP"
	approved := mkEvent(2, TypeToolApproved)
	approved.Data = json.RawMessage(`{"tool_use_id":"tu-1"}`)
	if _, err := reg.Apply(mkBatch(s, 0, mkEvent(1, TypeToolCheck), approved)); err != nil {
		t.Fatal(err)
	}
	if got, want := rec.list(), []string{s + "#1", s + "#2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %v, want %v", got, want)
	}
	if info := streamInfo(t, reg, s); info.Counts[TypeToolApproved] != 1 || info.Counts[CountUnknown] != 0 {
		t.Fatalf("counts = %v", info.Counts)
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
	if ack, err := reg.Apply(mkBatch(s, 0, mkEvent(1, TypeTurnStart), mkEvent(2, TypeTurnComplete))); err != nil || ack != 2 {
		t.Fatalf("ack = %d, err = %v; want 2", ack, err)
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

func endEvent(seq int64, reason string) Event {
	e := mkEvent(seq, TypeSessionEnd)
	e.Data = json.RawMessage(`{"reason":"` + reason + `"}`)
	return e
}

// session.end also fires on /clear and /resume; the same process and
// stream go on (session.switch follows), so those reasons do not end it.
func TestApply_SessionEndOnClearDoesNotEnd(t *testing.T) {
	for _, reason := range []string{"clear", "resume"} {
		clk := newFakeClock()
		reg := NewRegistry(clk.Now)
		const s = "streamAAA"
		sw := mkEvent(3, TypeSessionSwitch)
		sw.SID = sidB
		sw.Data = json.RawMessage(`{"prev_sid":"` + sidA + `","source":"` + reason + `"}`)
		reg.Apply(mkBatch(s, 0, mkEvent(1, TypeSessionStart), endEvent(2, reason)))
		if info := streamInfo(t, reg, s); info.Ended || !info.EndedAt.IsZero() {
			t.Fatalf("%s: session.end must not end the stream: %+v", reason, info)
		}
		reg.Apply(mkBatch(s, 0, sw))
		clk.Advance(31 * time.Minute)
		reg.Evict()
		info, ok := reg.BySID(sidB)
		if !ok || info.Stream != s || info.Ended {
			t.Fatalf("%s: the stream must stay, on the new sid, 30 min later: %+v, %v", reason, info, ok)
		}
	}

	// Any other reason ends it, and it goes 30 min later.
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	reg.Apply(mkBatch("streamEND", 0, endEvent(1, "prompt_input_exit")))
	if info := streamInfo(t, reg, "streamEND"); !info.Ended || !info.EndedAt.Equal(clk.Now()) {
		t.Fatalf("an exit must end the stream: %+v", info)
	}
	clk.Advance(31 * time.Minute)
	reg.Evict()
	if _, ok := reg.Events("streamEND", 0); ok {
		t.Fatal("an ended stream is evicted 30 min later")
	}
}

// A stream that reports again after it ended (any later applied event) is
// alive: it is reopened, so it is not evicted 30 min later as ended.
func TestApply_EventAfterEndReopens(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	const s = "streamAAA"
	reg.Apply(mkBatch(s, 0, endEvent(1, "other")))
	reg.Apply(mkBatch(s, 0, endEvent(1, "other"))) // a resend does not reopen
	if info := streamInfo(t, reg, s); !info.Ended {
		t.Fatalf("info = %+v", info)
	}
	reg.Apply(mkBatch(s, 0, mkEvent(2, TypeHeartbeat)))
	if info := streamInfo(t, reg, s); info.Ended || !info.EndedAt.IsZero() {
		t.Fatalf("a later event must reopen the stream: %+v", info)
	}
	clk.Advance(40 * time.Minute)
	reg.Evict()
	if _, ok := reg.Events(s, 0); !ok {
		t.Fatal("a reopened stream is not evicted as ended")
	}
}

func TestBySID_FollowsSwitch(t *testing.T) {
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

	// /clear: session.end{clear}, then session.switch carrying the new sid.
	sw := mkEvent(3, TypeSessionSwitch)
	sw.SID = sidB
	sw.Data = json.RawMessage(`{"prev_sid":"` + sidA + `","source":"clear"}`)
	reg.Apply(mkBatch(s, 0, endEvent(2, "clear"), sw))
	if info, ok := reg.BySID(sidB); !ok || info.Stream != s || info.SID != sidB || info.Ended {
		t.Fatalf("BySID(new sid) = %+v, %v", info, ok)
	}
	if _, ok := reg.BySID(sidA); ok {
		t.Fatal("the old sid must no longer find the stream")
	}

	// /resume back to the first session: the stream follows again.
	back := mkEvent(5, TypeSessionSwitch)
	back.Data = json.RawMessage(`{"prev_sid":"` + sidB + `","source":"resume"}`)
	reg.Apply(mkBatch(s, 0, endEvent(4, "resume"), back))
	if info, ok := reg.BySID(sidA); !ok || info.Stream != s {
		t.Fatalf("BySID after resume = %+v, %v", info, ok)
	}

	// Two streams on one sid (a session resumed elsewhere): the newest wins.
	clk.Advance(time.Minute)
	reg.Apply(mkBatch("streamOTH", 0, mkEvent(1, TypeHeartbeat))) // also on sidA
	if info, _ := reg.BySID(sidA); info.Stream != "streamOTH" {
		t.Fatalf("BySID = %s, want the stream seen last", info.Stream)
	}
}

// After a daemon restart a live stream resumes with heartbeats only: no
// session.start, yet the envelope names its cwd and that it is
// interactive, from the first delivery on.
// Mutation gate: drop the envelope copy in Apply → red.
func TestApply_EnvelopeFillsCwdAndInteractive(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	var mu sync.Mutex
	var seen []StreamInfo
	reg.Subscribe(func(info StreamInfo, _ Event) {
		mu.Lock()
		seen = append(seen, info)
		mu.Unlock()
	})
	const s = "streamRST"
	b := mkBatch(s, 0, mkEvent(41, TypeHeartbeat), mkEvent(42, TypeHeartbeat))
	b.CWD, b.Interactive = "/work/repo", true
	if _, err := reg.Apply(b); err != nil {
		t.Fatal(err)
	}
	if info := streamInfo(t, reg, s); info.CWD != "/work/repo" || !info.Interactive {
		t.Fatalf("cwd / interactive = %q / %v, want /work/repo / true", info.CWD, info.Interactive)
	}
	if info, ok := reg.BySID(sidA); !ok || info.CWD != "/work/repo" || !info.Interactive {
		t.Fatalf("BySID = %+v, %v", info, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("delivered %d events, want 2", len(seen))
	}
	for i, info := range seen {
		if info.CWD != "/work/repo" || !info.Interactive {
			t.Fatalf("delivery %d carried cwd / interactive = %q / %v", i, info.CWD, info.Interactive)
		}
	}
}

// A batch without the envelope fields (an older mod) keeps what
// session.start set; an envelope with them agrees with it.
func TestApply_EnvelopeWithoutCwdKeepsSessionStartCwd(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	const s = "streamOLD"
	start := mkEvent(1, TypeSessionStart)
	start.Data = json.RawMessage(`{"cwd":"/work/repo","surface":"terminal"}`)
	if _, err := reg.Apply(mkBatch(s, 0, start)); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Apply(mkBatch(s, 0, mkEvent(2, TypeHeartbeat))); err != nil { // cwd "", interactive false
		t.Fatal(err)
	}
	if info := streamInfo(t, reg, s); info.CWD != "/work/repo" || !info.Interactive {
		t.Fatalf("after an envelope without the fields: cwd / interactive = %q / %v", info.CWD, info.Interactive)
	}
	b := mkBatch(s, 0, mkEvent(3, TypeHeartbeat))
	b.CWD, b.Interactive = "/work/repo", true
	if _, err := reg.Apply(b); err != nil {
		t.Fatal(err)
	}
	if info := streamInfo(t, reg, s); info.CWD != "/work/repo" || !info.Interactive {
		t.Fatalf("after a full envelope: cwd / interactive = %q / %v", info.CWD, info.Interactive)
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

// Event data is copied on receipt, per subscriber and per read, so neither
// the poster, a subscriber nor a reader can alter the stored history.
func TestRegistry_EventDataIsOwned(t *testing.T) {
	reg := NewRegistry(newFakeClock().Now)
	const s = "streamAAA"
	const orig = `{"turn_id":"t1"}`
	scribble := func(b []byte) {
		for i := range b {
			b[i] = 'X'
		}
	}
	// The first subscriber scribbles over what it receives; the second must
	// still get the original bytes.
	reg.Subscribe(func(_ StreamInfo, e Event) { scribble(e.Data) })
	var second []string
	reg.Subscribe(func(_ StreamInfo, e Event) { second = append(second, string(e.Data)) })

	src := mkEvent(1, TypeTurnStart)
	src.Data = json.RawMessage(orig)
	b := mkBatch(s, 0, src)
	if ack, err := reg.Apply(b); err != nil || ack != 1 {
		t.Fatalf("ack = %d, err = %v; want 1", ack, err)
	}
	scribble(b.Events[0].Data) // the poster reuses its buffer
	if want := []string{orig}; !reflect.DeepEqual(second, want) {
		t.Fatalf("second subscriber got %q, want %q", second, want)
	}
	evs, _ := reg.Events(s, 0)
	if len(evs) != 1 || string(evs[0].Data) != orig {
		t.Fatalf("Events = %+v after the source and a subscriber were mutated, want data %s", evs, orig)
	}
	scribble(evs[0].Data) // a reader mutates its result
	evs, _ = reg.Events(s, 0)
	if len(evs) != 1 || string(evs[0].Data) != orig {
		t.Fatalf("Events = %+v after a reader mutated its result, want data %s", evs, orig)
	}
}

// pinAll fills reg with MaxStreams streams, "pinned0000" to "pinned0255",
// whose deliveries block in a subscriber, so each is pinned by its own
// Apply. Stream i is seen 1+i seconds after the clock's start, so
// pinned0000 has the oldest last_seen. Deliveries of every other stream go
// to rec. release unblocks the subscribers and waits for the pinned Applies
// to return.
func pinAll(t *testing.T, reg *Registry, clk *fakeClock) (rec *recorder, release func()) {
	t.Helper()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	t.Cleanup(func() { unblockOnce.Do(func() { close(unblock) }) })
	rec = &recorder{}
	reg.Subscribe(func(info StreamInfo, e Event) {
		if strings.HasPrefix(info.Stream, "pinned") {
			entered <- struct{}{}
			<-unblock
			return
		}
		rec.fn(info, e)
	})
	errs := make(chan error, MaxStreams)
	for i := range MaxStreams {
		clk.Advance(time.Second)
		go func() {
			_, err := reg.Apply(mkBatch(fmt.Sprintf("pinned%04d", i), 0, mkEvent(1, TypeHeartbeat)))
			errs <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("pinned%04d did not reach its subscriber", i)
		}
	}
	return rec, func() {
		t.Helper()
		unblockOnce.Do(func() { close(unblock) })
		for range MaxStreams {
			select {
			case err := <-errs:
				if err != nil {
					t.Errorf("pinned Apply: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a pinned Apply did not return after the release")
			}
		}
	}
}

// MaxStreams is a hard limit: with every stream in the middle of Apply,
// a batch for a new stream is refused, and nothing is added or delivered.
func TestApply_HardCapWhenAllPinned(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	rec, release := pinAll(t, reg, clk)

	const extra = 10
	errs := make([]error, extra)
	var wg sync.WaitGroup
	for i := range extra {
		wg.Go(func() {
			_, errs[i] = reg.Apply(mkBatch(fmt.Sprintf("extra%04d", i), 0, mkEvent(1, TypeTurnStart)))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, ErrRegistryFull) {
			t.Errorf("extra%04d: err = %v, want ErrRegistryFull", i, err)
		}
	}
	if n := len(reg.Streams()); n != MaxStreams {
		t.Fatalf("%d streams with every stream pinned, want %d", n, MaxStreams)
	}
	if _, ok := reg.Events("extra0000", 0); ok {
		t.Fatal("a refused stream must not be added")
	}
	if got := rec.list(); len(got) != 0 {
		t.Fatalf("deliveries = %v: a refused batch must not be delivered", got)
	}

	release()
	clk.Advance(time.Second)
	if ack, err := reg.Apply(mkBatch("streamNEW", 0, mkEvent(1, TypeHeartbeat))); err != nil || ack != 1 {
		t.Fatalf("after the release: ack = %d, err = %v; want 1, nil", ack, err)
	}
	has := func(id string) bool {
		_, ok := reg.Events(id, 0)
		return ok
	}
	if n := len(reg.Streams()); n != MaxStreams {
		t.Fatalf("%d streams, want %d", n, MaxStreams)
	}
	if !has("streamNEW") || has("pinned0000") || !has("pinned0001") {
		t.Fatal("the new stream must be admitted by evicting the oldest last_seen (pinned0000)")
	}
	if got, want := rec.list(), []string{"streamNEW#1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %v, want %v", got, want)
	}
}

// Reject for a new stream is a no-op when the registry is full and no
// stream can be evicted; a known stream still counts its rejection.
func TestReject_NoOpWhenFull(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	_, release := pinAll(t, reg, clk)

	reg.Reject("streamNEW")
	if n := len(reg.Streams()); n != MaxStreams {
		t.Fatalf("%d streams after a Reject with every stream pinned, want %d", n, MaxStreams)
	}
	if _, ok := reg.Events("streamNEW", 0); ok {
		t.Fatal("Reject must not add a stream when none can be evicted")
	}
	reg.Reject("pinned0005")
	if info := streamInfo(t, reg, "pinned0005"); info.Rejected != 1 {
		t.Fatalf("rejected = %d on a known stream, want 1", info.Rejected)
	}

	release()
	clk.Advance(time.Second)
	reg.Reject("streamNEW")
	if info := streamInfo(t, reg, "streamNEW"); info.Rejected != 1 {
		t.Fatalf("after the release: info = %+v, want rejected 1", info)
	}
	if _, ok := reg.Events("pinned0000", 0); ok || len(reg.Streams()) != MaxStreams {
		t.Fatal("after the release Reject admits the stream by evicting the oldest last_seen")
	}
}
