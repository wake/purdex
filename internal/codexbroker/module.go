package codexbroker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
)

// realDialer is the production Dialer for unix socket RPC. Used by
// EvalPredicateA (broker /thread/list) and KillSequence stepGraceful
// (broker /shutdown). Has no state — ok to share across goroutines.
type realDialer struct{}

// Dial implements Dialer. dialTimeout caps initial-connect blocking; the
// caller (EvalPredicateA / stepGraceful) sets read/write deadlines on the
// returned conn from rpcCtx.
func (realDialer) Dial(network, address string) (net.Conn, error) {
	return net.DialTimeout(network, address, 1*time.Second)
}

// realPaneChecker shells out to `tmux list-panes -aF '#{pane_id}'` once
// per scan and caches the result. The cache lives for ttl (default 5s)
// because predicate C is invoked once per broker per scan and a single
// fork/exec amortises across the whole inventory pass.
//
// PR review finding B: production wiring sets ttl=DefaultPaneCacheTTL.
// The exec is best-effort — a missing tmux binary or socket failure
// returns IsAlive=(false, error), which is what EvalPredicateC already
// expects and treats as "no pane evidence". The decision layer never
// crashes on the error.
type realPaneChecker struct {
	mu     sync.Mutex
	ttl    time.Duration
	expiry time.Time
	panes  map[string]bool
	err    error
}

// DefaultPaneCacheTTL is the lifetime of one tmux list-panes snapshot.
const DefaultPaneCacheTTL = 5 * time.Second

func newRealPaneChecker() *realPaneChecker {
	return &realPaneChecker{ttl: DefaultPaneCacheTTL}
}

// IsAlive implements PaneAliveChecker. Refreshes the cache when expired
// and consults the in-memory map.
func (c *realPaneChecker) IsAlive(pane string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pane == "" {
		return false, nil
	}
	now := time.Now()
	if c.panes == nil || now.After(c.expiry) {
		c.refresh(now)
	}
	if c.err != nil {
		return false, c.err
	}
	return c.panes[pane], nil
}

// refresh runs tmux list-panes once. Caller holds c.mu.
func (c *realPaneChecker) refresh(now time.Time) {
	c.expiry = now.Add(c.ttl)
	cmd := exec.Command("tmux", "list-panes", "-aF", "#{pane_id}")
	out, err := cmd.Output()
	if err != nil {
		c.err = err
		c.panes = map[string]bool{}
		return
	}
	c.err = nil
	m := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			m[line] = true
		}
	}
	c.panes = m
}

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

	// quarantineDegraded captures the PR review finding D fail-closed
	// state. When the on-disk quarantine.json fails to load (corruption,
	// I/O error other than IsNotExist), the file is renamed aside and
	// this flag is set. The sweep handler's ApplyDisabled is wired to a
	// non-empty reason while degraded; mode=apply requests then return
	// 503 with that reason. Operator clears the situation by deleting the
	// .bak rename and restarting the daemon.
	quarantineDegraded bool
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

	// PR review finding D: fail-closed on a corrupt quarantine.json. The
	// helper renames the corrupt file aside, populates m.quarantine with
	// an empty in-memory file (so the sweep handler can still write fresh
	// entries during the degraded window), and sets m.quarantineDegraded
	// = true. The sweep handler picks the flag up via ApplyDisabled below
	// and returns 503 on mode=apply until operator restart.
	if loadErr := m.loadQuarantineFromPath(quarantinePath(root)); loadErr != nil {
		log.Printf("[codexbroker] quarantine load DEGRADED (apply disabled until restart): %v", loadErr)
	}

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

	// Sweep handler. PR review finding C: wire QuarantineStore +
	// QuarantinePath + Lister so an identity-mismatch from KillSequence.Run
	// translates into a persisted E2 quarantine entry instead of being
	// silently logged.
	m.sweepHandler = &SweepHandler{
		ScanFn: func(ctx context.Context) ([]BrokerRecord, error) {
			res, err := m.scanner.Scan(ctx)
			if err != nil {
				return nil, err
			}
			return res.Brokers, nil
		},
		EvalFn:          EvalDecision,
		KillerFactory:   m.buildKillSequence,
		Quarantine:      m.quarantine,
		QuarantineStore: &QuarantineStore{},
		QuarantinePath:  quarantinePath(root),
		Lister:          NewPsLister(),
		Registry:        m.launchRegistry,
		E1Tracker:       m.e1Tracker,
		ApplyDisabled:   m.applyDisabledReason(),
		BaseDecisionOpts: DecisionOpts{
			FS:     NewOsFS(),
			Lister: NewPsLister(),
			// PR review finding B: previously left Dialer + Panes nil,
			// silently pushing predicates A + C toward false and brokers
			// toward baseline Kill=true. Production now wires both seams
			// so EvalPredicateA can reach a live broker socket and
			// EvalPredicateC can confirm a tmux pane is still alive.
			Dialer: realDialer{},
			Panes:  newRealPaneChecker(),
		},
	}

	return nil
}

// buildKillSequence returns a KillRunner backed by a real *KillSequence for
// the supplied broker. Production path; tests inject their own factory.
//
// PR review finding B: previously omitted Dialer, so KillSequence.Step 2
// (graceful RPC shutdown) was silently never attempted — every operator-
// issued kill jumped straight to SIGTERM/SIGKILL. Now wires realDialer.
func (m *Module) buildKillSequence(rec BrokerRecord) KillRunner {
	return &KillSequence{
		Rec:       rec,
		Lister:    NewPsLister(),
		Dialer:    realDialer{},
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

// loadQuarantineFromPath populates m.quarantine from disk and reports any
// fail-closed condition (PR review finding D).
//
// Outcomes:
//
//   - File absent → m.quarantine = empty Version=1, returns nil.
//   - Successful load → m.quarantine = decoded file, returns nil.
//   - Corruption / non-IsNotExist I/O error → file is renamed to
//     <path>.bak-<unixTs> for forensics, m.quarantine = empty Version=1,
//     m.quarantineDegraded = true, returns the load error.
//
// The handler-side reaction (mode=apply rejected with 503) is wired in
// Init via SweepHandler.ApplyDisabled = m.applyDisabledReason().
func (m *Module) loadQuarantineFromPath(path string) error {
	store := &QuarantineStore{}
	qf, err := store.Load(path)
	if err == nil {
		m.quarantine = qf
		m.quarantineDegraded = false
		return nil
	}
	// IsNotExist is treated as "first-run steady state" by QuarantineStore.Load
	// itself (it returns an empty file + nil err); reaching here means a real
	// error: corruption or genuine I/O failure.
	bakPath := fmt.Sprintf("%s.bak-%d", path, time.Now().Unix())
	if renameErr := os.Rename(path, bakPath); renameErr != nil && !errors.Is(renameErr, os.ErrNotExist) {
		log.Printf("[codexbroker] quarantine corrupt-rename failed (%s -> %s): %v", path, bakPath, renameErr)
	}
	m.quarantine = &QuarantineFile{Version: 1}
	m.quarantineDegraded = true
	return err
}

// applyDisabledReason returns the SweepHandler.ApplyDisabled value to wire
// at Init time. Empty when the module is healthy; non-empty (and explicit)
// when the operator must intervene before mode=apply is safe.
func (m *Module) applyDisabledReason() string {
	if m.quarantineDegraded {
		return "quarantine_load_failed"
	}
	return ""
}
