package modevents

import (
	"encoding/json"
	"log"
	"maps"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry limits (spec §6.4).
const (
	RingSize   = 256              // applied known events kept per stream
	MaxStreams = 256              // streams kept; the oldest last_seen goes first
	EndedTTL   = 30 * time.Minute // an ended stream is kept this long after session.end
	IdleTTL    = 2 * time.Hour    // any stream is kept this long after its last batch
)

// CountUnknown is the Counts key for event types this daemon does not know.
const CountUnknown = "unknown"

// StreamInfo is a snapshot of one mod stream. Counts is a copy the caller
// owns.
type StreamInfo struct {
	Stream       string
	Agent        string
	SID          string // the latest event's sid (moves on /clear)
	CWD          string // from session.start
	Interactive  bool   // a session.start was seen
	CCVersion    string
	ModVersion   string
	FirstSeen    time.Time // set once, when the registry first heard of the stream
	LastSeen     time.Time
	LastSeq      int64
	Gaps         int64
	DroppedTotal int64
	Rejected     int64
	Ended        bool
	EndedAt      time.Time
	Counts       map[string]int64
}

func (i StreamInfo) clone() StreamInfo {
	i.Counts = maps.Clone(i.Counts)
	return i
}

// Registry holds the live mod streams in memory and delivers their known
// events to subscribers. Use NewRegistry; it is safe for concurrent use.
//
// Locking: mu guards the stream map and every stream's state. Each stream
// also has an order mutex that one Apply holds from before its state
// update until its last delivery returns, so a stream's events reach
// subscribers in seq order while other streams, the readers and eviction
// (which only take mu) carry on.
type Registry struct {
	now func() time.Time

	mu      sync.Mutex
	streams map[string]*stream

	subMu sync.Mutex
	subs  []*subscriber // copy-on-write: replaced, never mutated in place
}

type stream struct {
	order sync.Mutex // held by one Apply across its update and delivery

	// Guarded by Registry.mu.
	info StreamInfo
	ring [RingSize]Event
	head int // index of the oldest ring entry
	n    int // ring entries in use
	pins int // Apply calls holding this stream; eviction skips it while > 0
}

type subscriber struct {
	fn   func(StreamInfo, Event)
	live atomic.Bool
}

type delivery struct {
	info StreamInfo
	ev   Event
}

// NewRegistry returns an empty registry reading time from now.
func NewRegistry(now func() time.Time) *Registry {
	return &Registry{now: now, streams: make(map[string]*stream)}
}

// Apply applies one decoded batch and returns the stream's highest applied
// seq (the ack). Events at or below that seq are retries and are skipped;
// a seq jump counts a gap and is applied. Known events are kept in the
// stream's ring and delivered to every subscriber synchronously, in seq
// order, before Apply returns; the next Apply of the same stream waits for
// that delivery.
func (r *Registry) Apply(b Batch) int64 {
	r.mu.Lock()
	r.evictLocked(r.now())
	s := r.admitLocked(b.Stream)
	s.pins++
	r.mu.Unlock()

	s.order.Lock()
	defer s.order.Unlock()
	defer func() {
		r.mu.Lock()
		s.pins--
		r.mu.Unlock()
	}()

	r.mu.Lock()
	now := r.now()
	in := &s.info
	in.Agent, in.CCVersion, in.ModVersion = b.Agent, b.CCVersion, b.ModVersion
	in.LastSeen = now
	in.DroppedTotal = max(in.DroppedTotal, b.DroppedTotal)
	var out []delivery
	for _, e := range b.Events {
		if e.Seq <= in.LastSeq {
			continue
		}
		// A stream new to this registry (the daemon restarted under a
		// running mod) starts where the mod's queue does: not a gap.
		if in.LastSeq > 0 && e.Seq > in.LastSeq+1 {
			in.Gaps++
		}
		in.LastSeq = e.Seq
		in.SID = e.SID
		// session.end also fires on /clear and /resume, after which the
		// same mod load keeps reporting: any later event reopens the stream.
		if in.Ended && e.Type != TypeSessionEnd {
			in.Ended, in.EndedAt = false, time.Time{}
		}
		switch e.Type {
		case TypeSessionStart:
			var d struct {
				CWD string `json:"cwd"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				in.CWD = d.CWD
			}
			in.Interactive = true
		case TypeSessionEnd:
			in.Ended, in.EndedAt = true, now
		}
		if !IsKnownType(e.Type) {
			in.Counts[CountUnknown]++
			continue
		}
		in.Counts[e.Type]++
		s.push(e)
		out = append(out, delivery{info: in.clone(), ev: e})
	}
	ack := in.LastSeq
	r.mu.Unlock()

	r.deliver(out) // still under s.order
	return ack
}

// Reject counts a 400-rejected batch on a valid stream id, creating the
// stream (under the same cap as Apply) when it is new.
func (r *Registry) Reject(streamID string) {
	if !ValidStream(streamID) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.evictLocked(now)
	s := r.admitLocked(streamID)
	s.info.Rejected++
	s.info.LastSeen = now
}

// admitLocked returns the stream, creating it with first_seen = last_seen
// = now when new; a new stream past MaxStreams evicts the others with the
// oldest last_seen. r.mu must be held.
func (r *Registry) admitLocked(id string) *stream {
	if s, ok := r.streams[id]; ok {
		return s
	}
	now := r.now()
	s := &stream{info: StreamInfo{Stream: id, FirstSeen: now, LastSeen: now, Counts: map[string]int64{}}}
	r.streams[id] = s
	if len(r.streams) > MaxStreams {
		var cands []*stream
		for _, c := range r.streams {
			if c != s && c.pins == 0 {
				cands = append(cands, c)
			}
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].info.LastSeen.Before(cands[j].info.LastSeen) })
		for _, c := range cands {
			if len(r.streams) <= MaxStreams {
				break
			}
			delete(r.streams, c.info.Stream)
		}
	}
	return s
}

// Evict drops ended streams EndedTTL after session.end and any stream
// IdleTTL after its last batch. Apply and Reject run it too; the module
// also runs it on a ticker.
func (r *Registry) Evict() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked(r.now())
}

func (r *Registry) evictLocked(now time.Time) {
	for id, s := range r.streams {
		if s.pins > 0 {
			continue
		}
		if (s.info.Ended && !now.Before(s.info.EndedAt.Add(EndedTTL))) || !now.Before(s.info.LastSeen.Add(IdleTTL)) {
			delete(r.streams, id)
		}
	}
}

func (s *stream) push(e Event) {
	if s.n < RingSize {
		s.ring[(s.head+s.n)%RingSize] = e
		s.n++
		return
	}
	s.ring[s.head] = e
	s.head = (s.head + 1) % RingSize
}

// Subscribe registers fn for every known event applied from now on. fn
// runs synchronously inside Apply and holds that stream's ordering, so it
// must not block; a panic in it is recovered and logged. cancel stops
// further deliveries and may be called more than once.
func (r *Registry) Subscribe(fn func(StreamInfo, Event)) (cancel func()) {
	sub := &subscriber{fn: fn}
	sub.live.Store(true)
	r.subMu.Lock()
	r.subs = append(append([]*subscriber(nil), r.subs...), sub)
	r.subMu.Unlock()
	return func() {
		if !sub.live.Swap(false) {
			return
		}
		r.subMu.Lock()
		next := make([]*subscriber, 0, len(r.subs))
		for _, x := range r.subs {
			if x != sub {
				next = append(next, x)
			}
		}
		r.subs = next
		r.subMu.Unlock()
	}
}

func (r *Registry) deliver(out []delivery) {
	if len(out) == 0 {
		return
	}
	r.subMu.Lock()
	subs := r.subs
	r.subMu.Unlock()
	for _, d := range out {
		for _, sub := range subs {
			if sub.live.Load() {
				callSubscriber(sub.fn, d.info, d.ev)
			}
		}
	}
}

func callSubscriber(fn func(StreamInfo, Event), info StreamInfo, e Event) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[modevents] subscriber panic on %s seq %d (%s): %v", info.Stream, e.Seq, e.Type, p)
		}
	}()
	fn(info, e)
}

// Streams returns every stream, the most recently seen first.
func (r *Registry) Streams() []StreamInfo {
	r.mu.Lock()
	out := make([]StreamInfo, 0, len(r.streams))
	for _, s := range r.streams {
		out = append(out, s.info.clone())
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Stream < out[j].Stream
	})
	return out
}

// Events returns the stream's ring entries with seq > after, oldest first;
// ok is false when the stream is unknown.
func (r *Registry) Events(streamID string, after int64) (events []Event, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.streams[streamID]
	if !ok {
		return nil, false
	}
	events = []Event{}
	for i := range s.n {
		if e := s.ring[(s.head+i)%RingSize]; e.Seq > after {
			events = append(events, e)
		}
	}
	return events, true
}

// BySID returns the stream whose latest sid is sid; when several match,
// the one seen most recently.
func (r *Registry) BySID(sid string) (StreamInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *stream
	for _, s := range r.streams {
		if s.info.SID != sid {
			continue
		}
		if best == nil || s.info.LastSeen.After(best.info.LastSeen) ||
			(s.info.LastSeen.Equal(best.info.LastSeen) && s.info.FirstSeen.After(best.info.FirstSeen)) {
			best = s
		}
	}
	if best == nil {
		return StreamInfo{}, false
	}
	return best.info.clone(), true
}
