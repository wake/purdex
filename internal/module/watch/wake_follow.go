package watchmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watchwake"
)

// This file is the side of a host that is not the source of the wake settings (spec section 4): it keeps a copy of the source's document, reads
// it again every minute and when the source says it changed, passes a PUT on to the source, and tells the source's host what it suppressed.
//
// Every change of the copy goes through apply, one at a time (the lock is held for the whole of it), and an answer is judged by who it came from
// and by its revision, so the copy only moves forward: an answer that asked another source than the one set now is dropped; from the same
// source only a larger revision is taken; when the source (or its host id) is not the one the copy came from, the copy starts again from this
// answer whatever its revision (revisions mean something only inside one source).

// Peer routes of the wake settings, on the source host.
const (
	pathWakeGet        = "/api/peers/watch/wake-settings/get"
	pathWakePut        = "/api/peers/watch/wake-settings/put"
	pathWakeChanged    = "/api/peers/watch/wake-settings/changed"
	pathWakeSuppressed = "/api/peers/watch/wake-suppressed"
)

// wakePollEvery is how often a host that is not the source reads the source's document again.
const wakePollEvery = 60 * time.Second

// wakeCacheDoc is the copy: what host_config keeps in the row `watch_wake_cache` and what the host serves from memory.
type wakeCacheDoc struct {
	Settings  json.RawMessage `json:"settings"`
	Etag      string          `json:"etag"`
	Revision  int64           `json:"revision"`
	Readonly  bool            `json:"readonly"`
	Source    string          `json:"source"`
	SourceID  string          `json:"source_id"`
	FetchedAt time.Time       `json:"fetched_at"`
}

type wakeFollower struct {
	mu      sync.Mutex
	loaded  bool
	cache   *wakeCacheDoc
	rowRev  int64 // the revision of the watch_wake_cache row, for the compare-and-swap of its next write
	dirty   bool  // the memory is ahead of the row: the write failed, try again with the next update
	fresh   bool  // the last read of the source succeeded (false until the first one of this run, and after a failed one)
	polling bool  // a read of the source is under way
	again   bool  // a notice came in while it was: read once more after
}

// wakeResult is an answer of the source with who was asked: the alias set as the source when the question was sent, and the host id the
// answer came from.
type wakeResult struct {
	source string
	view   wakeSettingsView
}

// load reads the stored copy once. A row that does not read is left alone and the copy starts empty.
func (f *wakeFollower) load(m *Module) {
	if f.loaded {
		return
	}
	f.loaded = true
	rows, err := m.wakeRows()
	if err != nil {
		return
	}
	raw, rev, ok, err := rows.Get(hostconfig.KeyWatchWakeCache)
	if err != nil || !ok {
		return
	}
	f.rowRev = rev
	var c wakeCacheDoc
	if json.Unmarshal(raw, &c) != nil || len(c.Settings) == 0 || c.Source == "" {
		m.logf("[watch] the stored copy of the wake settings does not read: starting without one")
		return
	}
	f.cache = &c
}

// apply takes an answer of the source into the copy (see the top of the file). It returns whether the copy moved to a new revision or a new
// source, in which case the clients of this host are told.
func (m *Module) applyWake(res wakeResult) bool {
	f := &m.wakeFollow
	f.mu.Lock()
	f.load(m)
	if cur, isSource := m.wakeSourceAlias(); isSource || cur != res.source { // asked a source that is not the one set now
		f.mu.Unlock()
		return false
	}
	v := res.view
	c := f.cache
	sameSource := c != nil && c.Source == res.source && c.SourceID == v.SourceID
	f.fresh = true
	if sameSource && v.Revision <= c.Revision { // the same document or an older answer: the copy stays; the read still counts as a fetch
		c.FetchedAt = m.now()
		f.mu.Unlock()
		return false
	}
	f.cache = &wakeCacheDoc{Settings: v.Settings, Etag: v.Etag, Revision: v.Revision, Readonly: v.Readonly, Source: res.source, SourceID: v.SourceID, FetchedAt: m.now()}
	f.dirty = true
	m.persistWakeCache(f)
	doc := wakeDoc{etag: v.Etag, revision: v.Revision}
	f.mu.Unlock()
	m.wakeChanged(res.source, doc)
	return true
}

// persistWakeCache writes the copy to its row; a failure keeps the copy in memory and tries again with the next update. The caller holds f.mu.
func (m *Module) persistWakeCache(f *wakeFollower) {
	rows, err := m.wakeRows()
	if err != nil || f.cache == nil {
		return
	}
	raw, err := json.Marshal(f.cache)
	if err != nil {
		return
	}
	rev, err := rows.Put(hostconfig.KeyWatchWakeCache, raw, f.rowRev)
	if errors.Is(err, hostconfig.ErrConflict) { // this host is the only writer of the row: its revision was just not known (a first write after a restart that could not read it)
		if _, cur, _, gerr := rows.Get(hostconfig.KeyWatchWakeCache); gerr == nil {
			rev, err = rows.Put(hostconfig.KeyWatchWakeCache, raw, cur)
		}
	}
	if err != nil {
		m.logf("[watch] keeping the copy of the wake settings in memory only: %v", err)
		return
	}
	f.rowRev, f.dirty = rev, false
}

// followerView is the answer of a host that is not the source: the copy of the source it has (stale while the last read of the source failed or
// none was made yet), or the defaults - stale, never fetched, no source id - when it has none from this source.
func (m *Module) followerView(source string) wakeSettingsView {
	f := &m.wakeFollow
	f.mu.Lock()
	f.load(m)
	c := f.cache
	fresh := f.fresh
	var copyOf wakeCacheDoc
	have := c != nil && c.Source == source
	if have {
		copyOf = *c
	}
	f.mu.Unlock()
	if !have {
		def := watchwake.Default(m.zone())
		return wakeSettingsView{
			Settings: watchwake.Marshal(def), Etag: watchwake.Etag(def),
			Source: source, Stale: true,
			KnownKinds: watchwake.KnownKinds, Suppressed: m.suppressed.list(), PatrolStatus: wakePatrolStatus{},
		}
	}
	at := copyOf.FetchedAt
	return wakeSettingsView{
		Settings: copyOf.Settings, Etag: copyOf.Etag, Revision: copyOf.Revision, Readonly: copyOf.Readonly,
		Source: source, SourceID: copyOf.SourceID, Stale: !fresh, FetchedAt: &at,
		KnownKinds: watchwake.KnownKinds, Suppressed: m.suppressed.list(), PatrolStatus: wakePatrolStatus{},
	}
}

// markWakeStale records that the source could not be read: what the host has is served as stale until a read succeeds.
func (m *Module) markWakeStale() {
	f := &m.wakeFollow
	f.mu.Lock()
	f.fresh = false
	f.mu.Unlock()
}

// background runs fn on a goroutine the module's Stop waits for; with the module not running it does nothing (a test sets backgroundSync to run
// it in place).
func (m *Module) background(fn func(ctx context.Context)) {
	if m.backgroundSync {
		fn(context.Background())
		return
	}
	ctx, done, ok := m.enterFor(context.Background(), wakeCallBudget)
	if !ok {
		return
	}
	go func() {
		defer done()
		fn(ctx)
	}()
}

// wakeCallBudget bounds a read of the source, a note to another host, a report.
const wakeCallBudget = 30 * time.Second

// requestWakePoll reads the source's document soon: now, or when the read under way is done (notices that come in meanwhile are one).
func (m *Module) requestWakePoll() {
	f := &m.wakeFollow
	f.mu.Lock()
	if f.polling {
		f.again = true
		f.mu.Unlock()
		return
	}
	f.polling = true
	f.mu.Unlock()
	m.background(func(ctx context.Context) {
		for {
			m.pollWake(ctx)
			f.mu.Lock()
			if !f.again {
				f.polling = false
				f.mu.Unlock()
				return
			}
			f.again = false
			f.mu.Unlock()
		}
	})
}

// wakeFollowLoop reads the source at start and then every wakePollEvery, until ctx ends.
func (m *Module) wakeFollowLoop(ctx context.Context) {
	every := m.wakePollEvery
	if every <= 0 {
		every = wakePollEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	m.requestWakePoll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.requestWakePoll()
		}
	}
}

// wakeEnvelope is what the peer routes of the wake settings answer: the inner status and body travel inside a 200, so a 400's fields and a 409's
// current document reach the caller as they were (HostCaller keeps neither the status nor the body of a refusal).
type wakeEnvelope struct {
	HostID string          `json:"host_id"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// callSource asks the source host's route and returns the envelope. ok is false when the source could not be asked or did not answer in the
// envelope's form.
func (m *Module) callSource(ctx context.Context, source, path string, body func(toHostID string) any) (env wakeEnvelope, hostID string, ok bool) {
	entry, _, found := m.peerEntry(source)
	if !found || entry.HostID == "" || m.caller == nil {
		return env, "", false
	}
	res := m.caller.Call(ctx, entry.HostID, path, body(entry.HostID))
	if res.Class != peersmod.ClassDone {
		return env, entry.HostID, false
	}
	if json.Unmarshal(res.Body, &env) != nil || env.Status == 0 || len(env.Body) == 0 {
		return env, entry.HostID, false
	}
	return env, entry.HostID, true
}

// pollWake reads the source's document and takes it into the copy. A host that is the source reads nothing.
func (m *Module) pollWake(ctx context.Context) {
	source, isSource := m.wakeSourceAlias()
	if isSource {
		return
	}
	env, hostID, ok := m.callSource(ctx, source, pathWakeGet, func(to string) any { return map[string]string{"to_host_id": to} })
	var v wakeSettingsView
	if !ok || env.Status != http.StatusOK || json.Unmarshal(env.Body, &v) != nil || !v.IsSource || v.SourceID != hostID || len(v.Settings) == 0 {
		m.markWakeStale()
		return
	}
	m.applyWake(wakeResult{source: source, view: v})
}

// forwardWakePut passes a PUT to the source and answers what it answered, status and body as they came. A source that cannot be reached (or
// that is too old to have the route) is a 503 source_unreachable: it may have been written, and the caller reads again. A PUT that went through
// updates the copy at once, so the next GET of this host shows it.
func (m *Module) forwardWakePut(ctx context.Context, source string, body wakePutBody) wakeReply {
	unreachable := wakeReply{http.StatusServiceUnavailable, map[string]string{"error": "source_unreachable", "source": source}}
	env, _, ok := m.callSource(ctx, source, pathWakePut, func(to string) any {
		return map[string]any{"to_host_id": to, "settings": body.Settings, "expect_etag": body.ExpectEtag}
	})
	if !ok {
		return unreachable
	}
	if env.Status == http.StatusOK {
		var v wakeSettingsView
		if json.Unmarshal(env.Body, &v) == nil && v.IsSource && len(v.Settings) > 0 {
			m.applyWake(wakeResult{source: source, view: v})
		}
	}
	return wakeReply{env.Status, env.Body}
}

// tellFleetWakeChanged tells the other hosts that the document has a new revision, so they read it now rather than at their next poll. A host
// that cannot be told is not told again: its poll finds it.
func (m *Module) tellFleetWakeChanged(revision int64) {
	if m.caller == nil {
		return
	}
	m.core.CfgMu.RLock()
	hosts := append([]string(nil), peerHostIDs(m.core.Cfg.Peers.Hosts)...)
	m.core.CfgMu.RUnlock()
	for _, id := range hosts {
		m.background(func(ctx context.Context) {
			m.caller.Call(ctx, id, pathWakeChanged, map[string]any{"to_host_id": id, "revision": revision})
		})
	}
}
