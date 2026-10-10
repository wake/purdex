package resourcesmod

import (
	"context"
	"fmt"
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

// goCacheReadme is what `go` writes into a build cache it creates; the trim refuses a directory that does not have it.
const goCacheReadme = "This directory holds cached build artifacts from the Go build system.\nRun \"go clean -cache\" if the directory is getting too large.\n"

func putText(t *testing.T, path, text string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
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
	putText(t, filepath.Join(c.dir, "README"), goCacheReadme, ageOld)
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
// After Stop no trim is started (and nothing is added to the wait group a Close is waiting on): a request still in flight
// at shutdown must not start a goroutine behind it (codex attack). Run under -race.
func TestDiskGuard_NoTrimStartsAfterStop(t *testing.T) {
	f := newDiskFix(t, 10*diskGiB)
	var walks atomic.Int32
	f.m.trimHook = func() { walks.Add(1) }
	f.m.markStopped()
	f.m.wg.Wait() // what Close does
	rec := f.post(cidA, "test-full", 0)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post: %d", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if walks.Load() != 0 || !exists(filepath.Join(f.cache.dir, "ab/0123-a")) {
		t.Fatalf("a trim started after Stop (%d walks)", walks.Load())
	}
}

// The warning is a fact about the grant, kept with the lease: a lease granted under the floor still shows it on a later
// poll, and a lease granted while the disk was healthy never gets one because the disk got low afterwards (codex critic).
// Mutation gate: a global standing warning read at answer time → the second assertion (or the first, later) is red.
func TestDiskGuard_TheWarningIsTheGrantsAndStaysWithIt(t *testing.T) {
	f := newDiskFix(t, 40*diskGiB)
	healthy := f.heavy(cidA)
	if healthy.Warning != "" {
		t.Fatalf("a warning with 40 GiB free: %q", healthy.Warning)
	}
	f.free.Store(2 * diskGiB)
	low := f.heavy(cidB)
	if low.Warning == "" {
		t.Fatal("no warning on a lease granted under the floor")
	}
	get := func(id string) resources.LeaseResponse {
		return decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+id, nil))
	}
	f.clock.ms.Add((10 * time.Minute).Milliseconds())
	f.free.Store(40 * diskGiB) // the disk recovered
	if got := get(low.ID); got.Warning == "" {
		t.Errorf("the grant's warning was lost on a later poll: %+v", got)
	}
	if got := get(healthy.ID); got.Warning != "" {
		t.Errorf("a lease granted on a healthy disk got a warning later: %q", got.Warning)
	}
	// and a replay of the POST gives the same answer
	rec := f.post(cidB, "test-full", 0)
	if got := decodeLease(t, rec); rec.Code != http.StatusOK || got.Warning == "" {
		t.Errorf("replay: %d %+v", rec.Code, got)
	}
}

// A pass nobody asked for (the sampler's, the sweeper's) that finds the disk low starts the trim and does not wait for it,
// but it does not grant a guarded lease before the trim is done either: the grant comes from the pass the trim runs when it
// ends, with the disk judged after it (codex critic). Mutation gate: grant while the trim runs → red.
func TestDiskGuard_ABackgroundPassDoesNotGrantBeforeTheTrimEnds(t *testing.T) {
	f := newDiskFix(t, 2*diskGiB)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release) // a failing assertion must not leave the walk parked: Stop waits for it
	var inWalk atomic.Bool
	f.m.trimHook = func() { inWalk.Store(true); <-gate }
	f.set.set(resources.Settings{Mode: resources.ModeLease})
	r := baseRow("bg1", "c-bg1")
	r.Kind, r.Weight = "test-full", 35
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-1000, f.nowMS()+300000, f.nowMS()+30000
	mustCreate(t, f.m.store, r)
	f.m.admissionPass(context.Background(), "") // what the sampler and the sweeper do
	waitFor(t, "the walk to start", inWalk.Load)
	f.m.admissionPass(context.Background(), "") // and again, while it runs
	if st := f.state("bg1"); st != resources.StateWaiting {
		t.Fatalf("granted before the trim ended: %s", st)
	}
	release()
	waitFor(t, "the grant after the trim", func() bool { return f.state("bg1") == resources.StateHeld })
	if w := f.m.warnFor(f.row("bg1")); w == "" {
		t.Error("granted under the floor without a warning")
	}
}

// The cache directory itself is not followed if it is a symlink: pointing GOCACHE at a link to somewhere else must not make
// that somewhere else a cache (codex critic). Mutation gate: open the path as given → the files go (red).
func TestTrimGoCache_ARootThatIsASymlinkIsRefused(t *testing.T) {
	c := newFakeCache(t)
	link := filepath.Join(filepath.Dir(c.dir), "go-build-link")
	if err := os.Symlink(c.dir, link); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(idleSampler(), nil)
	if _, err := m.trimGoCache(context.Background(), link, time.Now().Add(-2*time.Hour)); err == nil {
		t.Error("a symlink was accepted as the cache directory")
	}
	if !exists(filepath.Join(c.dir, "ab/0123-a")) {
		t.Error("files behind a symlinked cache directory were deleted")
	}
}

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

// Only a directory that is really a Go build cache is trimmed (codex attack): its README is the one `go` writes. A directory
// with two-hex-digit subdirectories and old files but no such README (a project, a data folder, a GOCACHE pointed at the wrong
// place) is refused and nothing in it is touched. Mutation gate: skip the marker check → the files go (red).
func TestTrimGoCache_OnlyADirectoryThatIsAGoCache(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	cases := map[string]func(dir string){
		"no README":      func(string) {},
		"another README": func(dir string) { putText(t, filepath.Join(dir, "README"), "my notes\n", ageOld) },
		"a long README that is not go's": func(dir string) {
			putText(t, filepath.Join(dir, "README"), "This directory holds the notes of my project, not Go's cache, and it is long enough.\n", ageOld)
		},
		"a README that is a symlink": func(dir string) {
			putText(t, filepath.Join(base, "elsewhere-README"), goCacheReadme, ageOld)
			if err := os.Symlink(filepath.Join(base, "elsewhere-README"), filepath.Join(dir, "README")); err != nil {
				t.Fatal(err)
			}
		},
		"a README that is a directory": func(dir string) { mustMkdirAll(t, filepath.Join(dir, "README")) },
	}
	i := 0
	for name, setup := range cases {
		i++
		dir := filepath.Join(base, fmt.Sprintf("userdata%d", i))
		put(t, filepath.Join(dir, "ab", "report.pdf"), ageOld, 10) // data of the user that happens to sit in a "ab" folder
		setup(dir)
		m := newTestModule(idleSampler(), nil)
		if _, err := m.trimGoCache(context.Background(), dir, time.Now().Add(-2*time.Hour)); err == nil {
			t.Errorf("%s: accepted as a Go cache", name)
		}
		if !exists(filepath.Join(dir, "ab", "report.pdf")) {
			t.Errorf("%s: a file was deleted from a directory that is not a Go cache", name)
		}
	}
}

func mustMkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
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
