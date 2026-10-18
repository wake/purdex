package quarantine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/worker/locks"
)

type fakeChecker struct {
	mu    sync.Mutex
	gone  map[string]bool
	err   map[string]error
	calls []string
}

func (f *fakeChecker) Gone(_ context.Context, unit string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, unit)
	if e := f.err[unit]; e != nil {
		return false, e
	}
	return f.gone[unit], nil
}

type rig struct {
	t        *testing.T
	root     string // work root
	markers  string
	locks    *locks.Table
	checker  *fakeChecker
	now      time.Time
	openOpts []Option
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var is a symlink, and the store refuses a work root that is not real
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{
		t: t, root: filepath.Join(base, "work"), markers: filepath.Join(base, "quarantine"),
		locks: locks.New(), checker: &fakeChecker{gone: map[string]bool{}, err: map[string]error{}},
		now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		t.Fatal(err)
	}
	r.openOpts = []Option{WithWorkRoot(r.root)}
	return r
}

func (r *rig) open() *Store {
	r.t.Helper()
	s, err := Open(r.markers, r.locks, r.checker, func() time.Time { return r.now }, r.openOpts...)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

// entry makes a job directory with a file in it and returns its entry.
func (r *rig) entry(job string, keys ...string) Entry {
	r.t.Helper()
	dir := filepath.Join(r.root, "h-"+job)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	return Entry{Owner: "src/" + job, JobID: job, Origin: "mlab", ClaimID: "c-" + job, Dir: dir, Keys: keys, Unit: "wwd-" + job}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func markerFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestAddWritesMarkerAtomically(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("j1", "k1")
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	files := markerFiles(t, r.markers)
	if len(files) != 1 || !strings.HasSuffix(files[0], ".json") {
		t.Fatalf("want exactly one .json marker and no tmp leftovers, got %v", files)
	}
	fi, err := os.Stat(filepath.Join(r.markers, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %v, want 0600", fi.Mode().Perm())
	}
	got := s.Held()
	if len(got) != 1 || got[0].JobID != "j1" || !got[0].Since.Equal(r.now) {
		t.Fatalf("Held = %+v", got)
	}
}

func TestAddRejectsBadEntry(t *testing.T) {
	r := newRig(t)
	s := r.open()
	for name, e := range map[string]Entry{
		"no owner":  {JobID: "j", Dir: filepath.Join(r.root, "d")},
		"no job id": {Owner: "o", Dir: filepath.Join(r.root, "d")},
		"no dir":    {Owner: "o", JobID: "j"},
	} {
		if err := s.Add(e); err == nil {
			t.Errorf("%s: Add succeeded", name)
		}
	}
	if len(markerFiles(t, r.markers)) != 0 {
		t.Fatal("a rejected entry left a marker")
	}
}

func TestStaleTmpFilesAreRemovedAtOpen(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(r.markers, 0o700); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(r.markers, "abc.json.tmp-123")
	if err := os.WriteFile(tmp, []byte(`{"Owner":"o"`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := r.open()
	if exists(tmp) {
		t.Fatal("half-written tmp file survived Open")
	}
	if len(s.Held()) != 0 {
		t.Fatal("a tmp file became an entry")
	}
}

func TestRestoreReacquiresHoldsAfterRestart(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if err := s.Add(r.entry("j1", "k1", "k2")); err != nil {
		t.Fatal(err)
	}
	// restart: a fresh lock table and a fresh store over the same marker dir
	r.locks = locks.New()
	s2 := r.open()
	if err := s2.Restore(); err != nil {
		t.Fatal(err)
	}
	if h, ok := r.locks.Holder("k1"); !ok || h != "src/j1" {
		t.Fatalf("k1 holder = %q %v", h, ok)
	}
	_, err := r.locks.Acquire("src/other", []string{"k2"}, nil)
	var busy *locks.BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("same key must be refused after Restore, got %v", err)
	}
}

func TestRestoreKeepsMarkerWhenKeyTaken(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	r.locks = locks.New()
	if _, err := r.locks.Acquire("src/intruder", []string{"k1"}, nil); err != nil {
		t.Fatal(err)
	}
	s2 := r.open()
	if err := s2.Restore(); err == nil {
		t.Fatal("a conflict must be reported to the caller")
	}
	if len(markerFiles(t, r.markers)) != 1 || len(s2.Held()) != 1 {
		t.Fatal("marker must stay when its key is taken")
	}
	if h, _ := r.locks.Holder("k1"); h != "src/intruder" {
		t.Fatalf("Restore stole the key: holder %q", h)
	}
	// once the other holder is gone, the next Sweep takes the hold
	r.locks.ReleaseOwner("src/intruder")
	if _, err := s2.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h, _ := r.locks.Holder("k1"); h != "src/j1" {
		t.Fatalf("Sweep did not retake the hold: holder %q", h)
	}
}

func TestRestoreReportsAHoldWithExtraKeys(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if _, err := r.locks.Acquire("src/j1", []string{"k1", "k2"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(); err == nil {
		t.Fatal("the owner holds more keys than the marker names: releasing it would free keys the marker never owned")
	}
}

func TestRestoreReportsAHoldWithOtherKeys(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if _, err := r.locks.Acquire("src/j1", []string{"other"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(); err == nil {
		t.Fatal("the owner holds different keys than the marker names: that must be reported")
	}
}

// While Add is between writing the marker and updating the entry, a Sweep that holds the older snapshot must wait, not delete
// the marker that was just written.
func TestAddIsAtomicAgainstASweepOfTheOlderEntry(t *testing.T) {
	r := newRig(t)
	old := r.entry("j1", "k1")
	if _, err := r.locks.Acquire(old.Owner, old.Keys, nil); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	rc := &blockingChecker{started: started, release: release}
	s, err := Open(r.markers, r.locks, rc, func() time.Time { return r.now }, r.openOpts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(old); err != nil {
		t.Fatal(err)
	}
	done := make(chan int)
	go func() { n, _ := s.Sweep(context.Background()); done <- n }()
	<-started // Sweep has its snapshot and is waiting in the checker
	newer := old
	newer.ClaimID = "second-claim"
	if err := s.Add(newer); err != nil {
		t.Fatal(err)
	}
	close(release)
	if n := <-done; n != 0 {
		t.Fatalf("the older snapshot cleared %d", n)
	}
	if len(markerFiles(t, r.markers)) != 1 || len(s.Held()) != 1 {
		t.Fatalf("marker or entry lost: %v %v", markerFiles(t, r.markers), s.Held())
	}
	if s2 := r.open(); len(s2.Held()) != 1 || s2.Held()[0].ClaimID != "second-claim" {
		t.Fatalf("the newer marker did not survive a restart: %+v", s2.Held())
	}
}

type blockingChecker struct{ started, release chan struct{} }

func (c *blockingChecker) Gone(ctx context.Context, unit string) (bool, error) {
	close(c.started)
	<-c.release
	return true, nil
}

// replacingChecker re-adds the entry while Sweep is asking about its unit.
type replacingChecker struct {
	onCall func()
}

func (c *replacingChecker) Gone(ctx context.Context, unit string) (bool, error) {
	c.onCall()
	return true, nil
}

func TestSweepDoesNotUndoAnAddOfTheSameOwner(t *testing.T) {
	r := newRig(t)
	old := r.entry("j1", "k1")
	if _, err := r.locks.Acquire(old.Owner, old.Keys, nil); err != nil {
		t.Fatal(err)
	}
	var s *Store
	newer := old
	newer.ClaimID = "second-claim"
	rc := &replacingChecker{onCall: func() {
		if err := s.Add(newer); err != nil {
			t.Error(err)
		}
	}}
	var err error
	s, err = Open(r.markers, r.locks, rc, func() time.Time { return r.now }, r.openOpts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(old); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v: the replaced entry must not be cleared", n, err)
	}
	if got := s.Held(); len(got) != 1 || got[0].ClaimID != "second-claim" {
		t.Fatalf("Held = %+v", got)
	}
	if _, held := r.locks.Holder("k1"); !held {
		t.Fatal("the newer entry lost its hold")
	}
	if len(markerFiles(t, r.markers)) != 1 {
		t.Fatalf("marker files = %v", markerFiles(t, r.markers))
	}
	if !exists(old.Dir) {
		t.Fatal("directory removed under the newer entry")
	}
}

func TestMarkerThatIsASymlinkOrBreaksTheJobRelationIsIgnored(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	name := markerFiles(t, r.markers)[0]
	genuine := filepath.Join(r.markers, name)
	raw, err := os.ReadFile(genuine)
	if err != nil {
		t.Fatal(err)
	}
	// 1. the marker file replaced by a symlink to a copy elsewhere
	copyPath := filepath.Join(t.TempDir(), "copy.json")
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(genuine); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(copyPath, genuine); err != nil {
		t.Fatal(err)
	}
	if got := r.open().Held(); len(got) != 0 {
		t.Fatalf("a symlinked marker was obeyed: %+v", got)
	}
	// 2. a unit that is not the job's own, written under the right file name
	if err := os.Remove(genuine); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), `"Unit":"wwd-j1"`, `"Unit":"sshd"`, 1)
	if bad == string(raw) {
		t.Fatalf("test setup: unit not found in %s", raw)
	}
	if err := os.WriteFile(genuine, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := r.open().Held(); len(got) != 0 {
		t.Fatalf("a marker naming someone else's unit was obeyed: %+v", got)
	}
}

func TestSymlinkedWorkRootFailsClosed(t *testing.T) {
	r := newRig(t)
	link := filepath.Join(filepath.Dir(r.root), "work-link")
	if err := os.Symlink(r.root, link); err != nil {
		t.Fatal(err)
	}
	e := r.entry("j1", "k1")
	e.Dir = filepath.Join(link, filepath.Base(e.Dir))
	r.openOpts = []Option{WithWorkRoot(link)}
	s := r.open()
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	r.checker.gone[e.Unit] = true
	if n, _ := s.Sweep(context.Background()); n != 0 {
		t.Fatalf("cleared %d under a symlinked work root", n)
	}
	if !exists(filepath.Join(r.root, "h-j1")) {
		t.Fatal("directory removed through a symlinked work root")
	}
}

func TestRestoreAfterAddInSameProcessIsHarmless(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if _, err := r.locks.Acquire("src/j1", []string{"k1"}, nil); err != nil { // the runner's hold
		t.Fatal(err)
	}
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(); err != nil {
		t.Fatal(err)
	}
	if h, _ := r.locks.Holder("k1"); h != "src/j1" {
		t.Fatalf("holder = %q", h)
	}
}

func TestSweepClearsOnlyWhenUnitIsProvablyGone(t *testing.T) {
	r := newRig(t)
	s := r.open()
	gone := r.entry("gone", "kg")
	unknown := r.entry("unknown", "ku")
	alive := r.entry("alive", "ka")
	for _, e := range []Entry{gone, unknown, alive} {
		if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	r.checker.gone["wwd-gone"] = true
	r.checker.err["wwd-unknown"] = errors.New("systemctl timed out")
	n, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cleared = %d, want 1", n)
	}
	if exists(gone.Dir) {
		t.Error("gone: directory should be removed")
	}
	if _, held := r.locks.Holder("kg"); held {
		t.Error("gone: hold should be released")
	}
	for _, e := range []Entry{unknown, alive} {
		if !exists(e.Dir) {
			t.Errorf("%s: directory must stay", e.JobID)
		}
		if _, held := r.locks.Holder(e.Keys[0]); !held {
			t.Errorf("%s: hold must stay", e.JobID)
		}
	}
	if len(s.Held()) != 2 || len(markerFiles(t, r.markers)) != 2 {
		t.Fatalf("two entries and markers must remain, got %d / %v", len(s.Held()), markerFiles(t, r.markers))
	}
}

func TestSweepNeverClearsEntryWithoutUnit(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("mac", "k1")
	e.Unit = ""
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	r.checker.gone[""] = true
	if n, _ := s.Sweep(context.Background()); n != 0 {
		t.Fatalf("cleared %d", n)
	}
	if len(r.checker.calls) != 0 {
		t.Fatalf("checker was asked about an empty unit: %v", r.checker.calls)
	}
	if !exists(e.Dir) {
		t.Fatal("directory removed")
	}
}

func TestSweepRefusesUnsafeDirs(t *testing.T) {
	r := newRig(t)
	s := r.open()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(r.root, "h-link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	nested := r.entry("nested")
	nested.Dir = filepath.Join(nested.Dir, "sub")
	if err := os.MkdirAll(nested.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"symlink":     link,
		"outside":     victim,
		"work root":   r.root,
		"nested":      nested.Dir,
		"traversal":   filepath.Join(r.root, "h-x", "..", "..", "victim"),
		"root of fs":  string(os.PathSeparator),
		"parent dots": r.root + string(os.PathSeparator) + "..",
	}
	for name, dir := range cases {
		e := Entry{Owner: "src/" + name, JobID: name, Dir: dir, Keys: []string{"k-" + name}, Unit: "wwd-" + name}
		r.checker.gone[e.Unit] = true
		if err := s.Add(e); err != nil {
			t.Fatalf("%s: Add: %v", name, err)
		}
		if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cleared %d unsafe entries", n)
	}
	if !exists(victim) || !exists(r.root) || !exists(nested.Dir) {
		t.Fatal("a directory outside the allowed shape was removed")
	}
	for name := range cases {
		if _, held := r.locks.Holder("k-" + name); !held {
			t.Errorf("%s: hold released although the directory was refused", name)
		}
	}
}

func TestSweepWithoutWorkRootFailsClosed(t *testing.T) {
	r := newRig(t)
	r.openOpts = nil
	s := r.open()
	e := r.entry("j1", "k1")
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	r.checker.gone[e.Unit] = true
	if n, _ := s.Sweep(context.Background()); n != 0 {
		t.Fatalf("cleared %d without a work root", n)
	}
	if !exists(e.Dir) {
		t.Fatal("directory removed without a work root")
	}
	if _, held := r.locks.Holder("k1"); !held {
		t.Fatal("hold released without a work root")
	}
}

func TestSweepSurvivesAlreadyRemovedDir(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("j1", "k1")
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e.Dir); err != nil {
		t.Fatal(err)
	}
	r.checker.gone[e.Unit] = true
	if n, err := s.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(s.Held()) != 0 {
		t.Fatal("entry stays")
	}
}

func TestTamperedMarkerIsIgnoredNotObeyed(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	name := markerFiles(t, r.markers)[0]
	raw, err := os.ReadFile(filepath.Join(r.markers, name))
	if err != nil {
		t.Fatal(err)
	}
	// a marker copied under another name: its file name no longer matches its owner
	if err := os.WriteFile(filepath.Join(r.markers, "deadbeef.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.markers, "garbage.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := r.open()
	if got := s2.Held(); len(got) != 1 {
		t.Fatalf("Held = %+v, want only the genuine marker", got)
	}
	// the genuine marker gone: the copy under another name must not stand in for it
	if err := os.Remove(filepath.Join(r.markers, name)); err != nil {
		t.Fatal(err)
	}
	if got := r.open().Held(); len(got) != 0 {
		t.Fatalf("a marker under the wrong file name was obeyed: %+v", got)
	}
	if !exists(filepath.Join(r.markers, "garbage.json")) || !exists(filepath.Join(r.markers, "deadbeef.json")) {
		t.Fatal("unreadable markers must be left for a human, not deleted")
	}
}

func TestClear(t *testing.T) {
	r := newRig(t)
	s := r.open()
	a, b := r.entry("a", "ka"), r.entry("b", "kb")
	for _, e := range []Entry{a, b} {
		if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Clear("a"); err != nil {
		t.Fatal(err)
	}
	if _, held := r.locks.Holder("ka"); held {
		t.Error("a: hold stays")
	}
	if exists(a.Dir) {
		t.Error("a: a safe directory is removed on manual clear")
	}
	if got := s.Held(); len(got) != 1 || got[0].JobID != "b" {
		t.Fatalf("Held = %+v", got)
	}
	if err := s.Clear("nope"); err == nil {
		t.Error("clearing an unknown job should say so")
	}
	if len(markerFiles(t, r.markers)) != 1 {
		t.Fatal("marker of a still held")
	}
}

func TestClearKeepsUnsafeDirButReleases(t *testing.T) {
	r := newRig(t)
	s := r.open()
	outside := t.TempDir()
	e := Entry{Owner: "src/x", JobID: "x", Dir: outside, Keys: []string{"kx"}}
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear("x"); err != nil {
		t.Fatal(err)
	}
	if !exists(outside) {
		t.Fatal("a directory outside the work root was removed")
	}
	if _, held := r.locks.Holder("kx"); held {
		t.Fatal("hold stays after a manual clear")
	}
}

func TestQuarantinedReportsMarkedDirs(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("j1")
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	if !s.Quarantined(e.Dir) || !s.Quarantined(e.Dir+string(os.PathSeparator)) {
		t.Fatal("marked dir not reported")
	}
	if s.Quarantined(filepath.Join(r.root, "other")) {
		t.Fatal("unmarked dir reported")
	}
}

func TestHeldReturnsCopy(t *testing.T) {
	r := newRig(t)
	s := r.open()
	if err := s.Add(r.entry("j1", "k1")); err != nil {
		t.Fatal(err)
	}
	h := s.Held()
	h[0].Keys[0] = "mutated"
	h[0].JobID = "mutated"
	if got := s.Held()[0]; got.JobID != "j1" || got.Keys[0] != "k1" {
		t.Fatalf("caller changed the store: %+v", got)
	}
}

func TestBeginWritesTheMarkerBeforeAnyUnitExistsAndSweepLeavesALiveEntryAlone(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("a", "mysql")
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil { // the runner's hold while the job runs
		t.Fatal(err)
	}
	if err := s.Begin(e); err != nil {
		t.Fatal(err)
	}
	if len(markerFiles(t, r.markers)) != 1 {
		t.Fatalf("markers %v: Begin writes the marker at once", markerFiles(t, r.markers))
	}
	r.checker.gone["wwd-a"] = true // the unit does not exist yet: "absent" must not be read as "stopped" for a run that is starting
	if n, err := s.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("cleared %d err %v: a live entry is not swept", n, err)
	}
	if _, ok := r.locks.Holder("mysql"); !ok || !exists(e.Dir) || len(markerFiles(t, r.markers)) != 1 {
		t.Fatal("the hold, the directory and the marker all stay")
	}
	if len(r.checker.calls) != 0 {
		t.Fatalf("the checker was asked about %v: nothing is asked about a run that is going on", r.checker.calls)
	}
}

func TestAddSettlesALiveEntryAndThenSweepClearsItOnceTheUnitIsGone(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("a", "mysql")
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(e); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err != nil { // the run ended and its unit could not be proven stopped
		t.Fatal(err)
	}
	if n, _ := s.Sweep(context.Background()); n != 0 {
		t.Fatal("the unit is not gone")
	}
	r.checker.gone["wwd-a"] = true
	if n, err := s.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("cleared %d err %v: once settled, a quarantined entry is cleared like any", n, err)
	}
	if _, ok := r.locks.Holder("mysql"); ok || exists(e.Dir) {
		t.Fatal("the hold and the directory go with it")
	}
}

func TestAnEntryLoadedFromDiskIsNeverLive(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("a", "mysql")
	if err := s.Begin(e); err != nil { // this process dies while the job runs
		t.Fatal(err)
	}
	s2 := r.open() // the next process
	if err := s2.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.locks.Holder("mysql"); !ok {
		t.Fatal("Restore puts the keys of a run that was going on back")
	}
	if n, _ := s2.Sweep(context.Background()); n != 0 {
		t.Fatal("the unit is still running")
	}
	r.checker.gone["wwd-a"] = true
	if n, err := s2.Sweep(context.Background()); err != nil || n != 1 || exists(e.Dir) {
		t.Fatalf("cleared %d err %v: with the unit gone the leftovers of a killed daemon's job go", n, err)
	}
}

func TestDiscardForgetsTheMarkerButNeverTheHoldOrTheDirectory(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("a", "mysql")
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(e); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(e.Owner); err != nil {
		t.Fatal(err)
	}
	if len(markerFiles(t, r.markers)) != 0 || len(s.Held()) != 0 {
		t.Fatalf("markers %v held %v", markerFiles(t, r.markers), s.Held())
	}
	if _, ok := r.locks.Holder("mysql"); !ok || !exists(e.Dir) {
		t.Fatal("the hold is the runner's and the directory the executor's")
	}
	s2 := r.open() // a restart finds nothing to put back
	if err := s2.Restore(); err != nil || len(s2.Held()) != 0 {
		t.Fatalf("restore err %v held %v", err, s2.Held())
	}
}

func TestDiscardOfAnUnknownOwnerIsNothingAndLeavesAnUnreadableMarkerForAHuman(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(r.markers, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(r.markers, markerName("src/broken"))
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := r.open()
	if err := s.Discard("src/broken"); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard("src/nobody"); err != nil {
		t.Fatal(err)
	}
	if !exists(bad) {
		t.Fatal("a marker that could not be read is kept on purpose; Discard only drops entries it loaded")
	}
}

func TestBeginRejectsABadEntryLikeAddDoes(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("a")
	e.Unit = "wwd-other"
	if err := s.Begin(e); err == nil {
		t.Fatal("want an error")
	}
	if len(markerFiles(t, r.markers)) != 0 {
		t.Fatal("nothing written")
	}
}
