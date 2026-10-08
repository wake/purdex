package peers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
)

// fakeRootView is the process table ProcessRoots reads, as one fake: the
// pids it lists, each with a start time or a start error. Read counts calls
// so a test can prove nothing reached for argv.
type fakeRootView struct {
	procs map[int]fakeRootProc
	reads int
}

type fakeRootProc struct {
	start    time.Time
	startErr error
}

func (f *fakeRootView) Alive(pid int) bool { _, ok := f.procs[pid]; return ok }

func (f *fakeRootView) Start(pid int) (time.Time, error) {
	p, ok := f.procs[pid]
	if !ok {
		return time.Time{}, iagent.ErrNotInSnapshot
	}
	return p.start, p.startErr
}

func (f *fakeRootView) StartTime(pid int) (string, error) {
	return "", errors.New("fakeRootView: StartTime text is not used by ProcessRoots")
}

func (f *fakeRootView) PPID(pid int) (int, error) {
	return 0, errors.New("fakeRootView: PPID is not used by ProcessRoots")
}

func (f *fakeRootView) Read(pid int) (iagent.ProcessInfo, error) {
	f.reads++
	return iagent.ProcessInfo{}, errors.New("fakeRootView: Read would read argv")
}

// rootsEntry is one registry file for rootsFixture. Its inbox is a real file
// under the dir (so the os.Stat probe passes) unless noInbox.
type rootsEntry struct {
	pid       int
	sid       string
	procStart string // registry text; defaults to targetProcStart
	tmux      string
	noInbox   bool
}

func rootsFixture(t *testing.T, entries ...rootsEntry) (*OriginResolver, string) {
	t.Helper()
	dir := t.TempDir()
	for _, e := range entries {
		sock := filepath.Join(dir, fmt.Sprintf("%d.sock", e.pid))
		if !e.noInbox {
			if err := os.WriteFile(sock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ps := e.procStart
		if ps == "" {
			ps = targetProcStart
		}
		writeRegistryFixture(t, dir, fmt.Sprintf("%d.json", e.pid),
			fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/w%d","procStart":%q,"version":"2.1.270","tmux":%q,"messagingSocketPath":%q,"name":"n%d","status":"idle"}`,
				e.pid, e.sid, e.pid, ps, e.tmux, sock, e.pid))
	}
	m := &Module{
		core:        newTestCore(t, "mlab:abc123", "mlab"),
		registryDir: dir,
		liveness:    allLiveLiveness(fixture76973ProcStart), // must not be consulted by ProcessRoots
		logf:        func(string, ...any) {},
	}
	return &OriginResolver{m: m}, dir
}

func rootsBySID(roots []resources.Root) map[string]resources.Root {
	out := map[string]resources.Root{}
	for _, r := range roots {
		out[r.SessionID] = r
	}
	return out
}

func TestProcessRoots_SkipsProxiesAndDead(t *testing.T) {
	r, _ := rootsFixture(t,
		rootsEntry{pid: 10, sid: "sid-live", tmux: "mt0:@1.%1"},
		rootsEntry{pid: 20, sid: "sid-dead-pid"},
		rootsEntry{pid: 30, sid: "sid-mismatch", procStart: "Mon Sep 14 09:00:00 2026"},
		rootsEntry{pid: 40, sid: "sid-proxy"},
		rootsEntry{pid: 50, sid: "sid-no-inbox", noInbox: true},
	)
	r.m.helpers = &helperManager{helpers: map[ipeers.OriginKey]*helper{
		{AgentSessionID: "x"}: {pid: 40},
	}}
	view := &fakeRootView{procs: map[int]fakeRootProc{
		10: {start: fixture76973ProcStart},
		// 20 is not in the snapshot: dead.
		30: {start: fixture76973ProcStart}, // alive, but not the process the file names
		40: {start: fixture76973ProcStart},
		50: {start: fixture76973ProcStart},
	}}

	roots, err := r.processRoots(view)
	if err != nil {
		t.Fatal(err)
	}
	got := rootsBySID(roots)
	if len(got) != 1 {
		t.Fatalf("roots = %+v, want only sid-live", roots)
	}
	live := got["sid-live"]
	if live.PID != 10 || live.ProcStart != targetProcStart || live.Tmux != "mt0:@1.%1" || live.Cwd != "/w10" {
		t.Fatalf("live root = %+v", live)
	}
}

// TestProcessRoots_StartMismatchLeftOut pins the start-time check that makes
// it safe for resources.Attribute not to compare Root.ProcStart itself: the
// registry file names a process by pid and start time, and a pid that was
// reused by another process since has a different start. ProcessRoots must
// leave such a session out, so every Root handed to Attribute names the
// process currently running under its pid.
func TestProcessRoots_StartMismatchLeftOut(t *testing.T) {
	r, _ := rootsFixture(t,
		rootsEntry{pid: 10, sid: "sid-same"},
		rootsEntry{pid: 20, sid: "sid-reused", procStart: "Mon Sep 14 09:00:00 2026"},
	)
	view := &fakeRootView{procs: map[int]fakeRootProc{
		10: {start: fixture76973ProcStart},
		// pid 20 is alive but started at fixture76973ProcStart, not at the
		// time its registry file says: the pid was reused.
		20: {start: fixture76973ProcStart},
	}}
	roots, err := r.processRoots(view)
	if err != nil {
		t.Fatal(err)
	}
	got := rootsBySID(roots)
	if _, ok := got["sid-reused"]; ok {
		t.Fatalf("a reused pid became a root: %+v", roots)
	}
	if _, ok := got["sid-same"]; !ok || len(got) != 1 {
		t.Fatalf("roots = %+v, want exactly sid-same", roots)
	}
}

// Codex attack (medium): after a resume the old process can linger as a
// zombie with its registry file and inbox still there, both under the same
// session id. Nothing here can tell a zombie (no state in the snapshot, and
// Liveness.Zombie would cost a fork), so a session id is one root: the
// newest process wins. Two rows for one session would have the later one
// overwrite the earlier in `pdx team` and be listed twice in `pdx lease ls`.
func TestProcessRoots_TwinSessionKeepsNewestProcess(t *testing.T) {
	later := fixture76973ProcStart.Add(time.Hour)
	for name, entries := range map[string][]rootsEntry{
		"older file first": {
			{pid: 10, sid: "sid-twin"},
			{pid: 20, sid: "sid-twin", procStart: "Sun Sep 13 16:22:36 2026"},
		},
		"newer file first": {
			{pid: 20, sid: "sid-twin", procStart: "Sun Sep 13 16:22:36 2026"},
			{pid: 10, sid: "sid-twin"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := rootsFixture(t, append(entries, rootsEntry{pid: 30, sid: "sid-other"})...)
			view := &fakeRootView{procs: map[int]fakeRootProc{
				10: {start: fixture76973ProcStart},
				20: {start: later},
				30: {start: fixture76973ProcStart},
			}}
			roots, err := r.processRoots(view)
			if err != nil {
				t.Fatal(err)
			}
			got := rootsBySID(roots)
			if len(roots) != 2 || got["sid-twin"].PID != 20 || got["sid-other"].PID != 30 {
				t.Fatalf("roots = %+v, want sid-twin -> pid 20 (the newest) and sid-other", roots)
			}
		})
	}
}

func TestProcessRoots_NoFork(t *testing.T) {
	r, _ := rootsFixture(t, rootsEntry{pid: 10, sid: "sid-1"})
	view := &fakeRootView{procs: map[int]fakeRootProc{10: {start: fixture76973ProcStart}}}

	var captured ipeers.Liveness
	orig := readRegistry
	readRegistry = func(dir string, live ipeers.Liveness) ([]ipeers.Entry, int, error) {
		captured = live
		return orig(dir, live)
	}
	t.Cleanup(func() { readRegistry = orig })

	if _, err := r.processRoots(view); err != nil {
		t.Fatal(err)
	}
	if captured.Info != nil {
		t.Fatal("Liveness.Info is set: it reads argv and may fork ps")
	}
	if captured.Zombie != nil {
		t.Fatal("Liveness.Zombie is set: it forks ps")
	}
	if captured.PidAlive == nil || captured.StartTime == nil || captured.Stat == nil {
		t.Fatalf("liveness incomplete: %+v", captured)
	}
	// Both answer from the snapshot and nothing else: a pid the snapshot
	// lacks is dead / unreadable, never looked up elsewhere.
	if !captured.PidAlive(10) || captured.PidAlive(99999) {
		t.Fatal("PidAlive does not follow the snapshot")
	}
	if got, err := captured.StartTime(10); err != nil || !got.Equal(fixture76973ProcStart) {
		t.Fatalf("StartTime(10) = %v, %v", got, err)
	}
	if _, err := captured.StartTime(99999); err == nil {
		t.Fatal("StartTime for a pid the snapshot lacks must fail")
	}
	if view.reads != 0 {
		t.Fatalf("snapshot Read called %d times", view.reads)
	}
}

func TestProcessRoots_UnreadableStartLeftOut(t *testing.T) {
	r, _ := rootsFixture(t,
		rootsEntry{pid: 10, sid: "sid-ok"},
		rootsEntry{pid: 20, sid: "sid-bad-start"},
	)
	view := &fakeRootView{procs: map[int]fakeRootProc{
		10: {start: fixture76973ProcStart},
		20: {startErr: errors.New("lstart does not parse")},
	}}
	roots, err := r.processRoots(view)
	if err != nil {
		t.Fatal(err)
	}
	got := rootsBySID(roots)
	if _, ok := got["sid-bad-start"]; ok || len(got) != 1 {
		t.Fatalf("roots = %+v, want only sid-ok", roots)
	}
}

func TestProcessRoots_StatErrorIsUnknown(t *testing.T) {
	// A non-ENOENT stat error (ENOTDIR: a path component is a file) proves
	// nothing about the session, so it is left out; it must not panic.
	r, dir := rootsFixture(t, rootsEntry{pid: 10, sid: "sid-ok"})
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegistryFixture(t, dir, "20.json",
		fmt.Sprintf(`{"pid":20,"sessionId":"sid-stat","cwd":"/w","procStart":%q,"version":"2.1.270","messagingSocketPath":%q,"name":"n","status":"idle"}`,
			targetProcStart, filepath.Join(blocker, "20.sock")))
	view := &fakeRootView{procs: map[int]fakeRootProc{
		10: {start: fixture76973ProcStart},
		20: {start: fixture76973ProcStart},
	}}
	roots, err := r.processRoots(view)
	if err != nil {
		t.Fatal(err)
	}
	got := rootsBySID(roots)
	if _, ok := got["sid-stat"]; ok || len(got) != 1 {
		t.Fatalf("roots = %+v, want only sid-ok", roots)
	}
}

func TestProcessRoots_NilSnapshotAndReadError(t *testing.T) {
	r, _ := rootsFixture(t, rootsEntry{pid: 10, sid: "sid-1"})
	if _, err := r.ProcessRoots(nil); err == nil {
		t.Fatal("a nil snapshot must be an error")
	}
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.m.registryDir = file
	if _, err := r.processRoots(&fakeRootView{}); err == nil {
		t.Fatal("an unreadable registry must be an error")
	}
}

// The resolver is what the resources module looks up under
// OriginResolverKey.
var _ resources.RootSource = (*OriginResolver)(nil)
