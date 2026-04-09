package cc

import (
	"encoding/json"
	"sync"

	"github.com/wake/tmux-box/internal/agent"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/tmux"
)

// Provider implements agent.AgentProvider for Claude Code.
type Provider struct {
	detector *Detector
	tmuxExec tmux.Executor
	cfg      *config.Config
	cfgMu    *sync.RWMutex
}

// NewProvider creates a CC provider. Pass nil for detector/tmuxExec during testing.
func NewProvider(detector *Detector, tmuxExec tmux.Executor, cfg *config.Config, cfgMu *sync.RWMutex) *Provider {
	return &Provider{detector: detector, tmuxExec: tmuxExec, cfg: cfg, cfgMu: cfgMu}
}

func (p *Provider) Type() string        { return "cc" }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) IconHint() string    { return "cc" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "cc"
	}
	if p.detector == nil {
		return false
	}
	if ctx.TmuxTarget == "" {
		return false
	}
	status := p.detector.Detect(ctx.TmuxTarget)
	return status != StatusNormal && status != StatusNotInCC
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCCStatus(eventName, rawEvent)
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	if p.detector == nil {
		return false
	}
	status := p.detector.Detect(tmuxTarget)
	return status == StatusCCIdle || status == StatusCCRunning || status == StatusCCWaiting
}

// RegisterServices registers this provider's services into the core service registry.
func (p *Provider) RegisterServices(registry *core.ServiceRegistry) {
	registry.Register(DetectorKey, CCDetector(p.detector))
	registry.Register(HistoryKey, CCHistoryProvider(p))
	registry.Register(OperatorKey, CCOperator(p))
}
