package hosttransfer

import (
	"context"
	"log"

	"github.com/wake/purdex/internal/core"
)

// Module serves host transfer codes over /api/host-transfer* (spec §6).
// The parked payloads carry host tokens: they live only in this process's
// memory, are never logged or written to disk, and Stop drops them.
type Module struct {
	store *Store
	// tokenFn reads the daemon's admin token live. Each handler reads it
	// once and authenticates the request against that snapshot (authorize):
	// an empty token (or a nil tokenFn) closes both endpoints, since
	// TokenAuth is open without a token and host credentials must never be
	// parked or handed out unauthenticated.
	tokenFn func() string
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "hosttransfer" }
func (m *Module) Dependencies() []string { return nil }

// Init starts from an empty store and reads the admin token the way the
// outer chain does (cmd/pdx/http_chain.go tokenFn): live, under CfgMu.RLock.
func (m *Module) Init(c *core.Core) error {
	m.store = NewStore()
	m.tokenFn = func() string {
		c.CfgMu.RLock()
		defer c.CfgMu.RUnlock()
		if c.Cfg == nil {
			return ""
		}
		return c.Cfg.Token
	}
	return nil
}

// Start logs a banner — the module's only log line. Expiry needs no
// background loop: each parked code arms its own timer (and every call
// sweeps as a second line of defence).
func (m *Module) Start(_ context.Context) error {
	log.Println("[hosttransfer] endpoints enabled")
	return nil
}

// Stop drops every parked payload and closes the store for good: the HTTP
// server outlives StopModules, and a request arriving in between must not
// park or hand out anything.
func (m *Module) Stop(_ context.Context) error {
	if m.store != nil {
		m.store.Stop()
	}
	return nil
}
