package resourcesmod

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

type fakeWatchSettings struct {
	s   watchsettings.Settings
	err error
}

func (f *fakeWatchSettings) WatchSettings() (watchsettings.Settings, error) { return f.s, f.err }

// watchRig is a module whose tick runs the watcher, with a clock that moves by a tick per call of tick and every outside read
// counted.
type watchRig struct {
	t           *testing.T
	m           *Module
	dir         string
	set         *fakeWatchSettings
	rset        *fakeSettings
	procs       atomic.Pointer[[]resources.Proc]
	now         time.Time
	held        []gate.Holder
	isGate      atomic.Int64
	argv        atomic.Int64
	cwd         atomic.Int64
	lockPID     atomic.Int64
	loadGate    atomic.Int64
	gatePath    atomic.Value
	sampleErr   atomic.Bool
	unsupported atomic.Bool
}

const wtick = 20 * time.Second // not the detector's default of 5 s: a pass that ignored Input.Interval would see every gap as a break

func newWatchRig(t *testing.T, mode string) *watchRig {
	t.Helper()
	r := &watchRig{t: t, dir: t.TempDir(), set: &fakeWatchSettings{s: watchsettings.Default()}, rset: &fakeSettings{}, now: time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)}
	r.set.s.Enabled = true
	r.rset.set(resources.Settings{Mode: mode})
	sampler := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		if r.unsupported.Load() {
			return resources.HostRaw{}, nil, resources.ErrUnsupported
		}
		if r.sampleErr.Load() {
			return resources.HostRaw{}, nil, os.ErrInvalid
		}
		var procs []resources.Proc
		if p := r.procs.Load(); p != nil {
			procs = *p
		}
		return goodRaw(), procs, nil
	}}
	m, _ := initedModule(t, r.dir, r.rset, sampler)
	m.interval = wtick
	m.now = func() time.Time { return r.now }
	m.watch.src = r.set
	m.watch.env = watchEnv{
		loadGate: func(path string) (gate.Config, error) {
			r.loadGate.Add(1)
			r.gatePath.Store(path)
			return gate.Defaults(), nil
		},
		held:     func(gate.Config) ([]gate.Holder, []gate.Holder) { return r.held, nil },
		lockPIDs: func(gate.Config, gate.Holder) []int { r.lockPID.Add(1); return []int{4242} },
		isGate:   func(context.Context, int) bool { r.isGate.Add(1); return false },
		argv:     func(context.Context, int) []string { r.argv.Add(1); return []string{"go", "test", "./..."} },
		cwd:      func(context.Context, int) string { r.cwd.Add(1); return "/work/tree" },
	}
	r.m = m
	return r
}

func (r *watchRig) tick() {
	r.t.Helper()
	r.m.tick(context.Background())
	r.now = r.now.Add(wtick)
}

func (r *watchRig) ticks(n int) {
	for i := 0; i < n; i++ {
		r.tick()
	}
}

func (r *watchRig) setProcs(p []resources.Proc) { r.procs.Store(&p) }

func (r *watchRig) records() []watch.Record {
	r.t.Helper()
	recs, err := watch.ReadLog(filepath.Join(r.dir, "watch"), time.Time{}, "")
	if err != nil {
		r.t.Fatal(err)
	}
	return recs
}

func proc(pid, ppid int, comm string, pcpu float64, rssMB uint64) resources.Proc {
	return resources.Proc{PID: pid, PPID: ppid, Comm: comm, Pcpu: pcpu, RSSBytes: rssMB << 20}
}

// a shell with a `go test` below it using `cores` cores.
func heavyTree(cores float64) []resources.Proc {
	return []resources.Proc{proc(1, 0, "launchd", 0, 10), proc(10, 1, "zsh", 0, 5), proc(100, 10, "go", cores*100, 300)}
}

func TestWatch_AHeavyTreeIsRecordedOnceWithOneArgvAndOneCwdRead(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.setProcs(heavyTree(3))
	r.ticks(30) // 2.5 minutes: the window (60 s) is long past
	recs := r.records()
	if len(recs) != 1 || recs[0].Kind != watch.KindUnmanagedHeavy {
		t.Fatalf("records = %+v", recs)
	}
	if recs[0].Cwd != "/work/tree" || strings.Join(recs[0].Argv, " ") != "go test ./..." {
		t.Fatalf("record = %+v", recs[0])
	}
	if r.argv.Load() != 1 || r.cwd.Load() != 1 {
		t.Fatalf("argv reads %d, cwd reads %d: one of each per episode", r.argv.Load(), r.cwd.Load())
	}
	if _, err := os.Stat(filepath.Join(r.dir, "watch", "state.json")); err != nil {
		t.Fatalf("the state is flushed: %v", err)
	}
}

// The steady state reads nothing extra: with many trees and none over a threshold there is no argv read, no cwd read and no lock
// lookup; the only calls are the gate config and the pdx-named processes' gate check.
func TestWatch_NoTreeOverAThresholdCostsNoArgvCwdOrLockRead(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	procs := []resources.Proc{proc(1, 0, "launchd", 0, 10), proc(10, 1, "zsh", 0, 5)}
	for i := 0; i < 40; i++ {
		procs = append(procs, proc(100+i, 10, "node", 20, 100)) // 40 trees at 0.2 core and 100 MB
	}
	r.setProcs(procs)
	r.ticks(40)
	if r.argv.Load() != 0 || r.cwd.Load() != 0 || r.lockPID.Load() != 0 || r.isGate.Load() != 0 {
		t.Fatalf("argv %d cwd %d lock %d isGate %d, all zero", r.argv.Load(), r.cwd.Load(), r.lockPID.Load(), r.isGate.Load())
	}
	if len(r.records()) != 0 {
		t.Fatal("nothing to record")
	}
}

// The watcher is independent of the lease mode: with the mode off the tick still samples and records, and publishes "off".
func TestWatch_WorksWithTheModeOff(t *testing.T) {
	r := newWatchRig(t, resources.ModeOff)
	r.setProcs(heavyTree(3))
	r.ticks(30)
	if len(r.records()) != 1 {
		t.Fatalf("records = %+v", r.records())
	}
	snap := r.m.latest.Load()
	if snap == nil || snap.Available || snap.Reason != resources.ReasonOff {
		t.Fatalf("the snapshot says %+v, want off", snap)
	}
}

func TestWatch_OffMeansNoSamplingWhenBothAreOff(t *testing.T) {
	r := newWatchRig(t, resources.ModeOff)
	r.set.s.Enabled = false
	r.setProcs(heavyTree(3))
	r.ticks(5)
	if n := r.m.sampler.(*fakeSampler).calls.Load(); n != 0 {
		t.Fatalf("the sampler ran %d times with the mode off and the watcher off", n)
	}
}

func TestWatch_AFailedSampleSkipsThePassAndNothingElse(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.setProcs(heavyTree(3))
	r.ticks(3)
	before := r.loadGate.Load()
	r.sampleErr.Store(true)
	r.ticks(2)
	if r.loadGate.Load() != before {
		t.Fatal("a pass ran on a failed sample")
	}
	r.sampleErr.Store(false)
	r.ticks(30)
	if len(r.records()) != 1 {
		t.Fatalf("records after the gap = %+v", r.records())
	}
}

func TestWatch_ADisabledOrUnreadableSettingLeavesItOff(t *testing.T) {
	for name, set := range map[string]*fakeWatchSettings{
		"disabled":   {s: watchsettings.Settings{Enabled: false}},
		"unreadable": {err: os.ErrInvalid},
	} {
		r := newWatchRig(t, resources.ModeMeasure)
		r.m.watch.src = set
		r.setProcs(heavyTree(3))
		r.ticks(30)
		if len(r.records()) != 0 || r.loadGate.Load() != 0 {
			t.Fatalf("%s: the watcher ran", name)
		}
		if _, err := os.Stat(filepath.Join(r.dir, "watch")); !os.IsNotExist(err) {
			t.Fatalf("%s: files were made", name)
		}
	}
	r := newWatchRig(t, resources.ModeMeasure)
	r.m.watch.src = nil
	r.setProcs(heavyTree(3))
	r.ticks(30)
	if len(r.records()) != 0 {
		t.Fatal("no reader: the watcher ran")
	}
}

// A record or state failure is logged and never stops the sampler or the publish.
func TestWatch_IOFailuresDoNotStopTheSampler(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.setProcs(heavyTree(3))
	// the watch directory is a file: neither the state nor the log can be written
	if err := os.WriteFile(filepath.Join(r.dir, "watch"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.ticks(30)
	snap := r.m.latest.Load()
	if snap == nil || !snap.Available {
		t.Fatalf("the publish stopped: %+v", snap)
	}
	if r.m.sampler.(*fakeSampler).calls.Load() != 30 {
		t.Fatal("the sampler stopped")
	}
}

// An orphaned slot is looked up (lsof) at most once per pass, and only an orphaned one.
func TestWatch_AnOrphanedSlotIsLookedUpOnlyOnceItIsReported(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.setProcs([]resources.Proc{proc(1, 0, "launchd", 0, 10)})
	r.held = []gate.Holder{
		{Class: gate.ClassHeavy, Slot: 1, PID: 900, PGID: 901, Orphaned: true, Started: r.now},
		{Class: gate.ClassHeavy, Slot: 2, PID: 902, PGID: 903, Started: r.now},
	}
	r.ticks(200) // 16 minutes: past the 15 minute orphan time
	recs := r.records()
	var e3 int
	for _, rec := range recs {
		if rec.Kind == watch.KindSlotOrphaned {
			e3++
		}
	}
	if e3 != 1 || r.lockPID.Load() > 2 {
		t.Fatalf("E3 records %d, lock lookups %d; records %+v", e3, r.lockPID.Load(), recs)
	}
}

// With the mode off a failed sample only skips the watcher's pass: it never turns the published "off" into "sample_failed".
func TestWatch_AFailedSampleWithTheModeOffStaysOff(t *testing.T) {
	r := newWatchRig(t, resources.ModeOff)
	r.sampleErr.Store(true)
	r.ticks(6)
	snap := r.m.latest.Load()
	if snap == nil || snap.Reason != resources.ReasonOff {
		t.Fatalf("snapshot = %+v, want reason off", snap)
	}
}

func TestLsofCwd_ReadsAProcessWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof")
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := lsofCwd(context.Background(), os.Getpid())
	if a, b := mustReal(t, got), mustReal(t, want); a != b {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
	if lsofCwd(context.Background(), 0x7ffffff0) != "" {
		t.Fatal("a process that is not there has no cwd")
	}
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// isOwnGate is not fooled by a process that only has the name: the test binary is not `pdx gate`.
func TestIsOwnGate_RefusesAProcessThatIsNotOne(t *testing.T) {
	if isOwnGate(context.Background(), os.Getpid()) {
		t.Fatal("the test binary was taken for a gate")
	}
	if isOwnGate(context.Background(), 0x7ffffff0) {
		t.Fatal("a process that is not there was taken for a gate")
	}
}

// With the mode off, a platform that cannot be sampled leaves "off" published and the loop alive (the setting is still read).
func TestWatch_UnsupportedPlatformWithTheModeOffStaysOff(t *testing.T) {
	r := newWatchRig(t, resources.ModeOff)
	r.unsupported.Store(true)
	if stop := r.m.tick(context.Background()); stop {
		t.Fatal("the loop was stopped")
	}
	snap := r.m.latest.Load()
	if snap == nil || snap.Reason != resources.ReasonOff {
		t.Fatalf("snapshot = %+v, want reason off", snap)
	}
	// with the mode measure the old behaviour stands: unavailable, and the loop ends
	r2 := newWatchRig(t, resources.ModeMeasure)
	r2.unsupported.Store(true)
	if stop := r2.m.tick(context.Background()); !stop {
		t.Fatal("measure mode on an unsupported platform ends the loop")
	}
	if s := r2.m.latest.Load(); s == nil || s.Reason != resources.ReasonUnsupportedPlatform {
		t.Fatalf("snapshot = %+v", s)
	}
}

// The watcher reads the gate's config from the file the daemon was started with, not the default one.
func TestWatch_TheGateConfigIsTheDaemonsOwnFile(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.m.core.Cfg.Path = "/custom/place/config.toml"
	r.setProcs(heavyTree(0.1))
	r.ticks(2)
	if got, _ := r.gatePath.Load().(string); got != "/custom/place/config.toml" {
		t.Fatalf("gate config path = %q", got)
	}
}

// A never-written setting runs the watcher (on by default, spec §12.2).
func TestWatch_NeverWrittenSettingMeansOn(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.m.watch.src = &fakeWatchSettings{s: watchsettings.Default()}
	r.setProcs(heavyTree(3))
	r.ticks(30)
	if len(r.records()) != 1 {
		t.Fatalf("records = %+v", r.records())
	}
}

type fakeEventSink struct {
	mu   sync.Mutex
	recs []watch.Record
}

func (f *fakeEventSink) Enqueue(rec watch.Record) {
	f.mu.Lock()
	f.recs = append(f.recs, rec)
	f.mu.Unlock()
}

// A recorded event is handed to the watch module's delivery queue (found in the registry), once, as the record on disk says it.
func TestWatch_ARecordedEventIsHandedToTheDeliveryQueueOnce(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	sink := &fakeEventSink{}
	r.m.core.Registry.Register(watch.EventSinkKey, sink)
	r.setProcs(heavyTree(3))
	r.ticks(30)
	recs := r.records()
	if len(recs) != 1 {
		t.Fatalf("records = %+v", recs)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.recs) != 1 || sink.recs[0].ID != recs[0].ID || sink.recs[0].Kind != watch.KindUnmanagedHeavy {
		t.Fatalf("sink got %+v; the record is %+v", sink.recs, recs[0])
	}
}

// Without the watch module's sink (not registered) the event is still recorded.
func TestWatch_WithoutASinkTheEventIsOnlyRecorded(t *testing.T) {
	r := newWatchRig(t, resources.ModeMeasure)
	r.setProcs(heavyTree(3))
	r.ticks(30)
	if len(r.records()) != 1 {
		t.Fatalf("records = %+v", r.records())
	}
}

// #3072: the footprint the environment reads reaches the detector. A tree of 1.5 GB of RSS and 0.1 core is no event when its
// footprint is small, and is one when the environment has none.
func TestWatch_TheFootprintIsWhatTheDetectorCountsBy(t *testing.T) {
	big := []resources.Proc{proc(1, 0, "launchd", 0, 10), proc(10, 1, "zsh", 0, 5), proc(100, 10, "node", 10, 1500)}
	r := newWatchRig(t, resources.ModeMeasure)
	r.m.watch.env.footprint = func(int) (uint64, bool) { return 50 << 20, true }
	r.setProcs(big)
	r.ticks(30)
	if n := len(r.records()); n != 0 {
		t.Fatalf("records = %d, want none: the footprint is small", n)
	}
	r2 := newWatchRig(t, resources.ModeMeasure)
	r2.setProcs(big)
	r2.ticks(30)
	if n := len(r2.records()); n != 1 {
		t.Fatalf("records = %d, want 1 without a footprint", n)
	}
}
