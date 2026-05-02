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

// watcherID is the per-watcher identity token. Pointer-equality on
// *watcherID is the unique-per-call discriminator used by watchLoop's
// self-cleanup and by StopWatchOwned. The struct intentionally carries
// a non-zero-size field so the runtime cannot coalesce two distinct
// `new(watcherID)` allocations into the same address (Go is allowed to
// alias zero-size struct pointers — `&struct{}{}` may equal another
// `&struct{}{}` — which would silently break ownership).
type watcherID struct {
	_ [1]byte
}

type watchEntry struct {
	cancel func()
	id     *watcherID // unique identity token for the active watcher
}

// WatchHandle is an opaque ownership token returned by Watch and
// consumed by StopWatchOwned. It binds a stop request to the specific
// watchEntry that Watch installed, so a stale stop call (e.g. a
// detector teardown that races a same-target re-arm) cannot
// accidentally cancel a replacement watcher installed by a later
// Watch.
//
// Callers MUST treat WatchHandle as opaque — fields are unexported.
// Zero-value WatchHandle never matches any live entry; passing it to
// StopWatchOwned is a no-op that returns false.
//
// Why unexported fields: production code should always derive
// handles from Watch. Anything that constructs a WatchHandle by hand
// is fabricating ownership it never had — the type system refuses
// that statically.
//
// Done channel (W6-6 R4 F6): closed when the underlying watchLoop
// exits — baseline-capture failure, StopWatch / StopWatchOwned cancel,
// same-target replacement cancel, StopAllWatches, or daemon shutdown.
// One-shot signal; once closed it stays closed. Callers (notably the
// codex ScreenChange detector) observe Done() so a transient capture
// failure does not park the detector forever waiting on emit/ctx.
// Zero-value WatchHandle returns a nil channel from Done(), which
// disables the select case rather than firing it spuriously — this
// keeps test fakes that fabricate WatchHandle{} compatible.
type WatchHandle struct {
	target string
	id     *watcherID
	done   <-chan struct{}
}

// Done returns a channel that is closed when the watchLoop backing
// this handle has exited. See the WatchHandle GoDoc for the full
// lifecycle contract.
//
// Zero-value WatchHandle returns nil — production callers selecting
// on Done() must therefore tolerate a nil channel (which disables
// the select case rather than firing it). This is intentional: it
// lets test fakes that return WatchHandle{} from a faked Watch stay
// compatible without inadvertently signaling premature exit.
func (h WatchHandle) Done() <-chan struct{} {
	return h.done
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
