package codexbroker

import (
	"context"
	"log"
	"net/http"

	"github.com/wake/purdex/internal/core"
)

// Module wires the codex broker inventory into the daemon as a core.Module.
//
// P1 is read-only and has no goroutines, no shared state, and no dependencies
// on other modules. Auth is enforced by the daemon's TokenAuth middleware
// upstream of the mux this module registers on.
type Module struct {
	scanner *Scanner
	handler *Handler
}

// New returns an unconfigured Module. Call Init before RegisterRoutes.
func New() *Module {
	return &Module{}
}

// Name implements core.Module.
func (m *Module) Name() string { return "codexbroker" }

// Dependencies implements core.Module. P1 has no module dependencies.
func (m *Module) Dependencies() []string { return nil }

// Init constructs the production Scanner using the default plugin data root,
// socket glob roots, and PsLister. It does not perform any I/O.
//
// We tolerate a nil core because P1 doesn't need any shared service from it.
// A daemon-level wiring change (e.g. injecting config) would surface here.
func (m *Module) Init(_ *core.Core) error {
	root, err := PluginDataRoot()
	if err != nil {
		// Propagate the home-dir lookup failure so the daemon refuses to
		// start with a half-configured module rather than silently failing
		// later under load.
		return err
	}
	socketRoots, err := SocketGlobRoots()
	if err != nil {
		return err
	}
	m.scanner = NewScanner(ScannerOpts{
		FS:             NewOsFS(),
		Lister:         NewPsLister(),
		PluginDataRoot: root,
		SocketRoots:    socketRoots,
	})
	m.handler = NewHandler(m.scanner)
	return nil
}

// RegisterRoutes implements core.Module. Only one read-only route in P1.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if m.handler == nil {
		// Defensive: tests that bypass Init shouldn't crash the mux.
		m.handler = NewHandler(m.scanner)
	}
	mux.HandleFunc("GET /api/codex/brokers", m.handler.HandleBrokers)
}

// Start implements core.Module. P1 has no background work.
func (m *Module) Start(_ context.Context) error {
	log.Println("[codexbroker] inventory endpoint enabled")
	return nil
}

// Stop implements core.Module. P1 has nothing to clean up.
func (m *Module) Stop(_ context.Context) error { return nil }
