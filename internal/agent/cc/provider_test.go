package cc_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	cc "github.com/wake/purdex/internal/agent/cc"
)

func TestCCProvider_Identify_NativeBinary(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	if !p.Identify(agent.ProcessInfo{ExePath: "/usr/local/bin/claude"}) {
		t.Fatal("Identify should accept claude binary")
	}
}

func TestCCProvider_Identify_WithArgv0VersionString(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	info := agent.ProcessInfo{
		ExePath: "/Users/x/.local/bin/claude",
		Argv:    []string{"2.1.114", "--print"},
	}
	if !p.Identify(info) {
		t.Fatal("Identify should use ExePath basename, not argv[0]")
	}
}

func TestCCProvider_Identify_NodeWrapper(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	info := agent.ProcessInfo{
		ExePath: "/usr/bin/node",
		Argv:    []string{"node", "/lib/@anthropic-ai/claude-code/index.js"},
	}
	if !p.Identify(info) {
		t.Fatal("Identify should accept claude node wrapper")
	}
}

func TestCCProvider_Identify_Negative(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	if p.Identify(agent.ProcessInfo{
		ExePath: "/usr/bin/node",
		Argv:    []string{"node", "/tmp/not-agent.js"},
	}) {
		t.Fatal("Identify should reject unrelated node process")
	}
	if p.Identify(agent.ProcessInfo{
		ExePath: "/opt/homebrew/bin/codex",
		Argv:    []string{"codex"},
	}) {
		t.Fatal("Identify should reject codex process")
	}
}

// TestCCSupportedStatuses asserts cc.Provider implements StatusSupporter and
// declares the full Phase 1 status set: Running, Waiting, Idle, Error, Clear.
// Order is implementation-defined; we compare as a set.
func TestCCSupportedStatuses(t *testing.T) {
	var p any = cc.NewProvider(nil, nil, nil, nil)
	ss, ok := p.(agent.StatusSupporter)
	if !ok {
		t.Fatal("cc.Provider must implement agent.StatusSupporter")
	}
	got := ss.SupportedStatuses()
	want := map[agent.Status]bool{
		agent.StatusRunning: true,
		agent.StatusWaiting: true,
		agent.StatusIdle:    true,
		agent.StatusError:   true,
		agent.StatusClear:   true,
	}
	if len(got) != len(want) {
		t.Fatalf("SupportedStatuses len = %d, want %d (got %v)", len(got), len(want), got)
	}
	seen := make(map[agent.Status]bool, len(got))
	for _, s := range got {
		if seen[s] {
			t.Fatalf("SupportedStatuses contains duplicate %q (got %v)", s, got)
		}
		seen[s] = true
		if !want[s] {
			t.Fatalf("SupportedStatuses contains unexpected %q (got %v)", s, got)
		}
	}
}

// TestCCSupportedStatuses_DerivesFromEvents asserts the post-Commit-3
// invariant that SupportedStatuses is computed from Events().EmitsStatus
// union rather than a hard-coded literal. Compared as sets.
func TestCCSupportedStatuses_DerivesFromEvents(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	ss := any(p).(agent.StatusSupporter)
	got := ss.SupportedStatuses()

	gotSet := make(map[agent.Status]bool, len(got))
	for _, s := range got {
		gotSet[s] = true
	}
	wantSet := make(map[agent.Status]bool)
	for _, e := range p.Events() {
		for _, s := range e.EmitsStatus {
			wantSet[s] = true
		}
	}
	if len(gotSet) != len(wantSet) {
		t.Fatalf("SupportedStatuses=%v, events union=%v (len mismatch)", got, wantSet)
	}
	for s := range wantSet {
		if !gotSet[s] {
			t.Errorf("SupportedStatuses missing %q (from events union)", s)
		}
	}
	for s := range gotSet {
		if !wantSet[s] {
			t.Errorf("SupportedStatuses contains %q not in events union", s)
		}
	}
}

// TestSupportedStatusesReturnsFreshSlice guards the defensive-copy convention
// (per plan §1.1): mutating a returned slice must not affect a subsequent
// call. Verified on cc; codex/opencode follow the same literal-return pattern.
func TestSupportedStatusesReturnsFreshSlice(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	ss := any(p).(agent.StatusSupporter)
	first := ss.SupportedStatuses()
	if len(first) == 0 {
		t.Fatal("SupportedStatuses returned empty slice")
	}
	first[0] = agent.Status("__mutated__")
	second := ss.SupportedStatuses()
	if second[0] == "__mutated__" {
		t.Fatalf("SupportedStatuses shares backing array between calls: %v", second)
	}
}
