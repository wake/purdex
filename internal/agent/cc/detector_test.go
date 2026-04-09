package cc_test

import (
	"testing"

	cc "github.com/wake/tmux-box/internal/agent/cc"
	"github.com/wake/tmux-box/internal/tmux"
)

func TestDetectStatus(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := cc.NewDetector(fake, []string{"claude", "cld"})

	tests := []struct {
		name     string
		cmd      string
		content  string
		expected cc.Status
	}{
		{"shell idle", "zsh", "", cc.StatusNormal},
		{"non-cc command", "node", "", cc.StatusNotInCC},
		{"cc idle", "claude", "❯ ", cc.StatusCCIdle},
		{"cc running", "claude", "⠋ Reading file...", cc.StatusCCRunning},
		{"cc waiting permission", "claude", "Allow  Deny", cc.StatusCCWaiting},
		{"cc alias idle", "cld", "❯ ", cc.StatusCCIdle},
		{"cc idle with status bar below", "claude", "❯ \n─────────\n  project [Opus 4.6] 100% left", cc.StatusCCIdle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.SetPaneCommand("test", tt.cmd)
			fake.SetPaneContent("test", tt.content)
			fake.SetPaneChildren("test", nil)
			status := d.Detect("test")
			if status != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, status)
			}
		})
	}
}

func TestDetectViaChildProcess(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := cc.NewDetector(fake, []string{"claude"})
	fake.SetPaneCommand("test", "2.1.77")
	fake.SetPaneChildren("test", []string{"claude"})
	fake.SetPaneContent("test", "❯ ")
	status := d.Detect("test")
	if status != cc.StatusCCIdle {
		t.Fatalf("expected cc-idle via child process detection, got %s", status)
	}
}

func TestDetectViaChildProcessRunning(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := cc.NewDetector(fake, []string{"claude"})
	fake.SetPaneCommand("test", "2.1.77")
	fake.SetPaneChildren("test", []string{"claude"})
	fake.SetPaneContent("test", "⠋ Writing code...")
	status := d.Detect("test")
	if status != cc.StatusCCRunning {
		t.Fatalf("expected cc-running via child process, got %s", status)
	}
}

func TestDetectViaChildProcessWithPath(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := cc.NewDetector(fake, []string{"claude"})
	fake.SetPaneCommand("test", "2.1.77")
	fake.SetPaneChildren("test", []string{"/Users/wake/.local/bin/claude"})
	fake.SetPaneContent("test", "❯ ")
	status := d.Detect("test")
	if status != cc.StatusCCIdle {
		t.Fatalf("expected cc-idle via child path basename, got %s", status)
	}
}

func TestDetectViaPaneContentFallback(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := cc.NewDetector(fake, []string{"claude"})
	fake.SetPaneCommand("test", "unknown-process")
	fake.SetPaneChildren("test", []string{"npm"})
	fake.SetPaneContent("test", "  tmp [Opus 4.6 (1M context)] ░░░░ 100% left\n❯ ")
	status := d.Detect("test")
	if status != cc.StatusCCIdle {
		t.Fatalf("expected cc-idle via content fallback, got %s", status)
	}
}

func TestDetectNotCCWhenNoSignatures(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := cc.NewDetector(fake, []string{"claude"})
	fake.SetPaneCommand("test", "vim")
	fake.SetPaneChildren("test", nil)
	fake.SetPaneContent("test", "-- INSERT --")
	status := d.Detect("test")
	if status != cc.StatusNotInCC {
		t.Fatalf("expected not-in-cc, got %s", status)
	}
}
