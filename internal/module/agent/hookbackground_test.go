package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/lights"
)

const hookBgStart = "Sun Apr 20 01:30:00 2026"

func hookBgBody(purdexName string, raw map[string]any) string {
	rawJSON, _ := json.Marshal(raw)
	return `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"` + hookBgStart + `","purdex_name":"` + purdexName + `","raw_event":` + string(rawJSON) + `,"agent_type":"cc"}`
}

func hookBgSessionStart(source string) string {
	return hookBgBody("PdxSessionStart", map[string]any{"source": source, "session_id": "ses_bg"})
}

// hookBgStop is a cc Stop with the given in-flight tasks and cron count.
func hookBgStop(tasks []map[string]any, crons int) string {
	cs := make([]map[string]any, crons)
	for i := range cs {
		cs[i] = map[string]any{"id": "cron", "schedule": "*/5 * * * *", "recurring": true, "prompt": "p"}
	}
	return hookBgBody("PdxStop", map[string]any{
		"hook_event_name":  "Stop",
		"stop_hook_active": false,
		"background_tasks": tasks,
		"session_crons":    cs,
	})
}

func hookBgTask(typ string) map[string]any {
	return map[string]any{"id": "task-" + typ, "type": typ, "status": "running", "description": "d"}
}

// sendAndLastBackground posts body and returns the background of the last
// hook frame it emitted.
func sendAndLastBackground(t *testing.T, m *Module, body string) string {
	t.Helper()
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)
	sendBody(t, m, body)
	msgs := drainBroadcasts(sub, 150*time.Millisecond)
	if len(msgs) == 0 {
		t.Fatalf("no hook frame for %s", body)
	}
	return msgs[len(msgs)-1].Background
}

func hookBackgroundEntries(m *Module) int {
	m.modMu.Lock()
	defer m.modMu.Unlock()
	return len(m.hookBackground)
}

// With no mod at all, a root frame's Stop reports the background work it
// lists: a monitor or workflow task and crons draw a symbol; a shell task
// draws none. The next SessionStart wipes it, and so does the frame going
// away (SessionEnd, the sweep).
func TestStop_BackgroundSymbol(t *testing.T) {
	cases := []struct {
		name   string
		tasks  []map[string]any
		crons  int
		wantBg string
	}{
		{"monitor task", []map[string]any{hookBgTask("monitor")}, 0, string(lights.BackgroundMonitor)},
		{"workflow beats monitor", []map[string]any{hookBgTask("monitor"), hookBgTask("workflow")}, 0, string(lights.BackgroundWorkflow)},
		{"shell task only", []map[string]any{hookBgTask("shell")}, 0, ""},
		{"subagent task only", []map[string]any{hookBgTask("subagent")}, 0, ""},
		{"crons", nil, 2, string(lights.BackgroundSchedule)},
		{"monitor beats crons", []map[string]any{hookBgTask("monitor")}, 1, string(lights.BackgroundMonitor)},
		{"nothing in flight", nil, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := delegationModuleWithRealCCProvider(t)
			sendBody(t, m, hookBgSessionStart("startup"))
			if got := sendAndLastBackground(t, m, hookBgStop(tc.tasks, tc.crons)); got != tc.wantBg {
				t.Fatalf("emitted background = %q, want %q", got, tc.wantBg)
			}
		})
	}

	t.Run("a later Stop replaces the symbol", func(t *testing.T) {
		m := delegationModuleWithRealCCProvider(t)
		sendBody(t, m, hookBgSessionStart("startup"))
		sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("monitor")}, 0))
		if got := sendAndLastBackground(t, m, hookBgStop(nil, 0)); got != "" {
			t.Fatalf("background after an empty Stop = %q, want cleared", got)
		}
		if n := hookBackgroundEntries(m); n != 0 {
			t.Fatalf("%d entries kept for an empty symbol", n)
		}
	})

	t.Run("the next SessionStart clears it", func(t *testing.T) {
		for _, source := range []string{"startup", "clear", "resume"} {
			m := delegationModuleWithRealCCProvider(t)
			sendBody(t, m, hookBgSessionStart("startup"))
			sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("monitor")}, 0))
			if n := hookBackgroundEntries(m); n != 1 {
				t.Fatalf("source %s precondition: %d entries, want 1", source, n)
			}
			if got := sendAndLastBackground(t, m, hookBgSessionStart(source)); got != "" {
				t.Fatalf("source %s: emitted background = %q, want cleared", source, got)
			}
			if n := hookBackgroundEntries(m); n != 0 {
				t.Fatalf("source %s: %d entries left", source, n)
			}
		}
	})

	t.Run("SessionEnd deleting the frame clears it", func(t *testing.T) {
		m := delegationModuleWithRealCCProvider(t)
		sendBody(t, m, hookBgSessionStart("startup"))
		sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("monitor")}, 0))
		sendBody(t, m, hookBgBody("PdxSessionEnd", map[string]any{"session_id": "ses_bg", "reason": "other"}))
		if rows, _ := m.frames.ListByPane("%5"); len(rows) != 0 {
			t.Fatalf("precondition: frame still there after SessionEnd: %+v", rows)
		}
		if n := hookBackgroundEntries(m); n != 0 {
			t.Fatalf("%d entries left after the frame was deleted", n)
		}
	})

	t.Run("the sweep deleting the frame clears it", func(t *testing.T) {
		m := delegationModuleWithRealCCProvider(t)
		sendBody(t, m, hookBgSessionStart("startup"))
		sendBody(t, m, hookBgStop([]map[string]any{hookBgTask("monitor")}, 0))
		frame := findCCFrameRow(t, m)
		if err := m.clearFrame(frame, "pid_dead"); err != nil {
			t.Fatalf("clearFrame: %v", err)
		}
		if n := hookBackgroundEntries(m); n != 0 {
			t.Fatalf("%d entries left after the sweep deleted the frame", n)
		}
	})

	t.Run("only a root frame's Stop counts", func(t *testing.T) {
		m := delegationModuleWithRealCCProvider(t)
		sendBody(t, m, hookBgSessionStart("startup"))
		root := findCCFrameRow(t, m)
		child := seedChildFrame(t, m, "%5", "cc", 300, "t300", root.FrameID)
		req := EventRequest{TmuxPaneID: "%5", SenderPID: child.PID, SenderStartTime: child.ProcessStartTime, AgentType: "cc",
			RawEvent: json.RawMessage(`{"background_tasks":[{"id":"t","type":"monitor","status":"running"}]}`)}
		m.noteHookBackground(req, agentpkg.LifecycleStop, time.Now().UnixNano())
		if n := hookBackgroundEntries(m); n != 0 {
			t.Fatalf("a child frame's Stop recorded %d entries", n)
		}
	})
}

// Two hooks of one process can be applied out of order: a Stop that was
// stamped before a SessionStart may reach the symbol after the SessionStart
// cleared it. The late Stop belongs to the old conversation, so it must not
// write its symbol into the new one (U1-2a-4 F3); a Stop stamped after the
// SessionStart still counts.
func TestStop_BackgroundLateStopDoesNotRevive(t *testing.T) {
	m := delegationModuleWithRealCCProvider(t)
	sendBody(t, m, hookBgSessionStart("startup"))
	frameID := findCCFrameRow(t, m).FrameID
	stopReq := func(task string) EventRequest {
		return EventRequest{TmuxPaneID: "%5", SenderPID: 200, SenderStartTime: hookBgStart, AgentType: "cc",
			RawEvent: json.RawMessage(`{"background_tasks":[{"id":"t","type":"` + task + `","status":"running"}]}`)}
	}

	// The Stop is stamped, then its applyFrameEvent finishes and it pauses;
	// the SessionStart (stamped later) completes first and clears.
	oldStopTs := time.Now().UnixNano()
	sendBody(t, m, hookBgSessionStart("clear"))
	m.noteHookBackground(stopReq("monitor"), agentpkg.LifecycleStop, oldStopTs)
	if n := hookBackgroundEntries(m); n != 0 {
		t.Fatalf("a Stop stamped before the SessionStart wrote %d entries into the new session", n)
	}

	// The normal order still sets it, and the clear does not stick.
	m.noteHookBackground(stopReq("monitor"), agentpkg.LifecycleStop, time.Now().UnixNano())
	m.modMu.Lock()
	got := m.hookBackground[frameID]
	m.modMu.Unlock()
	if got != lights.BackgroundMonitor {
		t.Fatalf("a Stop stamped after the SessionStart: symbol %q, want monitor", got)
	}
}

// An older CC sends neither field, and a payload may be anything: no symbol,
// never a panic.
func TestHookBackgroundOf_MissingOrMalformed(t *testing.T) {
	for _, raw := range []string{``, `{}`, `null`, `[]`, `not json`, `{"background_tasks":"x"}`, `{"background_tasks":[{"type":5}]}`} {
		if got := hookBackgroundOf(json.RawMessage(raw)); got != "" {
			t.Fatalf("hookBackgroundOf(%q) = %q, want none", raw, got)
		}
	}
}

// The hook symbol is a fallback: a pane a live mod stream drives shows the
// stream's background, even an empty one, and it comes back the moment the
// stream is not live.
func TestStop_BackgroundYieldsToLiveMod(t *testing.T) {
	m, clk := overlayModule(t)
	frame := seedIdentityFrame(t, m, "%5", "cc", 200, hookBgStart, 10, modSID1, "/w")
	m.core = &core.Core{Events: core.NewEventsBroadcaster()}
	m.setHookBackground(frame.FrameID, lights.BackgroundMonitor, time.Now().UnixNano())

	if got := paneProjection(t, m, "%5"); got.Background != string(lights.BackgroundMonitor) || got.Source != SourceHook {
		t.Fatalf("no stream: background %q source %q, want the hook symbol from the hook", got.Background, got.Source)
	}

	feedMod(m, modStrm, modStart, modTurnStart)
	if got := paneProjection(t, m, "%5"); got.Background != "" || got.Source != SourceMod {
		t.Fatalf("live stream: background %q source %q, want the mod's (empty) from the mod", got.Background, got.Source)
	}

	clk.Set(modT0.Add(time.Minute)) // past the live window: the stream went quiet
	if got := paneProjection(t, m, "%5"); got.Background != string(lights.BackgroundMonitor) || got.Source != SourceHook {
		t.Fatalf("stale stream: background %q source %q, want the hook symbol back", got.Background, got.Source)
	}
}
