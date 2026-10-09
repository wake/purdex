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
	"github.com/wake/purdex/internal/team"
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
	feed     team.ApprovalFeed
	cache    *convfeed.Cache
	resolver *convfeed.Resolver
	maxBody  int // tests lower it
	subSem   chan struct{}

	mu      sync.Mutex
	cur     *generation // the latest Start's (kept after Stop: its context is cancelled, so nothing is admitted)
	running bool        // a Start whose sweeper has not returned yet
	idle    sync.WaitGroup
	tweak   wsTuning
}

// generation is one Start..Stop lifetime: its sweeper, and the WebSockets that live in it. A later Start makes a new
// one, so an earlier Stop never waits for (or cancels) connections of the next lifetime.
type generation struct {
	ctx         context.Context
	cancel      context.CancelFunc
	sweeperDone chan struct{}
	ws          sync.WaitGroup
}

// wsTuning lets tests shorten the live stream's timings and queue; zero values are the spec's.
type wsTuning struct {
	poll, reresolve time.Duration
	queue           int
}

func (m *Module) pollEvery() time.Duration {
	if m.tweak.poll > 0 {
		return m.tweak.poll
	}
	return defaultPollEvery
}

func (m *Module) reresolveEvery() time.Duration {
	if m.tweak.reresolve > 0 {
		return m.tweak.reresolve
	}
	return defaultReresolveEvery
}

func (m *Module) queueCap() int {
	if m.tweak.queue > 0 {
		return m.tweak.queue
	}
	return sendQueue
}

// admitWS counts one more live WebSocket and returns the context it lives in and the counter to call Done on: the
// current lifetime's (refused once its Stop has begun), or Background for a module that was never started (tests).
// Stop cancels under the same lock, so a connection is either refused or counted before Stop starts to wait.
func (m *Module) admitWS() (ctx context.Context, wg *sync.WaitGroup, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		m.idle.Add(1)
		return context.Background(), &m.idle, true
	}
	if m.cur.ctx.Err() != nil {
		return nil, nil, false
	}
	m.cur.ws.Add(1)
	return m.cur.ctx, &m.cur.ws, true
}

// New returns the module.
func New() *Module { return &Module{maxBody: maxBody, subSem: make(chan struct{}, maxSubagentReads)} }

// WithIndex sets the conversation index the resolver consults after the live pane (nil: the bounded lookup only).
func (m *Module) WithIndex(idx convfeed.IndexLookup) *Module {
	m.index = idx
	return m
}

func (m *Module) Name() string           { return "conversation" }
func (m *Module) Dependencies() []string { return []string{"agent", "team"} }

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
	fsvc, ok := c.Registry.Get(team.ApprovalFeedKey)
	if !ok {
		return fmt.Errorf("conversation: service %q not registered", team.ApprovalFeedKey)
	}
	feed, ok := fsvc.(team.ApprovalFeed)
	if !ok {
		return fmt.Errorf("conversation: service %q does not implement team.ApprovalFeed (%T)", team.ApprovalFeedKey, fsvc)
	}
	m.feed = feed
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
	mux.HandleFunc("GET /ws/conversations/{provider}/{session_id}", m.handleWS)
}

// Start runs the cache sweeper in a new lifetime; a second Start while one runs does nothing.
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	gctx, cancel := context.WithCancel(ctx)
	g := &generation{ctx: gctx, cancel: cancel, sweeperDone: make(chan struct{})}
	m.cur, m.running = g, true
	go func() {
		m.cache.Run(gctx, sweepEvery)
		m.mu.Lock() // the sweeper is gone: a later Start may begin again, whoever stopped this one
		if m.cur == g {
			m.running = false
		}
		m.mu.Unlock()
		close(g.sweeperDone)
	}()
	log.Println("[conversation] endpoints enabled")
	return nil
}

// Stop ends the current lifetime: its sweeper and its WebSockets, and waits for both (or for ctx). The module counts as
// running until the sweeper has returned, so a concurrent Start does not start a second one.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	g := m.cur
	if g != nil {
		g.cancel() // under m.mu: admitWS refuses afterwards, or its connection was counted before the wait below
	}
	m.mu.Unlock()
	if g == nil {
		return nil
	}
	wsDone := make(chan struct{})
	go func() { g.ws.Wait(); close(wsDone) }()
	for _, ch := range []chan struct{}{g.sweeperDone, wsDone} {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
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
