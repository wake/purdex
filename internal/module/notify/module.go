// Package notify keeps the groups the App writes back for the notification title (notify-title spec §3.2, plan N-2): the name of the
// custom task group, or of the team a tab was filed under, that a session's root belongs to. The daemon cannot work these out by
// itself (they live in the user's profile, on one host), so the App tells each host the ones that matter to it; the agent module's
// labeler asks this module, from memory, when no team of its own has a name for the session.
//
// The module is always mounted and does not depend on APNs: the Mac's notifications use the same title. A store that cannot be
// opened turns it off (no route, no overlay, no capability) and the daemon goes on, like the devices module.
package notify

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/notifylabel"
)

const (
	// retention is how long a device may go without writing before everything it wrote is dropped (a device that was replaced must not
	// leave its values behind for ever).
	retention = 30 * 24 * time.Hour
	// sweepEvery is how often the quiet devices are looked for.
	sweepEvery = 24 * time.Hour
)

// Module implements core.Module.
type Module struct {
	core *core.Core

	mu      sync.Mutex // serialises the writes (the file and the table change together) and guards the fields below
	store   *Store
	ready   bool
	initErr string
	cancel  context.CancelFunc
	done    chan struct{}

	tbl groupTable // root -> group, read by the labeler on every Label: memory only
}

// New returns the module.
func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "notify" }
func (m *Module) Dependencies() []string { return nil }

// Init opens notify.db, builds the table from it and publishes the module as the labeler's overlay. A failure leaves the module
// off (recorded, logged, nil returned).
func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.CfgMu.RLock()
	dataDir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	st, err := OpenStore(filepath.Join(dataDir, "notify.db"))
	if err == nil {
		err = m.load(st)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if st != nil {
			st.Close()
		}
		m.ready, m.initErr = false, err.Error()
		log.Printf("[notify] disabled: %v", err)
		return nil
	}
	m.store, m.ready, m.initErr = st, true, ""
	c.Registry.Register(notifylabel.OverlayKey, m)
	return nil
}

// load builds the table from the file.
func (m *Module) load(st *Store) error {
	best, err := st.Best()
	if err != nil {
		return err
	}
	m.tbl.replace(best)
	return nil
}

// Status is what the daemon reports for the module (the notify.group_labels.v1 capability follows `ready`).
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready, "init_error": m.initErr}
}

func (m *Module) live() *Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready {
		return nil
	}
	return m.store
}

// GroupOf implements notifylabel.Overlay: the group the App wrote for the root session, "" when there is none. Memory only.
func (m *Module) GroupOf(rootSessionID string) string { return m.tbl.groupOf(rootSessionID) }

// sweep drops the devices that have not written since retention before now (unix milliseconds) and rebuilds the table.
func (m *Module) sweep(now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready {
		return nil
	}
	n, err := m.store.Sweep(now - retention.Milliseconds())
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("[notify] swept %d row(s) of devices that went quiet", n)
		return m.load(m.store)
	}
	return nil
}

// Start sweeps once and then every day, until Stop.
func (m *Module) Start(ctx context.Context) error {
	if m.live() == nil {
		return nil
	}
	if err := m.sweep(time.Now().UnixMilli()); err != nil {
		log.Printf("[notify] sweep: %v", err)
	}
	sweepCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel, m.done = cancel, make(chan struct{})
	done := m.done
	m.mu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-t.C:
				if err := m.sweep(time.Now().UnixMilli()); err != nil {
					log.Printf("[notify] sweep: %v", err)
				}
			}
		}
	}()
	return nil
}

// Stop ends the sweep and turns the module not-ready; the store is released by Close, after the HTTP server drained.
func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done, m.ready = nil, nil, false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}

// RegisterRoutes mounts the one route; a module that is off has none (404).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if m.live() == nil {
		return
	}
	mux.HandleFunc("PUT /api/notify/group-labels", m.handlePut)
}

// Close releases the store. Idempotent.
func (m *Module) Close() error {
	m.mu.Lock()
	st := m.store
	m.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.Close()
}

// groupTable is the root -> group map the labeler reads on every Label: a read lock and a map lookup, no I/O.
type groupTable struct {
	mu sync.RWMutex
	m  map[string]string
}

func (t *groupTable) replace(best map[string]Entry) {
	next := make(map[string]string, len(best))
	for root, e := range best {
		next[root] = e.Group
	}
	t.mu.Lock()
	t.m = next
	t.mu.Unlock()
}

func (t *groupTable) groupOf(root string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.m[root]
}
