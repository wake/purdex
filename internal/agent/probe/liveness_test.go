package probe

import (
	"fmt"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/tmux"
)

func stubLivenessSeams(t *testing.T) {
	t.Helper()
	origRead := readProcessInfoFn
	origTree := listProcessTreeFn
	t.Cleanup(func() {
		readProcessInfoFn = origRead
		listProcessTreeFn = origTree
	})
}

func registerTestIdentifier(p *Prober) {
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool {
		return info.ExePath == "/usr/local/bin/claude"
	})
}

func TestIsAliveFor_PaneProcess(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	registerTestIdentifier(p)
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid != 100 {
			return agentpkg.ProcessInfo{}, fmt.Errorf("unexpected pid %d", pid)
		}
		return agentpkg.ProcessInfo{PID: 100, ExePath: "/usr/local/bin/claude"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) { return map[int][]int{}, nil }

	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive when pane pid identifies as cc")
	}
}

func TestIsAliveFor_ShellIsDead(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	registerTestIdentifier(p)
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, ExePath: "/bin/zsh"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) { return map[int][]int{}, nil }

	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead when pane pid tree contains no matching process")
	}
}

func TestIsAliveFor_DescendantProcess(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	registerTestIdentifier(p)
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 100:
			return agentpkg.ProcessInfo{PID: 100, ExePath: "/bin/zsh"}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, ExePath: "/usr/local/bin/claude"}, nil
		default:
			return agentpkg.ProcessInfo{}, fmt.Errorf("unexpected pid %d", pid)
		}
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return map[int][]int{100: []int{200}}, nil
	}

	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive when descendant identifies as cc")
	}
}

func TestIsAliveFor_RecursiveDescendantUsesCacheWithinTTL(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	registerTestIdentifier(p)
	now := time.Unix(100, 0)
	p.now = func() time.Time { return now }
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 100:
			return agentpkg.ProcessInfo{PID: 100, ExePath: "/bin/zsh"}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, ExePath: "/usr/local/bin/claude"}, nil
		default:
			return agentpkg.ProcessInfo{}, fmt.Errorf("unexpected pid %d", pid)
		}
	}
	tree := map[int][]int{100: []int{200}}
	listProcessTreeFn = func() (map[int][]int, error) { return tree, nil }

	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive on first lookup")
	}
	tree = map[int][]int{100: []int{}}
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected cached descendant lookup to keep reporting alive")
	}
}

func TestIsAliveFor_RecursiveDescendantCacheExpires(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	registerTestIdentifier(p)
	now := time.Unix(100, 0)
	p.now = func() time.Time { return now }
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 100:
			return agentpkg.ProcessInfo{PID: 100, ExePath: "/bin/zsh"}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, ExePath: "/usr/local/bin/claude"}, nil
		default:
			return agentpkg.ProcessInfo{}, fmt.Errorf("unexpected pid %d", pid)
		}
	}
	tree := map[int][]int{100: []int{200}}
	listProcessTreeFn = func() (map[int][]int, error) { return tree, nil }

	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive before descendant exits")
	}
	tree = map[int][]int{100: []int{}}
	now = now.Add(recursiveDescendantCacheTTL + time.Millisecond)
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead after cache expiry")
	}
}

func TestIsAliveFor_RecursiveDescendantCacheInvalidatesOnPanePIDChange(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	registerTestIdentifier(p)
	now := time.Unix(100, 0)
	p.now = func() time.Time { return now }
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 100, 101:
			return agentpkg.ProcessInfo{PID: pid, ExePath: "/bin/zsh"}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, ExePath: "/usr/local/bin/claude"}, nil
		default:
			return agentpkg.ProcessInfo{}, fmt.Errorf("unexpected pid %d", pid)
		}
	}
	tree := map[int][]int{100: []int{200}}
	listProcessTreeFn = func() (map[int][]int, error) { return tree, nil }

	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive before pane pid changes")
	}

	fake.SetPanePID("sess:", "101")
	tree = map[int][]int{101: []int{}}
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead after pane pid changes")
	}
}

func TestIsAliveFor_UnknownAgentType(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)

	if p.IsAliveFor("unknown", "sess:") {
		t.Fatal("expected dead for unregistered agent type")
	}
}

func TestRegisterIdentifier_ReplacesExisting(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, ExePath: "/usr/local/bin/cld"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) { return map[int][]int{}, nil }

	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return false })
	if p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected dead before identifier replacement")
	}

	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/cld" })
	if !p.IsAliveFor("cc", "sess:") {
		t.Fatal("expected alive after identifier replacement")
	}
}
