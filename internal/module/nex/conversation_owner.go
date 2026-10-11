package nex

import (
	"context"
	"sync"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"lab.protype.tw/wake/nexen/store"
)

// ConversationOwnerKey is the service-registry key under which nex publishes its ConversationOwnerSource (the
// conversation module reads it at call time; nex does not import conversation).
const ConversationOwnerKey = convfeed.ExecOwnerKey

const (
	// ownerCacheTTL is how long one session's owner identity is reused: header, per-tick light and capabilities share it.
	ownerCacheTTL = 2 * time.Second
	// errCacheTTL is how long a failed or partial scan is remembered, so a sick store is not hammered by every tick.
	errCacheTTL = time.Second
	// ownerSweepEvery bounds the cleanup of expired entries: at most one sweep per interval.
	ownerSweepEvery = 30 * time.Second
	// ownerLookupTimeout bounds one LiveWorker call, whatever the caller's context allows.
	ownerLookupTimeout = 1500 * time.Millisecond
)

// ConversationOwnerSource answers "which live execution runs this session". An error means "could not tell".
type ConversationOwnerSource interface {
	LiveWorker(ctx context.Context, sid string) (convfeed.ExecOwner, bool, error)
}

// ownerCacheEntry is one session's scan result: only who owns it, never the light (read fresh on every call).
// at is when the scan started.
type ownerCacheEntry struct {
	at             time.Time
	ok             bool
	execID         string
	transcriptPath string
	state          store.State // the row's state at scan time: the light's fallback
	err            error
}

func (e ownerCacheEntry) ttl() time.Duration {
	if e.err != nil {
		return errCacheTTL
	}
	return ownerCacheTTL
}

// execOwnerCache is a per-session TTL cache with single-flight scans. The zero value works.
type execOwnerCache struct {
	mu        sync.Mutex
	now       func() time.Time // tests inject
	m         map[string]ownerCacheEntry
	lastSweep time.Time
	flights   map[string]*ownerFlight
}

type ownerFlight struct {
	done chan struct{}
	res  ownerCacheEntry
}

func (c *execOwnerCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *execOwnerCache) get(key string) (ownerCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || c.clock().Sub(e.at) >= e.ttl() {
		return ownerCacheEntry{}, false
	}
	return e, true
}

func (c *execOwnerCache) put(key string, e ownerCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]ownerCacheEntry{}
	}
	if now := c.clock(); now.Sub(c.lastSweep) >= ownerSweepEvery {
		c.lastSweep = now
		for k, old := range c.m {
			if now.Sub(old.at) >= old.ttl() {
				delete(c.m, k)
			}
		}
	}
	c.m[key] = e
}

// do runs scan once per key at a time: concurrent callers share the one answer. The scan runs on its own context
// (bounded by ownerLookupTimeout), not the first caller's, so one cancelled caller cannot fail the others; each caller
// waits only as long as its own ctx allows.
func (c *execOwnerCache) do(ctx context.Context, key string, scan func(context.Context) ownerCacheEntry) (ownerCacheEntry, error) {
	c.mu.Lock()
	f, running := c.flights[key]
	if !running {
		if c.flights == nil {
			c.flights = map[string]*ownerFlight{}
		}
		f = &ownerFlight{done: make(chan struct{})}
		c.flights[key] = f
		start := c.clock()
		go func() {
			sctx, cancel := context.WithTimeout(context.Background(), ownerLookupTimeout)
			defer cancel()
			res := scan(sctx)
			res.at = start
			c.put(key, res)
			c.mu.Lock()
			delete(c.flights, key)
			c.mu.Unlock()
			f.res = res
			close(f.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-f.done:
		return f.res, nil
	case <-ctx.Done():
		return ownerCacheEntry{}, ctx.Err()
	}
}

// LiveWorker returns the live execution that owns the session (executionOwns, newest first). Only the owner's identity
// is cached (2 s); the light is computed on every call.
func (m *Module) LiveWorker(ctx context.Context, sid string) (convfeed.ExecOwner, bool, error) {
	key := store.NormalizeResumeSessionID(sid)
	ctx, cancel := context.WithTimeout(ctx, ownerLookupTimeout)
	defer cancel()
	e, hit := m.execOwners.get(key)
	if !hit {
		var err error
		if e, err = m.execOwners.do(ctx, key, func(sctx context.Context) ownerCacheEntry { return m.scanOwner(sctx, key) }); err != nil {
			return convfeed.ExecOwner{}, false, err
		}
	}
	if e.err != nil {
		return convfeed.ExecOwner{}, false, e.err
	}
	if !e.ok {
		return convfeed.ExecOwner{}, false, nil
	}
	return convfeed.ExecOwner{ExecID: e.execID, Light: m.executionLight(e.execID, e.state), TranscriptPath: e.transcriptPath, Live: true, At: m.execOwners.clock()}, true, nil
}

// scanOwner finds the owner among the session's live rows. A partial scan (an error after some rows) is an error:
// the newest row may be the one that is missing.
func (m *Module) scanOwner(ctx context.Context, sid string) ownerCacheEntry {
	rows, err := m.liveWorkersFor(ctx, sid)
	if err != nil {
		return ownerCacheEntry{err: err}
	}
	for _, e := range rows { // newest first
		if executionOwns(e, sid) {
			return ownerCacheEntry{ok: true, execID: e.ID, transcriptPath: e.TranscriptPath, state: e.State}
		}
	}
	return ownerCacheEntry{}
}

// executionLight: the projector's latest digest through projectWorker (it knows pending_permission); before the
// projector has one, the row state alone (never waiting). A state it does not know is "unknown", never idle.
func (m *Module) executionLight(id string, rowState store.State) string {
	m.projMu.Lock()
	p := m.proj
	m.projMu.Unlock()
	if p != nil {
		if d, ok := p.latestDigest(id); ok {
			return digestLight(d)
		}
	}
	return rowLight(rowState)
}

func digestLight(d rowDigest) string {
	switch d.State {
	case "queued", "running", "idle", "failed", "rejected":
		if s := projectWorker(d); s != workerClear {
			return string(s)
		}
	}
	return "unknown"
}

func rowLight(s store.State) string {
	switch s {
	case store.StateRunning, store.StateQueued:
		return string(workerRunning)
	case store.StateIdle:
		return string(workerIdle)
	case store.StateFailed, store.StateRejected:
		return string(workerError)
	}
	return "unknown"
}

// latestDigest is the digest of the last row pushed for an execution; not for a removed or unparsed one.
func (p *projector) latestDigest(id string) (rowDigest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.pushed[id]
	if !ok || r.removed || r.unparsed {
		return rowDigest{}, false
	}
	return r.digest, true
}
