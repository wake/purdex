package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// readSweepNormalizedEvent waits for the next broadcast on sub, decodes the
// HostEvent envelope, asserts the envelope Type, then unmarshals
// envelope.Value into a NormalizedEvent. Fails the test on timeout, decode
// error, or wrong envelope Type.
func readSweepNormalizedEvent(t *testing.T, sub *core.EventSubscriber) agentpkg.NormalizedEvent {
	t.Helper()
	select {
	case raw := <-sub.SendCh():
		var env core.HostEvent
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v (raw=%s)", err, raw)
		}
		if env.Type != "hook" {
			t.Fatalf("envelope.Type = %q, want %q", env.Type, "hook")
		}
		var ev agentpkg.NormalizedEvent
		if err := json.Unmarshal([]byte(env.Value), &ev); err != nil {
			t.Fatalf("unmarshal NormalizedEvent: %v (value=%s)", err, env.Value)
		}
		return ev
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast")
		return agentpkg.NormalizedEvent{}
	}
}

// Issue #717 — when sweep clears the only frame in a session, the
// projection returned by projectionForSession is nil. Before the fix,
// buildProjectionNormalized's `projection == nil` branch passed through
// the empty Status from sweep's DeriveResult{}, so the WS broadcast
// carried `status=""` and the SPA's handleNormalizedEvent (which keys
// the clear path on `status === 'clear'`) left the agent indicator
// stuck. Asserts the broadcast NormalizedEvent has Status=clear.
func TestSweep_PidDeadBroadcastsStatusClearWhenSessionEmpty(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "opencode",
		PID:              99999,
		PPID:             1,
		ProcessStartTime: "dead",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	isPidAliveFn = func(int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0 (sanity: sweep should have deleted the dead frame)", len(frames))
	}

	ev := readSweepNormalizedEvent(t, sub)
	if ev.RawEventName != "sweep:pid_dead" {
		t.Fatalf("RawEventName = %q, want %q", ev.RawEventName, "sweep:pid_dead")
	}
	if ev.Status != string(agentpkg.StatusClear) {
		t.Fatalf("Status = %q, want %q (issue #717: sweep must broadcast status=clear when session empties)", ev.Status, agentpkg.StatusClear)
	}
	if ev.AgentType != "opencode" {
		t.Fatalf("AgentType = %q, want %q (frame.AgentType fallback)", ev.AgentType, "opencode")
	}
}

// Issue #717 regression guard — when sweep clears one of several frames
// in the same tmux session (different panes), the broadcast must carry
// the surviving TopFrame's Status, NOT 'clear'. Drives the third
// branch in buildProjectionNormalized (projection != nil && TopFrame
// != nil), which already overrides AgentType + Status, so this test
// passes both before and after the fix and proves the Option A change
// does not regress multi-pane sessions. Also doubles as the
// race-recovery contract for the round-2 re-resolve fix in
// afterFrameCleared: if a hook handler has created a new frame for
// this session between projectionForSession and Broadcast, the fresh
// projection picks it up and the broadcast carries that frame's
// status instead of a stale clear.
func TestSweep_PidDeadBroadcastsSiblingStatusWhenSessionNonEmpty(t *testing.T) {
	m := newSweepTestModule(t)
	if fakeTmux, ok := m.tmux.(*tmux.FakeExecutor); ok {
		fakeTmux.SetPaneSessionName("%6", "work")
	} else {
		t.Fatalf("expected FakeExecutor, got %T", m.tmux)
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	// Dead frame on %5 — sweep will clear this one.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "opencode",
		PID:              99999,
		PPID:             1,
		ProcessStartTime: "dead",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert dead frame: %v", err)
	}
	// Live sibling on %6 (same tmux session "work") — must remain the
	// projection's TopFrame and dictate the broadcast Status.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%6",
		AgentType:        "codex",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert live sibling: %v", err)
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "live", nil
		}
		return "", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	survivors, err := m.frames.ListByPane("%6")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(survivors) != 1 || survivors[0].AgentType != "codex" {
		t.Fatalf("survivors = %+v, want one codex frame", survivors)
	}

	ev := readSweepNormalizedEvent(t, sub)
	if ev.RawEventName != "sweep:pid_dead" {
		t.Fatalf("RawEventName = %q, want %q", ev.RawEventName, "sweep:pid_dead")
	}
	if ev.Status != string(agentpkg.StatusRunning) {
		t.Fatalf("Status = %q, want %q (must reflect surviving sibling, not falsely clear)", ev.Status, agentpkg.StatusRunning)
	}
	if ev.AgentType != "codex" {
		t.Fatalf("AgentType = %q, want %q (must reflect surviving TopFrame)", ev.AgentType, "codex")
	}
}
