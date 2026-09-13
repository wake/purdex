package agent

import (
	"context"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// TestModuleInit_RegistersOwnerResolver pins that Init exposes the module as
// an OwnerResolver through the service registry, right after "agent.module" —
// the peers module (a later task) depends on this to answer session-ownership
// queries without importing the agent package's unexported internals.
func TestModuleInit_RegistersOwnerResolver(t *testing.T) {
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	m.tmux = fake

	cfg := &config.Config{}
	c := core.New(core.CoreDeps{
		Config:   cfg,
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	c.Registry.Register(session.RegistryKey, &fakeFastSessionProvider{})

	if err := m.Init(c); err != nil {
		t.Fatalf("Init: %v", err)
	}

	svc, ok := c.Registry.Get(OwnerResolverKey)
	if !ok {
		t.Fatalf("OwnerResolverKey not registered")
	}
	if _, ok := svc.(OwnerResolver); !ok {
		t.Fatalf("registered service does not implement OwnerResolver: %T", svc)
	}
}

// TestModule_ResolveSessionOwner_UnknownCode_FoundFalse pins that the exported
// wrapper delegates straight through to resolveSessionOwner: an unknown code
// answers found:false, same as the unexported method.
func TestModule_ResolveSessionOwner_UnknownCode_FoundFalse(t *testing.T) {
	m, _, _ := newProvenanceQueryModule(t)

	owner, found := m.ResolveSessionOwner(context.Background(), "nonexistent-code")
	if found {
		t.Fatalf("found = true, want false (owner = %+v)", owner)
	}
}

// TestModule_ResolveSessionOwner_KnownOwner_DelegatesFields replicates the
// smallest existing provenance success case
// (TestHandleSessionProvenance_OneRoot) through the exported resolver method
// and asserts the fields the peers module needs, including Status — the
// owning frame's Purdex agent status.
func TestModule_ResolveSessionOwner_KnownOwner_DelegatesFields(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owner, found := m.ResolveSessionOwner(context.Background(), codeOf(t, "$0"))
	if !found {
		t.Fatalf("found = false, want true")
	}
	if owner.AgentType != "cc" {
		t.Errorf("AgentType = %q, want %q", owner.AgentType, "cc")
	}
	if owner.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", owner.SessionID, "sess-1")
	}
	if owner.Status != string(agentpkg.StatusIdle) {
		t.Errorf("Status = %q, want %q", owner.Status, string(agentpkg.StatusIdle))
	}
}
