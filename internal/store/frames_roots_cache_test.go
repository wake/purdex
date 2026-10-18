package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

func rootsFrame(t *testing.T, s *FramesStore, pane string, pid int, sid string) Frame {
	t.Helper()
	f, err := s.Upsert(Frame{
		PaneID: pane, AgentType: "cc", PID: pid, PPID: 1, ProcessStartTime: "t" + strconv.Itoa(pid),
		Status: agentpkg.StatusIdle, StartedAt: 10, LastSeenAt: 10, Verified: true, SessionID: sid,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func cachedRoots(t *testing.T, s *FramesStore, sid string) []Frame {
	t.Helper()
	got, err := s.ListRootsBySessionIDCached(sid)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The copy is reused until the TTL, and a write that bypassed the store (so no touch) is found no later than the TTL.
func TestRootsCache_ServesACopyUntilTheTTL(t *testing.T) {
	s := openTestFramesStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	framesNow = func() time.Time { return now }
	t.Cleanup(func() { framesNow = time.Now })
	f := rootsFrame(t, s, "%1", 100, "S1")
	if got := cachedRoots(t, s, "S1"); len(got) != 1 {
		t.Fatalf("roots = %d", len(got))
	}
	if _, err := s.db.Exec(`UPDATE agent_frames SET status = 'running' WHERE frame_id = ?`, f.FrameID); err != nil { // no touch
		t.Fatal(err)
	}
	if got := cachedRoots(t, s, "S1"); got[0].Status != agentpkg.StatusIdle {
		t.Fatalf("within the TTL the copy should answer, got %q", got[0].Status)
	}
	now = now.Add(rootsCacheTTL)
	if got := cachedRoots(t, s, "S1"); got[0].Status != "running" {
		t.Fatalf("at the TTL the table should be read again, got %q", got[0].Status)
	}
	if _, err := s.db.Exec(`UPDATE agent_frames SET status = 'done' WHERE frame_id = ?`, f.FrameID); err != nil { // no touch
		t.Fatal(err)
	}
	now = now.Add(-time.Hour) // a clock that stepped back is not a fresh copy
	if got := cachedRoots(t, s, "S1"); got[0].Status != "done" {
		t.Fatalf("a copy from the future was served: %q", got[0].Status)
	}
}

// Every method that writes agent_frames shows its write to the very next cached read, with the clock standing still.
func TestRootsCache_EveryWritePathShowsAtOnce(t *testing.T) {
	type op struct {
		name  string
		do    func(t *testing.T, s *FramesStore, f Frame)
		check func(t *testing.T, s *FramesStore, f Frame)
	}
	gone := func(t *testing.T, s *FramesStore, f Frame) {
		if got := cachedRoots(t, s, "S1"); len(got) != 0 {
			t.Fatalf("the frame should be gone, roots = %d", len(got))
		}
	}
	status := func(want agentpkg.Status) func(*testing.T, *FramesStore, Frame) {
		return func(t *testing.T, s *FramesStore, f Frame) {
			if got := cachedRoots(t, s, "S1"); len(got) != 1 || got[0].Status != want {
				t.Fatalf("roots = %+v, want one frame with status %q", got, want)
			}
		}
	}
	ops := []op{
		{"Upsert", func(t *testing.T, s *FramesStore, f Frame) { rootsFrame(t, s, "%2", 200, "S1") },
			func(t *testing.T, s *FramesStore, f Frame) {
				if got := cachedRoots(t, s, "S1"); len(got) != 2 {
					t.Fatalf("roots = %d, want 2", len(got))
				}
			}},
		{"UpdateStatusAndLastSeen", func(t *testing.T, s *FramesStore, f Frame) {
			if err := s.UpdateStatusAndLastSeen(f.FrameID, agentpkg.StatusRunning, 11); err != nil {
				t.Fatal(err)
			}
		}, status(agentpkg.StatusRunning)},
		{"UpdateHookPath", func(t *testing.T, s *FramesStore, f Frame) {
			f.Status = agentpkg.StatusRunning
			if err := s.UpdateHookPath(f); err != nil {
				t.Fatal(err)
			}
		}, status(agentpkg.StatusRunning)},
		{"UpdateHookPathAndResetSubagents", func(t *testing.T, s *FramesStore, f Frame) {
			f.Status = agentpkg.StatusRunning
			if err := s.UpdateHookPathAndResetSubagents(f); err != nil {
				t.Fatal(err)
			}
		}, status(agentpkg.StatusRunning)},
		{"UpsertIfUnchanged", func(t *testing.T, s *FramesStore, f Frame) {
			f.Status = agentpkg.StatusRunning
			if ok, _, err := s.UpsertIfUnchanged(f, f.LastSeenAt); err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		}, status(agentpkg.StatusRunning)},
		{"UpdateSessionIdentity", func(t *testing.T, s *FramesStore, f Frame) {
			if err := s.UpdateSessionIdentity(f.FrameID, "S2", "/w", s.NextIdentitySeq()); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, s *FramesStore, f Frame) {
			if got := cachedRoots(t, s, "S1"); len(got) != 0 {
				t.Fatalf("S1 should have no roots any more, got %d", len(got))
			}
			if got := cachedRoots(t, s, "S2"); len(got) != 1 {
				t.Fatalf("S2 should have the frame, got %d", len(got))
			}
		}},
		{"SetTranscriptPath", func(t *testing.T, s *FramesStore, f Frame) {
			if err := s.SetTranscriptPath(f.FrameID, "/t.jsonl", s.NextIdentitySeq()); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, s *FramesStore, f Frame) {
			if got := cachedRoots(t, s, "S1"); len(got) != 1 || got[0].TranscriptPath != "/t.jsonl" {
				t.Fatalf("roots = %+v", got)
			}
		}},
		{"Delete", func(t *testing.T, s *FramesStore, f Frame) {
			if err := s.Delete(f.FrameID); err != nil {
				t.Fatal(err)
			}
		}, gone},
		{"ClaimDelete", func(t *testing.T, s *FramesStore, f Frame) {
			if ok, err := s.ClaimDelete(f.FrameID, ""); err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		}, gone},
		{"DeleteIfUnchanged", func(t *testing.T, s *FramesStore, f Frame) {
			if ok, err := s.DeleteIfUnchanged(f.FrameID, f.LastSeenAt); err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		}, gone},
		{"ClaimEnd", func(t *testing.T, s *FramesStore, f Frame) {
			if ok, err := s.ClaimEnd(f.FrameID, "", nil); err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
		}, gone},
	}
	for _, o := range ops {
		t.Run(o.name, func(t *testing.T) {
			s := openTestFramesStore(t)
			now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
			framesNow = func() time.Time { return now } // the clock stands still: only the write can end the copy
			t.Cleanup(func() { framesNow = time.Now })
			f := rootsFrame(t, s, "%1", 100, "S1")
			if got := cachedRoots(t, s, "S1"); len(got) != 1 { // warm the copy
				t.Fatalf("roots = %d", len(got))
			}
			o.do(t, s, f)
			o.check(t, s, f)
		})
	}
	t.Run("SetTmuxInstance", func(t *testing.T) {
		s := openTestFramesStore(t)
		f := rootsFrame(t, s, "%1", 100, "S1")
		before := s.gen.Load()
		if err := s.SetTmuxInstance(f.FrameID, "123:456"); err != nil {
			t.Fatal(err)
		}
		if s.gen.Load() == before {
			t.Fatal("a write to agent_frames did not end the cached copies")
		}
	})
}

// A write that lands after the table was read but before the copy is filed must not leave a copy that hides it.
func TestRootsCache_AWriteDuringTheReadIsNotHidden(t *testing.T) {
	s := openTestFramesStore(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	framesNow = func() time.Time { return now }
	t.Cleanup(func() { framesNow = time.Now; rootsAfterQuery = func() {} })
	f := rootsFrame(t, s, "%1", 100, "S1")
	rootsAfterQuery = func() {
		rootsAfterQuery = func() {}
		if err := s.UpdateStatusAndLastSeen(f.FrameID, agentpkg.StatusRunning, 20); err != nil {
			t.Error(err)
		}
	}
	if got := cachedRoots(t, s, "S1"); got[0].Status != agentpkg.StatusIdle { // this read began before the write
		t.Fatalf("first read: %q", got[0].Status)
	}
	if got := cachedRoots(t, s, "S1"); got[0].Status != agentpkg.StatusRunning {
		t.Fatalf("the copy filed after the write hid it: %q", got[0].Status)
	}
}

// A store built without the shared counter never serves a copy.
func TestRootsCache_NoCounterNoCache(t *testing.T) {
	s := openTestFramesStore(t)
	s.gen = nil
	f := rootsFrame(t, s, "%1", 100, "S1")
	if got := cachedRoots(t, s, "S1"); len(got) != 1 {
		t.Fatal("roots")
	}
	if _, err := s.db.Exec(`UPDATE agent_frames SET status = 'running' WHERE frame_id = ?`, f.FrameID); err != nil {
		t.Fatal(err)
	}
	if got := cachedRoots(t, s, "S1"); got[0].Status != "running" {
		t.Fatal("a store without a generation served a copy")
	}
}

// Two FramesStore values over one AgentEventStore share the counter: a write through one ends the other's copies.
func TestRootsCache_StoresOverOneDatabaseShareTheGeneration(t *testing.T) {
	events := openTestAgentEventStore(t)
	a, err := events.Frames()
	if err != nil {
		t.Fatal(err)
	}
	b, err := events.Frames()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	framesNow = func() time.Time { return now }
	t.Cleanup(func() { framesNow = time.Now })
	f := rootsFrame(t, a, "%1", 100, "S1")
	if got := cachedRoots(t, b, "S1"); len(got) != 1 {
		t.Fatal("roots")
	}
	if err := a.Delete(f.FrameID); err != nil {
		t.Fatal(err)
	}
	if got := cachedRoots(t, b, "S1"); len(got) != 0 {
		t.Fatal("the other store served a deleted frame")
	}
}

// setupWriters are the helpers migrateFramesDB calls while the database is opened, before a FramesStore (and so any cached
// copy) exists.
var setupWriters = map[string]bool{"clearStaleSubagentsJSON": true, "addFrameRootColumns": true}

// The structural pin: any method of this package that writes agent_frames has to call touch, or the cache would outlive the
// write. A new writer that forgets fails here, not in production.
func TestEveryAgentFramesWriteTouches(t *testing.T) {
	write := regexp.MustCompile(`(?is)(INSERT\s+(OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM)\s+agent_frames\b`)
	fset := token.NewFileSet()
	files, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, pkg := range files {
		for fname, file := range pkg.Files {
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				writes, touches := false, false
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.BasicLit:
						if x.Kind == token.STRING && write.MatchString(x.Value) {
							writes = true
						}
					case *ast.CallExpr:
						if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "touch" {
							touches = true
						}
					}
					return true
				})
				if !writes {
					continue
				}
				// schema set-up and migrations run before any store exists; they are the only exemption
				if fd.Recv == nil && (strings.HasPrefix(fd.Name.Name, "migrate") || setupWriters[fd.Name.Name]) {
					continue
				}
				if fd.Recv == nil && !touches {
					t.Errorf("%s: %s writes agent_frames outside a FramesStore method", fname, fd.Name.Name)
					continue
				}
				checked++
				if !touches {
					t.Errorf("%s: %s writes agent_frames but never calls touch", fname, fd.Name.Name)
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d writers found: the scan is not looking at the right files", checked)
	}
}
