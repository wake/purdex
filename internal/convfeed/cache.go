package convfeed

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// DefaultMaxEntries is how many conversations the cache holds at once.
	DefaultMaxEntries = 16
	// DefaultIdle is how long an unpinned entry is kept after its last use.
	DefaultIdle = 10 * time.Minute
)

// ErrBusy is returned by Acquire when every entry is pinned and the cache is full: nothing can be evicted to make room.
var ErrBusy = errors.New("convfeed: every cache entry is in use")

// CacheOptions configures a Cache; zero values take the defaults.
type CacheOptions struct {
	Max  int
	Idle time.Duration
	Now  func() time.Time // tests inject a fake clock
}

// Cache holds one Entry per conversation. Its lock covers only the map and the pin counts: an entry's own mutex is
// what a refresh holds, so a slow transcript never blocks the lookup of another conversation.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*cached
	seq     uint64 // advances on every use: the LRU order, exact when the clock is coarse
	max     int
	idle    time.Duration
	now     func() time.Time
}

type cached struct {
	entry    *Entry
	pins     int
	lastUsed time.Time
	lastSeq  uint64
}

// NewCache returns an empty cache.
func NewCache(o CacheOptions) *Cache {
	c := &Cache{entries: map[string]*cached{}, max: o.Max, idle: o.Idle, now: o.Now}
	if c.max <= 0 {
		c.max = DefaultMaxEntries
	}
	if c.idle <= 0 {
		c.idle = DefaultIdle
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Acquire returns the entry of sessionID, creating it if needed, and pins it until release is called. Concurrent
// callers for one session get the same entry (the map insert happens under the lock, and creating an empty entry
// reads nothing). release is idempotent. An entry is evicted only while unpinned: idle for the idle time, or the
// oldest one when a new session needs the room; with every entry pinned and the cache full, ErrBusy.
func (c *Cache) Acquire(ctx context.Context, sessionID string) (*Entry, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil { // cancelled while waiting for the lock: change nothing
		return nil, nil, err
	}
	now := c.now()
	c.sweepIdle(now)
	ce, ok := c.entries[sessionID]
	if !ok {
		if len(c.entries) >= c.max && !c.evictOldestUnpinned() {
			return nil, nil, ErrBusy
		}
		ce = &cached{entry: NewEntry(sessionID)}
		c.entries[sessionID] = ce
	}
	ce.pins++
	c.touch(ce, now)
	var once sync.Once
	release := func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			ce.pins--
			c.touch(ce, c.now())
		})
	}
	return ce.entry, release, nil
}

// touch marks ce as the most recently used. c.mu is held.
func (c *Cache) touch(ce *cached, now time.Time) {
	c.seq++
	ce.lastSeq = c.seq
	ce.lastUsed = now
}

// Sweep drops the unpinned entries idle for the idle time. Acquire sweeps too, but a quiet cache would keep its last
// entries (and their normalized transcripts) forever, so the owner calls Sweep on a timer (see Run).
func (c *Cache) Sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepIdle(c.now())
}

// Run sweeps every interval (a minute when interval is not positive) until ctx is done.
func (c *Cache) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Sweep()
		}
	}
}

// Len is how many entries the cache holds (tests and diagnostics).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// sweepIdle drops unpinned entries unused for the idle time. c.mu is held.
func (c *Cache) sweepIdle(now time.Time) {
	for id, ce := range c.entries {
		if ce.pins == 0 && now.Sub(ce.lastUsed) >= c.idle {
			delete(c.entries, id)
		}
	}
}

// evictOldestUnpinned removes the least recently used unpinned entry; false when every entry is pinned. c.mu is held.
func (c *Cache) evictOldestUnpinned() bool {
	var oldest string
	var at uint64
	found := false
	for id, ce := range c.entries {
		if ce.pins > 0 {
			continue
		}
		if !found || ce.lastSeq < at {
			oldest, at, found = id, ce.lastSeq, true
		}
	}
	if found {
		delete(c.entries, oldest)
	}
	return found
}
