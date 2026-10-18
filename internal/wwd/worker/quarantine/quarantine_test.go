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
	base := t.TempDir()
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
	if err := s2.Restore(); err != nil {
		t.Fatal(err)
	}
	if len(markerFiles(t, r.markers)) != 1 || len(s2.Held()) != 1 {
		t.Fatal("marker must stay when its key is taken")
	}
	if h, _ := r.locks.Holder("k1"); h != "src/intruder" {
		t.Fatalf("Restore stole the key: holder %q", h)
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
