package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func newVerifyTestModule(t *testing.T) *Module {
	t.Helper()
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })
	m := New(events)
	m.registry = agentpkg.NewRegistry()
	m.tmux = tmux.NewFakeExecutor()
	if m.traceSink != nil {
		t.Cleanup(func() { m.traceSink.Close() })
	}
	return m
}

func stubVerifySeams(t *testing.T) {
	t.Helper()
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origAncestors := pidAncestorIncludesFn
	origRead := readProcessInfoFn
	origResolvePane := resolvePanePIDFn
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		pidAncestorIncludesFn = origAncestors
		readProcessInfoFn = origRead
		resolvePanePIDFn = origResolvePane
	})
}

func TestVerify_AcceptsPaneNativeHook(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return pid == 200 && ancestor == 100 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 200, PPID: 100, ExePath: "/usr/local/bin/claude", Argv: []string{"claude"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		AgentType:       "cc",
		EventName:       "Stop",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if !decision.Accepted {
		t.Fatalf("verify should accept native pane hook, got %+v", decision)
	}
}

func TestVerify_RejectsDetachedRuntime(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		identify: func(agentpkg.ProcessInfo) bool { return true },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 999 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return false }

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		AgentType:       "codex",
		EventName:       "Stop",
		SenderPID:       999,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "pid_not_in_pane_tree" {
		t.Fatalf("reason = %q, want pid_not_in_pane_tree", decision.Reason)
	}
}

func TestVerify_AcceptsNestedAgent(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/opt/homebrew/bin/codex" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 300 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return pid == 300 && ancestor == 100 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 300, PPID: 200, ExePath: "/opt/homebrew/bin/codex", Argv: []string{"codex"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		AgentType:       "codex",
		EventName:       "Stop",
		SenderPID:       300,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if !decision.Accepted {
		t.Fatalf("verify should accept nested agent, got %+v", decision)
	}
}

func TestVerify_RejectsDeadPid(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return false }

	decision := m.verifyEvent(EventRequest{SenderPID: 404})
	if decision.Reason != "pid_dead" {
		t.Fatalf("reason = %q, want pid_dead", decision.Reason)
	}
}

func TestVerify_RejectsPidReuse(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 02:00:00 2026", nil }

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "pid_reused" {
		t.Fatalf("reason = %q, want pid_reused", decision.Reason)
	}
}

func TestVerify_RejectsIdentifyMismatch(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return true }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 200, PPID: 100, ExePath: "/opt/homebrew/bin/codex", Argv: []string{"codex"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		AgentType:       "cc",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "identify_mismatch" {
		t.Fatalf("reason = %q, want identify_mismatch", decision.Reason)
	}
}

func TestVerify_RejectsPaneUnresolvable(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%missing",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "pane_unresolvable" {
		t.Fatalf("reason = %q, want pane_unresolvable", decision.Reason)
	}
}

func TestVerify_RejectsUncertainSender(t *testing.T) {
	m := newVerifyTestModule(t)

	decision := m.verifyEvent(EventRequest{
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
		SenderUncertain: true,
	})
	if decision.Reason != "sender_uncertain" {
		t.Fatalf("reason = %q, want sender_uncertain", decision.Reason)
	}
}

func TestVerify_RejectsWhenStartTimeLookupFails(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "", errStub("ps failed") }

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "start_time_unavailable" {
		t.Fatalf("reason = %q, want start_time_unavailable", decision.Reason)
	}
}

func TestVerify_RejectsWhenProcessLookupFails(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(agentpkg.ProcessInfo) bool { return true },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return true }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{}, errStub("lookup failed")
	}

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		AgentType:       "cc",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "process_lookup_failed" {
		t.Fatalf("reason = %q, want process_lookup_failed", decision.Reason)
	}
}
