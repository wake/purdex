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
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "hosttransfer" }
func (m *Module) Dependencies() []string { return nil }

// Init starts from an empty store.
func (m *Module) Init(_ *core.Core) error {
	m.store = NewStore()
	return nil
}

// Start logs a banner — the module's only log line; no background work (the
// store sweeps expired codes on every call).
func (m *Module) Start(_ context.Context) error {
	log.Println("[hosttransfer] endpoints enabled")
	return nil
}

// Stop drops every parked payload.
func (m *Module) Stop(_ context.Context) error {
	if m.store != nil {
		m.store.Clear()
	}
	return nil
}
