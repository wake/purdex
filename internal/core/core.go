package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/tmux"
)

// Module is the interface all daemon modules implement.
type Module interface {
	Name() string
	Dependencies() []string
	Init(core *Core) error
	RegisterRoutes(mux *http.ServeMux)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Closer is the optional interface a module can implement to clean up resources
// during shutdown. Modules without Closer are skipped.
type Closer interface {
	Close() error
}

// ShutdownBudget is the single deadline shared by StopModules and the HTTP
// server's Shutdown during daemon shutdown (spec §4.5).
const ShutdownBudget = 10 * time.Second

// CoreDeps holds the shared infrastructure injected into Core.
type CoreDeps struct {
	Config   *config.Config
	Tmux     tmux.Executor
	Registry *ServiceRegistry
}

// Core holds shared infrastructure and manages module lifecycle.
type Core struct {
	Cfg           *config.Config
	CfgMu         sync.RWMutex // protects Cfg
	CfgPath       string       // path to config.toml for persistence
	Tmux          tmux.Executor
	Registry      *ServiceRegistry
	Events        *EventsBroadcaster
	Tickets       *TicketStore
	Pairing       PairingState
	SetupSecrets  *SetupSecretStore
	PairingSecret string      // hex(3 bytes), used for /api/pair/verify
	failedVerify  int32       // atomic counter for brute-force protection
	TmuxAliveFunc func() bool // injected by session module; returns cached tmux reachability
	// HostAuthObserver is injected by the peers module (Init) and called by
	// the peer auth middleware's matcher (cmd/pdx/http_chain.go) for every
	// successful host-token match, UNDER CfgMu.RLock, with the matched
	// entry's alias and config.TokenFingerprint of the bearer — so a config
	// writer (UpdateConfig holds CfgMu.Lock) cannot interleave between the
	// match and its observation, and every authentication that completed
	// before a rotation commit/cancel is visible to that gate. Nil-safe:
	// the matcher skips it when unset. It must take only locks that are
	// ordered AFTER CfgMu (the peers module takes its own rotMu, never CfgMu).
	HostAuthObserver func(alias, tokenFingerprint string)
	modules          []Module
	configChangeMu   sync.Mutex // protects onConfigChange
	onConfigChange   []func()   // config change callbacks

	// bootNex is the [nex] section the daemon booted with, captured in New
	// from the loaded config before any module init or PUT /api/config can
	// run. /api/info compares it with the live Cfg.Nex to report
	// restart_required (spec §4.4.2) whether nex is disabled, ready, or
	// soft-failed. Never mutated after New.
	bootNex config.NexConfig
}

// New creates a Core from the given dependencies.
func New(deps CoreDeps) *Core {
	reg := deps.Registry
	if reg == nil {
		reg = NewServiceRegistry()
	}
	var bootNex config.NexConfig
	if deps.Config != nil {
		bootNex = deps.Config.Clone().Nex
	}
	return &Core{
		bootNex:      bootNex,
		Cfg:          deps.Config,
		Tmux:         deps.Tmux,
		Registry:     reg,
		Events:       NewEventsBroadcaster(),
		Tickets:      NewTicketStore(),
		SetupSecrets: NewSetupSecretStore(5 * time.Minute),
	}
}

// AddModule appends a module to the lifecycle.
func (c *Core) AddModule(m Module) {
	c.modules = append(c.modules, m)
}

// Mounted reports whether a module with the given Name() was added via
// AddModule, regardless of Init/Start order.
func (c *Core) Mounted(name string) bool {
	for _, m := range c.modules {
		if m.Name() == name {
			return true
		}
	}
	return false
}

// StatusReporter is the optional interface a module implements to publish
// runtime facts through GET /api/info. nex is the first: whether its engine
// is serving, why not, and the config it was assembled with (spec §4.4.2).
type StatusReporter interface{ Status() map[string]any }

// ModuleStatus returns the named module's Status(), or nil,false when the
// module is not mounted or does not implement StatusReporter.
func (c *Core) ModuleStatus(name string) (map[string]any, bool) {
	for _, m := range c.modules {
		if m.Name() != name {
			continue
		}
		if r, ok := m.(StatusReporter); ok {
			return r.Status(), true
		}
		return nil, false
	}
	return nil, false
}

// InitModules sorts modules by dependency order, then calls Init on each.
func (c *Core) InitModules() error {
	sorted, err := topoSort(c.modules)
	if err != nil {
		return fmt.Errorf("dependency sort: %w", err)
	}
	c.modules = sorted

	for _, m := range c.modules {
		if err := m.Init(c); err != nil {
			return fmt.Errorf("module %s init: %w", m.Name(), err)
		}
	}
	return nil
}

// RegisterRoutes calls RegisterRoutes on each module in registration order.
func (c *Core) RegisterRoutes(mux *http.ServeMux) {
	for _, m := range c.modules {
		m.RegisterRoutes(mux)
	}
}

// StartModules calls Start on each module in registration order.
func (c *Core) StartModules(ctx context.Context) error {
	for _, m := range c.modules {
		if err := m.Start(ctx); err != nil {
			return fmt.Errorf("module %s start: %w", m.Name(), err)
		}
	}
	return nil
}

// StopModules calls Stop on each module in reverse dependency order.
// All modules are stopped even if some return errors.
func (c *Core) StopModules(ctx context.Context) error {
	var errs []error
	for i := len(c.modules) - 1; i >= 0; i-- {
		if err := c.modules[i].Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("module %s stop: %w", c.modules[i].Name(), err))
		}
	}
	return errors.Join(errs...)
}

// CloseModules calls Close on every module implementing Closer, in reverse
// registration order, joining errors; modules without Closer are skipped.
func (c *Core) CloseModules() error {
	var errs []error
	for i := len(c.modules) - 1; i >= 0; i-- {
		closer, ok := c.modules[i].(Closer)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("module %s close: %w", c.modules[i].Name(), err))
		}
	}
	return errors.Join(errs...)
}

// UpdateConfig is the single serialised writer of the runtime config:
//  1. CfgMu.Lock(); next := c.Cfg.Clone()
//  2. err := mutate(&next); if err != nil -> Unlock, return err (nothing changed)
//  3. if CfgPath != "" { if err := config.WriteFile(CfgPath, next); err != nil -> Unlock, return err (c.Cfg untouched) }
//  4. *c.Cfg = next // commit: pointer identity preserved for other holders
//  5. CfgMu.Unlock(); c.NotifyConfigChange() // AFTER unlock: agent callbacks take RLock
//
// mutate runs on a deep copy, so editing or deleting a Peers.Hosts element
// (or any other slice) can never touch the live backing array, and neither
// a mutate error nor a write error changes runtime state.
func (c *Core) UpdateConfig(mutate func(cfg *config.Config) error) error {
	c.CfgMu.Lock()
	next := c.Cfg.Clone()

	if err := mutate(&next); err != nil {
		c.CfgMu.Unlock()
		return err
	}

	if c.CfgPath != "" {
		if err := config.WriteFile(c.CfgPath, next); err != nil {
			c.CfgMu.Unlock()
			return err
		}
	}

	*c.Cfg = next
	c.CfgMu.Unlock()

	c.NotifyConfigChange()
	return nil
}

// OnConfigChange registers a callback invoked after config is updated via PUT.
func (c *Core) OnConfigChange(fn func()) {
	c.configChangeMu.Lock()
	defer c.configChangeMu.Unlock()
	c.onConfigChange = append(c.onConfigChange, fn)
}

// NotifyConfigChange invokes all registered config change callbacks.
func (c *Core) NotifyConfigChange() {
	c.configChangeMu.Lock()
	fns := make([]func(), len(c.onConfigChange))
	copy(fns, c.onConfigChange)
	c.configChangeMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// RegisterCoreRoutes registers routes owned by Core itself (not by modules).
func (c *Core) RegisterCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/host-events", c.Events.HandleHostEvents)
	mux.HandleFunc("GET /api/info", c.handleInfo)
	mux.HandleFunc("GET /api/config", c.handleGetConfig)
	mux.HandleFunc("PUT /api/config", c.handlePutConfig)
	mux.HandleFunc("POST /api/ws-ticket", c.handleWsTicket)
	mux.HandleFunc("GET /api/ready", c.handleReady)
	mux.HandleFunc("POST /api/pair/verify", c.handlePairVerify)
	mux.HandleFunc("POST /api/pair/setup", c.handlePairSetup)
	mux.HandleFunc("POST /api/token/auth", c.handleTokenAuth)
}
