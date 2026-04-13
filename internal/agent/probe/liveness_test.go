package probe_test

import (
	"testing"

	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

type fakeContentMatcher struct {
	result bool
}

func (f *fakeContentMatcher) LooksLikeAgent(string) bool { return f.result }

func TestIsAliveFor_DirectCommand(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude", "cld"})

	fake.SetPaneCommand("sess:", "claude")
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive when pane command is registered CC command")
	}
}

func TestIsAliveFor_ShellIsDead(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "zsh")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when pane command is shell")
	}
}

func TestIsAliveFor_ChildProcess(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "node")
	fake.SetPaneChildren("sess:", []string{"/usr/local/bin/claude"})
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive when child process matches (basename)")
	}
}

func TestIsAliveFor_ContentFallback(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})
	p.RegisterContentMatcher("cc", &fakeContentMatcher{result: true})

	fake.SetPaneCommand("sess:", "node")
	fake.SetPaneChildren("sess:", []string{"npm"})
	fake.SetPaneContent("sess:", "❯ prompt here")
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive via content fallback")
	}
}

func TestIsAliveFor_NoContentMatcherReturnsDead(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "node")
	fake.SetPaneChildren("sess:", []string{"npm"})
	fake.SetPaneContent("sess:", "❯ prompt here")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when no content matcher registered")
	}
}

func TestIsAliveFor_ContentMatcherReturnsFalse(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})
	p.RegisterContentMatcher("cc", &fakeContentMatcher{result: false})

	fake.SetPaneCommand("sess:", "vim")
	fake.SetPaneChildren("sess:", nil)
	fake.SetPaneContent("sess:", "-- INSERT --")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when content matcher returns false")
	}
}

func TestIsAliveFor_UnknownAgentType(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)

	fake.SetPaneCommand("sess:", "claude")
	if p.IsAliveFor("unknown", "sess:") {
		t.Fatal("expected dead for unregistered agent type")
	}
}

func TestUpdateProcessNames(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	p := probe.New(fake)
	p.RegisterProcessNames("cc", []string{"claude"})

	fake.SetPaneCommand("sess:", "cld")
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("cld should not be alive before update")
	}

	p.UpdateProcessNames("cc", []string{"claude", "cld"})
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("cld should be alive after update")
	}
}
