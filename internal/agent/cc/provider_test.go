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
