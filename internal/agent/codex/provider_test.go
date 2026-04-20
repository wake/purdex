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
