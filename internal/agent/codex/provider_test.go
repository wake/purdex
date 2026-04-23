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
