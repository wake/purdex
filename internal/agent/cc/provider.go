package cc

import (
	"encoding/json"
	"sync"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/tmux"
)

// Provider implements agent.AgentProvider for Claude Code.
type Provider struct {
	prober   *probe.Prober
	tmuxExec tmux.Executor
	cfg      *config.Config
	cfgMu    *sync.RWMutex
}

// NewProvider creates a CC provider. Pass nil for prober/tmuxExec during testing.
func NewProvider(prober *probe.Prober, tmuxExec tmux.Executor, cfg *config.Config, cfgMu *sync.RWMutex) *Provider {
	return &Provider{prober: prober, tmuxExec: tmuxExec, cfg: cfg, cfgMu: cfgMu}
}

func (p *Provider) Type() string        { return "cc" }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) IconHint() string    { return "cc" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "cc"
	}
	if p.prober == nil {
		return false
	}
	if ctx.TmuxTarget == "" {
		return false
	}
	return p.prober.IsAliveFor("cc", ctx.TmuxTarget)
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCCStatus(eventName, rawEvent)
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	if p.prober == nil {
		return false
	}
	return p.prober.IsAliveFor("cc", tmuxTarget)
}

// RegisterServices registers this provider's services into the core service registry.
func (p *Provider) RegisterServices(registry *core.ServiceRegistry) {
	registry.Register(HistoryKey, CCHistoryProvider(p))
	registry.Register(OperatorKey, CCOperator(p))
}

func (p *Provider) CheckStatusline() (agent.StatuslineState, error) {
	path, err := ccSettingsPath()
	if err != nil {
		return agent.StatuslineState{}, err
	}
	return detectStatuslineMode(path)
}

func (p *Provider) InstallStatuslinePdx(pdxPath string) error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return installStatuslinePdx(path, pdxPath)
}

func (p *Provider) InstallStatuslineWrap(pdxPath, inner string) error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return installStatuslineWrap(path, pdxPath, inner)
}

func (p *Provider) RemoveStatusline() error {
	path, err := ccSettingsPath()
	if err != nil {
		return err
	}
	return removeStatusline(path)
}
