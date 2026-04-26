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

type ActivitySignal string

const (
	ActivitySignalRunning     ActivitySignal = "running"
	ActivitySignalIdle        ActivitySignal = "idle"
	ActivitySignalShellPrompt ActivitySignal = "shell_prompt"
)

// ActivityCallback is called when the watcher reaches a state transition.
type ActivityCallback func(target string, signal ActivitySignal)

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
