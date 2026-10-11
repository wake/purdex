package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/module/session"
)

// NA-0a (no-agent launch panel spec §2.2 `agent_running`): a live root frame answers for the session, or the hook state
// says an agent runs there — any type, so a codex or opencode run counts as much as a Claude Code one.

func newRunningModule(t *testing.T) *Module {
	t.Helper()
	m, fake, sessions := newProvenanceQueryModule(t)
	sessions.sessions = []session.SessionInfo{{Code: codeOf(t, "$0"), Name: "work"}}
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	return m
}

func TestAgentRunning_LiveRootFrame_AnyAgentType(t *testing.T) {
	for _, agentType := range []string{"cc", "codex", "opencode"} {
		t.Run(agentType, func(t *testing.T) {
			m := newRunningModule(t)
			seedIdentityFrame(t, m, "%5", agentType, 100, "t100", 42, "sess-1", "/w/a")
			withProcessTree(t, map[int]int{100: 200, 200: 1})
			withLivePids(t, map[int]string{100: "t100"})
			running, err := m.AgentRunning(context.Background(), codeOf(t, "$0"))
			if err != nil || !running {
				t.Fatalf("running=%v err=%v, want true", running, err)
			}
		})
	}
}

func TestAgentRunning_NoFrameNoHookState_IsNotRunning(t *testing.T) {
	m := newRunningModule(t)
	running, err := m.AgentRunning(context.Background(), codeOf(t, "$0"))
	if err != nil || running {
		t.Fatalf("running=%v err=%v, want false", running, err)
	}
}

// A dead frame (its process gone, the sweep has not removed the row yet) is not an agent running.
func TestAgentRunning_DeadFrameIsNotRunning(t *testing.T) {
	m := newRunningModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/a")
	withProcessTree(t, map[int]int{200: 1})
	withLivePids(t, map[int]string{})
	running, err := m.AgentRunning(context.Background(), codeOf(t, "$0"))
	if err != nil || running {
		t.Fatalf("running=%v err=%v, want false", running, err)
	}
}

// The hook state: the session's event row exists while its agent runs (SessionEnd deletes it), for sessions the frame
// model has not caught up with.
func TestAgentRunning_HookStateSaysRunning(t *testing.T) {
	for _, agentType := range []string{"cc", "codex", "opencode"} {
		t.Run(agentType, func(t *testing.T) {
			m := newRunningModule(t)
			if err := m.events.Set("work", "PdxUserPromptSubmit", json.RawMessage(`{}`), agentType, 5); err != nil {
				t.Fatal(err)
			}
			running, err := m.AgentRunning(context.Background(), codeOf(t, "$0"))
			if err != nil || !running {
				t.Fatalf("running=%v err=%v, want true", running, err)
			}
		})
	}
}

// A hook state under another session's name says nothing about this one.
func TestAgentRunning_OtherSessionsHookStateIsNotThisOnes(t *testing.T) {
	m := newRunningModule(t)
	if err := m.events.Set("elsewhere", "PdxUserPromptSubmit", json.RawMessage(`{}`), "cc", 5); err != nil {
		t.Fatal(err)
	}
	running, err := m.AgentRunning(context.Background(), codeOf(t, "$0"))
	if err != nil || running {
		t.Fatalf("running=%v err=%v, want false", running, err)
	}
}

func TestAgentRunning_UnknownSessionOrEmptyCode_IsNotRunning(t *testing.T) {
	m := newRunningModule(t)
	for _, code := range []string{"", "nosuch"} {
		running, err := m.AgentRunning(context.Background(), code)
		if err != nil || running {
			t.Fatalf("code %q: running=%v err=%v, want false", code, running, err)
		}
	}
}

// The module publishes itself under AgentRunningKey as an AgentRunning.
var _ AgentRunning = (*Module)(nil)
