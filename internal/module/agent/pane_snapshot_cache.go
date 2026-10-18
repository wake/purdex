package agent

import (
	"sort"
	"sync"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// #3092 A: a hook's projection read needs only its own session's panes, and the
// tmux listing that names them changes far slower than hooks arrive (≈1.5 a
// second). The listing is therefore shared between reads for a short TTL instead
// of forked per hook.

// defaultPaneSnapTTL is how long a successful listing is reused. Set on Module at
// Init; zero (tests, no Init) means every read takes a fresh listing.
const defaultPaneSnapTTL = 2 * time.Second

// maxSnapRelists bounds how often one read re-lists because the layout kept changing under it.
const maxSnapRelists = 3

// paneSnapCache is the shared listing. mu is a leaf lock: it is never held while
// tmux is forked, and nothing under it calls back into the module.
type paneSnapCache struct {
	mu     sync.Mutex
	panes  map[string]tmux.PanePlacement // last successful, non-fallback listing
	taken  time.Time
	gen    uint64 // m.tmuxGen when the fork started
	flight *snapFlight
	// absent: panes a forced listing did not list, so a projection of a pane that tmux does not know (a frame left behind by a pane that
	// is gone) does not force another listing at every event; an entry holds for the TTL and until the layout generation moves (#3316 C).
	absent map[string]absentPane
}

type absentPane struct {
	at  time.Time
	gen uint64
}

// maxAbsentPanes bounds the absent map; past it the expired entries are dropped.
const maxAbsentPanes = 256

// recentlyAbsent says a forced listing within ttl, in the current layout generation, did not list the pane.
func (c *paneSnapCache) recentlyAbsent(paneID string, ttl time.Duration, gen uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.absent[paneID]
	return ok && a.gen == gen && time.Since(a.at) <= ttl
}

// noteAbsent records the pane as not listed by a listing taken now.
func (c *paneSnapCache) noteAbsent(paneID string, ttl time.Duration, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.absent == nil {
		c.absent = make(map[string]absentPane)
	}
	if len(c.absent) >= maxAbsentPanes {
		for id, a := range c.absent {
			if a.gen != gen || time.Since(a.at) > ttl {
				delete(c.absent, id)
			}
		}
		if len(c.absent) >= maxAbsentPanes {
			return // a flood of unknown panes: not remembered, each is still answered
		}
	}
	c.absent[paneID] = absentPane{at: time.Now(), gen: gen}
}

// snapFlight is one in-progress fork other readers may wait on.
type snapFlight struct {
	started time.Time
	gen     uint64 // m.tmuxGen when the fork started
	done    chan struct{}
	snap    *paneSnapshot // result of takePaneSnapshot (panes and fallback only are shared)
	err     error
}

// bumpTmuxGen says the tmux layout this daemon knows of changed (a frame was
// created or ended, a pane was spawned or adopted): the next read re-lists.
func (m *Module) bumpTmuxGen() { m.tmuxGen.Add(1) }

// view makes a per-read snapshot over a shared listing: the deadline and the
// timed-out flag belong to ONE read, never to the cache.
func (m *Module) snapshotView(panes map[string]tmux.PanePlacement, fallback bool) *paneSnapshot {
	return &paneSnapshot{m: m, panes: panes, fallback: fallback, deadline: time.Now().Add(paneSnapshotTimeout)}
}

// flightView is the snapshot a caller gets from a finished flight. A fallback keeps the flight's own deadline (the batch call
// already spent part of it, and a waiter's wait counts too): the per-pane lookups that stand in for the batch share ONE budget.
func (m *Module) flightView(f *snapFlight) *paneSnapshot {
	if f.snap.fallback {
		return &paneSnapshot{m: m, fallback: true, deadline: f.snap.deadline}
	}
	return m.snapshotView(f.snap.panes, false)
}

// cachedPaneSnapshot is takePaneSnapshot behind the shared listing. fromCache says
// the answer was not freshly listed for this call. force skips the TTL and the
// generation check (a caller whose pane or session is not in the listing).
// (nil, false, nil) when there is no tmux.
func (m *Module) cachedPaneSnapshot(force bool) (*paneSnapshot, bool, error) {
	if m == nil || m.tmux == nil {
		return nil, false, nil
	}
	ttl := m.paneSnapTTL
	if ttl <= 0 {
		s, err := m.takePaneSnapshot()
		return s, false, err
	}
	c := &m.snapCache
	callStart := time.Now()
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		if !force && c.panes != nil && time.Since(c.taken) <= ttl && c.gen == m.tmuxGen.Load() {
			panes := c.panes
			c.mu.Unlock()
			return m.snapshotView(panes, false), true, nil
		}
		if f := c.flight; f != nil {
			c.mu.Unlock()
			<-f.done
			// A forced caller needs a listing taken after it asked; one started earlier may predate the change.
			if force && f.started.Before(callStart) && attempt < maxSnapRelists {
				continue
			}
			// The layout changed (a frame was created or ended) after this listing began: it may predate the pane the caller needs.
			if f.gen != m.tmuxGen.Load() && attempt < maxSnapRelists {
				continue
			}
			if f.err != nil {
				return nil, false, f.err
			}
			if f.snap == nil {
				return nil, false, nil
			}
			return m.flightView(f), false, nil
		}
		gen := m.tmuxGen.Load()
		f := &snapFlight{started: time.Now(), gen: gen, done: make(chan struct{})}
		c.flight = f
		c.mu.Unlock()

		f.snap, f.err = m.takePaneSnapshot() // the fork, outside every lock

		c.mu.Lock()
		if f.err == nil && f.snap != nil && !f.snap.fallback {
			c.panes, c.taken, c.gen = f.snap.panes, time.Now(), gen
		}
		c.flight = nil
		c.mu.Unlock()
		close(f.done)
		// The layout changed while we were forking: the answer may predate the pane the caller needs. List again (a few times at most).
		if f.err == nil && gen != m.tmuxGen.Load() && attempt < maxSnapRelists {
			continue
		}
		if f.err != nil {
			return nil, false, f.err
		}
		if f.snap == nil {
			return nil, false, nil
		}
		return m.flightView(f), false, nil
	}
}

// sessionPaneCandidates are the panes that may belong to sessionName: those the
// listing names so, plus every Ambiguous pane (resolved per pane, only if it has
// a frame). Sorted for a stable query.
func (s *paneSnapshot) sessionPaneCandidates(sessionName string) []string {
	var ids []string
	for id, p := range s.panes {
		if p.Ambiguous || p.SessionName == sessionName {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
