package agent

import (
	"testing"

	"github.com/wake/purdex/internal/store"
)

// NA-0a (no-agent launch panel spec §2.1, §2.4): a ROOT SessionStart stamps the frame with the tmux generation it ran
// under; the SessionEnd that wins the claim writes the session's last run from the frame — with the frame's own
// generation — and never for a child. The store tests carry the transaction's rules; these carry the wiring.

func sessionStartReq(pid int, startTime, sessionID string) EventRequest {
	return EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5", AgentType: "cc", SenderPID: pid, SenderStartTime: startTime,
		PurdexName: "PdxSessionStart", RawEvent: []byte(`{"session_id":"` + sessionID + `","cwd":"/w/proj"}`),
	}
}

func sessionEndReq(pid int, startTime, sessionID string) EventRequest {
	return EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5", AgentType: "cc", SenderPID: pid, SenderStartTime: startTime,
		PurdexName: "PdxSessionEnd", RawEvent: []byte(`{"session_id":"` + sessionID + `"}`),
	}
}

// startRoot runs a root SessionStart through the module and records the identity the real provider would have (the
// fake providers carry no SessionIdentifier).
func startRoot(t *testing.T, m *Module) store.Frame {
	t.Helper()
	m.buildNormalizedForTest(t, sessionStartReq(200, "t200", "S1"))
	f, err := m.frames.GetByIdentity("%5", 200, "t200")
	if err != nil || f == nil {
		t.Fatalf("frame: %v", err)
	}
	if err := m.frames.UpdateSessionIdentity(f.FrameID, "S1", "/w/proj", 1<<40); err != nil {
		t.Fatal(err)
	}
	return *f
}

func TestLastRun_RootSessionStartStampsTheGeneration_SessionEndWritesIt(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	f := startRoot(t, m)
	inst, isRoot, found, err := m.frames.RootInfo(f.FrameID)
	if err != nil || !found || !isRoot || inst != exitTestInstance {
		t.Fatalf("after SessionStart: instance=%q root=%v found=%v err=%v", inst, isRoot, found, err)
	}

	m.buildNormalizedForTest(t, sessionEndReq(200, "t200", "S1"))
	got, err := m.frames.LastRun("work-code")
	if err != nil || got == nil {
		t.Fatalf("LastRun: %+v %v", got, err)
	}
	if got.TmuxInstance != exitTestInstance || got.AgentType != "cc" || got.SessionID != "S1" || got.Cwd != "/w/proj" || got.EndedAt == 0 {
		t.Fatalf("last run = %+v", got)
	}
}

// A SessionEnd of the OLD tmux generation that arrives after a restart writes the frame's own (old) generation, not the
// one tmux has now: the new generation's plan compares and finds no record.
func TestLastRun_LateSessionEndAfterATmuxRestart_WritesTheFramesOldGeneration(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	startRoot(t, m)
	m.sessions = fakeProviderWithInstance("9999:1799999999") // tmux restarted; the old run's SessionEnd arrives only now
	m.buildNormalizedForTest(t, sessionEndReq(200, "t200", "S1"))
	got, _ := m.frames.LastRun("work-code")
	if got == nil || got.TmuxInstance != exitTestInstance {
		t.Fatalf("last run = %+v, want the frame's generation %q", got, exitTestInstance)
	}
}

func TestLastRun_ChildSessionEndWritesNothing(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	parent := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")
	if err := m.frames.SetTmuxInstance(parent.FrameID, exitTestInstance); err != nil {
		t.Fatal(err)
	}
	child := seedChildFrame(t, m, "%5", "cc", 300, "t300", parent.FrameID)
	if err := m.frames.SetTmuxInstance(child.FrameID, exitTestInstance); err != nil {
		t.Fatal(err)
	}
	m.buildNormalizedForTest(t, sessionEndReq(300, "t300", "child-session"))
	if got, _ := m.frames.LastRun("work-code"); got != nil {
		t.Fatalf("a child's end wrote %+v", got)
	}
}

// Parent first, child later: the child's link was cleared by ON DELETE SET NULL but it was never a root.
func TestLastRun_ParentEndsFirst_ChildEndNeverOverwrites(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	parent := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")
	if err := m.frames.SetTmuxInstance(parent.FrameID, exitTestInstance); err != nil {
		t.Fatal(err)
	}
	child := seedChildFrame(t, m, "%5", "cc", 300, "t300", parent.FrameID)
	if err := m.frames.SetTmuxInstance(child.FrameID, exitTestInstance); err != nil {
		t.Fatal(err)
	}
	m.buildNormalizedForTest(t, sessionEndReq(200, "t200", "S1"))
	m.buildNormalizedForTest(t, sessionEndReq(300, "t300", "child-session"))
	got, _ := m.frames.LastRun("work-code")
	if got == nil || got.SessionID != "S1" {
		t.Fatalf("last run = %+v, want the root's S1", got)
	}
}

func TestLastRun_FrameWithoutAGenerationWritesNothing(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1") // created before this feature: never stamped
	m.buildNormalizedForTest(t, sessionEndReq(200, "t200", "S1"))
	if got, _ := m.frames.LastRun("work-code"); got != nil {
		t.Fatalf("wrote %+v for a frame with no generation", got)
	}
}

// The sweep ends a dead process without a SessionEnd: it records no run (only a SessionEnd names the run that ended).
func TestLastRun_SweepClaimRecordsNothing(t *testing.T) {
	m := newProvenanceTestModule(t, exitTestInstance)
	f := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")
	if err := m.frames.SetTmuxInstance(f.FrameID, exitTestInstance); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := m.claimFrameEnd(f, "", nil, nil); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if got, _ := m.frames.LastRun("work-code"); got != nil {
		t.Fatalf("the sweep wrote %+v", got)
	}
}
