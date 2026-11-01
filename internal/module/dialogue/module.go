// Package dialogue is the "dialogue" daemon module: it writes what was said in every Claude Code tmux session to
// plain-text segment files under <data_dir>/dialogue/<session id>/NNN.md (spec docs/specs/2026-11-01-session-dialogue-log-spec.md).
// This file is the module shell; writer.go, store.go and render.go do the work, intake.go feeds them.
//
// Like the workbook it replaces it is not a reason to keep the daemon down: a store that cannot be opened is recorded, the
// module does nothing and reports not ready.
package dialogue

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/wake/purdex/internal/convturns"
	"github.com/wake/purdex/internal/core"
	agentmod "github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/team"
)

// turnEndSubscriber is the part of the agent module's TerminalSessions the module needs.
type turnEndSubscriber interface {
	SubscribeTurnEnd(fn func(agentmod.TurnEndEvent)) (unsubscribe func())
}

// Module is the dialogue module.
type Module struct {
	core *core.Core

	mu      sync.Mutex
	store   *Store
	closing *Store // the store Stop set aside for Close (guarded by mu)
	ready   bool
	initErr string
	// startErr says why Start did not begin recording (a service it needs is missing); ready is false then.
	startErr string

	intake       *intake
	unsubTurnEnd func()
}

func New() *Module { return &Module{} }

func (m *Module) Name() string { return "dialogue" }

// Dependencies: agent (turn ends), conversation (the turns), team (the relay's previous session).
func (m *Module) Dependencies() []string { return []string{"agent", "conversation", "team"} }

// Init opens dialogue.db. A failure leaves the module off (recorded, logged, nil returned).
func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.CfgMu.RLock()
	dataDir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	st, err := OpenStore(filepath.Join(dataDir, "dialogue.db"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.ready, m.initErr = false, err.Error()
		log.Printf("[dialogue] disabled: %v", err)
		return nil
	}
	m.store, m.ready, m.initErr = st, true, ""
	return nil
}

// RegisterRoutes: PR 1 has no HTTP API (the files are the interface); the core's Module interface wants the method.
func (m *Module) RegisterRoutes(*http.ServeMux) {}

// Status is what the daemon reports for the module.
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready, "init_error": m.initErr, "start_error": m.startErr}
}

// notReady records why Start has nothing to do: Status then reports ready=false with start_error set.
func (m *Module) notReady(why string) error {
	log.Printf("[dialogue] %s", why)
	m.mu.Lock()
	m.ready, m.startErr = false, why
	m.mu.Unlock()
	return nil
}

// Start builds the writer and the intake from whatever the registry offers and subscribes to the turn ends. Without the
// conversation module's reader or the turn-end source there is nothing to do, and it says so.
func (m *Module) Start(context.Context) error {
	m.mu.Lock()
	st, ready := m.store, m.ready
	m.mu.Unlock()
	if !ready || st == nil {
		return nil
	}
	reg := m.core.Registry
	var turns convturns.Reader
	var sub turnEndSubscriber
	var pred team.PredecessorReader
	if reg != nil {
		if svc, ok := reg.Get(convturns.Key); ok {
			turns, _ = svc.(convturns.Reader)
		}
		if svc, ok := reg.Get(agentmod.TerminalSessionsKey); ok {
			sub, _ = svc.(turnEndSubscriber)
		}
		if svc, ok := reg.Get(team.PredecessorReaderKey); ok {
			pred, _ = svc.(team.PredecessorReader)
		}
	}
	if turns == nil {
		return m.notReady("no conversation reader registered; turn ends are not recorded")
	}
	if sub == nil {
		return m.notReady("no turn-end source registered; turn ends are not recorded")
	}
	m.core.CfgMu.RLock()
	dataDir := m.core.Cfg.DataDir
	m.core.CfgMu.RUnlock()
	logf := func(format string, args ...any) { log.Printf(format, args...) }
	var prevOf func(sid string) (string, bool)
	if pred != nil {
		prevOf = func(sid string) (string, bool) {
			prev, _, ok, err := pred.PredecessorOf(sid)
			if err != nil {
				logf("[dialogue] predecessor of session %s: %v", sid, err)
				return "", false
			}
			return prev, ok
		}
	}
	w := NewWriter(filepath.Join(dataDir, "dialogue"), st, prevOf, Hooks{}, WithLogf(logf))
	in := newIntake(intakeDeps{turns: turns, writer: w, store: st, logf: logf, timing: defaultTiming})
	m.mu.Lock()
	m.intake = in
	m.unsubTurnEnd = sub.SubscribeTurnEnd(in.OnTurnEnd)
	m.mu.Unlock()
	return nil
}

// Stop unsubscribes and waits for the workers, so nothing writes once it returns; the store itself is released by Close.
func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	st, in, unsub := m.store, m.intake, m.unsubTurnEnd
	m.store, m.ready, m.intake, m.unsubTurnEnd = nil, false, nil, nil
	if st != nil {
		m.closing = st // released by Close, after the HTTP server drained
	}
	m.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if in != nil {
		in.Stop()
	}
	return nil
}

// Close releases the store Stop set aside. It runs after the HTTP server has drained. Idempotent.
func (m *Module) Close() error {
	m.mu.Lock()
	st := m.closing
	m.closing = nil
	m.mu.Unlock()
	if st == nil {
		return nil
	}
	return st.Close()
}
