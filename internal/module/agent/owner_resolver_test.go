package agent

import (
	"context"
	"errors"
	"testing"
	"time"

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

	owner, found, err := m.ResolveSessionOwner(context.Background(), "nonexistent-code")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
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

	owner, found, err := m.ResolveSessionOwner(context.Background(), codeOf(t, "$0"))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
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

// TestModule_ResolveSessionOwner_PanesOfSessionFails_ReturnsErr pins that a
// panesOfSession failure — here, the request deadline already expired before
// the walk could look at a single frame — is reported through the returned
// error, not silently folded into found:false. The peers module (Item 1,
// #988) needs to tell "owner resolution failed" apart from "no owner" so a
// session with a live agent is never reported to the SPA as no_agent.
func TestModule_ResolveSessionOwner_PanesOfSessionFails_ReturnsErr(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	orig := provenanceTimeout
	provenanceTimeout = -1 // expired before the first frame is looked at
	t.Cleanup(func() { provenanceTimeout = orig })

	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owner, found, err := m.ResolveSessionOwner(context.Background(), codeOf(t, "$0"))
	if err == nil {
		t.Fatalf("err = nil, want non-nil (owner = %+v, found = %v)", owner, found)
	}
	if found {
		t.Fatalf("found = true, want false when the resolver failed")
	}
}

// blockingEnumerationExecutor blocks the panesOfSession enumeration's very
// first PaneSessionID call until its context is cancelled, then returns
// ctx.Err() — simulating a request whose deadline expires WHILE the
// enumeration is in flight for the session's only pane, rather than before
// the walk starts (that case is TestModule_ResolveSessionOwner_PanesOfSessionFails_ReturnsErr,
// which never reaches PaneSessionID at all). panesOfSession used to fold that
// returned error into "continue", finishing enumeration with an empty pane
// list and err:nil, which read as "session has no root agent" rather than "the
// walk timed out".
type blockingEnumerationExecutor struct {
	*tmux.FakeExecutor
}

func (e *blockingEnumerationExecutor) PaneSessionID(ctx context.Context, target string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Second):
		// The context never reached the call: fail loudly rather than let the
		// test pass on a timeout that looks like cancellation.
		return "", errors.New("PaneSessionID was never cancelled")
	}
}

// TestModule_ResolveSessionOwner_EnumerationTimesOut_ReturnsErr pins the fix
// for the swallow above: a context that expires DURING panesOfSession's
// enumeration of the session's only pane must surface as err != nil,
// found:false — never as the "no owner" outcome a genuinely rootless session
// produces (TestModule_ResolveSessionOwner_NoRootFrame_FoundFalseErrNil).
func TestModule_ResolveSessionOwner_EnumerationTimesOut_ReturnsErr(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	exec := &blockingEnumerationExecutor{FakeExecutor: fake}
	m.tmux = exec
	orig := provenanceTimeout
	provenanceTimeout = 30 * time.Millisecond
	t.Cleanup(func() { provenanceTimeout = orig })

	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")

	start := time.Now()
	owner, found, err := m.ResolveSessionOwner(context.Background(), codeOf(t, "$0"))
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("the request took %v: the deadline never reached the enumeration call", elapsed)
	}
	if err == nil {
		t.Fatalf("err = nil, want non-nil (owner = %+v, found = %v)", owner, found)
	}
	if found {
		t.Fatalf("found = true, want false when the enumeration timed out")
	}
}

// TestModule_ResolveSessionOwner_NoRootFrame_FoundFalseErrNil pins the other
// half of the contract: a session with panes but genuinely no root agent
// frame (never a resolver error) still answers found:false, err:nil, exactly
// as before this change.
func TestModule_ResolveSessionOwner_NoRootFrame_FoundFalseErrNil(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	// No seeded identity frame: the pane has no root agent frame at all.

	owner, found, err := m.ResolveSessionOwner(context.Background(), codeOf(t, "$0"))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if found {
		t.Fatalf("found = true, want false (owner = %+v)", owner)
	}
}
