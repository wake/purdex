package hostconfig

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/wake/purdex/internal/alertsettings"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/personas"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/watchsettings"
	"github.com/wake/purdex/internal/workbooksettings"
)

// Module serves per-host launcher config over /api/hostconfig*.
type Module struct {
	core  *core.Core
	store *Store
	home  func() (string, error) // daemon user's home; injectable for tests

	beforeUnattendedPut func() // test seam: between SetUnattended's read and its CAS; nil in production

	personaNow  func() int64 // ms; the stamp of a save; nil = the clock (tests)
	personaMu   sync.Mutex
	personaSubs map[int]func()
	personaSeq  int
	stopped     bool // Stop ran: personas.v1 is no longer announced
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{home: os.UserHomeDir} }

func (m *Module) Name() string           { return "hostconfig" }
func (m *Module) Dependencies() []string { return nil }

// Init opens (or creates) the host config SQLite database inside DataDir.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	var err error
	m.store, err = OpenStore(filepath.Join(c.Cfg.DataDir, "host_config.db"))
	if err != nil {
		return err
	}
	// The team module reads the relay switches through this view (spec §8.7 (a)).
	c.Registry.Register(RelaySwitchesKey, m)
	// ... the relay prompt bodies through this one (spec §8.8) ...
	c.Registry.Register(RelayPromptsKey, m)
	// ... the member launch command through this one (spec §7.2 step 4) ...
	c.Registry.Register(TeamSettingsKey, m)
	// ... the resources module reads the host resource settings through this
	// one (lease plan Task 1.1) ...
	c.Registry.Register(resources.SettingsKey, m)
	// ... and reads and writes the unattended switch through this one
	// (unattended spec D-U23-1), its only writer.
	c.Registry.Register(UnattendedKey, m)
	// ... and the relay-quota rule's switch through this one (#2062).
	c.Registry.Register(RelayQuotaKey, m)
	// ... and the session workbook setting through this one (workbook plan WB-1b.1).
	c.Registry.Register(workbooksettings.Key, m)
	// ... and the load watcher's setting through this one (host-load-control plan W1b-3) ...
	c.Registry.Register(watchsettings.Key, m)
	// ... and the resume templates through this one (the session module's launch plan, NA-0).
	c.Registry.Register(ResumeTemplatesKey, m)
	// ... and the fleet's probe hosts through this one (fleet plan F-1b1).
	c.Registry.Register(ProbeHostsKey, m)
	// ... and the fleet row through this one.
	c.Registry.Register(FleetConfigKey, m)
	// ... and the personas (name pool v2 spec §4) through this one.
	c.Registry.Register(personas.Key, m)
	// ... and the wwd alert delivery's switch through this one (WWD-8a-2a-0).
	c.Registry.Register(alertsettings.Key, m)
	// ... and the two rows of the watcher's wake settings through this one (wake settings spec section 4, WK-1a).
	c.Registry.Register(WakeRowStoreKey, m)
	return nil
}

// RegisterRoutes wires up all /api/hostconfig endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hostconfig", m.handleGet)
	mux.HandleFunc("PUT /api/hostconfig/projects", m.putHandler(KeyProjects, func(raw []byte, _ Entry) (any, error) { return normalizeProjects(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/commands", m.putHandler(KeyCommands, func(raw []byte, _ Entry) (any, error) { return normalizeCommands(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/resume-templates", m.putHandler(KeyResumeTemplates, func(raw []byte, _ Entry) (any, error) { return normalizeResumeTemplates(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/quick-replies", m.putHandler(KeyQuickReplies, func(raw []byte, cur Entry) (any, error) { return normalizeQuickReplies(raw, cur) }))
	mux.HandleFunc("PUT /api/hostconfig/relay", m.putHandler(KeyRelay, func(raw []byte, _ Entry) (any, error) { return normalizeRelay(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/team", m.putHandler(KeyTeam, func(raw []byte, _ Entry) (any, error) { return normalizeTeam(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/relay_quota", m.putHandler(KeyRelayQuota, func(raw []byte, _ Entry) (any, error) { return normalizeRelayQuota(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/workbook", m.putHandler(KeyWorkbook, func(raw []byte, _ Entry) (any, error) { return normalizeWorkbook(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/wwd_alerts", m.putHandler(KeyWwdAlerts, func(raw []byte, _ Entry) (any, error) { return normalizeWwdAlerts(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/watch", m.putHandler(KeyWatch, func(raw []byte, cur Entry) (any, error) {
		stored := watchsettings.Default()
		if len(cur.Value) > 0 {
			if s, err := normalizeWatch(cur.Value); err == nil {
				stored = s
			}
		}
		return normalizeWatchOver(raw, stored, true)
	}))
	mux.HandleFunc("PUT /api/hostconfig/probe-hosts", m.putHandler(KeyProbeHosts, func(raw []byte, cur Entry) (any, error) { return normalizeProbeHosts(raw, cur) }))
	mux.HandleFunc("PUT /api/hostconfig/fleet", m.putHandler(KeyFleet, func(raw []byte, _ Entry) (any, error) { return normalizeFleet(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/resources", m.putHandler(KeyResources, func(raw []byte, _ Entry) (any, error) { return normalizeResources(raw) }))
	mux.HandleFunc("POST /api/hostconfig/check-path", m.handleCheckPath)
	// the personas have a route of their own (4 MiB body, a stamp, an event) and are not in GET /api/hostconfig
	mux.HandleFunc("GET /api/hostconfig/personas", m.handleGetPersonas)
	mux.HandleFunc("PUT /api/hostconfig/personas", m.handlePutPersonas)
}

// Status is what /api/info reads to announce personas.v1: the routes are there from a successful Init until Stop.
func (m *Module) Status() map[string]any {
	m.personaMu.Lock()
	defer m.personaMu.Unlock()
	return map[string]any{"ready": m.store != nil && !m.stopped}
}

// Start logs a banner; no background work required.
func (m *Module) Start(_ context.Context) error {
	log.Println("[hostconfig] endpoints enabled")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *Module) Stop(_ context.Context) error {
	m.personaMu.Lock()
	m.stopped = true
	m.personaMu.Unlock()
	return nil // the store is released by Close, after the HTTP server drained (#1033)
}

// Close releases the store. It runs from CloseModules, after the HTTP server has drained (spec §4.5), which is why Stop
// leaves the store open: a request in flight during shutdown must not meet a closed database (#1033). Idempotent
// (sql.DB.Close is).
func (m *Module) Close() error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
