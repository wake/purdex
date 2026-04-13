package probe

import (
	"sync"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

// ContentMatcher is an optional Liveness fallback.
// Providers implement this to detect their agent via screen content
// when process name matching fails (e.g. CC launched via wrapper script).
type ContentMatcher interface {
	LooksLikeAgent(content string) bool
}

// ReadinessChecker determines the detailed status of an agent.
type ReadinessChecker interface {
	CheckReadiness(target string) ReadinessResult
}

// ReadinessResult is the output of a ReadinessChecker.
type ReadinessResult struct {
	Status agent.Status
	Raw    string // captured pane content (debug, optional)
}

// ActivityCallback is called when screen content changes during a watch.
type ActivityCallback func(target string)

// processMatcher holds the known command names for one agent type.
type processMatcher struct {
	commands map[string]bool
}

// Prober provides layered probing: Liveness → Activity → Readiness.
type Prober struct {
	tmux tmux.Executor

	matcherMu sync.RWMutex
	matchers  map[string]*processMatcher  // agentType → matcher
	content   map[string]ContentMatcher   // agentType → optional
	readiness map[string]ReadinessChecker // agentType → checker

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
		tmux:      tmux,
		matchers:  make(map[string]*processMatcher),
		content:   make(map[string]ContentMatcher),
		readiness: make(map[string]ReadinessChecker),
		watchers:  make(map[string]watchEntry),
	}
}

// RegisterProcessNames registers process names for a given agent type.
func (p *Prober) RegisterProcessNames(agentType string, names []string) {
	cmds := make(map[string]bool, len(names))
	for _, n := range names {
		cmds[n] = true
	}
	p.matcherMu.Lock()
	p.matchers[agentType] = &processMatcher{commands: cmds}
	p.matcherMu.Unlock()
}

// UpdateProcessNames replaces process names for a given agent type.
// Called from OnConfigChange to handle dynamic CC command name updates.
func (p *Prober) UpdateProcessNames(agentType string, names []string) {
	p.RegisterProcessNames(agentType, names)
}

// RegisterContentMatcher registers an optional content-based fallback for Liveness.
func (p *Prober) RegisterContentMatcher(agentType string, m ContentMatcher) {
	p.matcherMu.Lock()
	p.content[agentType] = m
	p.matcherMu.Unlock()
}

// RegisterReadiness registers a ReadinessChecker for a given agent type.
func (p *Prober) RegisterReadiness(agentType string, checker ReadinessChecker) {
	p.matcherMu.Lock()
	p.readiness[agentType] = checker
	p.matcherMu.Unlock()
}
