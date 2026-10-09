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
	max     int
	idle    time.Duration
	now     func() time.Time
}

type cached struct {
	entry    *Entry
	pins     int
	lastUsed time.Time
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
	ce.lastUsed = now
	var once sync.Once
	release := func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			ce.pins--
			ce.lastUsed = c.now()
		})
	}
	return ce.entry, release, nil
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
	var at time.Time
	found := false
	for id, ce := range c.entries {
		if ce.pins > 0 {
			continue
		}
		if !found || ce.lastUsed.Before(at) {
			oldest, at, found = id, ce.lastUsed, true
		}
	}
	if found {
		delete(c.entries, oldest)
	}
	return found
}
