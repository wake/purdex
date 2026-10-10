package resourcesmod

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// #2470: before a heavy lease is granted, the volume that holds the Go build cache is looked at; below the watermark the
// cache's old entries are trimmed first (at most once per 10 minutes, off the admission lock), and a volume still under the
// hard floor afterwards is granted anyway with a warning. Every test uses t.TempDir() and an injected free-space function.

const (
	diskGiB = int64(1) << 30
	ageOld  = 3 * time.Hour    // older than the 2 h horizon
	ageNew  = 30 * time.Minute // younger
)

func put(t *testing.T, path string, age time.Duration, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// fakeCache lays out a build cache: old and recent entries in two-hex-digit directories, Go's own top-level files, and
// things that must never be touched. It returns the cache dir and the directory outside it.
type fakeCache struct{ dir, outside string }

func newFakeCache(t *testing.T) fakeCache {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := fakeCache{dir: filepath.Join(base, "go-build"), outside: filepath.Join(base, "outside")}
	put(t, filepath.Join(c.dir, "ab", "0123-a"), ageOld, 100)
	put(t, filepath.Join(c.dir, "ab", "0123-d"), ageOld, 4000)
	put(t, filepath.Join(c.dir, "ab", "4567-d"), ageNew, 100) // recent: kept
	put(t, filepath.Join(c.dir, "ff", "89ab-a"), ageOld, 100)
	put(t, filepath.Join(c.dir, "trim.txt"), ageOld, 10) // Go's own top-level files are not entries
	put(t, filepath.Join(c.dir, "README"), ageOld, 10)
	put(t, filepath.Join(c.dir, "zz", "x-a"), ageOld, 10)    // not a two-hex-digit directory
	put(t, filepath.Join(c.dir, "abc", "x-a"), ageOld, 10)   // nor is this
	put(t, filepath.Join(c.outside, "precious"), ageOld, 10) // the thing a symlink would point at
	put(t, filepath.Join(c.outside, "dir", "inner"), ageOld, 10)
	return c
}

// diskFix is a route fixture whose disk guard is on, with the free space and the cache dir injected.
type diskFix struct {
	*routeFix
	cache fakeCache
	free  atomic.Int64
	stats atomic.Int32 // how many times the free space was asked
}

func newDiskFix(t *testing.T, freeBytes int64) *diskFix {
	f := &diskFix{routeFix: newRouteFix(t, resources.ModeLease), cache: newFakeCache(t)}
	f.free.Store(freeBytes)
	f.m.diskFree = func(string) (int64, error) { f.stats.Add(1); return f.free.Load(), nil }
	f.m.goCacheDir = func() string { return f.cache.dir }
	return f
}

func (f *diskFix) heavy(cid string) resources.LeaseResponse {
	f.t.Helper()
	rec := f.post(cid, "test-full", 0)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("post: %d %s", rec.Code, rec.Body.String())
	}
	return decodeLease(f.t, rec)
}

func (f *diskFix) release(r resources.LeaseResponse) {
	f.t.Helper()
	if rec := f.do(http.MethodDelete, "/api/resources/leases/"+r.ID, nil); rec.Code != http.StatusOK {
		f.t.Fatalf("release: %d", rec.Code)
	}
}

// Below the watermark: old entries go, recent ones stay, and the lease is granted. Mutation gate: no trim → red.
func TestDiskGuard_BelowTheWatermarkTrimsOldEntriesThenGrants(t *testing.T) {
	f := newDiskFix(t, 10*diskGiB)
	r := f.heavy(cidA)
	if r.State != resources.StateHeld || !r.Granted {
		t.Fatalf("lease = %+v", r)
	}
	c := f.cache.dir
	for _, p := range []string{"ab/0123-a", "ab/0123-d", "ff/89ab-a"} {
		if exists(filepath.Join(c, p)) {
			t.Errorf("old entry %s was kept", p)
		}
	}
	if !exists(filepath.Join(c, "ab/4567-d")) {
		t.Error("a recent entry was deleted")
	}
	if f.logs.count("trimmed") != 1 {
		t.Errorf("want one log line naming the bytes freed, got %v", f.logs.lines)
	}
}

// Above the watermark: nothing is deleted. Mutation gate: always trim → red.
func TestDiskGuard_AboveTheWatermarkLeavesTheCacheAlone(t *testing.T) {
	f := newDiskFix(t, 40*diskGiB)
	f.heavy(cidA)
	if !exists(filepath.Join(f.cache.dir, "ab/0123-a")) {
		t.Fatal("the cache was trimmed with plenty of room")
	}
	if f.logs.count("trimmed") != 0 {
		t.Errorf("a trim was logged: %v", f.logs.lines)
	}
}

// Only the heavy kinds look at the disk: a light lease (no kind, small weight) does not even ask.
func TestDiskGuard_LightLeasesDoNotLookAtTheDisk(t *testing.T) {
	f := newDiskFix(t, 1*diskGiB)
	rec := f.post(cidA, "", 5)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post: %d", rec.Code)
	}
	if f.stats.Load() != 0 || !exists(filepath.Join(f.cache.dir, "ab/0123-a")) {
		t.Fatalf("a light lease looked at the disk (%d) or trimmed", f.stats.Load())
	}
	for _, kind := range []string{"test-pkg", "build", "lint-full"} { // the other guarded kinds ask
		before := f.stats.Load()
		cid := map[string]string{"test-pkg": cidB, "build": cidC, "lint-full": "44444444-4444-4444-8444-444444444444"}[kind]
		if rec := f.post(cid, kind, 0); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d", kind, rec.Code)
		}
		if f.stats.Load() == before {
			t.Errorf("%s did not look at the disk", kind)
		}
	}
}

// At most one trim per 10 minutes: a second heavy lease right after finds the cache as the first left it; once 10
// minutes have passed a new trim runs. Mutation gate: no rate limit → the second trim deletes the re-created file (red).
func TestDiskGuard_TrimsAreRateLimited(t *testing.T) {
	f := newDiskFix(t, 10*diskGiB)
	r := f.heavy(cidA)
	f.release(r)
	again := filepath.Join(f.cache.dir, "ab", "again-a")
	put(t, again, ageOld, 10)
	f.clock.ms.Add((9*time.Minute + 59*time.Second).Milliseconds())
	r = f.heavy(cidB)
	f.release(r)
	if !exists(again) {
		t.Fatal("a second trim ran inside 10 minutes")
	}
	f.clock.ms.Add((2 * time.Second).Milliseconds())
	f.heavy(cidC)
	if exists(again) {
		t.Fatal("no trim after the 10 minutes")
	}
}

// Two heavy requests meeting a low disk at once: one walk, the other does not wait for it or start a second.
func TestDiskGuard_OneTrimAtATime(t *testing.T) {
	f := newDiskFix(t, 10*diskGiB)
	var walks atomic.Int32
	gate := make(chan struct{})
	f.m.trimHook = func() { walks.Add(1); <-gate }
	done := make(chan struct{})
	go func() { f.heavy(cidA); close(done) }()
	waitFor(t, "the first walk to start", func() bool { return walks.Load() == 1 })
	second := make(chan resources.LeaseResponse, 1)
	go func() { rec := f.post(cidB, "test-full", 0); second <- decodeLease(t, rec) }()
	select {
	case <-second: // answered (granted or queued) without waiting for the walk
	case <-time.After(3 * time.Second):
		t.Fatal("a second request waited for the first request's walk")
	}
	close(gate)
	<-done
	if walks.Load() != 1 {
		t.Fatalf("%d walks", walks.Load())
	}
}

// The walk does not hold the admission lock (stateMu): a transition on the rows is not kept waiting by a slow trim.
// Mutation gate: trim inside passOnce's lock → TryLock fails (red).
func TestDiskGuard_TheWalkDoesNotHoldTheAdmissionLock(t *testing.T) {
	f := newDiskFix(t, 10*diskGiB)
	gate := make(chan struct{})
	var inWalk atomic.Bool
	f.m.trimHook = func() { inWalk.Store(true); <-gate }
	done := make(chan struct{})
	go func() { f.heavy(cidA); close(done) }()
	waitFor(t, "the walk", inWalk.Load)
	if !f.m.stateMu.TryLock() {
		t.Fatal("stateMu is held during the walk")
	}
	f.m.stateMu.Unlock()
	close(gate)
	<-done
}

// Still under the hard floor after the trim: granted all the same, with a warning in the log and in the answer. Above the
// floor (but under the watermark) there is no warning. Mutation gate: refuse or stay silent → red.
func TestDiskGuard_UnderTheHardFloorGrantsWithAWarning(t *testing.T) {
	f := newDiskFix(t, 2*diskGiB)
	r := f.heavy(cidA)
	if !r.Granted || r.State != resources.StateHeld || !strings.Contains(r.Warning, "disk") {
		t.Fatalf("lease = %+v", r)
	}
	if f.logs.count("low on disk") == 0 {
		t.Errorf("no warning in the log: %v", f.logs.lines)
	}
	g := newDiskFix(t, 5*diskGiB)
	if r := g.heavy(cidA); r.Warning != "" {
		t.Errorf("a warning with 5 GiB free: %q", r.Warning)
	}
	h := newDiskFix(t, 40*diskGiB)
	if r := h.heavy(cidA); r.Warning != "" {
		t.Errorf("a warning with 40 GiB free: %q", r.Warning)
	}
}

// A volume that frees up after the trim is judged on what is there after it.
func TestDiskGuard_TheWarningIsJudgedAfterTheTrim(t *testing.T) {
	f := newDiskFix(t, 2*diskGiB)
	f.m.trimHook = func() { f.free.Store(20 * diskGiB) } // the trim frees space
	if r := f.heavy(cidA); r.Warning != "" {
		t.Fatalf("warned although the trim brought the volume above the floor: %q", r.Warning)
	}
}

// ---- the trim itself ----

func trimFor(t *testing.T, c fakeCache) (int64, error) {
	t.Helper()
	m := newTestModule(idleSampler(), nil)
	return m.trimGoCache(context.Background(), c.dir, time.Now().Add(-2*time.Hour))
}

func TestTrimGoCache_DeletesOldKeepsRecentAndReportsBytes(t *testing.T) {
	c := newFakeCache(t)
	freed, err := trimFor(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if freed != 100+4000+100 {
		t.Errorf("freed %d bytes, want 4200", freed)
	}
	if !exists(filepath.Join(c.dir, "ab/4567-d")) {
		t.Error("a recent entry was deleted")
	}
}

// The age is strict: 2 hours less a second is kept, 2 hours and a second is gone.
func TestTrimGoCache_TheHorizonIsTwoHours(t *testing.T) {
	c := newFakeCache(t)
	put(t, filepath.Join(c.dir, "cd", "young-a"), 2*time.Hour-time.Minute, 10)
	put(t, filepath.Join(c.dir, "cd", "older-a"), 2*time.Hour+time.Minute, 10)
	if _, err := trimFor(t, c); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(c.dir, "cd/young-a")) || exists(filepath.Join(c.dir, "cd/older-a")) {
		t.Fatal("the 2 h horizon is wrong")
	}
}

// The entry directories are exactly two lower-case hex digits (checked on the name alone: a case-insensitive volume cannot
// hold both "ab" and "AB"). Mutation gate: accept any two characters, or upper case → red.
func TestHexDir(t *testing.T) {
	for name, want := range map[string]bool{"ab": true, "00": true, "ff": true, "9e": true, "AB": false, "aB": false, "a": false,
		"abc": false, "zz": false, "g0": false, "": false, "a/": false, "..": false, ".a": false} {
		if hexDir(name) != want {
			t.Errorf("hexDir(%q) = %v, want %v", name, !want, want)
		}
	}
}

// Nothing outside the cache's two-hex-digit directories is ever deleted, and no symlink is followed. Mutation gate: follow
// symlinks (os.Stat / RemoveAll) → the outside files go (red); accept any directory name → zz/ abc/ AB/ go (red).
func TestTrimGoCache_NeverTouchesAnythingElse(t *testing.T) {
	c := newFakeCache(t)
	// a two-hex-digit directory that is a symlink to elsewhere
	if err := os.Symlink(c.outside, filepath.Join(c.dir, "ee")); err != nil {
		t.Fatal(err)
	}
	// symlinks inside a real two-hex-digit directory: to a file, to a directory, dangling
	if err := os.Symlink(filepath.Join(c.outside, "precious"), filepath.Join(c.dir, "ab", "link-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(c.outside, "dir"), filepath.Join(c.dir, "ab", "dirlink-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(c.outside, "gone"), filepath.Join(c.dir, "ab", "dangling-a")); err != nil {
		t.Fatal(err)
	}
	// a directory inside an entry directory, with an old file in it
	put(t, filepath.Join(c.dir, "ab", "subdir", "deep-a"), ageOld, 10)
	// give the symlink targets' mtimes the old age too (the files were created old), then trim
	if _, err := trimFor(t, c); err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{
		filepath.Join(c.outside, "precious"), filepath.Join(c.outside, "dir", "inner"),
		filepath.Join(c.dir, "trim.txt"), filepath.Join(c.dir, "README"),
		filepath.Join(c.dir, "zz", "x-a"), filepath.Join(c.dir, "abc", "x-a"),
		filepath.Join(c.dir, "ee"), filepath.Join(c.dir, "ab", "subdir", "deep-a"),
		filepath.Join(c.dir, "ab", "link-a"), filepath.Join(c.dir, "ab", "dirlink-a"), filepath.Join(c.dir, "ab", "dangling-a"),
	} {
		if !exists(keep) {
			t.Errorf("%s was deleted", keep)
		}
	}
}

// A cache directory that is not an absolute path, or is the filesystem root or the home directory, is refused: a bad
// GOCACHE must not turn the trim into something else.
func TestTrimGoCache_RefusesADirectoryThatIsNotACache(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, d := range []string{"", "/", "relative/go-build", ".", home} {
		m := newTestModule(idleSampler(), nil)
		if _, err := m.trimGoCache(context.Background(), d, time.Now()); err == nil {
			t.Errorf("%q was accepted as a cache directory", d)
		}
	}
	// a cache that does not exist is not an error: there is nothing to trim
	m := newTestModule(idleSampler(), nil)
	if n, err := m.trimGoCache(context.Background(), filepath.Join(t.TempDir(), "absent"), time.Now()); err != nil || n != 0 {
		t.Errorf("absent cache: %d %v", n, err)
	}
}

// A walk that is told to stop stops: the trim honours its context, so a huge cache cannot hold a request for long.
func TestTrimGoCache_StopsWhenItsContextEnds(t *testing.T) {
	c := newFakeCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := newTestModule(idleSampler(), nil)
	if _, err := m.trimGoCache(ctx, c.dir, time.Now().Add(-2*time.Hour)); err == nil {
		t.Error("a cancelled trim reported success")
	}
	if !exists(filepath.Join(c.dir, "ab/0123-a")) {
		t.Error("a cancelled trim deleted files")
	}
}

// ---- where the cache is ----

func TestGoCacheDir_ComesFromGoEnvAndFallsBack(t *testing.T) {
	want := t.TempDir()
	t.Setenv("GOCACHE", want)
	if got := resolveGoCacheDir(); got != want {
		t.Errorf("GOCACHE elsewhere: %q, want %q", got, want)
	}
	// no go on PATH and no GOCACHE: the platform's cache directory
	t.Setenv("GOCACHE", "")
	t.Setenv("PATH", t.TempDir())
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache dir")
	}
	if got, wantFB := resolveGoCacheDir(), filepath.Join(cache, "go-build"); got != wantFB {
		t.Errorf("fallback: %q, want %q", got, wantFB)
	}
}

// The free-space reading is of the volume that holds the cache, and a cache that is not there yet is looked up at its
// nearest existing parent.
func TestFreeBytes_ReadsTheVolumeOfTheNearestExistingParent(t *testing.T) {
	n, err := freeBytesOf(filepath.Join(t.TempDir(), "not", "yet", "there"))
	if err != nil || n <= 0 {
		t.Fatalf("free = %d, %v", n, err)
	}
}

var _ = sync.Mutex{}
