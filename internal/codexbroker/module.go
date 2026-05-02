package codexbroker

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// Module wires the codex broker inventory + sweep API into the daemon as a
// core.Module.
//
// P2 adds: SweepHandler, QuarantineStore (file-backed), LaunchRegistryFile
// (read-only), and a daemon-lifetime E1Tracker. The sweep route is the only
// new HTTP surface; GET /api/codex/brokers (P1) is unchanged.
//
// Auth is enforced by the daemon's TokenAuth middleware upstream of the mux
// this module registers on.
type Module struct {
	scanner        *Scanner
	handler        *Handler
	sweepHandler   *SweepHandler
	quarantine     *QuarantineFile
	launchRegistry *LaunchRegistryFile
	e1Tracker      *E1Tracker

	pluginDataRoot string
	auditDir       string
}

// New returns an unconfigured Module. Call Init before RegisterRoutes.
func New() *Module {
	return &Module{}
}

// Name implements core.Module.
func (m *Module) Name() string { return "codexbroker" }

// Dependencies implements core.Module. P2 has no module dependencies (still
// stand-alone).
func (m *Module) Dependencies() []string { return nil }

// Init constructs the production Scanner + sweep handler using the default
// plugin data root. Quarantine and launch registry files are loaded
// best-effort: a missing file produces an empty in-memory state and Init
// continues.
func (m *Module) Init(_ *core.Core) error {
	root, err := PluginDataRoot()
	if err != nil {
		return err
	}
	m.pluginDataRoot = root
	m.auditDir = filepath.Join(root, "audit")

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

	// Quarantine — best-effort load; missing file → empty.
	qStore := &QuarantineStore{}
	qf, err := qStore.Load(quarantinePath(root))
	if err != nil {
		log.Printf("[codexbroker] quarantine load error (continuing with empty): %v", err)
		qf = &QuarantineFile{Version: 1}
	}
	m.quarantine = qf

	// Launch registry — best-effort load; missing file → empty.
	rStore := &LaunchRegistry{}
	rf, err := rStore.Load(launchRegistryPath(root))
	if err != nil {
		log.Printf("[codexbroker] launch registry load error (continuing with empty): %v", err)
		rf = &LaunchRegistryFile{Version: 1}
	}
	m.launchRegistry = rf

	// E1 tracker — daemon-lifetime in-memory.
	m.e1Tracker = NewE1Tracker()

	// Sweep handler.
	m.sweepHandler = &SweepHandler{
		ScanFn: func(ctx context.Context) ([]BrokerRecord, error) {
			res, err := m.scanner.Scan(ctx)
			if err != nil {
				return nil, err
			}
			return res.Brokers, nil
		},
		EvalFn:        EvalDecision,
		KillerFactory: m.buildKillSequence,
		Quarantine:    m.quarantine,
		Registry:      m.launchRegistry,
		E1Tracker:     m.e1Tracker,
		BaseDecisionOpts: DecisionOpts{
			FS:     NewOsFS(),
			Lister: NewPsLister(),
			// Dialer / Panes left nil; predicates degrade gracefully.
		},
	}

	return nil
}

// buildKillSequence returns a KillRunner backed by a real *KillSequence for
// the supplied broker. Production path; tests inject their own factory.
func (m *Module) buildKillSequence(rec BrokerRecord) KillRunner {
	return &KillSequence{
		Rec:       rec,
		Lister:    NewPsLister(),
		FS:        NewOsFS(),
		Signaller: realSignaller{},
		AuditDir:  m.auditDir,
	}
}

// RegisterRoutes implements core.Module. Adds the P2 sweep route alongside
// the P1 inventory route.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if m.handler == nil {
		m.handler = NewHandler(m.scanner)
	}
	mux.HandleFunc("GET /api/codex/brokers", m.handler.HandleBrokers)
	if m.sweepHandler != nil {
		mux.HandleFunc("POST /api/codex/brokers/sweep", m.sweepHandler.HandleSweep)
	}
}

// Start implements core.Module. P2 has no background work — the audit
// pruner + 30s tick land in P3.
func (m *Module) Start(_ context.Context) error {
	log.Println("[codexbroker] inventory + sweep endpoint enabled")
	return nil
}

// Stop implements core.Module. Nothing to clean up.
func (m *Module) Stop(_ context.Context) error { return nil }
