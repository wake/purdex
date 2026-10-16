package watchmod

import (
	"context"
	"sync"
	"time"
)

// The watcher on another host is known from the last search or announce; its host does not tell this one when it rests or goes.
// refreshRemote asks that host for its holder again, at most once per staleAfter (the bound the spec gives for seeing a watcher
// that ended), and lets the locator record the answer: an awake watcher stays or is found, a resting one stops being a place to
// deliver to but stays known for a wake, and one that is gone is forgotten. It runs with the holder reads and on the ticker.

type remoteState struct {
	mu   sync.Mutex
	last time.Time
}

// refreshRemote refreshes the remote watcher the locator knows (remembered, else the last one), when the last refresh is older than
// staleAfter or the clock went back. A host that cannot be asked leaves what is known as it is.
func (m *Module) refreshRemote(ctx context.Context) {
	cur, last := m.loc.Remembered()
	target := cur
	if target == nil {
		target = last
	}
	m.core.CfgMu.RLock()
	ownID := m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()
	if target == nil || target.HostID == ownID {
		return
	}
	m.remote.mu.Lock()
	if age := m.now().Sub(m.remote.last); !m.remote.last.IsZero() && age >= 0 && age <= staleAfter {
		m.remote.mu.Unlock()
		return
	}
	m.remote.last = m.now()
	m.remote.mu.Unlock()
	ans, err := (asker{m}).Holder(ctx, target.Alias)
	if err != nil {
		return
	}
	if ans.HostID == "" {
		ans.HostID = target.HostID
	}
	m.loc.Observe(target.Alias, ans)
}

// refreshRemoteBackground is refreshRemote for the ticker, tracked by Stop like a reconcile.
func (m *Module) refreshRemoteBackground() {
	ctx, done, ok := m.enter(context.Background())
	if !ok {
		return
	}
	defer done()
	m.refreshRemote(ctx)
}
