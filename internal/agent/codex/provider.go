package codex

import (
	"encoding/json"

	"github.com/wake/tmux-box/internal/agent"
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
	return isCodexProcess(ctx.ProcessName)
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCodexStatus(eventName, rawEvent)
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	cmd := checkPaneProcess(tmuxTarget)
	return isCodexProcess(cmd)
}
