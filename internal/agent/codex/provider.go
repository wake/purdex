package codex

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Type() string        { return "codex" }
func (p *Provider) DisplayName() string { return "Codex" }
func (p *Provider) IconHint() string    { return "codex" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "codex"
	}
	return false
}

func (p *Provider) Identify(proc agent.ProcessInfo) bool {
	exeName := strings.ToLower(filepath.Base(proc.ExePath))
	if exeName == "codex" {
		return true
	}
	if !agent.IsJSRuntime(exeName) {
		return false
	}
	return agent.ArgvContainsFragment(proc.Argv, "@openai/codex", "/codex/", "/codex-cli/", "codex/dist/cli.js")
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCodexStatus(eventName, rawEvent)
}

// SupportedStatuses declares the Status values codex.Provider's DeriveStatus
// may emit, derived from Events().EmitsStatus. Events() is the SSoT after the
// issue #613 installer expansion; this shim keeps the StatusSupporter
// contract (Phase 1) working. Return order is lexicographic for determinism.
func (p *Provider) SupportedStatuses() []agent.Status {
	return agent.DeriveSupportedStatuses(p.Events())
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	return false // Deprecated: agent module uses prober.IsAliveFor directly
}
