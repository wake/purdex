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

// ProbeIntents declares the probe-driven status transitions for the codex
// agent. W6-3 first PR scope: one intent — ProcessDead — that recovers the
// missing StopFailure transition codex 0.124.0 does not emit. Future W6 PRs
// MAY append more intents but MUST keep ProcessDead present and stable.
//
// Implements agent.ProbeIntentProvider (optional capability — providers
// without ProbeIntents behave identically to pre-W6-3).
//
// Per spec §4.1: gating set {Running, Waiting} mirrors the only states where
// codex is supposed to be doing work; OnSignal mapping (PaneAlive=true →
// Error, false → Clear) splits W6-3 (codex died with pane intact) from W6-4
// (entire pane disappeared) on the runtime observation captured by
// StartProcessDeadDetector.
func (p *Provider) ProbeIntents() []agent.ProbeIntent {
	return []agent.ProbeIntent{
		{
			Kind:          agent.ProbeIntentKindProcessDead,
			OnEntryStatus: []agent.Status{agent.StatusRunning, agent.StatusWaiting},
			OnSignal:      onProcessDead,
		},
	}
}

// onProcessDead maps a ProcessDead signal to the recovery status, splitting
// W6-3 (PaneAlive=true → Error) and W6-4 (PaneAlive=false → Clear) on the
// pane existence observation that the detector captured. Defends against
// dispatcher misuse: a Signal with a non-ProcessDead Kind returns an empty
// Status (drop the signal) rather than mapping to Error or Clear.
func onProcessDead(sig agent.Signal) agent.Status {
	if sig.Kind != agent.ProbeIntentKindProcessDead {
		return ""
	}
	if sig.PaneAlive {
		return agent.StatusError
	}
	return agent.StatusClear
}
