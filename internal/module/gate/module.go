// Package gatemod is the daemon's side of the load gate (host-load-control spec; watcher-area spec §8): the history of what
// the gate decided, read from the gate directory. A1 is the decisions route; A2 (the config) comes later in this module.
package gatemod

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/gate"
	ipeers "github.com/wake/purdex/internal/peers"
)

// Module is the gate module. It holds no state of the gate: the files of the gate directory are the truth.
type Module struct {
	mu    sync.Mutex
	ready bool // Init succeeded and Stop has not run: gate.decisions.v1 is announced
	// dir is the gate directory (empty: gate.DefaultDir() at each request, so a test that sets PDX_GATE_DIR is followed).
	dir string
}

func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "gate" }
func (m *Module) Dependencies() []string { return nil }

func (m *Module) Init(*core.Core) error {
	m.mu.Lock()
	m.ready = true
	m.mu.Unlock()
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	m.ready = false
	m.mu.Unlock()
	return nil
}

// Status is what /api/info reads to announce gate.decisions.v1.
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready}
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gate/decisions", m.handleDecisions)
}

func (m *Module) gateDir() string {
	if m.dir != "" {
		return m.dir
	}
	return gate.DefaultDir()
}

// requireAdmin is the check of the watch module's routes: the admin token only, no device token.
func (m *Module) requireAdmin(w http.ResponseWriter, r *http.Request, what string) bool {
	if _, isDevice := devices.PrincipalFrom(r.Context()); isDevice || !devices.IsAdmin(r.Context()) {
		writeErr(w, http.StatusForbidden, "forbidden", "only the admin token "+what)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, ipeers.APIError{Error: code, Detail: detail})
}
