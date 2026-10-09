// Package conversation serves one Claude Code conversation over HTTP: a window of its turns, normalized by ccnorm and
// followed from its transcript file by convfeed (spec docs/specs/2026-10-08-interface-u1-spec.md §8.2). The package
// holds the cache of followed conversations and the resolver that finds a session's transcript; the handlers only
// validate, acquire, refresh and encode.
package conversation

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
)

// OwnerSource finds the confirmed live panes of a Claude Code session: the agent module.
type OwnerSource interface {
	ConfirmedOwners(ctx context.Context, sessionID string) ([]agent.PaneOwner, error)
}

const (
	// maxBody is the largest encoded response body (spec §8.2).
	maxBody = 4 << 20
	// sweepEvery is how often the cache drops idle entries when no request comes.
	sweepEvery = time.Minute
	agentKey   = "agent.module"
)

// Module is the "conversation" daemon module.
type Module struct {
	core     *core.Core
	index    convfeed.IndexLookup
	cache    *convfeed.Cache
	resolver *convfeed.Resolver
	maxBody  int // tests lower it
	subSem   chan struct{}

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{} // closed when the sweeper has returned
}

// New returns the module.
func New() *Module { return &Module{maxBody: maxBody, subSem: make(chan struct{}, maxSubagentReads)} }

// WithIndex sets the conversation index the resolver consults after the live pane (nil: the bounded lookup only).
func (m *Module) WithIndex(idx convfeed.IndexLookup) *Module {
	m.index = idx
	return m
}

func (m *Module) Name() string           { return "conversation" }
func (m *Module) Dependencies() []string { return []string{"agent"} }

func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(agentKey)
	if !ok {
		return fmt.Errorf("conversation: service %q not registered", agentKey)
	}
	owners, ok := svc.(OwnerSource)
	if !ok {
		return fmt.Errorf("conversation: service %q does not implement OwnerSource (%T)", agentKey, svc)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("conversation: home directory: %w", err)
	}
	m.cache = convfeed.NewCache(convfeed.CacheOptions{})
	m.resolver = &convfeed.Resolver{Home: home, Owners: ownerAdapter{owners}, Index: m.index}
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/conversations/{provider}/{session_id}", m.handleSnapshot)
	mux.HandleFunc("GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}", m.handleSubagent)
}

// Start runs the cache sweeper; a second Start while it runs does nothing.
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	go func() {
		m.cache.Run(ctx, sweepEvery)
		m.mu.Lock() // the module is no longer running: a later Start may begin again, whoever stopped this one
		if m.done == done {
			m.cancel, m.done = nil, nil
		}
		m.mu.Unlock()
		close(done)
	}()
	log.Println("[conversation] endpoints enabled")
	return nil
}

// Stop ends the sweeper and waits for it to return (or for ctx). The module counts as running until the sweeper has
// returned, so a concurrent Stop waits for the same exit and a concurrent Start does not start a second sweeper.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ownerAdapter maps the agent module's panes to the resolver's owners.
type ownerAdapter struct{ src OwnerSource }

func (a ownerAdapter) LiveSessions(ctx context.Context, sessionID string) ([]convfeed.Owner, error) {
	panes, err := a.src.ConfirmedOwners(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]convfeed.Owner, 0, len(panes))
	for _, p := range panes {
		out = append(out, convfeed.Owner{TranscriptPath: p.TranscriptPath, Status: p.Status, SeenAt: p.LastSeenAt})
	}
	return out, nil
}
