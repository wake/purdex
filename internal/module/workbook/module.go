// Package workbook is the "workbook" daemon module: a one-line summary of every turn that ended in a Claude Code
// session, kept per conversation (spec docs/specs/2026-10-09-session-workbook-spec.md). This file is the module shell and
// its store; the subscriber, the summariser and the API follow in later PRs.
//
// Like push and devices it is not a reason to keep the daemon down: a store that cannot be opened is recorded, the module
// does nothing and reports not ready.
package workbook

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/wake/purdex/internal/convturns"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// Module is the workbook module.
type Module struct {
	core *core.Core

	mu      sync.Mutex
	store   *Store
	ready   bool
	initErr string
	prompts PromptFiles // where Start wrote the prompts

	engine       *Engine // the subscriber and the job queue; nil until Start
	unsubTurnEnd func()
	stopReaper   context.CancelFunc

	// broadcast sends a host event (type, JSON-string value); the core's event hub, replaced in tests.
	broadcast func(eventType, value string)
}

func New() *Module { return &Module{} }

func (m *Module) Name() string { return "workbook" }

// Dependencies: agent (turn ends), team (lineage root and seat), conversation (last turns), hostconfig (settings).
func (m *Module) Dependencies() []string {
	return []string{"agent", "team", "conversation", "hostconfig"}
}

// Init opens workbook.db. A failure leaves the module off (recorded, logged, nil returned).
func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.CfgMu.RLock()
	dataDir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	st, err := OpenStore(filepath.Join(dataDir, "workbook.db"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.ready, m.initErr = false, err.Error()
		log.Printf("[workbook] disabled: %v", err)
		return nil
	}
	m.store, m.ready, m.initErr = st, true, ""
	// The host events follow the store's own transitions (no session: like backup:done and the profile events).
	m.broadcast = func(eventType, value string) {
		if m.core != nil && m.core.Events != nil {
			m.core.Events.Broadcast("", eventType, value)
		}
	}
	st.SetObserver(m.announce)
	return nil
}

// Status is what the daemon reports for the module (the capability follows `ready`).
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready, "init_error": m.initErr}
}

// live is the open store, nil when the module is off.
func (m *Module) live() *Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready {
		return nil
	}
	return m.store
}

// Start turns the entries a crash left pending into failed:stopped (plan D9); no events (clients refetch on reconnect).
func (m *Module) Start(context.Context) error {
	st := m.live()
	if st == nil {
		return nil
	}
	n, err := st.FailPending()
	if err != nil {
		// Entries may still read pending from the last run: the module must not look ready (the invariant of D9 is
		// "none pending after a start"), so it turns itself off like a failed Init and says why.
		m.disable(fmt.Errorf("start: %w", err))
		return nil
	}
	if n > 0 {
		log.Printf("[workbook] %d entries were pending at the last stop; marked failed (stopped)", n)
	}
	// The summariser reads its prompt by path (`claude --system-prompt-file`): written fresh at every start, so a file
	// from another version or with another mode never survives.
	m.core.CfgMu.RLock()
	dataDir := m.core.Cfg.DataDir
	m.core.CfgMu.RUnlock()
	files, err := WritePromptFiles(filepath.Join(dataDir, "workbook"))
	if err != nil {
		m.disable(fmt.Errorf("start: %w", err))
		return nil
	}
	m.mu.Lock()
	m.prompts = files
	m.mu.Unlock()
	m.startEngine(st)
	return nil
}

// turnEndSubscriber is the part of the agent module's TerminalSessions the engine needs.
type turnEndSubscriber interface {
	SubscribeTurnEnd(fn func(agent.TurnEndEvent)) (unsubscribe func())
}

// startEngine builds the engine from whatever the registry offers (a daemon without a module simply lacks that part) and
// subscribes it to the turn ends. Called from Start, after every Init.
func (m *Module) startEngine(st *Store) {
	d := Deps{Store: st}
	m.core.CfgMu.RLock()
	d.HostID = m.core.Cfg.HostID
	m.core.CfgMu.RUnlock()
	if reg := m.core.Registry; reg != nil {
		if svc, ok := reg.Get(convturns.Key); ok {
			d.Turns, _ = svc.(convturns.Reader)
		}
		if svc, ok := reg.Get(team.LineageRootKey); ok {
			d.Lineage, _ = svc.(team.LineageRootResolver)
		}
		if svc, ok := reg.Get(team.SeatReaderKey); ok {
			d.Seats, _ = svc.(team.SeatReader)
		}
	}
	eng := NewEngine(d)
	ctx, cancel := context.WithCancel(context.Background())
	go eng.RunReaper(ctx)
	var unsub func()
	if m.core.Registry != nil {
		if svc, ok := m.core.Registry.Get(agent.TerminalSessionsKey); ok {
			if s, ok := svc.(turnEndSubscriber); ok {
				unsub = s.SubscribeTurnEnd(eng.OnTurnEnd)
			}
		}
	}
	m.mu.Lock()
	m.engine, m.unsubTurnEnd, m.stopReaper = eng, unsub, cancel
	m.mu.Unlock()
}

// Jobs is what the mod socket's workbook routes call; nil while the module is off.
func (m *Module) Jobs() JobSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		return nil
	}
	return m.engine
}

// disable turns the module off after a failure that happened once it was running: not ready, the reason recorded, the
// store closed.
func (m *Module) disable(err error) {
	m.mu.Lock()
	st := m.store
	m.store, m.ready, m.initErr = nil, false, err.Error()
	m.mu.Unlock()
	log.Printf("[workbook] disabled: %v", err)
	if st != nil {
		st.Close()
	}
}

// Stop releases the store. Nothing writes after it returns.
func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	st, eng, unsub, stopReaper := m.store, m.engine, m.unsubTurnEnd, m.stopReaper
	m.store, m.ready, m.engine, m.unsubTurnEnd, m.stopReaper = nil, false, nil, nil, nil
	m.mu.Unlock()
	// the engine ends its entries while the store is still open
	if unsub != nil {
		unsub()
	}
	if stopReaper != nil {
		stopReaper()
	}
	if eng != nil {
		eng.Stop()
	}
	if st == nil {
		return nil
	}
	return st.Close()
}
