package probe

import (
	"fmt"
	"strings"
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

// --- FirstAliveAgentInTree tests (Phase 3 Commit 1) ---

func TestFirstAliveAgentInTree_HitFirstMatch(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool {
		return info.ExePath == "/usr/local/bin/claude"
	})
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 100:
			return agentpkg.ProcessInfo{PID: 100, ExePath: "/bin/zsh"}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, ExePath: "/usr/local/bin/claude"}, nil
		case 300:
			return agentpkg.ProcessInfo{PID: 300, ExePath: "/bin/cat"}, nil
		default:
			return agentpkg.ProcessInfo{}, fmt.Errorf("unexpected pid %d", pid)
		}
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return map[int][]int{100: {200, 300}}, nil
	}

	agentType, pid, err := p.FirstAliveAgentInTree("sess:")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentType != "cc" || pid != 200 {
		t.Fatalf("expected (cc, 200, nil), got (%q, %d, %v)", agentType, pid, err)
	}
}

func TestFirstAliveAgentInTree_NoMatch(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool {
		return info.ExePath == "/usr/local/bin/claude"
	})
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, ExePath: "/bin/zsh"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return map[int][]int{100: {200}}, nil
	}

	agentType, pid, err := p.FirstAliveAgentInTree("sess:")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentType != "" || pid != 0 {
		t.Fatalf("expected (\"\", 0, nil), got (%q, %d, %v)", agentType, pid, err)
	}
}

func TestFirstAliveAgentInTree_DescendantsError(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return true })
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, ExePath: "/bin/zsh"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return nil, fmt.Errorf("ps failed")
	}

	agentType, pid, err := p.FirstAliveAgentInTree("sess:")
	if err == nil {
		t.Fatal("expected error from descendants query failure")
	}
	if agentType != "" || pid != 0 {
		t.Fatalf("expected (\"\", 0, err), got (%q, %d, %v)", agentType, pid, err)
	}
}

func TestFirstAliveAgentInTree_RegistryOrder(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	// Both cc and codex identifiers match PID 200; cc registered first.
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool {
		return info.PID == 200
	})
	p.RegisterIdentifier("codex", func(info agentpkg.ProcessInfo) bool {
		return info.PID == 200
	})
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, ExePath: "/bin/irrelevant"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return map[int][]int{100: {200}}, nil
	}

	agentType, pid, err := p.FirstAliveAgentInTree("sess:")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentType != "cc" || pid != 200 {
		t.Fatalf("expected (cc, 200, nil) due to registration order, got (%q, %d, %v)", agentType, pid, err)
	}
}

// TestFirstAliveAgentInTree_UsesActivePanePID is a regression guard for
// PR #638 codex review round 1 P2: FirstAliveAgentInTree must resolve the
// pane PID via ActivePanePID (which uses `tmux display-message` and honors
// pane id targets exactly) rather than PanePID (which uses `tmux list-panes`
// that resolves a pane id target to its containing window and returns the
// FIRST listed pane — wrong for multi-pane windows where the hook came from
// a non-first sibling pane).
//
// Setup: distinct PanePID (100) and ActivePanePID (200) for the same target.
// Identifier matches PID 200 only. If the implementation regresses to PanePID,
// it will probe pid=100 (sibling pane), find no match, and return ("", 0, nil).
func TestFirstAliveAgentInTree_UsesActivePanePID(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("%5", "100")       // would-be wrong PID (list-panes first row sibling)
	fake.SetActivePanePID("%5", "200") // correct PID (display-message of pane %5)
	p := New(fake)
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool {
		return info.PID == 200
	})
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, ExePath: "/bin/irrelevant"}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return map[int][]int{200: {}}, nil
	}

	agentType, pid, err := p.FirstAliveAgentInTree("%5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentType != "cc" || pid != 200 {
		t.Fatalf("expected (cc, 200, nil) proving ActivePanePID path; got (%q, %d, %v) — regression to PanePID would yield (\"\", 0, nil)", agentType, pid, err)
	}
}

// TestFirstAliveAgentInTree_IdentifierPanic_RecoveredAsNoMatch verifies that
// a panic from a provider's Identify function is caught at the per-call
// boundary (safeIdentifyPID) and treated as a no-match — the walk continues
// to other PIDs and other identifiers, rather than crashing through to the
// hook handler. Regression guard for PR #638 codex review round 2 #2.
func TestFirstAliveAgentInTree_IdentifierPanic_RecoveredAsNoMatch(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	// First identifier panics on every PID; should not crash, walk continues.
	p.RegisterIdentifier("buggy", func(info agentpkg.ProcessInfo) bool {
		panic("simulated provider Identify panic")
	})
	// Second identifier matches PID=200 — must still be reachable after
	// the buggy identifier panics on all PIDs.
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool {
		return info.PID == 200
	})
	stubLivenessSeams(t)
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid}, nil
	}
	listProcessTreeFn = func() (map[int][]int, error) {
		return map[int][]int{100: {200}}, nil
	}

	agentType, pid, err := p.FirstAliveAgentInTree("sess:")
	if err != nil {
		t.Fatalf("unexpected error after identifier panic: %v", err)
	}
	if agentType != "cc" || pid != 200 {
		t.Fatalf("expected (cc, 200, nil) — walk should continue past panicking identifier; got (%q, %d, %v)", agentType, pid, err)
	}
}

// TestFirstAliveAgentInTree_TopLevelPanic_BecomesError verifies the outer
// defer recover converts an uncaught panic (e.g. from cachedDescendants
// internals) into an error so the rebuild caller can fail-soft instead of
// panicking through to the hook handler. Regression guard for PR #638
// codex review round 2 #2 (outer boundary).
func TestFirstAliveAgentInTree_TopLevelPanic_BecomesError(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("sess:", "100")
	p := New(fake)
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return false })
	stubLivenessSeams(t)
	// listProcessTreeFn panics — bypasses the per-call safeIdentifyPID
	// recovery and would crash the hook handler without the outer defer.
	listProcessTreeFn = func() (map[int][]int, error) {
		panic("simulated process tree query panic")
	}

	agentType, pid, err := p.FirstAliveAgentInTree("sess:")
	if err == nil {
		t.Fatal("expected error from top-level panic recovery")
	}
	if agentType != "" || pid != 0 {
		t.Fatalf("expected (\"\", 0, err); got (%q, %d, %v)", agentType, pid, err)
	}
	// err message should mention panic for caller diagnostics
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("expected error message to mention panic, got %q", err.Error())
	}
}

func TestRegisterIdentifier_PreservesOrder(t *testing.T) {
	p := New(tmux.NewFakeExecutor())
	p.RegisterIdentifier("codex", func(info agentpkg.ProcessInfo) bool { return false })
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return false })
	p.RegisterIdentifier("opencode", func(info agentpkg.ProcessInfo) bool { return false })

	want := []string{"codex", "cc", "opencode"}
	got := p.identifierOrderSnapshot()
	if len(got) != len(want) {
		t.Fatalf("expected %d identifiers, got %d (%v)", len(want), len(got), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("expected order[%d]=%q, got %q (full: %v)", i, name, got[i], got)
		}
	}

	// Re-register existing identifier: order must not change.
	p.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return false })
	got = p.identifierOrderSnapshot()
	if len(got) != len(want) {
		t.Fatalf("re-register should not grow order; got %v", got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("re-register changed order at [%d]: expected %q got %q (full: %v)", i, name, got[i], got)
		}
	}
}
