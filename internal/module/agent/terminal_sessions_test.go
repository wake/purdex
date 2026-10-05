package agent

import (
	"context"
	"errors"
	"testing"
)

func TestLiveBySessionID(t *testing.T) {
	m := newTestModule(t)
	live := seedRootWithIdentity(t, m, "%1", "cc", 101, "st-101", "S")
	dead := seedRootWithIdentity(t, m, "%2", "cc", 102, "st-102", "S")
	reused := seedRootWithIdentity(t, m, "%3", "cc", 103, "st-103", "S")
	unreadable := seedRootWithIdentity(t, m, "%4", "cc", 104, "st-104", "S")
	_ = seedRootWithIdentity(t, m, "%5", "codex", 105, "st-105", "S")
	_ = seedRootWithIdentity(t, m, "%6", "cc", 106, "st-106", "OTHER")
	child := seedChildFrame(t, m, "%1", "cc", 107, "st-107", live.FrameID)
	if err := m.frames.UpdateSessionIdentity(child.FrameID, "S", "", 1<<40); err != nil {
		t.Fatal(err)
	}

	withLivePids(t, map[int]string{101: "st-101", 103: "st-OTHER", 104: "st-104", 105: "st-105", 106: "st-106", 107: "st-107"})
	// 104: alive but its start time cannot be read.
	prev := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 104 {
			return "", errors.New("ps failed")
		}
		return prev(pid)
	}
	t.Cleanup(func() { processStartTimeFn = prev })

	got, err := m.LiveBySessionID(context.Background(), "cc", "S")
	if err != nil {
		t.Fatal(err)
	}
	byFrame := map[string]TerminalSession{}
	for _, g := range got {
		byFrame[g.FrameID] = g
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 (live verified + unreadable): %+v", len(got), got)
	}
	if g := byFrame[live.FrameID]; !g.Verified || g.SessionID != "S" || g.PaneID != "%1" || g.AgentType != "cc" || g.Cwd != "/w/p" {
		t.Errorf("live frame wrong: %+v", g)
	}
	if g, ok := byFrame[unreadable.FrameID]; !ok || g.Verified {
		t.Errorf("unreadable start time: want present and unverified, got %+v ok=%v", g, ok)
	}
	for _, f := range []string{dead.FrameID, reused.FrameID, child.FrameID} {
		if _, ok := byFrame[f]; ok {
			t.Errorf("frame %s must not be returned", f)
		}
	}
}

func TestLiveBySessionID_EmptySessionIDReturnsNothing(t *testing.T) {
	m := newTestModule(t)
	got, err := m.LiveBySessionID(context.Background(), "cc", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}
