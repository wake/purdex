package nex

import (
	"context"
	"sync"
	"time"

	"lab.protype.tw/wake/nexen/store"
)

// ConversationOwnerKey is the service-registry key under which nex publishes its ConversationOwnerSource (the
// conversation module reads it at call time; nex does not import conversation).
const ConversationOwnerKey = "nex.conversation-owner"

// ownerCacheTTL is how long one session's answer is reused: header, per-tick light and capabilities share it.
const ownerCacheTTL = 2 * time.Second

// ExecOwner is the live worker execution that runs a Claude session.
type ExecOwner struct {
	ExecID         string
	Light          string // running | waiting | idle | error
	TranscriptPath string // the row's transcript_path; may be empty
	Live           bool
}

// ConversationOwnerSource answers "which live execution runs this session". An error means "could not tell".
type ConversationOwnerSource interface {
	LiveWorker(ctx context.Context, sid string) (ExecOwner, bool, error)
}

type ownerCacheEntry struct {
	at    time.Time
	owner ExecOwner
	ok    bool
}

// execOwnerCache is a per-session TTL cache; errors are not cached. The zero value works.
type execOwnerCache struct {
	mu  sync.Mutex
	now func() time.Time // tests inject
	m   map[string]ownerCacheEntry
}

func (c *execOwnerCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *execOwnerCache) get(sid string) (ownerCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[sid]
	if !ok || c.clock().Sub(e.at) >= ownerCacheTTL {
		return ownerCacheEntry{}, false
	}
	return e, true
}

func (c *execOwnerCache) put(sid string, owner ExecOwner, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	if c.m == nil {
		c.m = map[string]ownerCacheEntry{}
	}
	for k, e := range c.m { // bounded: expired entries go as new ones arrive
		if now.Sub(e.at) >= ownerCacheTTL {
			delete(c.m, k)
		}
	}
	c.m[sid] = ownerCacheEntry{at: now, owner: owner, ok: ok}
}

// LiveWorker returns the newest live execution of the session (liveWorkersFor), with its light.
func (m *Module) LiveWorker(ctx context.Context, sid string) (ExecOwner, bool, error) {
	if e, hit := m.execOwners.get(sid); hit {
		return e.owner, e.ok, nil
	}
	rows, err := m.liveWorkersFor(ctx, sid)
	if err != nil && len(rows) == 0 {
		return ExecOwner{}, false, err
	}
	// a partial scan that found a live row still proves there is one (the newest may be missing: not cached)
	if len(rows) == 0 {
		m.execOwners.put(sid, ExecOwner{}, false)
		return ExecOwner{}, false, nil
	}
	e := rows[0]
	o := ExecOwner{ExecID: e.ID, Light: m.executionLight(e), TranscriptPath: e.TranscriptPath, Live: true}
	if err == nil {
		m.execOwners.put(sid, o, true)
	}
	return o, true, nil
}

// executionLight: the projector's latest digest through projectWorker (it knows pending_permission); before the
// projector has one, the row state alone (never waiting).
func (m *Module) executionLight(e store.Execution) string {
	m.projMu.Lock()
	p := m.proj
	m.projMu.Unlock()
	if p != nil {
		if d, ok := p.latestDigest(e.ID); ok {
			if s := projectWorker(d); s != workerClear {
				return string(s)
			}
		}
	}
	return rowLight(e.State)
}

func rowLight(s store.State) string {
	switch s {
	case store.StateRunning, store.StateQueued:
		return string(workerRunning)
	case store.StateFailed, store.StateRejected:
		return string(workerError)
	}
	return string(workerIdle)
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
