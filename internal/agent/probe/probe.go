package probe

import (
	"sync"
	"time"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

// ReadinessChecker determines the detailed status of an agent.
type ReadinessChecker interface {
	CheckReadiness(target string) ReadinessResult
}

// ReadinessResult is the output of a ReadinessChecker.
type ReadinessResult struct {
	Status agent.Status
	Raw    string // captured pane content (debug, optional)
}

// ScreenChangeKind classifies a raw screen-watcher event. The probe layer is
// dumb: it emits these without interpreting them as agent status. Callers
// (agent module / orchestrator) own the policy of mapping events to status
// transitions and deduplicating per-session.
type ScreenChangeKind string

const (
	// ScreenChanged is fired on every tick whose capture hash differs from
	// the previous baseline. Continuous-changing panes therefore fire
	// ScreenChanged every tick — orchestrator owns the dedup.
	ScreenChanged ScreenChangeKind = "changed"

	// ScreenStable is fired when the capture hash has matched the baseline
	// for IdleStableTicks consecutive ticks. The internal stable counter
	// resets after each fire so continuous-stable panes re-fire every N
	// ticks; orchestrator owns the dedup.
	ScreenStable ScreenChangeKind = "stable"
)

// ScreenChangeEvent is the raw output of a screen watcher. Probe layer emits
// these without interpreting them as agent status.
type ScreenChangeEvent struct {
	Kind       ScreenChangeKind
	Target     string    // tmux target e.g. "session-name:"
	Content    string    // captured content at the moment of the event
	OccurredAt time.Time // probe-layer wall clock
}

// ScreenChangeCallback receives screen-change events from a watcher. Callbacks
// must NOT terminate the watcher — call StopWatch explicitly.
type ScreenChangeCallback func(ScreenChangeEvent)

// WatchOptions tunes the watch loop behavior. Zero values fall back to safe
// defaults: when both TopLines and BottomLines are 0, the watcher hashes the
// full visible pane via tmux.CapturePaneContent(target, 0); IdleStableTicks
// = 0 → default 3.
//
// TopLines and BottomLines are mutually exclusive (only one may be > 0). If
// both are set, Watch returns early without registering — caller programming
// error.
type WatchOptions struct {
	// TopLines limits captured content to the top N lines via
	// tmux.CapturePaneTopLines (CapturePaneRange(target, 0, N-1)).
	TopLines int

	// BottomLines limits captured content to the bottom N lines via
	// legacy tmux.CapturePaneContent(target, N) semantics
	// (capture-pane -S -N). Preserves legacy capture mode for default
	// agent profiles (G5 parity gate).
	BottomLines int

	// IdleStableTicks is the number of consecutive identical-hash ticks
	// before ScreenStable is emitted. 0 means default 3.
	IdleStableTicks int
}

type IdentifyFunc func(agent.ProcessInfo) bool

// Prober provides layered probing: Liveness → Activity → Readiness.
type Prober struct {
	tmux tmux.Executor

	registryMu      sync.RWMutex
	identifiers     map[string]IdentifyFunc     // agentType → identify
	identifierOrder []string                    // insertion order of identifier registrations (FirstAliveAgentInTree relies on this for deterministic first-match; Go map iteration is unordered)
	readiness       map[string]ReadinessChecker // agentType → checker

	livenessMu      sync.Mutex
	descendantCache map[string]descendantCacheEntry // target → recursive descendant snapshot
	now             func() time.Time

	watcherMu sync.Mutex
	watchers  map[string]watchEntry // target → active watcher
}

type watchEntry struct {
	cancel func()
	id     *struct{} // unique identity token for the active watcher
}

// New creates a Prober backed by the given tmux executor.
func New(tmux tmux.Executor) *Prober {
	return &Prober{
		tmux:            tmux,
		identifiers:     make(map[string]IdentifyFunc),
		readiness:       make(map[string]ReadinessChecker),
		descendantCache: make(map[string]descendantCacheEntry),
		now:             time.Now,
		watchers:        make(map[string]watchEntry),
	}
}

func (p *Prober) RegisterIdentifier(agentType string, identify IdentifyFunc) {
	p.registryMu.Lock()
	if _, exists := p.identifiers[agentType]; !exists {
		p.identifierOrder = append(p.identifierOrder, agentType)
	}
	p.identifiers[agentType] = identify
	p.registryMu.Unlock()
}

// identifierOrderSnapshot returns a copy of the registered identifier names in
// insertion order. Used by tests and by FirstAliveAgentInTree to iterate
// deterministically.
func (p *Prober) identifierOrderSnapshot() []string {
	p.registryMu.RLock()
	defer p.registryMu.RUnlock()
	return append([]string(nil), p.identifierOrder...)
}

// RegisterReadiness registers a ReadinessChecker for a given agent type.
func (p *Prober) RegisterReadiness(agentType string, checker ReadinessChecker) {
	p.registryMu.Lock()
	p.readiness[agentType] = checker
	p.registryMu.Unlock()
}
