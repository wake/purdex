package modevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry limits (spec §6.4).
const (
	RingSize   = 256              // applied known events kept per stream
	MaxStreams = 256              // hard limit on streams held; see Apply
	EndedTTL   = 30 * time.Minute // an ended stream is kept this long after session.end
	IdleTTL    = 2 * time.Hour    // any stream is kept this long after its last batch
)

// CountUnknown is the Counts key for event types this daemon does not know.
const CountUnknown = "unknown"

// ErrRegistryFull is Apply's error for a batch on a stream the registry
// does not know while it holds MaxStreams streams and none can be evicted
// (every one is in the middle of an Apply). The handler answers 503
// registry_full; the mod backs off and resends.
var ErrRegistryFull = errors.New("modevents: registry full")

// StreamInfo is a snapshot of one mod stream. Counts is a copy the caller
// owns.
type StreamInfo struct {
	Stream       string
	Agent        string
	SID          string // the latest event's sid (moves on session.switch)
	CWD          string // from session.start or any batch's envelope
	Interactive  bool   // a session.start was seen, or an envelope said so
	CCVersion    string
	ModVersion   string
	FirstSeen    time.Time // set once, when the registry first heard of the stream
	LastSeen     time.Time
	LastSeq      int64
	Gaps         int64
	DroppedTotal int64
	Caps         []string  // from the latest batch that named any
	CapsAt       time.Time // when that batch arrived
	Rejected     int64
	Ended        bool // a session.end other than /clear or /resume; a later event clears it
	EndedAt      time.Time
	Counts       map[string]int64
}

func (i StreamInfo) clone() StreamInfo {
	i.Counts = maps.Clone(i.Counts)
	i.Caps = slices.Clone(i.Caps)
	return i
}

// clone returns e with its own copy of Data.
func (e Event) clone() Event {
	e.Data = bytes.Clone(e.Data)
	return e
}

// Registry holds the live mod streams in memory and delivers their known
// events to subscribers. Use NewRegistry; it is safe for concurrent use.
//
// Locking: mu guards the stream map and every stream's state. Each stream
// also has an order mutex that one Apply holds from before its state
// update until its last delivery returns, so a stream's events reach
// subscribers in seq order while other streams, the readers and eviction
// (which only take mu) carry on. The order mutex is not re-entrant: a
// subscriber must never lead back into Apply (see Subscribe).
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
// stream's ring, with their own copy of Data, and delivered to every
// subscriber synchronously, in seq order, before Apply returns; the next
// Apply of the same stream waits for that delivery. Delivery holds the
// stream's order mutex, which is not re-entrant: a subscriber that calls
// Apply on this Registry, directly or indirectly, deadlocks (see
// Subscribe).
//
// MaxStreams is a hard limit. A batch for a stream the registry does not
// know, while it holds MaxStreams streams, evicts the stream with the
// oldest last_seen that no Apply holds; when every stream is held it is
// refused with ErrRegistryFull, and nothing is added or delivered.
func (r *Registry) Apply(b Batch) (ack int64, err error) {
	r.mu.Lock()
	r.evictLocked(r.now())
	s, ok := r.admitLocked(b.Stream)
	if !ok {
		r.mu.Unlock()
		return 0, ErrRegistryFull
	}
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
	// A batch without caps (an older mod, or a mod that lost the feature) leaves the last ones to age out.
	if len(b.Caps) > 0 {
		in.Caps, in.CapsAt = slices.Clone(b.Caps), now
	}
	// Every batch of a U1-2a-1 mod names the cwd and that the session is
	// interactive, so a stream first heard after a daemon restart (no
	// session.start) has both from its first delivery. An older mod sends
	// neither, and what session.start set stays.
	if b.CWD != "" {
		in.CWD = b.CWD
	}
	if b.Interactive {
		in.Interactive = true
	}
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
		// A stream that reports again after it ended is alive: any later
		// event reopens it (an ending session.end ends it again below).
		in.Ended, in.EndedAt = false, time.Time{}
		switch e.Type {
		case TypeSessionStart:
			var d struct {
				CWD string `json:"cwd"`
			}
			// An empty cwd never erases one already known (the envelope's).
			if json.Unmarshal(e.Data, &d) == nil && d.CWD != "" {
				in.CWD = d.CWD
			}
			in.Interactive = true
		case TypeSessionEnd:
			if endsStream(e.Data) {
				in.Ended, in.EndedAt = true, now
			}
		}
		if !IsKnownType(e.Type) {
			in.Counts[CountUnknown]++
			continue
		}
		in.Counts[e.Type]++
		e = e.clone() // the caller may reuse the batch's buffers
		s.push(e)
		out = append(out, delivery{info: in.clone(), ev: e})
	}
	ack = in.LastSeq
	r.mu.Unlock()

	r.deliver(out) // still under s.order
	return ack, nil
}

// endsStream reports whether a session.end ends its stream. It also fires
// on /clear and /resume (reason clear / resume), after which the same
// process and stream go on with a session.switch; every other reason,
// including a missing one, ends it.
func endsStream(data json.RawMessage) bool {
	var d struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(data, &d)
	return d.Reason != "clear" && d.Reason != "resume"
}

// Reject counts a 400-rejected batch on a valid stream id, creating the
// stream (under the same hard cap as Apply) when it is new. A new stream
// that the cap refuses is not counted anywhere.
func (r *Registry) Reject(streamID string) {
	if !ValidStream(streamID) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.evictLocked(now)
	s, ok := r.admitLocked(streamID)
	if !ok {
		return
	}
	s.info.Rejected++
	s.info.LastSeen = now
}

// admitLocked returns the stream, creating it with first_seen = last_seen
// = now when new. A new stream at MaxStreams first evicts the stream with
// the oldest last_seen that no Apply holds; when every stream is held, ok
// is false and nothing changes. r.mu must be held.
func (r *Registry) admitLocked(id string) (s *stream, ok bool) {
	if s, ok := r.streams[id]; ok {
		return s, true
	}
	for len(r.streams) >= MaxStreams {
		var oldest *stream
		for _, c := range r.streams {
			if c.pins == 0 && (oldest == nil || c.info.LastSeen.Before(oldest.info.LastSeen)) {
				oldest = c
			}
		}
		if oldest == nil {
			return nil, false
		}
		delete(r.streams, oldest.info.Stream)
	}
	now := r.now()
	s = &stream{info: StreamInfo{Stream: id, FirstSeen: now, LastSeen: now, Counts: map[string]int64{}}}
	r.streams[id] = s
	return s, true
}

// CapsFresh is how recently a stream must have announced a capability for it to count (plan D11).
const CapsFresh = 30 * time.Second

// SessionCapable reports whether a live stream whose current session id is sid announced capability c within `within`.
func (r *Registry) SessionCapable(sid, c string, within time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for _, s := range r.streams {
		in := &s.info
		if in.SID == sid && !in.Ended && !in.CapsAt.IsZero() && now.Sub(in.CapsAt) <= within && slices.Contains(in.Caps, c) {
			return true
		}
	}
	return false
}

// CapableSession is a session id whose live stream announced a capability, and when (the latest batch that named it).
type CapableSession struct {
	SID string
	At  time.Time
}

// CapableSessions lists the current session ids of the live streams that announced capability c within `within`. A
// session served by two streams is listed once per stream (the caller takes the newest).
func (r *Registry) CapableSessions(c string, within time.Duration) []CapableSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var out []CapableSession
	for _, s := range r.streams {
		in := &s.info
		if in.SID != "" && !in.Ended && !in.CapsAt.IsZero() && now.Sub(in.CapsAt) <= within && slices.Contains(in.Caps, c) {
			out = append(out, CapableSession{SID: in.SID, At: in.CapsAt})
		}
	}
	return out
}

// NewestCapableStream is the live stream whose current session is sid and that announced capability c most recently
// within `within` (ties: the stream the registry heard of last): the one stream that owns a per-session job channel such
// as the prompt queue (U3-0b). false when none.
func (r *Registry) NewestCapableStream(sid, c string, within time.Duration) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var best *StreamInfo
	for _, s := range r.streams {
		in := &s.info
		if in.SID != sid || in.Ended || in.CapsAt.IsZero() || now.Sub(in.CapsAt) > within || !slices.Contains(in.Caps, c) {
			continue
		}
		if best == nil || in.CapsAt.After(best.CapsAt) || (in.CapsAt.Equal(best.CapsAt) && in.FirstSeen.After(best.FirstSeen)) {
			best = in
		}
	}
	if best == nil {
		return "", false
	}
	return best.Stream, true
}

// StreamCapable is SessionCapable for one named stream: that stream is live, its current session is sid, and it announced
// c within `within`. The workbook routes use it so a caller cannot take work for a session through a stream that is not
// that session's.
func (r *Registry) StreamCapable(stream, sid, c string, within time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.streams[stream]
	if !ok {
		return false
	}
	in := &s.info
	return in.SID == sid && !in.Ended && !in.CapsAt.IsZero() && r.now().Sub(in.CapsAt) <= within && slices.Contains(in.Caps, c)
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

// Subscribe registers fn for every known event applied from now on; each
// call gets its own copy of the event's Data.
//
// fn runs synchronously inside Apply, while Apply holds that stream's
// order mutex. fn must not block, and must not call Apply on the same
// Registry, directly or indirectly (for example by handing the work to
// another goroutine and waiting for it): the order mutex is not
// re-entrant, so doing so deadlocks. Every subscriber is checked for this
// when it is wired.
//
// A panic in fn is recovered and logged. cancel stops further deliveries
// and may be called more than once.
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
				callSubscriber(sub.fn, d.info, d.ev.clone())
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

// Events returns the stream's ring entries with seq > after, oldest first,
// each with its own copy of Data; ok is false when the stream is unknown.
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
			events = append(events, e.clone())
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
