package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// c9d9c7-67 PR2 (#2592): the Stop hook's background_tasks, counted by kind, for the conversation header ("1 shell still running").

func TestHookBackgroundCountOf(t *testing.T) {
	task := func(typ string) string { return `{"id":"t","type":"` + typ + `","status":"running"}` }
	cases := []struct {
		name string
		raw  string
		want HookBackground
	}{
		{"nothing", `{"background_tasks":[]}`, HookBackground{}},
		{"one shell", `{"background_tasks":[` + task("shell") + `]}`, HookBackground{Shell: 1}},
		{"shells and subagents", `{"background_tasks":[` + task("shell") + `,` + task("shell") + `,` + task("subagent") + `]}`, HookBackground{Shell: 2, Subagent: 1}},
		{"monitor and workflow are other", `{"background_tasks":[` + task("monitor") + `,` + task("workflow") + `]}`, HookBackground{Other: 2}},
		{"an unknown kind is other", `{"background_tasks":[` + task("future-kind") + `]}`, HookBackground{Other: 1}},
		{"crons are other", `{"session_crons":[{"id":"a"},{"id":"b"}]}`, HookBackground{Other: 2}},
		{"everything", `{"background_tasks":[` + task("shell") + `,` + task("subagent") + `,` + task("monitor") + `],"session_crons":[{"id":"c"}]}`, HookBackground{Shell: 1, Subagent: 1, Other: 2}},
	}
	for _, c := range cases {
		if got := hookBackgroundCountOf(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// An older CC sends neither field, and a payload may be anything: no counts, never a panic.
func TestHookBackgroundCountOf_MissingOrMalformed(t *testing.T) {
	for _, raw := range []string{``, `{}`, `null`, `[]`, `not json`, `{"background_tasks":"x"}`, `{"background_tasks":[{"type":5}]}`} {
		if got := hookBackgroundCountOf(json.RawMessage(raw)); !got.Zero() {
			t.Errorf("hookBackgroundCountOf(%q) = %+v, want none", raw, got)
		}
	}
}

func hookBgCountOf(m *Module, frameID string) (HookBackground, bool) {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	c, ok := m.hookBgCount[frameID]
	return c, ok
}

func TestStop_BackgroundCountsFollowTheStopSessionStartAndTheFrame(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	sendBody(t, m, hookBgSessionStart("startup"))
	frameID := findCCFrameRow(t, m).FrameID

	// a Stop that lists a shell and a subagent sets the counts; the next Stop replaces them
	sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("shell"), hookBgTask("subagent")}, 0))
	if c, ok := hookBgCountOf(m, frameID); !ok || c != (HookBackground{Shell: 1, Subagent: 1}) {
		t.Fatalf("after the first Stop: %+v ok=%v", c, ok)
	}
	sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("shell")}, 1))
	if c, _ := hookBgCountOf(m, frameID); c != (HookBackground{Shell: 1, Other: 1}) {
		t.Fatalf("after the second Stop: %+v", c)
	}
	// a Stop that lists nothing removes the entry
	sendBody(t, m, hookBgStop(nil, 0))
	if _, ok := hookBgCountOf(m, frameID); ok {
		t.Fatal("a Stop that lists nothing left the counts in place")
	}
	// a new conversation (SessionStart) has no background work yet
	sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("shell")}, 0))
	sendBody(t, m, hookBgSessionStart("clear"))
	if _, ok := hookBgCountOf(m, frameID); ok {
		t.Fatal("a SessionStart left the counts in place")
	}
	// the frame going away takes them too
	sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("shell")}, 0))
	m.forgetHookBackground(frameID)
	if _, ok := hookBgCountOf(m, frameID); ok {
		t.Fatal("forgetting the frame left the counts in place")
	}
}

// A Stop stamped before a SessionStart that reaches the map after it belongs to the old conversation: it must not write counts into the new one.
func TestStop_BackgroundCountsLateStopDoesNotRevive(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	sendBody(t, m, hookBgSessionStart("startup"))
	frameID := findCCFrameRow(t, m).FrameID
	stop := EventRequest{TmuxPaneID: "%5", SenderPID: 200, SenderStartTime: hookBgStart, AgentType: "cc",
		RawEvent: json.RawMessage(`{"background_tasks":[{"id":"t","type":"shell","status":"running"}]}`)}
	oldStopTs := time.Now().UnixNano()
	sendBody(t, m, hookBgSessionStart("clear"))
	m.noteHookBackground(stop, agentpkg.LifecycleStop, oldStopTs)
	if _, ok := hookBgCountOf(m, frameID); ok {
		t.Fatal("a Stop stamped before the SessionStart wrote counts into the new session")
	}
	m.noteHookBackground(stop, agentpkg.LifecycleStop, time.Now().UnixNano())
	if c, _ := hookBgCountOf(m, frameID); c != (HookBackground{Shell: 1}) {
		t.Fatalf("a Stop stamped after the SessionStart: %+v, want one shell", c)
	}
}

func TestStop_BackgroundCountsOnlyARootFrame(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	sendBody(t, m, hookBgSessionStart("startup"))
	root := findCCFrameRow(t, m)
	child := seedChildFrame(t, m, "%5", "cc", 300, "t300", root.FrameID)
	req := EventRequest{TmuxPaneID: "%5", SenderPID: child.PID, SenderStartTime: child.ProcessStartTime, AgentType: "cc",
		RawEvent: json.RawMessage(`{"background_tasks":[{"id":"t","type":"shell","status":"running"}]}`)}
	m.noteHookBackground(req, agentpkg.LifecycleStop, time.Now().UnixNano())
	if _, ok := hookBgCountOf(m, child.FrameID); ok {
		t.Fatal("a child frame's Stop recorded counts")
	}
}

// BackgroundWork finds the counts of the live root frame that reports the session; none for another session, a dead pid, or no listing.
func TestBackgroundWork_ReadsTheLiveRootFrameOfTheSession(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	orig := isPidAliveFn
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() { isPidAliveFn = orig })
	sendBody(t, m, hookBgSessionStart("startup")) // session_id ses_bg
	if _, ok := m.BackgroundWork("ses_bg"); ok {
		t.Fatal("counts before any Stop")
	}
	sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("shell"), hookBgTask("shell")}, 0))
	got, ok := m.BackgroundWork("ses_bg")
	if !ok || got != (HookBackground{Shell: 2}) {
		t.Fatalf("BackgroundWork = %+v ok=%v, want two shells", got, ok)
	}
	if _, ok := m.BackgroundWork("another-session"); ok {
		t.Error("another session got these counts")
	}
	if _, ok := m.BackgroundWork(""); ok {
		t.Error("an empty session id found counts")
	}
	isPidAliveFn = func(int) bool { return false }
	if _, ok := m.BackgroundWork("ses_bg"); ok {
		t.Error("a frame whose process is gone still reports background work")
	}
}
