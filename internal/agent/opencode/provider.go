package opencode

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

func (p *Provider) Type() string        { return "opencode" }
func (p *Provider) DisplayName() string { return "OpenCode" }
func (p *Provider) IconHint() string    { return "opencode" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "opencode"
	}
	return false
}

func (p *Provider) Identify(proc agent.ProcessInfo) bool {
	exeName := strings.ToLower(filepath.Base(proc.ExePath))
	if exeName == "opencode" {
		return true
	}
	if !agent.IsJSRuntime(exeName) {
		return false
	}
	return agent.ArgvContainsFragment(proc.Argv, "opencode-ai", "/opencode/", "/opencode-ai/", "opencode/dist")
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveOpenCodeStatus(eventName, rawEvent)
}

// SupportedStatuses declares the Status values opencode.Provider's
// DeriveStatus may emit, derived from Events().EmitsStatus. Events() is the
// SSoT; this shim keeps the StatusSupporter contract (Phase 1) working.
// Return order is lexicographic for determinism.
func (p *Provider) SupportedStatuses() []agent.Status {
	return agent.DeriveSupportedStatuses(p.Events())
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	return false
}
