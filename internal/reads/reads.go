// Package reads is the vocabulary of read sync (badge spec §7): the hook cursor a read is "through", the rule for what a read
// covers, and the daemon's bounded in-memory record of the last read of each session. It has no dependencies, so the reads
// module (route, WebSocket frames) and the push module (reads carried on pushes) can share it.
package reads

import (
	"container/list"
	"sync"
)

// Cursor is a hook cursor: the (epoch, seq) the daemon stamps on every `hook` frame. It orders the events of one daemon
// process exactly, with no clock; a new epoch (a restart) makes older cursors incomparable.
type Cursor struct {
	Epoch string `json:"epoch"`
	Seq   uint64 `json:"seq"`
}

// Covers reports whether a read through c clears an unread that was set by the event with cursor unread: the same epoch and
// unread.Seq ≤ c.Seq. Another epoch never clears anything. An unread with no cursor (epoch "", set by a frame that is not from
// the emit slot, or before this feature) is covered by any read of its code.
func (c Cursor) Covers(unread Cursor) bool {
	if unread.Epoch == "" {
		return true
	}
	return c.Epoch == unread.Epoch && unread.Seq <= c.Seq
}

// Read is one session read: the session code and the cursor it is through.
type Read struct {
	Code    string `json:"code"`
	Through Cursor `json:"through"`
}

// FeedKey is the service-registry key under which the reads module publishes its Feed.
const FeedKey = "reads.feed"

// Feed is what the push module subscribes to: every read the daemon accepted (an advance of lastRead), from any device. fn
// runs on a goroutine of its own and may block briefly; reads reach it in the order they were accepted.
type Feed interface {
	SubscribeReads(fn func(Read)) (unsubscribe func())
}

// DefaultMax bounds the store: the codes beyond it evict the least recently updated.
const DefaultMax = 4096

// Store is lastRead: the largest cursor read per session code, in memory only (never on disk, forgotten on restart).
type Store struct {
	mu      sync.Mutex
	max     int
	epoch   string // the daemon's current epoch, as far as it has seen one
	byCode  map[string]*list.Element
	updated *list.List // front = least recently updated
}

type entry struct {
	code string
	at   Cursor
}

// NewStore makes a store of at most max codes (DefaultMax when max ≤ 0).
func NewStore(max int) *Store {
	if max <= 0 {
		max = DefaultMax
	}
	return &Store{max: max, byCode: map[string]*list.Element{}, updated: list.New()}
}

// SetCurrentEpoch tells the store which epoch the daemon is in now (the newest one a hook frame carried), so a read from
// that epoch can replace one from an older epoch; without it no epoch is known to be newer.
func (s *Store) SetCurrentEpoch(epoch string) {
	s.mu.Lock()
	s.epoch = epoch
	s.mu.Unlock()
}

// Advance records a read of code through t and reports whether it moved lastRead forward. Only an advance is an update (it
// makes the code the most recent for eviction, and is announced). A smaller or equal T never replaces the current one, nor
// does the T of another epoch unless it is the current epoch's and replaces an older one's; a cursorless T is recorded only
// for a code with no read yet, and any cursor replaces a cursorless one.
func (s *Store) Advance(code string, t Cursor) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.byCode[code]
	if !ok {
		s.byCode[code] = s.updated.PushBack(&entry{code: code, at: t})
		for s.updated.Len() > s.max {
			oldest := s.updated.Front()
			delete(s.byCode, oldest.Value.(*entry).code)
			s.updated.Remove(oldest)
		}
		return true
	}
	e := el.Value.(*entry)
	switch {
	case t.Epoch == "":
		return false // nothing to add to a read that exists
	case e.at.Epoch == "":
		// any cursor beats none
	case e.at.Epoch == t.Epoch:
		if t.Seq <= e.at.Seq {
			return false
		}
	default: // two real epochs: only the current epoch's T may replace an older one
		if t.Epoch != s.epoch {
			return false
		}
	}
	e.at = t
	s.updated.MoveToBack(el)
	return true
}

// Get is the last read of code.
func (s *Store) Get(code string) (Cursor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.byCode[code]; ok {
		return el.Value.(*entry).at, true
	}
	return Cursor{}, false
}

// Len is how many codes the store holds.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updated.Len()
}

// Snapshot is every remembered read, least recently updated first; never nil.
func (s *Store) Snapshot() []Read {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Read, 0, s.updated.Len())
	for el := s.updated.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		out = append(out, Read{Code: e.code, Through: e.at})
	}
	return out
}

// Prune deletes the codes for which keep is false and returns how many went.
func (s *Store) Prune(keep func(code string) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for el := s.updated.Front(); el != nil; {
		next := el.Next()
		if code := el.Value.(*entry).code; !keep(code) {
			delete(s.byCode, code)
			s.updated.Remove(el)
			n++
		}
		el = next
	}
	return n
}
