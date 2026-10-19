package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestParseProcStat(t *testing.T) {
	// the command name may hold spaces and parentheses; fields count from the last ')'
	line := "4242 (my (odd) cmd) S 100 4242 4242 0 -1 4194304 1 0 0 0 250 50 0 0 20 0 1 0 987654 1000000 3000 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n"
	p, ok := ParseProcStat(line)
	if !ok || p.Comm != "my (odd) cmd" || p.PPID != 100 || p.UTime != 250 || p.STime != 50 || p.Start != 987654 || p.RSSPages != 3000 {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	for _, bad := range []string{"", "1 (x", "1 (x) S 1 2", "garbage"} {
		if _, ok := ParseProcStat(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func TestParseNULList(t *testing.T) {
	got := ParseNULList([]byte("node\x00--port\x003000\x00"))
	if strings.Join(got, "|") != "node|--port|3000" {
		t.Fatalf("%q", got)
	}
	if got := ParseNULList(nil); len(got) != 0 {
		t.Fatalf("empty: %q", got)
	}
	if got := ParseNULList([]byte("a\x00\x00b\x00")); strings.Join(got, "|") != "a||b" {
		t.Fatalf("an empty argument is kept: %q", got)
	}
}

func TestParseStatusUID(t *testing.T) {
	if u, ok := ParseStatusUID("Name:\tx\nUid:\t1000\t0\t0\t1000\n"); !ok || u != 0 {
		t.Fatalf("effective is the second: %d %v", u, ok)
	}
	for _, bad := range []string{"", "Name:\tx\n", "Uid:\t1000\n", "Uid:\ta\tb\n"} {
		if _, ok := ParseStatusUID(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func TestParseBtime(t *testing.T) {
	if b, ok := ParseBtime("cpu  1 2 3\nbtime 1760000000\nprocesses 5\n"); !ok || b != 1760000000 {
		t.Fatalf("%d %v", b, ok)
	}
	if _, ok := ParseBtime("cpu 1\n"); ok {
		t.Fatal("no btime")
	}
}

// fakeProc is a /proc tree on disk.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T, btime int64) *fakeProc {
	f := &fakeProc{t: t, root: t.TempDir()}
	f.write("stat", fmt.Sprintf("cpu 1 2 3\nbtime %d\n", btime))
	return f
}

func (f *fakeProc) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// proc adds a process: cpu ticks (user+system), start in ticks since boot, rss in pages.
func (f *fakeProc) proc(pid, ppid int, comm string, ticks, start, rssPages uint64, argv []string, env []string) {
	f.write(fmt.Sprintf("%d/stat", pid), fmt.Sprintf("%d (%s) S %d %d 0 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 %d 0 %d 0\n", pid, comm, ppid, pid, ticks, start, rssPages))
	// real uid 0, effective uid 4242: the effective one is the process's
	f.write(fmt.Sprintf("%d/status", pid), fmt.Sprintf("Name:\t%s\nUid:\t0\t4242\t4242\t4242\nGid:\t0\t0\t0\t0\n", comm))
	f.write(fmt.Sprintf("%d/cmdline", pid), strings.Join(argv, "\x00")+"\x00")
	f.write(fmt.Sprintf("%d/environ", pid), strings.Join(env, "\x00")+"\x00")
}

func (f *fakeProc) remove(pid int) {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.root, fmt.Sprint(pid))); err != nil {
		f.t.Fatal(err)
	}
}

func TestProcfsReaderSnapshotSizesCPUAndAttribution(t *testing.T) {
	f := newFakeProc(t, 1_760_000_000)
	pageMB := uint64(os.Getpagesize())
	f.proc(10, 1, "node", 0, 500, 100<<20/pageMB, []string{"node", "vite"}, nil)
	f.proc(11, 10, "sh", 0, 600, 1, []string{"sh"}, []string{"WWD_JOB=j_abc"})
	r := NewProcfsReader(f.root, "")
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	snap, ctx, err := r.Snapshot(t0)
	if err != nil || len(snap.Procs) != 2 {
		t.Fatalf("%d procs err %v", len(snap.Procs), err)
	}
	var node ProcRec
	for _, p := range snap.Procs {
		if p.PID == 10 {
			node = p
		}
	}
	if node.Name != "node" || node.PPID != 1 || node.FootprintBytes != 100<<20 || node.CPUPct != 0 {
		t.Fatalf("first sight: %+v", node)
	}
	// the dir is owned by whoever runs the test: the uid must come from status, not from it
	if node.UID != 4242 {
		t.Errorf("uid %d want the effective uid 4242 of status", node.UID)
	}
	if o := Attribute(node, ProcContext{}); o != nil {
		t.Errorf("a user process the directory owner might call root's is not the system's: %+v", o)
	}
	if kindOf(node, ctx) != "vite" {
		t.Errorf("kind from cmdline")
	}
	if job, ok := ctx.JobOf(11); !ok || job != "j_abc" {
		t.Errorf("job marker from environ: %q %v", job, ok)
	}
	// no session id in the environments of this fake tree
	if _, ok := ctx.EnvSessionOf(10); ok {
		t.Error("no session id in the environment")
	}
	// 100 ticks of CPU in 10 s (clock tick = 10 ms) is 10 %
	f.proc(10, 1, "node", 100, 500, 100<<20/pageMB, []string{"node", "vite"}, nil)
	snap, _, err = r.Snapshot(t0.Add(10 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range snap.Procs {
		if p.PID == 10 && (p.CPUPct < 9.9 || p.CPUPct > 10.1) {
			t.Errorf("cpu %v want 10", p.CPUPct)
		}
	}
}

// A pid that names another process the second time (a new start time) starts its
// CPU share from zero instead of inheriting the old process's counter.
func TestProcfsReaderPidReuseDoesNotCarryCPU(t *testing.T) {
	f := newFakeProc(t, 1_760_000_000)
	f.proc(10, 1, "old", 100000, 500, 10, []string{"old"}, nil)
	r := NewProcfsReader(f.root, "")
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	if _, _, err := r.Snapshot(t0); err != nil {
		t.Fatal(err)
	}
	f.proc(10, 1, "new", 100050, 9000, 10, []string{"new"}, nil)
	snap, _, err := r.Snapshot(t0.Add(10 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Procs[0].Name != "new" || snap.Procs[0].CPUPct != 0 {
		t.Fatalf("%+v", snap.Procs[0])
	}
}

func TestProcfsReaderSkipsAProcessThatVanishedAndNonPidEntries(t *testing.T) {
	f := newFakeProc(t, 1_760_000_000)
	f.proc(10, 1, "a", 0, 500, 1, []string{"a"}, nil)
	f.write("12/cmdline", "x\x00") // a pid dir whose stat is gone: it exited between readdir and read
	f.write("self/stat", "not a pid")
	f.write("meminfo", "MemTotal: 1 kB\n")
	r := NewProcfsReader(f.root, "")
	snap, _, err := r.Snapshot(time.Now())
	if err != nil || len(snap.Procs) != 1 || snap.Procs[0].PID != 10 {
		t.Fatalf("%+v err %v", snap.Procs, err)
	}
}

func TestProcfsReaderNoProcRootIsAnError(t *testing.T) {
	if _, _, err := NewProcfsReader(filepath.Join(t.TempDir(), "missing"), "").Snapshot(time.Now()); err == nil {
		t.Fatal("an unreadable proc root must fail the snapshot")
	}
}

// A session file names the process by pid and by its start (boot time + ticks).
func TestProcfsReaderSessionAttribution(t *testing.T) {
	const btime = 1_760_000_000
	f := newFakeProc(t, btime)
	f.proc(20, 1, "claude", 0, 12345, 1, []string{"claude"}, nil)
	f.proc(21, 20, "node", 0, 12400, 1, []string{"node", "playwright-mcp"}, nil)
	started := time.Unix(btime+123, 0).UTC().Format("Mon Jan _2 15:04:05 2006")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20.json"), []byte(fmt.Sprintf(`{"pid":20,"sessionId":"sess-1","procStart":%q}`, started)), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewProcfsReader(f.root, dir)
	snap, ctx, err := r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := ctx.SessionOf(20)
	if !ok || ref != sessionRef("sess-1") {
		t.Fatalf("session of 20: %q %v", ref, ok)
	}
	var child ProcRec
	for _, p := range snap.Procs {
		if p.PID == 21 {
			child = p
		}
	}
	ctx.Table = nil
	ctx.ensureTable(snap)
	owner := Attribute(child, ctx)
	if owner == nil || owner.Kind != wire.OwnerResident || owner.Session != sessionRef("sess-1") {
		t.Fatalf("the playwright helper under the session: %+v", owner)
	}
}

// Start is the boot time plus the start ticks (10 ms each), and Fresh lists the table again from the stat files alone, with the same
// start times, and sees a process that appeared after the snapshot without disturbing the reader's own state.
func TestProcfsReaderStartAndFresh(t *testing.T) {
	const btime = 1_760_000_000
	f := newFakeProc(t, btime)
	f.proc(10, 1, "sh", 0, 500, 1, []string{"sh"}, nil) // started 5 s after boot
	r := NewProcfsReader(f.root, "")
	snap, ctx, err := r.Snapshot(time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC))
	if err != nil || len(snap.Procs) != 1 {
		t.Fatalf("%d procs err %v", len(snap.Procs), err)
	}
	if want := time.Unix(btime+5, 0); !snap.Procs[0].Start.Equal(want) {
		t.Fatalf("start %v want %v", snap.Procs[0].Start, want)
	}
	if ctx.Fresh == nil {
		t.Fatal("no Fresh lookup")
	}
	f.proc(11, 10, "sleep", 0, 1234, 1, []string{"sleep", "0.02"}, nil)
	got, err := ctx.Fresh()
	if err != nil || len(got) != 2 {
		t.Fatalf("%d rows err %v", len(got), err)
	}
	byPID := map[int]ProcRec{}
	for _, p := range got {
		byPID[p.PID] = p
	}
	if c := byPID[11]; c.PPID != 10 || c.Name != "sleep" || !c.Start.Equal(time.Unix(btime+12, 340_000_000)) {
		t.Fatalf("the new child: %+v", c)
	}
	// the reader's own state is as the snapshot left it: a second snapshot still works and still sees both
	if snap, _, err = r.Snapshot(time.Date(2026, 10, 18, 9, 0, 5, 0, time.UTC)); err != nil || len(snap.Procs) != 2 {
		t.Fatalf("after Fresh: %d procs err %v", len(snap.Procs), err)
	}
}
