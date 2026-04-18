package dev

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func parseSSE(body string) []streamEvent {
	var out []streamEvent
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err == nil {
			out = append(out, ev)
		}
	}
	return out
}

func writeBuildInfo(t *testing.T, dir string, info BuildInfo) {
	t.Helper()
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, ".build-info.json"), data, 0644); err != nil {
		t.Fatalf("write build-info: %v", err)
	}
}

func TestHandleCheckStream_NotStale(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "aaa", ElectronHash: "bbb"})

	m := &DevModule{
		repoRoot:    dir,
		versionFile: filepath.Join(dir, "VERSION"),
		hashFn: func(paths ...string) string {
			if len(paths) > 0 && paths[0] == "spa/" {
				return "aaa"
			}
			return "bbb"
		},
		buildCmd: func(*BuildSession) error { return nil },
	}

	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil)
	w := httptest.NewRecorder()
	m.handleCheckStream(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type: want text/event-stream, got %s", ct)
	}
	events := parseSSE(w.Body.String())
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "check" {
		t.Errorf("events[0].Type: want check, got %s", events[0].Type)
	}
	if events[0].Check == nil || events[0].Check.Building {
		t.Errorf("events[0].Check: want building=false, got %+v", events[0].Check)
	}
	if events[1].Type != "done" {
		t.Errorf("events[1].Type: want done, got %s", events[1].Type)
	}
}

func TestHandleCheckStream_TriggersBuildAndStreamsLog(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "old", ElectronHash: "old"})

	m := &DevModule{
		repoRoot:    dir,
		versionFile: filepath.Join(dir, "VERSION"),
		hashFn:      func(paths ...string) string { return "new" },
		buildCmd: func(s *BuildSession) error {
			s.append(BuildEvent{Type: BuildEventPhase, Phase: "install"})
			s.append(BuildEvent{Type: BuildEventStdout, Line: "hello"})
			s.append(BuildEvent{Type: BuildEventStderr, Line: "warn"})
			// Simulate build writing new build-info so post-build check is fresh
			writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "new", ElectronHash: "new"})
			return nil
		},
	}

	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil)
	w := httptest.NewRecorder()
	m.handleCheckStream(w, req)

	events := parseSSE(w.Body.String())
	if len(events) < 5 {
		t.Fatalf("want >=5 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "check" || events[0].Check == nil || !events[0].Check.Building {
		t.Errorf("initial event: want check building=true, got %+v", events[0])
	}

	sawPhase, sawStdout, sawStderr := false, false, false
	for _, ev := range events[1 : len(events)-1] {
		if ev.Type == "phase" && ev.Phase == "install" {
			sawPhase = true
		}
		if ev.Type == "stdout" && ev.Line == "hello" {
			sawStdout = true
		}
		if ev.Type == "stderr" && ev.Line == "warn" {
			sawStderr = true
		}
	}
	if !(sawPhase && sawStdout && sawStderr) {
		t.Errorf("missing build events phase=%v stdout=%v stderr=%v: %+v", sawPhase, sawStdout, sawStderr, events)
	}

	last := events[len(events)-1]
	if last.Type != "done" || last.Check == nil {
		t.Fatalf("last event: want done with check, got %+v", last)
	}
	if last.Check.Building {
		t.Errorf("final check: want building=false, got %+v", last.Check)
	}
	if last.Check.SPAHash != "new" {
		t.Errorf("final check SPAHash: want new, got %s", last.Check.SPAHash)
	}
}

func TestHandleCheckStream_LateSubscriberSeesReplay(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "old", ElectronHash: "old"})

	session := newBuildSession()
	session.append(BuildEvent{Type: BuildEventPhase, Phase: "install"})
	session.append(BuildEvent{Type: BuildEventStdout, Line: "line-A"})

	m := &DevModule{
		repoRoot:     dir,
		versionFile:  filepath.Join(dir, "VERSION"),
		hashFn:       func(paths ...string) string { return "new" },
		building:     true,
		buildSession: session,
		buildCmd:     func(*BuildSession) error { return nil }, // defensive — not expected to be called
	}

	// Finish the session in the background so the handler's loop exits.
	// Writes fresh build-info before flipping building flag so the handler's
	// terminal snapshotCheck observes a non-stale state (otherwise it would
	// think a new build is needed).
	go func() {
		time.Sleep(20 * time.Millisecond)
		session.append(BuildEvent{Type: BuildEventStdout, Line: "line-B"})
		writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "new", ElectronHash: "new"})
		m.mu.Lock()
		m.building = false
		m.mu.Unlock()
		session.append(BuildEvent{Type: BuildEventDone})
		session.finish()
	}()

	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil)
	w := httptest.NewRecorder()
	m.handleCheckStream(w, req)

	events := parseSSE(w.Body.String())
	if len(events) < 4 {
		t.Fatalf("want >=4 events, got %d: %+v", len(events), events)
	}

	hasA, hasB := false, false
	for _, ev := range events {
		if ev.Type == "stdout" && ev.Line == "line-A" {
			hasA = true
		}
		if ev.Type == "stdout" && ev.Line == "line-B" {
			hasB = true
		}
	}
	if !hasA {
		t.Errorf("missing replayed line-A: %+v", events)
	}
	if !hasB {
		t.Errorf("missing live line-B: %+v", events)
	}
	if last := events[len(events)-1]; last.Type != "done" {
		t.Errorf("last event: want done, got %s", last.Type)
	}
}

func TestHandleCheckStream_BuildFailureCarriesErrorInFinal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "old", ElectronHash: "old"})

	m := &DevModule{
		repoRoot:    dir,
		versionFile: filepath.Join(dir, "VERSION"),
		hashFn:      func(paths ...string) string { return "new" },
		buildCmd: func(s *BuildSession) error {
			s.append(BuildEvent{Type: BuildEventStderr, Line: "ERR_SOMETHING"})
			return &exitError{msg: "pnpm install failed: exit status 1"}
		},
	}

	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil)
	w := httptest.NewRecorder()
	m.handleCheckStream(w, req)

	events := parseSSE(w.Body.String())
	if len(events) < 3 {
		t.Fatalf("want >=3 events, got %d: %+v", len(events), events)
	}
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("last event: want done (we always send done as terminal, error info in check payload), got %s", last.Type)
	}
	if last.Check == nil {
		t.Fatal("last event check: want non-nil")
	}
	if last.Check.Building {
		t.Errorf("final building: want false, got true")
	}
	if !strings.Contains(last.Check.BuildError, "pnpm install failed") {
		t.Errorf("final buildError: want contains 'pnpm install failed', got %q", last.Check.BuildError)
	}
}

func TestHandleCheckStream_IncludesRequiresFullRebuild(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "aaa", ElectronHash: "bbb", RebuildHash: "old-rebuild"})

	m := &DevModule{
		repoRoot:    dir,
		versionFile: filepath.Join(dir, "VERSION"),
		hashFn: func(paths ...string) string {
			// Check which set of paths is asked
			if len(paths) == len(rebuildTrackedPaths) {
				return "new-rebuild"
			}
			if len(paths) > 0 && paths[0] == "spa/" {
				return "aaa"
			}
			return "bbb"
		},
		buildCmd: func(*BuildSession) error { return nil },
	}

	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil)
	w := httptest.NewRecorder()
	m.handleCheckStream(w, req)

	events := parseSSE(w.Body.String())
	if len(events) == 0 {
		t.Fatal("want at least 1 event")
	}
	if !events[0].Check.RequiresFullRebuild {
		t.Errorf("requiresFullRebuild: want true, got false (reason=%q)", events[0].Check.FullRebuildReason)
	}
	if events[0].Check.FullRebuildReason == "" {
		t.Errorf("fullRebuildReason: want non-empty")
	}
}

type exitError struct{ msg string }

func (e *exitError) Error() string { return e.msg }

// Guards against regressing the fix for: build completed but vite plugin
// failed to write .build-info.json → old handleCheckStream would re-enter
// snapshotCheck() and spuriously kick off another build. After fix, the
// terminal snapshot uses observeCheck() which never spawns builds.
func TestHandleCheckStream_TerminalSnapshotDoesNotTriggerSecondBuild(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "old", ElectronHash: "old"})

	var buildCalls atomic.Int32
	m := &DevModule{
		repoRoot:    dir,
		versionFile: filepath.Join(dir, "VERSION"),
		hashFn:      func(paths ...string) string { return "new" },
		buildCmd: func(s *BuildSession) error {
			buildCalls.Add(1)
			s.append(BuildEvent{Type: BuildEventStdout, Line: "building"})
			// Intentionally do NOT update .build-info.json — simulates vite
			// plugin failing to write for any reason. sourceChanged will
			// still look true after the build.
			return nil
		},
	}

	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil)
	w := httptest.NewRecorder()
	m.handleCheckStream(w, req)

	// Build should run exactly once. Prior buggy code would re-enter
	// snapshotCheck after channel close and kick off a second build.
	if got := buildCalls.Load(); got != 1 {
		t.Errorf("buildCalls: want 1, got %d", got)
	}

	events := parseSSE(w.Body.String())
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Errorf("last event: want done, got %s", last.Type)
	}
	// Post-build state — building must be false even though source still
	// looks stale (because .build-info.json was not updated).
	if last.Check == nil || last.Check.Building {
		t.Errorf("final building: want false, got %+v", last.Check)
	}
}

// Guards against subscription leaks when the HTTP client disconnects while
// a build is still streaming.
func TestHandleCheckStream_ClientDisconnectReleasesSubscription(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0644)
	writeBuildInfo(t, dir, BuildInfo{Version: "1.0.0", SPAHash: "old", ElectronHash: "old"})

	session := newBuildSession()
	session.append(BuildEvent{Type: BuildEventPhase, Phase: "install"})

	m := &DevModule{
		repoRoot:     dir,
		versionFile:  filepath.Join(dir, "VERSION"),
		hashFn:       func(paths ...string) string { return "new" },
		building:     true,
		buildSession: session,
		buildCmd:     func(*BuildSession) error { return nil },
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/dev/update/check/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		m.handleCheckStream(w, req)
		close(done)
	}()

	// Wait until the handler subscribes.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		n := len(session.subs)
		session.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	session.mu.Lock()
	if n := len(session.subs); n != 1 {
		session.mu.Unlock()
		t.Fatalf("want 1 subscriber before cancel, got %d", n)
	}
	session.mu.Unlock()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after client disconnect")
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if n := len(session.subs); n != 0 {
		t.Errorf("subscription leaked after disconnect: %d subs remain", n)
	}
}
