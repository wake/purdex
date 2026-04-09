package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/tmux-box/internal/agent"
)

type fakeProvider struct {
	agentType string
	claimFn   func(agent.ClaimContext) bool
}

func (f *fakeProvider) Type() string        { return f.agentType }
func (f *fakeProvider) DisplayName() string { return f.agentType }
func (f *fakeProvider) IconHint() string    { return f.agentType }
func (f *fakeProvider) Claim(ctx agent.ClaimContext) bool {
	if f.claimFn != nil {
		return f.claimFn(ctx)
	}
	return false
}
func (f *fakeProvider) DeriveStatus(string, json.RawMessage) agent.DeriveResult {
	return agent.DeriveResult{}
}
func (f *fakeProvider) IsAlive(string) bool { return true }

func TestRegistryGet(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc"})
	r.Register(&fakeProvider{agentType: "codex"})

	p, ok := r.Get("cc")
	if !ok || p.Type() != "cc" {
		t.Fatal("expected cc provider")
	}
	p, ok = r.Get("codex")
	if !ok || p.Type() != "codex" {
		t.Fatal("expected codex provider")
	}
	_, ok = r.Get("unknown")
	if ok {
		t.Fatal("expected no provider for unknown")
	}
}

func TestRegistryClaimPriority(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc", claimFn: func(ctx agent.ClaimContext) bool {
		return ctx.ProcessName == "claude"
	}})
	r.Register(&fakeProvider{agentType: "codex", claimFn: func(ctx agent.ClaimContext) bool {
		return ctx.ProcessName == "codex"
	}})

	p, ok := r.Claim(agent.ClaimContext{ProcessName: "codex", TmuxTarget: "sess:"})
	if !ok || p.Type() != "codex" {
		t.Fatal("expected codex to claim")
	}
	p, ok = r.Claim(agent.ClaimContext{ProcessName: "claude", TmuxTarget: "sess:"})
	if !ok || p.Type() != "cc" {
		t.Fatal("expected cc to claim")
	}
	_, ok = r.Claim(agent.ClaimContext{ProcessName: "bash", TmuxTarget: "sess:"})
	if ok {
		t.Fatal("expected no provider to claim bash")
	}
}

func TestRegistryAll(t *testing.T) {
	r := agent.NewRegistry()
	r.Register(&fakeProvider{agentType: "cc"})
	r.Register(&fakeProvider{agentType: "codex"})
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(all))
	}
}
