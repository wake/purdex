package codex_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

func TestCodexProvider_Identify_NativeBinary(t *testing.T) {
	p := codex.NewProvider()
	if !p.Identify(agent.ProcessInfo{ExePath: "/opt/homebrew/bin/codex"}) {
		t.Fatal("Identify should accept codex binary")
	}
}

func TestCodexProvider_Identify_WithArgv0VersionString(t *testing.T) {
	p := codex.NewProvider()
	info := agent.ProcessInfo{
		ExePath: "/Users/x/.local/bin/codex",
		Argv:    []string{"0.121.0", "--version"},
	}
	if !p.Identify(info) {
		t.Fatal("Identify should use ExePath basename, not argv[0]")
	}
}

func TestCodexProvider_Identify_NodeWrapper(t *testing.T) {
	p := codex.NewProvider()
	info := agent.ProcessInfo{
		ExePath: "/usr/bin/node",
		Argv:    []string{"node", "/lib/@openai/codex/dist/cli.js"},
	}
	if !p.Identify(info) {
		t.Fatal("Identify should accept codex node wrapper")
	}
}

func TestCodexProvider_Identify_Negative(t *testing.T) {
	p := codex.NewProvider()
	if p.Identify(agent.ProcessInfo{
		ExePath: "/usr/bin/node",
		Argv:    []string{"node", "/tmp/not-agent.js"},
	}) {
		t.Fatal("Identify should reject unrelated node process")
	}
	if p.Identify(agent.ProcessInfo{
		ExePath: "/usr/local/bin/claude",
		Argv:    []string{"claude"},
	}) {
		t.Fatal("Identify should reject claude process")
	}
}

// TestCodexSupportedStatuses_DerivesFromEvents asserts the post-Commit-5
// invariant that SupportedStatuses is computed from Events().EmitsStatus
// union rather than a hard-coded literal.
func TestCodexSupportedStatuses_DerivesFromEvents(t *testing.T) {
	p := codex.NewProvider()
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

// --- P2-T3: ProbeIntents() declaration tests ---

// TestProvider_ProbeIntents_DeclaresProcessDead asserts the codex provider
// declares ProcessDead as the first ProbeIntent (W6-3 + W6-4 scope) and
// the slice has at least one entry. The assertion is index-stable: future
// W6 PRs MAY append more intents at later positions but MUST keep
// ProcessDead at index 0 (per provider.go contract: stable order).
func TestProvider_ProbeIntents_DeclaresProcessDead(t *testing.T) {
	p := codex.NewProvider()
	pip, ok := any(p).(agent.ProbeIntentProvider)
	if !ok {
		t.Fatalf("codex.Provider does not implement agent.ProbeIntentProvider")
	}
	intents := pip.ProbeIntents()
	if len(intents) == 0 {
		t.Fatalf("ProbeIntents() returned empty slice")
	}
	if intents[0].Kind != agent.ProbeIntentKindProcessDead {
		t.Errorf("ProbeIntents()[0].Kind = %q, want %q (ProcessDead must remain at index 0)", intents[0].Kind, agent.ProbeIntentKindProcessDead)
	}
	if intents[0].OnSignal == nil {
		t.Errorf("ProbeIntents()[0].OnSignal = nil, want non-nil mapper")
	}
}

// TestProvider_ProbeIntents_OnEntryStatusContainsRunningWaiting verifies the
// gating set covers both Running and Waiting (codex process_dead inference
// only makes sense while codex is supposed to be working).
func TestProvider_ProbeIntents_OnEntryStatusContainsRunningWaiting(t *testing.T) {
	p := codex.NewProvider()
	pip, ok := any(p).(agent.ProbeIntentProvider)
	if !ok {
		t.Fatalf("codex.Provider does not implement agent.ProbeIntentProvider")
	}
	intents := pip.ProbeIntents()
	if len(intents) == 0 {
		t.Fatalf("ProbeIntents() returned no intents")
	}
	gating := make(map[agent.Status]bool, len(intents[0].OnEntryStatus))
	for _, s := range intents[0].OnEntryStatus {
		gating[s] = true
	}
	for _, want := range []agent.Status{agent.StatusRunning, agent.StatusWaiting} {
		if !gating[want] {
			t.Errorf("OnEntryStatus missing %q (have %v)", want, intents[0].OnEntryStatus)
		}
	}
}

// TestCodexSupportedStatuses asserts codex.Provider implements
// StatusSupporter and declares the same Phase 1 status set as cc/opencode
// post-DeriveStatus expansion (Commit 3).
func TestCodexSupportedStatuses(t *testing.T) {
	var p any = codex.NewProvider()
	ss, ok := p.(agent.StatusSupporter)
	if !ok {
		t.Fatal("codex.Provider must implement agent.StatusSupporter")
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
