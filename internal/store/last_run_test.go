package store

import (
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// NA-0a (no-agent launch panel spec §2.1): a root frame's end writes the session's last run in the same transaction
// that claim-deletes it; the frame carries its own tmux generation and a root flag fixed at creation.

func seedRoot(t *testing.T, s *FramesStore, pane string, pid int, sessionID, cwd string) Frame {
	t.Helper()
	f, err := s.Upsert(Frame{
		PaneID: pane, AgentType: "cc", PID: pid, PPID: 1, ProcessStartTime: "T", Status: agentpkg.StatusIdle,
		StartedAt: 10, LastSeenAt: 10, Verified: true, SessionID: sessionID, Cwd: cwd,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return f
}

func seedChild(t *testing.T, s *FramesStore, parent Frame, pid int, sessionID string) Frame {
	t.Helper()
	f, err := s.Upsert(Frame{
		PaneID: parent.PaneID, AgentType: "codex", PID: pid, PPID: parent.PID, ProcessStartTime: "T", Status: agentpkg.StatusIdle,
		StartedAt: 11, LastSeenAt: 11, Verified: true, ParentFrameID: parent.FrameID, SessionID: sessionID, Cwd: "/child",
	})
	if err != nil {
		t.Fatalf("Upsert child: %v", err)
	}
	return f
}

func mustLastRun(t *testing.T, s *FramesStore, code string) *LastRun {
	t.Helper()
	got, err := s.LastRun(code)
	if err != nil {
		t.Fatalf("LastRun: %v", err)
	}
	return got
}

func TestClaimEnd_RootWritesTheLastRunWithTheFramesOwnGeneration(t *testing.T) {
	s := openTestFramesStore(t)
	f := seedRoot(t, s, "%5", 200, "S1", "/w/proj")
	if err := s.SetTmuxInstance(f.FrameID, "111:1700"); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 99})
	if err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	got := mustLastRun(t, s, "abc123")
	want := LastRun{Code: "abc123", TmuxInstance: "111:1700", AgentType: "cc", SessionID: "S1", Cwd: "/w/proj", EndedAt: 99, PaneID: "%5"}
	if got == nil || *got != want {
		t.Fatalf("last run = %+v, want %+v", got, want)
	}
	if again, err := s.GetByIdentity("%5", 200, "T"); err != nil || again != nil {
		t.Fatalf("the frame survived the claim: %+v %v", again, err)
	}
}

func TestClaimEnd_OverwritesTheSessionsPreviousRun(t *testing.T) {
	s := openTestFramesStore(t)
	for i, sid := range []string{"S1", "S2"} {
		f := seedRoot(t, s, "%5", 200+i, sid, "/w/"+sid)
		if err := s.SetTmuxInstance(f.FrameID, "111:1700"); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.ClaimEnd(f.FrameID, sid, &RunEnd{Code: "abc123", EndedAt: int64(10 + i)}); err != nil || !ok {
			t.Fatalf("claim %d: %v %v", i, ok, err)
		}
	}
	if got := mustLastRun(t, s, "abc123"); got == nil || got.SessionID != "S2" || got.Cwd != "/w/S2" {
		t.Fatalf("one row per code, the later run wins: %+v", got)
	}
}

func TestClaimEnd_NothingIsWrittenWhenTheClaimIsNotWon(t *testing.T) {
	t.Run("another run owns the row", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := seedRoot(t, s, "%5", 200, "S-new", "/w")
		_ = s.SetTmuxInstance(f.FrameID, "111:1700")
		if ok, err := s.ClaimEnd(f.FrameID, "S-old", &RunEnd{Code: "abc123", EndedAt: 1}); err != nil || ok {
			t.Fatalf("claimed=%v err=%v, want false", ok, err)
		}
		if mustLastRun(t, s, "abc123") != nil {
			t.Fatal("a lost claim wrote a last run")
		}
		if got, _ := s.GetByIdentity("%5", 200, "T"); got == nil {
			t.Fatal("the newer run's frame was deleted")
		}
	})
	t.Run("a row with no recorded session id is not claimed by a session id", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := seedRoot(t, s, "%5", 200, "", "/w")
		_ = s.SetTmuxInstance(f.FrameID, "111:1700")
		if ok, _ := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 1}); ok {
			t.Fatal("claimed")
		}
		if mustLastRun(t, s, "abc123") != nil {
			t.Fatal("wrote")
		}
	})
	t.Run("the second of two ends finds the frame gone", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := seedRoot(t, s, "%5", 200, "S1", "/w")
		_ = s.SetTmuxInstance(f.FrameID, "111:1700")
		if ok, _ := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 1}); !ok {
			t.Fatal("first lost")
		}
		if ok, err := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 2}); ok || err != nil {
			t.Fatalf("second: %v %v", ok, err)
		}
		if got := mustLastRun(t, s, "abc123"); got == nil || got.EndedAt != 1 {
			t.Fatalf("the winner's write stands: %+v", got)
		}
	})
}

func TestClaimEnd_ConcurrentEndsOneWinsOneWrite(t *testing.T) {
	s := openTestFramesStore(t)
	f := seedRoot(t, s, "%5", 200, "S1", "/w")
	_ = s.SetTmuxInstance(f.FrameID, "111:1700")
	var wg sync.WaitGroup
	wins := make(chan int64, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(at int64) {
			defer wg.Done()
			if ok, err := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: at}); err == nil && ok {
				wins <- at
			}
		}(int64(i + 1))
	}
	wg.Wait()
	close(wins)
	var winners []int64
	for w := range wins {
		winners = append(winners, w)
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %v, want exactly one", winners)
	}
	if got := mustLastRun(t, s, "abc123"); got == nil || got.EndedAt != winners[0] {
		t.Fatalf("the written run is not the winner's: %+v vs %v", got, winners)
	}
}

func TestClaimEnd_ChildNeverWritesALastRun(t *testing.T) {
	s := openTestFramesStore(t)
	parent := seedRoot(t, s, "%5", 200, "S1", "/w")
	_ = s.SetTmuxInstance(parent.FrameID, "111:1700")
	child := seedChild(t, s, parent, 300, "C1")
	_ = s.SetTmuxInstance(child.FrameID, "111:1700") // even a stamped child is not a root
	if ok, err := s.ClaimEnd(child.FrameID, "C1", &RunEnd{Code: "abc123", EndedAt: 5}); err != nil || !ok {
		t.Fatalf("child claim: %v %v", ok, err)
	}
	if mustLastRun(t, s, "abc123") != nil {
		t.Fatal("a child frame wrote the session's last run")
	}
}

// Parent ends first: the child's parent_frame_id is cleared by ON DELETE SET NULL, and it then LOOKS like a root.
// is_root was fixed at creation, so its own end still writes nothing.
func TestClaimEnd_ParentEndsFirstChildStillNotARoot(t *testing.T) {
	s := openTestFramesStore(t)
	parent := seedRoot(t, s, "%5", 200, "S1", "/w")
	_ = s.SetTmuxInstance(parent.FrameID, "111:1700")
	child := seedChild(t, s, parent, 300, "C1")
	_ = s.SetTmuxInstance(child.FrameID, "111:1700")
	if ok, err := s.ClaimEnd(parent.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 5}); err != nil || !ok {
		t.Fatalf("parent claim: %v %v", ok, err)
	}
	if got, _ := s.GetByIdentity("%5", 300, "T"); got == nil || got.ParentFrameID != "" {
		t.Fatalf("setup: the child should survive with its parent link cleared: %+v", got)
	}
	if ok, err := s.ClaimEnd(child.FrameID, "C1", &RunEnd{Code: "abc123", EndedAt: 9}); err != nil || !ok {
		t.Fatalf("child claim: %v %v", ok, err)
	}
	if got := mustLastRun(t, s, "abc123"); got == nil || got.SessionID != "S1" || got.EndedAt != 5 {
		t.Fatalf("the child overwrote the root's run: %+v", got)
	}
}

func TestClaimEnd_EmptyGenerationOrCodeWritesNothingButStillDeletes(t *testing.T) {
	t.Run("the frame has no generation", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := seedRoot(t, s, "%5", 200, "S1", "/w") // never stamped
		if ok, err := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 1}); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		if mustLastRun(t, s, "abc123") != nil {
			t.Fatal("an unstamped frame wrote a record that could never be matched")
		}
	})
	t.Run("no session code", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := seedRoot(t, s, "%5", 200, "S1", "/w")
		_ = s.SetTmuxInstance(f.FrameID, "111:1700")
		if ok, err := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "", EndedAt: 1}); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		if mustLastRun(t, s, "") != nil {
			t.Fatal("wrote under an empty code")
		}
	})
	t.Run("the sweep's claim records no run", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := seedRoot(t, s, "%5", 200, "S1", "/w")
		_ = s.SetTmuxInstance(f.FrameID, "111:1700")
		if ok, err := s.ClaimEnd(f.FrameID, "", nil); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
	})
}

// The frame's own generation is written, never one sampled when the SessionEnd arrives: a SessionEnd of the old tmux
// generation that arrives after a restart must leave the new generation without a last run.
func TestClaimEnd_WritesTheFramesOldGenerationAfterARestart(t *testing.T) {
	s := openTestFramesStore(t)
	f := seedRoot(t, s, "%5", 200, "S1", "/w")
	_ = s.SetTmuxInstance(f.FrameID, "111:1700") // the old generation
	if ok, err := s.ClaimEnd(f.FrameID, "S1", &RunEnd{Code: "abc123", EndedAt: 5}); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	got := mustLastRun(t, s, "abc123")
	if got == nil || got.TmuxInstance != "111:1700" {
		t.Fatalf("last run = %+v", got)
	}
	// The plan for the new generation ("222:1800") compares and finds no record to lend.
	if got.TmuxInstance == "222:1800" {
		t.Fatal("setup")
	}
}

func TestSetTmuxInstance_TakeoverReplacesAndMissingFrameErrs(t *testing.T) {
	s := openTestFramesStore(t)
	f := seedRoot(t, s, "%5", 200, "S1", "/w")
	for _, inst := range []string{"111:1", "222:2", ""} {
		if err := s.SetTmuxInstance(f.FrameID, inst); err != nil {
			t.Fatal(err)
		}
		got, isRoot, found, err := s.RootInfo(f.FrameID)
		if err != nil || !found || !isRoot || got != inst {
			t.Fatalf("RootInfo after %q = %q root=%v found=%v err=%v", inst, got, isRoot, found, err)
		}
	}
	if err := s.SetTmuxInstance("nope", "x"); err == nil {
		t.Fatal("a missing frame should report ErrNoRows")
	}
}

func TestUpsert_FixesIsRootAtCreationAndNeverChangesIt(t *testing.T) {
	s := openTestFramesStore(t)
	parent := seedRoot(t, s, "%5", 200, "S1", "/w")
	child := seedChild(t, s, parent, 300, "C1")
	if _, isRoot, found, _ := s.RootInfo(parent.FrameID); !found || !isRoot {
		t.Fatal("a frame created without a parent is a root")
	}
	if _, isRoot, found, _ := s.RootInfo(child.FrameID); !found || isRoot {
		t.Fatal("a frame created with a parent is not")
	}
	// Re-upserting the same process (a SessionStart on the frame, now without the link) keeps the flag.
	if _, err := s.Upsert(Frame{PaneID: "%5", AgentType: "codex", PID: 300, PPID: 200, ProcessStartTime: "T", Status: agentpkg.StatusIdle, StartedAt: 11, LastSeenAt: 12, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if _, isRoot, _, _ := s.RootInfo(child.FrameID); isRoot {
		t.Fatal("an upsert onto an existing child turned it into a root")
	}
}

// Migration on a deployed DB: rows written before the columns existed get is_root from their parent link at that time,
// an empty generation, and the table is added; running it again changes nothing.
func TestMigrate_AddsRootColumnsAndLastRunTableToADeployedDB(t *testing.T) {
	events := openTestAgentEventStore(t)
	seedPreIdentitySchema(t, events.db)
	if _, err := events.db.Exec(`INSERT INTO agent_frames (frame_id, pane_id, agent_type, pid, ppid, process_start_time, parent_frame_id, status, started_at, last_seen_at)
		VALUES ('kid', '%1', 'codex', 7, 1, 'X', 'old-frame', 'idle', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	frames, err := events.Frames()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if inst, isRoot, found, _ := frames.RootInfo("old-frame"); !found || !isRoot || inst != "" {
		t.Fatalf("old root: inst=%q root=%v found=%v", inst, isRoot, found)
	}
	if _, isRoot, found, _ := frames.RootInfo("kid"); !found || isRoot {
		t.Fatalf("old child: root=%v found=%v", isRoot, found)
	}
	if got, err := frames.LastRun("anything"); err != nil || got != nil {
		t.Fatalf("empty table: %+v %v", got, err)
	}
	// The backfill ran once: a frame created after the migration is not re-derived by the second run.
	f := seedRoot(t, frames, "%9", 400, "S9", "/w")
	if _, err := events.db.Exec(`UPDATE agent_frames SET parent_frame_id = NULL WHERE frame_id = 'kid'`); err != nil {
		t.Fatal(err)
	}
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatal(err)
	}
	if _, isRoot, _, _ := frames.RootInfo("kid"); isRoot {
		t.Fatal("a re-run derived is_root from a parent link that was cleared later")
	}
	_ = f
}

// A frame that was created standalone and later learns its parent (a hook path / SessionStart update, or an upsert onto
// the same process) is a child from then on; its own end must not record the session's last run. The reverse never
// happens: a cleared link does not make a root.
func TestIsRoot_ClearsWhenAnExistingFrameAcquiresAParent(t *testing.T) {
	update := map[string]func(*FramesStore, Frame) error{
		"UpdateHookPath":                  func(s *FramesStore, f Frame) error { return s.UpdateHookPath(f) },
		"UpdateHookPathAndResetSubagents": func(s *FramesStore, f Frame) error { return s.UpdateHookPathAndResetSubagents(f) },
		"UpsertIfUnchanged":               func(s *FramesStore, f Frame) error { _, _, err := s.UpsertIfUnchanged(f, f.LastSeenAt); return err },
		"Upsert (conflict)":               func(s *FramesStore, f Frame) error { _, err := s.Upsert(f); return err },
	}
	for name, apply := range update {
		t.Run(name, func(t *testing.T) {
			s := openTestFramesStore(t)
			parent := seedRoot(t, s, "%5", 200, "S1", "/w")
			standalone := seedRoot(t, s, "%5", 300, "C1", "/w")
			if err := s.SetTmuxInstance(standalone.FrameID, "111:1700"); err != nil {
				t.Fatal(err)
			}
			if _, isRoot, _, _ := s.RootInfo(standalone.FrameID); !isRoot {
				t.Fatal("setup: created without a parent")
			}
			standalone.ParentFrameID = parent.FrameID
			if err := apply(s, standalone); err != nil {
				t.Fatal(err)
			}
			if _, isRoot, _, _ := s.RootInfo(standalone.FrameID); isRoot {
				t.Fatal("a frame that acquired a parent is still a root")
			}
			if ok, err := s.ClaimEnd(standalone.FrameID, "C1", &RunEnd{Code: "abc123", EndedAt: 5}); err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			if mustLastRun(t, s, "abc123") != nil {
				t.Fatal("a reparented child recorded the session's last run")
			}
		})
	}
	t.Run("a cleared link does not make a root", func(t *testing.T) {
		s := openTestFramesStore(t)
		parent := seedRoot(t, s, "%5", 200, "S1", "/w")
		child := seedChild(t, s, parent, 300, "C1")
		child.ParentFrameID = ""
		if err := s.UpdateHookPath(child); err != nil {
			t.Fatal(err)
		}
		if _, isRoot, _, _ := s.RootInfo(child.FrameID); isRoot {
			t.Fatal("clearing the link made a child a root")
		}
	})
}

// The new generation's run ended first; the OLD generation's SessionEnd arrives only afterwards. It must not replace the
// newer record (the record that is actually resumable). The same generation: the later end still wins.
func TestClaimEnd_OldGenerationNeverReplacesANewerGenerationsRecord(t *testing.T) {
	end := func(t *testing.T, s *FramesStore, pid int, sid, instance string, at int64) {
		t.Helper()
		f := seedRoot(t, s, "%5", pid, sid, "/w/"+sid)
		if err := s.SetTmuxInstance(f.FrameID, instance); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.ClaimEnd(f.FrameID, sid, &RunEnd{Code: "abc123", EndedAt: at}); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
	}
	t.Run("new generation first, old one late", func(t *testing.T) {
		s := openTestFramesStore(t)
		end(t, s, 200, "S-new", "222:1800", 10)
		end(t, s, 201, "S-old", "111:1700", 20) // later in time, older server
		if got := mustLastRun(t, s, "abc123"); got == nil || got.SessionID != "S-new" || got.TmuxInstance != "222:1800" {
			t.Fatalf("last run = %+v, want the newer generation's", got)
		}
	})
	t.Run("old generation first, new one replaces it", func(t *testing.T) {
		s := openTestFramesStore(t)
		end(t, s, 200, "S-old", "111:1700", 10)
		end(t, s, 201, "S-new", "222:1800", 20)
		if got := mustLastRun(t, s, "abc123"); got == nil || got.SessionID != "S-new" {
			t.Fatalf("last run = %+v", got)
		}
	})
	t.Run("same generation, the later end wins", func(t *testing.T) {
		s := openTestFramesStore(t)
		end(t, s, 200, "S1", "222:1800", 10)
		end(t, s, 201, "S2", "222:1800", 20)
		if got := mustLastRun(t, s, "abc123"); got == nil || got.SessionID != "S2" {
			t.Fatalf("last run = %+v", got)
		}
	})
}
