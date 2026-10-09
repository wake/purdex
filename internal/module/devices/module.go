// Package devices is the "devices" daemon module: the tokens of paired phones (QR pairing spec docs/specs/2026-10-09-qr-
// pairing-spec.md §3). It stores them (devices.db, only hashes), authenticates a `pdxd_` bearer for the daemon's token
// middleware, and serves the admin routes that mint, list, revoke and rename them.
//
// Like push it is not a reason to keep the daemon down: a store that cannot be opened is recorded, the module serves no
// route, authenticates nothing and announces no capability.
package devices

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
)

// RegistryKey is the service-registry key the module publishes its devices.Authenticator under (the daemon's token
// middleware reads it; the module is not imported by it).
const RegistryKey = "devices.authenticator"

// sweepEvery: how often rows that can no longer work are deleted.
const sweepEvery = time.Hour

// Module is the devices module.
type Module struct {
	core *core.Core

	mu      sync.Mutex
	store   *Store
	ready   bool
	initErr string

	onRevoke func(ids []string) // set by whoever must close what the revoked device has open (QP-1b's connection registry)
	cancel   context.CancelFunc
	done     chan struct{}
}

func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "devices" }
func (m *Module) Dependencies() []string { return nil }

// Init opens devices.db and publishes the authenticator. A failure leaves the module off (recorded, logged, nil returned).
func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.CfgMu.RLock()
	dataDir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	st, err := OpenStore(filepath.Join(dataDir, "devices.db"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.ready, m.initErr = false, err.Error()
		log.Printf("[devices] disabled: %v", err)
		return nil
	}
	m.store, m.ready, m.initErr = st, true, ""
	c.Registry.Register(RegistryKey, devices.Authenticator(m))
	return nil
}

// Status is what the daemon reports for the module (the devices.v1 capability follows `ready`).
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

// AuthenticateToken implements devices.Authenticator for the token middleware.
func (m *Module) AuthenticateToken(token string) (devices.Principal, bool) {
	st := m.live()
	if st == nil || !devices.IsDeviceToken(token) {
		return devices.Principal{}, false
	}
	return st.Authenticate(devices.Hash(token))
}

// SetOnRevoke registers what runs after devices were revoked, with their ids (nil clears it).
func (m *Module) SetOnRevoke(fn func(ids []string)) {
	m.mu.Lock()
	m.onRevoke = fn
	m.mu.Unlock()
}

func (m *Module) revoked(ids []string) {
	if len(ids) == 0 {
		return
	}
	m.mu.Lock()
	fn := m.onRevoke
	m.mu.Unlock()
	if fn != nil {
		fn(ids)
	}
}

// Start sweeps once and then every hour, until Stop.
func (m *Module) Start(ctx context.Context) error {
	st := m.live()
	if st == nil {
		return nil
	}
	if n, err := st.Sweep(); err != nil {
		log.Printf("[devices] sweep: %v", err)
	} else if n > 0 {
		log.Printf("[devices] swept %d dead row(s)", n)
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
				if _, err := st.Sweep(); err != nil {
					log.Printf("[devices] sweep: %v", err)
				}
			}
		}
	}()
	return nil
}

// Stop ends the sweep and closes the store.
func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	cancel, done, st := m.cancel, m.done, m.store
	m.cancel, m.done, m.ready = nil, nil, false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if st != nil {
		return st.Close()
	}
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if m.live() == nil {
		return // soft-failed: the routes do not exist (404)
	}
	mux.HandleFunc("POST /api/devices", m.handleMint)
	mux.HandleFunc("GET /api/devices", m.handleList)
	mux.HandleFunc("DELETE /api/devices/{id}", m.handleRevokeID)
	mux.HandleFunc("DELETE /api/devices", m.handleRevokePairing)
	mux.HandleFunc("PUT /api/devices/self", m.handleSelf)
}
