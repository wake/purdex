// Package agent provides the agent hook event module.
// It receives hook events from `pdx hook`, projects live state into frames,
// and broadcasts to WS subscribers. AgentEventStore remains as a legacy
// fallback source for pre-frame sessions and session rename compatibility.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/execstat"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	frames    *store.FramesStore
	traces    *store.TraceStore
	usage     *store.ContextUsageStore // persisted statusline readings (#2406); nil without an events DB
	sessions  session.SessionProvider
	registry  *agentpkg.Registry
	uploadDir string
	traceSink *hookTraceSink

	// sessionStarts fans granted SessionStarts out to in-process subscribers.
	sessionStarts sessionStartHub

	// turnEnds fans accepted main-turn Stops out to in-process subscribers (T-3a1);
	// turnEndSeq numbers the hooks as they arrive (stampTurnEnd).
	turnEnds turnEndHub
	// notifies fans every live tmux `hook` frame out to in-process subscribers (push, PU-3a).
	notifies   notifyHub
	turnEndSeq atomic.Int64

	prober    *probe.Prober
	probeOrch *probeOrchestrator
	tmux      tmux.Executor

	// ownerResolver is the test seam for the transcript handler; nil means
	// resolveSessionOwnerErr.
	ownerResolver func(ctx context.Context, code string) (PaneOwner, bool, error)

	mu             sync.Mutex
	currentStatus  map[string]agentpkg.Status
	subagents      map[string][]agentpkg.SubagentRef
	activeWatchers map[string]string // tmuxSession → agentType
	// lastEmittedLights is, per tmux session, the digest of the last light
	// frame sent for it (hook, probe, sweep or mod worker). The mod worker
	// emits only when its fresh digest differs. Protected by m.mu.
	lastEmittedLights map[string]lightsDigest
	// emit is the hook emit slot (hookemitter.go): every hook frame is read,
	// built, stamped and broadcast inside emit.mu. Total lock order:
	// emit.mu → mu → modMu.
	emit hookEmitter
	// nonTmuxLast is, per non-tmux agent code ("cc-<session id>"), the last
	// frame the slot sent for it, so a complete snapshot can list sessions that
	// have no pane, frame or agent_events row (nontmux_last.go). Protected by
	// emit.mu; nonTmuxNow is its clock (nil means time.Now), a test seam.
	nonTmuxLast map[string]nonTmuxEntry
	nonTmuxNow  func() time.Time

	// W6-3 P1-T4: ProbeIntent dispatcher state. activeProbeIntents and
	// probeIntentGen are protected by m.mu (same mutex as activeWatchers).
	// probeIntentDisp is the long-lived dispatcher pointer; created in New().
	activeProbeIntents map[string]map[agentpkg.ProbeIntentKind]activeIntent
	probeIntentGen     uint64
	probeIntentDisp    *probeIntentDispatcher

	// statusSnapshots caches the latest statusline payload per sessionCode.
	// Display-only, not persisted; guarded by snapshotMu (separate from mu
	// because hot-path agent.status POSTs shouldn't contend with hook writes).
	snapshotMu      sync.RWMutex
	statusSnapshots map[string]statusSnapshot
	// contextUsage keeps the last statusline context reading per CC session
	// id (not per session code: two CC panes in one tmux session must not
	// overwrite each other). Bounded by contextUsageCap; also under
	// snapshotMu. usageSeq breaks eviction ties within one millisecond.
	contextUsage map[string]ContextUsage
	usageSeq     int64
	// Persistence of contextUsage (#2406), also under snapshotMu: usageDirty holds the sessions whose reading must be
	// written at the next flush, usageDeleted the ones evicted since (their rows go), usagePersistedAt the At each
	// session's row holds (an unchanged value is rewritten only when that At is older than usageRefreshAfter).
	usageDirty         map[string]struct{}
	usageFlushMu       sync.Mutex           // one flush or removal at a time, snapshot to write
	usageDeleteFn      func([]string) error // test seam; nil = the store's Delete
	usageAfterSnapshot func()               // test seam: runs in a flush after its snapshot, before it writes
	usageDeleted       map[string]struct{}
	usagePersistedAt   map[string]int64

	// testObservers: per-nonce channel for the statusline self-test endpoint.
	// Guarded by testMu (separate from snapshotMu and mu so test traffic
	// cannot block production hook / status writes).
	testMu        sync.Mutex
	testObservers map[string]*testObserver

	// testSpawnProxy is a test seam; production leaves this nil so the handler
	// falls back to defaultSpawnTestProxy which execs the real pdx binary.
	testSpawnProxy func(nonce string) error

	sweepCancel context.CancelFunc
	sweepWG     sync.WaitGroup

	// usageCancel / usageWG run and join the context-usage flusher (#2406).
	usageCancel context.CancelFunc
	usageWG     sync.WaitGroup

	pathHintDedup  *PathHintDedupCache
	pathHintBuffer *PathHintRingBuffer

	// listFramesFn is a test seam for liveFrameProjections' frames.ListAll
	// (fault injection); nil in production.
	listFramesFn func() ([]store.Frame, error)

	// modLights is the mod event overlay's state (modlights.go).
	modLights
}

// Test seams for Module.New. framesInitFn failure is fatal (hook processing
// depends on frames); tracesInitFn failure is best-effort (trace
// observability degrades to nil, daemon continues).
var (
	framesInitFn = func(e *store.AgentEventStore) (*store.FramesStore, error) { return e.Frames() }
	tracesInitFn = func(e *store.AgentEventStore) (*store.TraceStore, error) { return e.Traces() }
)

// New creates a new agent Module backed by the given AgentEventStore.
//
// Returns an error ONLY when the frames store cannot be initialized —
// typically when on-disk agent_frames contains malformed subagents_json
// that migrateFramesDB refuses to classify. That failure is fatal because
// hook processing depends on m.frames; continuing with m.frames == nil
// would degrade silently via the module's m.frames == nil fallbacks.
//
// Trace store initialization is best-effort: the module already tolerates
// m.traces == nil / m.traceSink == nil in normal operation (no trace
// recording, hook processing still runs). A trace-table migration
// or corruption error is logged and the module continues without trace
// observability, not treated as a daemon-fatal condition.
func New(events *store.AgentEventStore) (*Module, error) {
	var frames *store.FramesStore
	var traces *store.TraceStore
	var usage *store.ContextUsageStore
	if events != nil {
		var err error
		frames, err = framesInitFn(events)
		if err != nil {
			return nil, fmt.Errorf("agent module: frames store: %w", err)
		}
		traces, err = tracesInitFn(events)
		if err != nil {
			log.Printf("[agent] traces store unavailable, continuing without trace observability: %v", err)
			traces = nil
		}
		if usage, err = events.ContextUsage(); err != nil {
			log.Printf("[agent] context usage persistence unavailable, readings stay in memory only: %v", err)
			usage = nil
		}
	}
	m := &Module{
		events:             events,
		frames:             frames,
		traces:             traces,
		usage:              usage,
		registry:           agentpkg.NewRegistry(),
		currentStatus:      make(map[string]agentpkg.Status),
		subagents:          make(map[string][]agentpkg.SubagentRef),
		activeWatchers:     make(map[string]string),
		lastEmittedLights:  make(map[string]lightsDigest),
		activeProbeIntents: make(map[string]map[agentpkg.ProbeIntentKind]activeIntent),
		statusSnapshots:    make(map[string]statusSnapshot),
		contextUsage:       make(map[string]ContextUsage),
		usageDirty:         make(map[string]struct{}),
		usageDeleted:       make(map[string]struct{}),
		usagePersistedAt:   make(map[string]int64),
		testObservers:      make(map[string]*testObserver),
		pathHintDedup:      NewPathHintDedupCache(5 * time.Second),
		pathHintBuffer:     NewPathHintRingBuffer(200),
		modLights:          newModLights(),
	}
	if traces != nil {
		m.traceSink = newHookTraceSink(traces)
	}
	// probeOrchestrator owns probe-watcher lifecycle. Created here (not in
	// Init) so it is always available for tests that bypass Init and assign
	// m.prober directly. The orchestrator resolves m.prober lazily, so
	// "AFTER prober is set" semantics hold at startWatch call time.
	m.probeOrch = newProbeOrchestrator(m)
	// W6-3 P1-T4: ProbeIntent dispatcher. Created here (not in Init) so the
	// pointer is non-nil throughout module lifetime; tests that bypass Init
	// can call manageActivityWatch / replay paths without nil-checking.
	// parentCtx defaults to context.Background; rotated in Stop().
	m.probeIntentDisp = newProbeIntentDispatcher(m)
	// W6-3 P2-T4: route declared ProbeIntent kinds to their per-agent detectors.
	// The closure resolves m.tmux lazily so it picks up Init()'s wiring (m.tmux
	// is nil at New() time and assigned during Init); callers (lifecycle plan)
	// only invoke startDetector after applyIntentLifecycle has read top-frame
	// state, by which point Init has long completed in production.
	//
	// Per spec §5.4 lines 776-780: dispatcher switches on Kind; future Kinds
	// add cases here. Unknown Kind falls through to defaultStartProbeIntentDetector
	// (waits on ctx, never emits) — defensive landing for a Kind that surfaces
	// in registry before its detector lands.
	m.probeIntentDisp.startDetector = func(ctx context.Context, mod *Module, kind agentpkg.ProbeIntentKind, paneID string, senderPID int, out chan<- agentpkg.Signal) {
		switch kind {
		case agentpkg.ProbeIntentKindProcessDead:
			codex.StartProcessDeadDetector(ctx, mod.tmux, paneID, senderPID, out)
		case agentpkg.ProbeIntentKindScreenChange:
			// W6-6: codex permission-approval recovery. The detector
			// observes top-10 lines of the pane via mod.prober.Watch
			// and emits one Signal once Phase A (ScreenStable) +
			// Phase B (ScreenChanged) complete with a verified codex
			// identity.
			//
			// isCodexAlive resolves identity via FirstAliveAgentInTree
			// rather than IsAliveFor: FirstAliveAgentInTree internally
			// uses tmux ActivePanePID(target) which honors paneID `%N`
			// targets exactly, while IsAliveFor's PanePID resolves a
			// pane id target to the FIRST pane of its window — wrong
			// for non-first siblings. The IsAliveFor inconsistency is
			// pre-existing infrastructure tracked in a follow-up issue
			// (W6-6 spec §11 line 744).
			isCodexAlive := func() bool {
				t, _, err := mod.prober.FirstAliveAgentInTree(paneID)
				return err == nil && t == "codex"
			}
			codex.StartScreenChangeDetector(ctx, mod.prober, isCodexAlive, paneID, senderPID, out)
		default:
			defaultStartProbeIntentDetector(ctx, mod, kind, paneID, senderPID, out)
		}
	}
	// supportedKinds MUST mirror the switch above. Lifecycle fails closed
	// for any kind missing here (audit F6). Adding a new kind requires
	// extending both the switch and this map together; the drift test
	// `TestProbeIntentDrift_AllDeclaredKindsHaveDispatcherCase` enforces parity.
	m.probeIntentDisp.supportedKinds = map[agentpkg.ProbeIntentKind]struct{}{
		agentpkg.ProbeIntentKindProcessDead:  {},
		agentpkg.ProbeIntentKindScreenChange: {},
	}
	return m, nil
}

func (m *Module) Name() string           { return "agent" }
func (m *Module) Dependencies() []string { return []string{"session", modeventsmod.ServiceName} }

// Init retrieves the SessionProvider, initializes the provider registry,
// and registers CC and Codex providers.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	m.initModLights(c)
	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		log.Printf("[agent] warning: session provider not found")
		return nil
	}
	m.sessions = svc.(session.SessionProvider)

	// Surface whether the hook hot path will use the LookupCodeByName fast
	// path or fall back to the 1+7×S ListSessions fan-out. Anything that
	// wraps SessionProvider with a decorator that drops LookupCodeByName
	// will be visible at startup instead of as a silent latency regression.
	if _, ok := m.sessions.(sessionCodeLookuper); ok {
		log.Print("[agent] hook fast-path active (LookupCodeByName available)")
	} else {
		log.Print("[agent] hook fast-path NOT active — sessions does not implement LookupCodeByName")
	}

	// Expose event store and module so other modules (e.g. session rename) can update it.
	c.Registry.Register("agent.events", m.events)
	c.Registry.Register("agent.module", m)
	c.Registry.Register(OwnerResolverKey, OwnerResolver(m))
	c.Registry.Register(TerminalSessionsKey, TerminalSessions(m))
	c.Registry.Register(NotifyFeedKey, NotifyFeed(m))

	if m.uploadDir == "" {
		c.CfgMu.RLock()
		m.uploadDir = c.Cfg.UploadDir
		c.CfgMu.RUnlock()
	}

	// Prober (shared across all providers)
	m.tmux = c.Tmux
	m.prober = probe.New(c.Tmux)
	ccProvider := agentcc.NewProvider(m.prober, c.Tmux, c.Cfg, &c.CfgMu)
	m.prober.RegisterIdentifier(ccProvider.Type(), ccProvider.Identify)
	m.prober.RegisterReadiness(ccProvider.Type(), agentcc.NewReadinessChecker(c.Tmux))
	ccProvider.RegisterServices(c.Registry)
	m.registry.Register(ccProvider)

	codexProvider := codex.NewProvider()
	m.prober.RegisterIdentifier(codexProvider.Type(), codexProvider.Identify)
	m.prober.RegisterReadiness(codexProvider.Type(), codex.NewReadinessChecker(c.Tmux))
	c.Registry.Register("agent.prober", m.prober)

	// Listen for config changes to update mutable module state.
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		newDir := c.Cfg.UploadDir
		c.CfgMu.RUnlock()
		if newDir != "" {
			m.mu.Lock()
			m.uploadDir = newDir
			m.mu.Unlock()
		}
	})

	// Codex provider
	m.registry.Register(codexProvider)

	opencodeProvider := opencode.NewProvider()
	m.prober.RegisterIdentifier(opencodeProvider.Type(), opencodeProvider.Identify)
	m.registry.Register(opencodeProvider)

	// Expose registry for other modules
	c.Registry.Register("agent.registry", m.registry)

	return nil
}

// RegisterRoutes registers the agent API endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/agent/event", m.handleEvent)
	mux.HandleFunc("GET /api/hooks/{agent}/status", m.handleHookStatus)
	mux.HandleFunc("POST /api/hooks/{agent}/setup", m.handleHookSetup)
	mux.HandleFunc("GET /api/agent/{agent}/statusline/status", m.handleStatuslineStatus)
	mux.HandleFunc("POST /api/agent/{agent}/statusline/setup", m.handleStatuslineSetup)
	mux.HandleFunc("POST /api/agent/cc/statusline/test", m.handleStatuslineTest)
	mux.HandleFunc("POST /api/agent/cc/statusline/test/ready", m.handleStatuslineTestReady)
	mux.HandleFunc("POST /api/agent/status", m.handleAgentStatus)
	mux.HandleFunc("GET /api/agent/title/status", m.handleTitleStatus)
	mux.HandleFunc("POST /api/agent/title/setup", m.handleTitleSetup)
	mux.HandleFunc("GET /api/agents/detect", m.handleDetect)

	// Ownership query: which agent owns this tmux session (spec §5.3)
	mux.HandleFunc("GET /api/sessions/{code}/provenance", m.handleSessionProvenance)
	mux.HandleFunc("GET /api/sessions/{code}/transcript", m.handleSessionTranscript)

	// Upload (unchanged)
	mux.HandleFunc("POST /api/agent/upload", m.handleUpload)
	mux.HandleFunc("GET /api/upload/stats", m.handleUploadStats)
	mux.HandleFunc("GET /api/upload/files", m.handleUploadFiles)
	mux.HandleFunc("DELETE /api/upload/files/{session}/{filename}", m.handleDeleteUploadFile)
	mux.HandleFunc("DELETE /api/upload/files/{session}", m.handleDeleteUploadSession)
	mux.HandleFunc("DELETE /api/upload/files", m.handleDeleteAllUploads)
}

// Start replays DB state and registers OnSubscribe callback.
//
// W6-3 P1-T6 (closes #698): after replayFromDB hydrates currentStatus +
// frame projection and startSweep is running, replayStatus walks every
// session and re-evaluates ProbeIntent gating so detectors armed before
// shutdown rearm after restart. Sequence MUST be replayFromDB → startSweep
// → replayStatus so detectors see fully-hydrated state on first poll
// (per spec §6.3 / §6.4).
func (m *Module) Start(_ context.Context) error {
	// Step timings (#1767): observation only, same order as before.
	execBase := execstat.Take()
	st := core.NewStepTimer(nil)
	st.Run("sweepOnce", func() {
		if err := m.sweepOnce(); err != nil {
			log.Printf("[agent] startup sweep: %v", err)
		}
	})
	st.Run("replayFromDB", m.replayFromDB)
	st.Run("startSweep", m.startSweep)
	st.Run("replayStatus", func() {
		if m.probeIntentDisp != nil {
			m.probeIntentDisp.replayStatus()
		}
	})
	// Outside the step timer, whose line is a fixed set (#1767): the persisted statusline readings come back for the
	// sessions that are live, then the flusher starts (#2406).
	m.restoreContextUsage(context.Background())
	m.startContextUsageFlush()
	log.Printf("[agent] start: %s", st)
	log.Print(startExecLine(execBase))

	m.startModLights()

	if m.core != nil {
		m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
			m.sendSnapshot(sub)
			m.sendStatuslineSnapshot(sub)
		})
	}

	log.Println("[agent] hook event endpoint registered")
	return nil
}

// getUploadDir returns the current upload directory under lock.
func (m *Module) getUploadDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uploadDir
}

// Stop cancels all active Activity watchers and resets transient state.
func (m *Module) Stop(_ context.Context) error {
	m.stopModLights()
	if m.usageCancel != nil { // the flusher writes the readings it still holds as it ends
		m.usageCancel()
		m.usageWG.Wait()
		m.usageCancel = nil
	}
	if m.sweepCancel != nil {
		m.sweepCancel()
		m.sweepWG.Wait()
		m.sweepCancel = nil
	}
	if m.prober != nil {
		m.prober.StopAllWatches()
	}
	// W6-3 P1-T4: cancel every armed ProbeIntent detector before clearing
	// activeWatchers. stopAll uses the same m.mu, so a concurrent applyStatus
	// either observes the pre-Stop active map or runs to completion — never
	// partial.
	if m.probeIntentDisp != nil {
		m.probeIntentDisp.stopAll()
	}
	m.mu.Lock()
	m.activeWatchers = make(map[string]string)
	m.mu.Unlock()
	// The trace sink is deliberately NOT closed here: HTTP is still draining
	// during Stop and hook handlers enqueue traces on their way out. It is
	// closed by Close (core.Closer), which runs after the server has drained.
	return nil
}

// Close implements core.Closer. It runs from CloseModules, after the HTTP
// server has drained, so every hook handler's trailing trace Enqueue (and any
// probe-intent consumer tail) has already been queued; Close then flushes the
// trace sink. Idempotent: hookTraceSink.Close is sync.Once-guarded.
func (m *Module) Close() error {
	m.traceSink.Close() // nil-safe
	return nil
}

// renameSessionLocked transfers in-memory agent state (subagents, currentStatus,
// activeWatchers, activeProbeIntents) from oldName to newName.  CALLER MUST
// hold m.mu.
//
// Returns a slice of CancelFuncs that the caller MUST invoke AFTER releasing
// m.mu. This routes the W6-3 P1-T5 ProbeIntent cleanup through the same
// post-unlock pattern as dispatchProbeIntentReeval: detector goroutines that
// were keyed under oldName get cancelled outside the lock so any concurrent
// applyProbeGuards re-acquisition does not deadlock. Caller may pass the
// returned slice to dispatchProbeIntentRenameCancels (or invoke each cancel
// directly).
func (m *Module) renameSessionLocked(oldName, newName string) []context.CancelFunc {
	if subs, ok := m.subagents[oldName]; ok {
		m.subagents[newName] = subs
		delete(m.subagents, oldName)
	}
	if status, ok := m.currentStatus[oldName]; ok {
		m.currentStatus[newName] = status
		delete(m.currentStatus, oldName)
	}
	if d, ok := m.lastEmittedLights[oldName]; ok {
		m.lastEmittedLights[newName] = d
		delete(m.lastEmittedLights, oldName)
	}
	if _, ok := m.activeWatchers[oldName]; ok {
		// W3 撤回: rename is now stop-only. Phase 4a-1 wired a stopWatch +
		// startWatch(newName) sequence to keep the screen-watcher alive across
		// rename, but with manageActivityWatch reduced to a default no-op
		// there is no production caller starting watchers in the first place.
		// W6 will reintroduce starts via ProbeIntent; until then, evict the
		// renamed entry and tear down the orchestrator-side watcher (if any).
		// activeWatchers eviction is m.mu-protected; caller holds m.mu.
		delete(m.activeWatchers, oldName)
		// orchestrator stop is lock-free wrt m.mu (it touches prober.watcherMu,
		// a different mutex) so calling it while we hold m.mu is safe.
		// nil-prober is handled inside stopWatch (R14 fix).
		m.probeOrch.stopWatch(oldName)
		// Codex finding #2 regression (kept under W3): migrate the active
		// graceWindow so a hook-set status that was just recorded under
		// oldName is not overwritten by a probe event arriving for newName
		// within probeGraceWindow once W6 wires starts again.
		m.probeOrch.migrateLastHookAt(oldName, newName)
	}

	// W6-3 P1-T5: drop ProbeIntent active entries keyed under oldName. The
	// dispatcher cannot rearm under newName until our caller invokes
	// dispatcher.applyStatus(newName, ...) post-unlock, so collecting +
	// returning the cancel list (rather than invoking inside the lock) keeps
	// the cancel + arm sequence contiguous from the dispatcher's POV.
	var toCancel []context.CancelFunc
	if perSession, ok := m.activeProbeIntents[oldName]; ok {
		for _, cur := range perSession {
			toCancel = append(toCancel, cur.cancel)
		}
		delete(m.activeProbeIntents, oldName)
	}
	return toCancel
}

// RenameSession transfers in-memory agent state from oldName to newName
// under the module's lock.  Used by callers that don't need to coordinate
// with other rename steps (e.g. tests).  Production callers should prefer
// RenameSessionAtomic to make the rename atomic with tmux + DB updates.
//
// W6-3 P1-T5: after the in-memory rename completes, the ProbeIntent
// dispatcher is re-evaluated for newName so any active codex / future
// per-agent probes stay armed against the renamed session. The lock is
// released before invoking dispatcher.applyStatus because the dispatcher
// itself acquires m.mu (deadlock otherwise).
func (m *Module) RenameSession(oldName, newName string) {
	m.mu.Lock()
	cancels := m.renameSessionLocked(oldName, newName)
	plan, hasPlan := m.captureProbeIntentReevalLocked(newName)
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.dispatchProbeIntentReeval(newName, plan, hasPlan)
}

// RenameSessionAtomic runs doRename under the module's lock and then
// transfers in-memory state from oldName to newName.  This makes the
// entire rename (tmux + DB + in-memory) atomic from the perspective of
// concurrent hook events: any handler that acquires m.mu while a rename
// is in progress observes either the pre-rename or post-rename state,
// never partial state.  If doRename returns an error, the in-memory
// transfer is skipped and the error is propagated.
//
// Tradeoff: doRename is expected to include the tmux rename exec.Command,
// which runs under the lock.  This can delay all concurrent hook handlers
// by the duration of the tmux call (~50ms normally).  This is an intentional
// choice: hook handlers are brief and renames are low-frequency (user-
// triggered), so sacrificing a small amount of throughput during rename
// in exchange for full atomicity is the right tradeoff.  doRename MUST NOT
// call any method that acquires m.mu (would deadlock).
func (m *Module) RenameSessionAtomic(oldName, newName string, doRename func() error) error {
	m.mu.Lock()
	if err := doRename(); err != nil {
		m.mu.Unlock()
		return err
	}
	cancels := m.renameSessionLocked(oldName, newName)
	plan, hasPlan := m.captureProbeIntentReevalLocked(newName)
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.dispatchProbeIntentReeval(newName, plan, hasPlan)
	return nil
}

// probeIntentReevalPlan records the (agentType, status) pair captured during
// rename so the post-unlock dispatcher.applyStatus call observes the same
// state the rename observed under m.mu. Routed through a struct (rather
// than two return values) so future fields (e.g. paneID overrides) extend
// without touching every call site.
type probeIntentReevalPlan struct {
	agentType string
	status    agentpkg.Status
}

// captureProbeIntentReevalLocked snapshots the post-rename ProbeIntent
// re-evaluation inputs: the new session's currentStatus + the top frame's
// agentType. Returns hasPlan=false when no currentStatus exists yet (rename
// of a session before any hook fired).
//
// CALLER MUST hold m.mu. lookupTopFrameForSessionLocked + projectionForSession
// touch their own mutexes (frames store, tmux pane resolver) that are
// independent of m.mu, so calling them under m.mu is safe.
func (m *Module) captureProbeIntentReevalLocked(session string) (probeIntentReevalPlan, bool) {
	status, ok := m.currentStatus[session]
	if !ok {
		return probeIntentReevalPlan{}, false
	}
	agentType := ""
	if proj, err := m.projectionForSession(session); err == nil && proj != nil && proj.TopFrame != nil {
		agentType = proj.TopFrame.AgentType
	}
	return probeIntentReevalPlan{agentType: agentType, status: status}, true
}

// dispatchProbeIntentReeval invokes dispatcher.applyStatus outside m.mu so
// the dispatcher can take its own lock. No-op when hasPlan=false (no
// currentStatus to evaluate against) or when dispatcher is nil (defensive;
// New() always wires it).
func (m *Module) dispatchProbeIntentReeval(session string, plan probeIntentReevalPlan, hasPlan bool) {
	if !hasPlan {
		return
	}
	if m.probeIntentDisp == nil {
		return
	}
	m.probeIntentDisp.applyStatus(session, plan.agentType, plan.status)
}

// replayFromDB rebuilds in-memory state from persisted frame projections and
// falls back to legacy agent_events for sessions that have not migrated yet.
func (m *Module) replayFromDB() {
	projectedSessions := make(map[string]struct{})
	if projections, err := m.liveSessionProjections(); err == nil {
		for _, item := range projections {
			projectedSessions[item.SessionName] = struct{}{}
			m.mu.Lock()
			syncProjectionState(m.currentStatus, m.subagents, item.SessionName, &item.Projection)
			m.mu.Unlock()
		}
	} else {
		log.Printf("[agent] replay frames: %v", err)
	}
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] replay: %v", err)
		return
	}
	for _, ev := range all {
		if _, ok := projectedSessions[ev.TmuxSession]; ok {
			continue
		}
		provider, ok := m.registry.Get(ev.AgentType)
		if !ok {
			continue
		}
		result := provider.DeriveStatus(ev.EventName, ev.RawEvent)
		if !result.Valid {
			// Pre-W2 stored EventName (e.g. opencode legacy literal "Stop")
			// no longer matches a Pdx-prefixed catalog entry; mirror the
			// hot-path invalid-result cleanup at handler.go:230 so a
			// subsequent sendSnapshot doesn't broadcast a stale row.
			if m.events != nil {
				if err := m.events.Delete(ev.TmuxSession); err != nil {
					log.Printf("[agent] replay cleanup of legacy event: %v", err)
				}
			}
			continue
		}
		if result.Status != "" {
			m.mu.Lock()
			m.currentStatus[ev.TmuxSession] = result.Status
			m.mu.Unlock()
		}
	}
}

// sendSnapshot gives a new WS subscriber the agent state of every session on
// this host. Everything happens in one critical section under emit.mu, the
// hook emit slot: the projections are read, the frames sent, the in-memory
// state synced and the baseline seeded while no hook, probe, sweep or mod
// worker frame can go out. Anything broadcast before it has seq <= H (the
// slot's high-water mark), anything after it has seq > H, so a client can
// order the snapshot against the live frames. A snapshot sent outside the
// slot could land after a newer frame and, with a baseline that already
// exists, leave this connection on the stale state for good.
//
// An agent.v2 subscriber gets one complete agent.snapshot frame
// (agentSnapshotFrame); every other subscriber gets the per-session replay
// hook frames it always had, now carrying (epoch, H, snapshot:true), which
// old clients ignore.
//
// Cost: a frame-projection read (one tmux call, see paneSnapshot), the
// legacy agent_events listing and the session list, once per subscribe.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	if m.sessions == nil {
		return
	}
	var stale []string
	defer func() {
		// stale legacy rows are deleted after the slot is released
		for _, name := range stale {
			if err := m.events.Delete(name); err != nil {
				log.Printf("[agent] snapshot cleanup of legacy event: %v", err)
			}
		}
	}()

	e := &m.emit
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.end(e.begin(), "", kindSnapshot)

	boot := ""
	if m.core != nil {
		boot = m.core.BootID
	}
	if e.epoch == "" {
		e.epoch = boot
	}
	// The legacy rows and the session names are read inside the slot too: the
	// snapshot is the complete list as of H, and a session that emitted its
	// first frame (seq <= H) while the names were read outside would be in
	// neither the snapshot nor the frames the client keeps. Cost (measured):
	// the agent_events listing is ~0.5 ms for 200 rows; ListSessions has a 1 s
	// cache that resolveSessionCode may just have warmed, ~27 ms cold for 5
	// sessions.
	legacy, legacyErr := m.readLegacyRows()
	items, staleNames, framesErr := m.snapshotItemsLocked(legacy)
	stale = staleNames
	for i := range items {
		items[i].event.Epoch, items[i].event.Seq, items[i].event.Snapshot = e.epoch, e.seq, true
	}

	if sub.Wants(core.FeatureAgentV2) {
		// agent.snapshot is authoritative: the client replaces the host's
		// whole agent state with it. A list built from a failed read would
		// clear sessions that are merely unreadable, so it is not sent; the
		// connection ends and the reconnect asks again.
		if framesErr != nil || legacyErr != nil {
			log.Printf("[agent] agent.snapshot not sent (frames: %v, legacy: %v); closing the subscriber so it reconnects", framesErr, legacyErr)
			if m.core != nil {
				m.core.Events.Remove(sub)
			}
			return
		}
		sent := m.sendAgentSnapshot(sub, e.epoch, e.seq, items)
		for _, it := range items {
			m.syncSnapshotItem(it, sent)
		}
		return
	}
	for _, it := range items {
		payload, _ := json.Marshal(it.event)
		data, _ := json.Marshal(core.HostEvent{Type: "hook", Session: it.code, Value: string(payload)})
		m.syncSnapshotItem(it, sub.TrySend(data))
	}
}

// snapshotItem is one session of a snapshot: the code it is sent under, the
// frame, and (for a frame projection) the projection that frame came from.
type snapshotItem struct {
	code    string
	session string // tmux session name; "" for a non-tmux session
	event   agentpkg.NormalizedEvent
	proj    *SessionProjection
}

// syncSnapshotItem brings the in-memory state of a frame-projection session
// to what the snapshot says and, when the frame reached the subscriber (sent),
// seeds its light baseline. Items that are not frame projections have nothing
// to sync.
func (m *Module) syncSnapshotItem(it snapshotItem, sent bool) {
	if it.proj == nil || it.session == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	syncProjectionState(m.currentStatus, m.subagents, it.session, it.proj)
	if sent {
		m.seedBaselineLocked(it.session, it.proj, it.event)
	}
}

// snapshotItemsLocked lists every session with an agent: the frame
// projections, the legacy agent_events sessions (rows read by readLegacyRows),
// and the non-tmux sessions the slot has seen (nonTmuxLast, expired first).
// Under emit.mu. framesErr is a failed frame-projection read: the list is then
// missing sessions (the legacy subscriber still gets what there is; agent.v2
// must not treat it as complete). stale are legacy rows to delete once the
// slot is released.
func (m *Module) snapshotItemsLocked(legacy *legacyRows) (items []snapshotItem, stale []string, framesErr error) {
	projectedSessions := make(map[string]struct{})
	projections, err := m.liveSessionProjections()
	if err != nil {
		log.Printf("[agent] snapshot frames: %v", err)
		framesErr = err
	} else {
		for i := range projections {
			item := projections[i]
			projectedSessions[item.SessionName] = struct{}{}
			if item.SessionCode == "" {
				continue
			}
			proj := item.Projection
			normalized := buildProjectionNormalized(&proj, proj.TopFrame.AgentType, "replay", time.Now().UnixNano(), agentpkg.DeriveResult{})
			items = append(items, snapshotItem{code: item.SessionCode, session: item.SessionName, event: normalized, proj: &proj})
		}
	}
	legacyItems, stale := m.legacySnapshotItems(legacy, projectedSessions)
	items = append(items, legacyItems...)

	m.expireNonTmuxLocked(m.nonTmuxClock())
	codes := make([]string, 0, len(m.nonTmuxLast))
	for code := range m.nonTmuxLast {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		ev := m.nonTmuxLast[code].event
		ev.RawEventName = "replay"
		items = append(items, snapshotItem{code: code, event: ev})
	}
	return items, stale, framesErr
}

// legacyRows is the agent_events table and the session names, read inside the
// emit slot (sendSnapshot).
type legacyRows struct {
	events     []store.AgentEvent
	nameToCode map[string]string
}

// readLegacyRows reads the legacy part of a snapshot. nil rows with a nil error
// means there is nothing legacy to list.
func (m *Module) readLegacyRows() (*legacyRows, error) {
	if m.events == nil {
		return nil, nil
	}
	all, err := m.events.ListAll()
	if err != nil {
		log.Printf("[agent] snapshot: %v", err)
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] snapshot sessions: %v", err)
		return nil, err
	}
	nameToCode := make(map[string]string, len(sessions))
	for _, s := range sessions {
		nameToCode[s.Name] = s.Code
	}
	return &legacyRows{events: all, nameToCode: nameToCode}, nil
}

// legacySnapshotItems is the agent_events part of the snapshot: the latest
// stored hook event of every session that has no frame projection. stale are
// the sessions whose stored event no longer derives to a valid status.
func (m *Module) legacySnapshotItems(rows *legacyRows, projectedSessions map[string]struct{}) (items []snapshotItem, stale []string) {
	if rows == nil {
		return nil, nil
	}
	for _, ev := range rows.events {
		if _, ok := projectedSessions[ev.TmuxSession]; ok {
			continue
		}
		code, ok := rows.nameToCode[ev.TmuxSession]
		if !ok {
			continue
		}
		var result agentpkg.DeriveResult
		if provider, ok := m.registry.Get(ev.AgentType); ok {
			result = provider.DeriveStatus(ev.EventName, ev.RawEvent)
		}
		if !result.Valid {
			// Pre-W2 stored EventName no longer matches a Pdx-prefixed
			// catalog entry; mirror the hot-path invalid-result cleanup at
			// handler.go:230 so the SPA doesn't see a resurrected
			// raw_event_name on cold reconnect (which would re-key
			// hook-module lastTrigger and surface stale legacy events
			// despite replayFromDB intentionally skipping them).
			stale = append(stale, ev.TmuxSession)
			continue
		}
		normalized := m.buildNormalized(ev.TmuxSession, ev.EventName, ev.AgentType, ev.BroadcastTs, result)
		items = append(items, snapshotItem{code: code, event: normalized})
	}
	return items, stale
}

// agentSnapshotFrame is the value of the one frame an agent.v2 subscriber gets
// on connect. Seq is the slot's high-water mark and is always present, 0
// included (nothing broadcast yet). Sessions is never null: an empty host
// sends [].
type agentSnapshotFrame struct {
	Epoch    string               `json:"epoch"`
	Seq      uint64               `json:"seq"`
	Sessions []agentSnapshotEntry `json:"sessions"`
}

type agentSnapshotEntry struct {
	Session string                   `json:"session"`
	Event   agentpkg.NormalizedEvent `json:"event"`
}

// sendAgentSnapshot sends the one complete agent.snapshot frame and reports
// whether it was queued. The subscriber is strict (agent.v2): a frame that
// does not fit ends the connection and the reconnect brings a new snapshot.
func (m *Module) sendAgentSnapshot(sub *core.EventSubscriber, epoch string, seq uint64, items []snapshotItem) bool {
	frame := agentSnapshotFrame{Epoch: epoch, Seq: seq, Sessions: make([]agentSnapshotEntry, 0, len(items))}
	for _, it := range items {
		frame.Sessions = append(frame.Sessions, agentSnapshotEntry{Session: it.code, Event: it.event})
	}
	payload, _ := json.Marshal(frame)
	data, _ := json.Marshal(core.HostEvent{Type: "agent.snapshot", Value: string(payload)})
	return sub.TrySend(data)
}

func (m *Module) liveFrameProjections() ([]SessionProjection, error) {
	projections, _, err := m.liveFrameProjectionsWithSnapshot()
	return projections, err
}

// liveFrameProjectionsWithSnapshot is liveFrameProjections that also returns
// the pane snapshot the frames were filtered with, so the caller's pane ->
// session-name step reads the same tmux answer instead of asking again. The
// snapshot is nil when there were no frames to filter (no tmux call is made
// then) or the batch call failed; a nil snapshot makes every lookup per pane.
func (m *Module) liveFrameProjectionsWithSnapshot() ([]SessionProjection, *paneSnapshot, error) {
	if m.frames == nil {
		return nil, nil, nil
	}
	listAll := m.frames.ListAll
	if m.listFramesFn != nil {
		listAll = m.listFramesFn
	}
	frames, err := listAll()
	if err != nil {
		return nil, nil, err
	}
	if len(frames) == 0 {
		return nil, nil, nil
	}
	snap, err := m.takePaneSnapshot()
	if err != nil {
		return nil, nil, err
	}
	frames = m.filterProjectionFrames(frames, snap)
	projections := BuildSessionProjections(frames)
	m.applyModOverlay(projections)
	return projections, snap, nil
}

// paneNameFunc is the pane -> tmux session name step of a read: from snap, or
// (snap == nil) one tmux lookup per pane.
func (m *Module) paneNameFunc(snap *paneSnapshot) func(paneID string) string {
	if snap == nil {
		return m.paneSessionName
	}
	return snap.sessionName
}

// replayProjectionCache memoises, for ONE replayStatus round, the live frame
// projections and the pane→tmux-session-name lookups (#1767 fix A). It is a
// local value passed by pointer through the replay call chain: never stored on
// Module, never shared across rounds or goroutines (callers hold m.mu).
type replayProjectionCache struct {
	projections []SessionProjection // set only after a SUCCESSFUL liveFrameProjections
	loaded      bool
	paneName    map[string]string // paneID -> session name; successful lookups only
}

// projectionForSessionWith is projectionForSession with an optional replay
// cache. rc == nil is exactly projectionForSession. Failures are never cached:
// a failed liveFrameProjections or PaneSessionName is retried by the next call.
func (m *Module) projectionForSessionWith(sessionName string, rc *replayProjectionCache) (*SessionProjection, error) {
	if rc == nil {
		return m.projectionForSession(sessionName)
	}
	if !rc.loaded {
		projections, err := m.liveFrameProjections()
		if err != nil {
			return nil, err
		}
		rc.projections = projections
		rc.loaded = true
	}
	return m.selectSessionProjectionBy(sessionName, rc.projections, func(paneID string) string {
		if name, ok := rc.paneName[paneID]; ok {
			return name
		}
		if m.tmux == nil {
			return ""
		}
		name, err := m.tmux.PaneSessionName(paneID)
		if err != nil {
			return ""
		}
		if rc.paneName == nil {
			rc.paneName = make(map[string]string)
		}
		rc.paneName[paneID] = name
		return name
	}), nil
}

// paneSessionName is the name half of resolvePaneSession: the tmux session
// name owning paneID, or "" when tmux is unavailable or the lookup fails.
func (m *Module) paneSessionName(paneID string) string {
	if m.tmux == nil {
		return ""
	}
	name, err := m.tmux.PaneSessionName(paneID)
	if err != nil {
		return ""
	}
	return name
}

func (m *Module) resolvePaneSession(paneID string) (string, string) {
	if m.tmux == nil {
		return "", ""
	}
	sessionName, err := m.tmux.PaneSessionName(paneID)
	if err != nil {
		return "", ""
	}
	return sessionName, m.resolveSessionCode(sessionName)
}

// nextProbeIntentGeneration returns a fresh monotonically increasing
// generation token for ProbeIntent detectors. CALLER MUST hold m.mu (the
// counter is m.mu-protected; call sites are inside applyIntentLifecycle
// which already holds the lock).
//
// Per spec §5.4 — uint64 overflow needs ~5×10¹⁹ increments and is therefore
// not a practical concern for daemon lifetime; no reuse risk.
func (m *Module) nextProbeIntentGeneration() uint64 {
	m.probeIntentGen++
	return m.probeIntentGen
}

// sessionStatusSnapshot pairs a session's top-frame agentType with its
// current status. Used by probeIntentDispatcher.replayStatus to drive
// applyStatus per-session after a daemon-restart hydrate.
type sessionStatusSnapshot struct {
	agentType string
	status    agentpkg.Status
}

// snapshotStatuses returns a session → (agentType, status) snapshot taken
// under m.mu. Used by probeIntentDispatcher.replayStatus (W6-3 P1-T6 /
// issue #698) so a daemon restart re-arms ProbeIntent detectors based on
// post-replay state.
//
// agentType is sourced from the top-frame projection (NOT from the legacy
// agent_events row) so the value matches what live hook handlers would
// produce. Sessions without a resolvable top frame still appear in the
// snapshot with agentType="" — applyIntentLifecycle handles that path
// (frame lookup fails inside m.mu → skip arm).
//
// rc (nil for the legacy per-call behaviour) memoises the live projections
// and pane→session lookups for one replay round (#1767).
func (m *Module) snapshotStatuses(rc *replayProjectionCache) map[string]sessionStatusSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]sessionStatusSnapshot, len(m.currentStatus))
	for session, status := range m.currentStatus {
		entry := sessionStatusSnapshot{status: status}
		if proj, err := m.projectionForSessionWith(session, rc); err == nil && proj != nil && proj.TopFrame != nil {
			entry.agentType = proj.TopFrame.AgentType
		}
		out[session] = entry
	}
	return out
}

// lookupTopFrameForSessionLocked returns the top frame's pane_id + pid for
// session. CALLER MUST hold m.mu. Returns ok=false when the session has no
// frame projection or no top frame.
//
// W6-3 P1-T3: used by probeIntentDispatcher to capture the (paneID, senderPID)
// snapshot when arming a ProbeIntent detector. The "Locked" suffix marks the
// caller-locked contract; the implementation reuses projectionForSession,
// whose data sources (m.frames sql store + m.tmux pane resolver) acquire
// their own mutexes that are independent of m.mu, so calling it under m.mu
// does not introduce a lock cycle.
//
// See spec §6.2 — the helper deliberately routes through the existing
// projection pipeline so daemon-restart hydrate replay (P1-T6) and the live
// hook path observe identical (paneID, pid) selection logic.
func (m *Module) lookupTopFrameForSessionLocked(session string) (paneID string, pid int, ok bool) {
	frame, ok := m.lookupTopFrameWith(session, nil)
	if !ok {
		return "", 0, false
	}
	return frame.PaneID, frame.PID, true
}

// lookupTopFrameWith is lookupTopFrameForSessionLocked with an optional
// replay cache (rc == nil keeps the per-call behaviour) and the whole top
// frame returned so callers can re-verify its identity. CALLER MUST hold m.mu.
func (m *Module) lookupTopFrameWith(session string, rc *replayProjectionCache) (*store.Frame, bool) {
	projection, err := m.projectionForSessionWith(session, rc)
	if err != nil || projection == nil || projection.TopFrame == nil {
		return nil, false
	}
	return projection.TopFrame, true
}

// manageActivityWatch is invoked by hook handlers as a status changes. The
// W3 撤回 stop-only path (no new ScreenChange watcher is ever started here)
// is preserved; W6-3 P1-T5 dispatches the ProbeIntent lifecycle to
// probeIntentDisp.applyStatus afterwards so per-agent probe gating runs on
// every status change.
//
// W3 撤回 rationale: Phase 4a-1 wired this function as the always-on probe
// start site, which violated the "probe is recovery-only" v2.0 contract by
// covering every Waiting/Running/Idle transition with a screen watcher. W6
// reintroduces starts via the ProbeIntent dispatcher (per-agent declared
// intents + lifecycle gating), NOT via probeOrch.startWatch.
//
// R3 fix: m.activeWatchers is owned by this function (and renameSessionLocked);
// the orchestrator deliberately does not touch it.
//
// Locking contract: m.mu is acquired and released INSIDE this function for
// the activeWatchers eviction step; the dispatcher.applyStatus call MUST
// happen AFTER m.mu is released because the dispatcher takes m.mu itself
// (see probe_intent_dispatcher.go reconcileSessionActive +
// applyIntentLifecycle critical sections).
func (m *Module) manageActivityWatch(session, agentType string, newStatus agentpkg.Status) {
	m.mu.Lock()
	_, wasWatching := m.activeWatchers[session]
	delete(m.activeWatchers, session)
	m.mu.Unlock()
	if wasWatching {
		m.probeOrch.stopWatch(session)
	}

	// W6-3 P1-T5: dispatch ProbeIntent lifecycle. dispatcher.applyStatus is
	// safe to call without m.mu held; it routes through reconcileSessionActive
	// + applyIntentLifecycle which take m.mu themselves for active-set
	// mutation, then start / cancel detector goroutines outside the lock.
	if m.probeIntentDisp != nil {
		m.probeIntentDisp.applyStatus(session, agentType, newStatus)
	}
}

// AgentStatus is the last status the module holds for a tmux session ("idle", "running", ...), ok false when it holds none.
// Read-only, under m.mu (P7-1: the team module's 70% idle notice type-asserts it as AgentStatusReader).
func (m *Module) AgentStatus(tmuxSession string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.currentStatus[tmuxSession]
	return string(st), ok
}
