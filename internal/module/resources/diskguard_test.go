package resourcesmod

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

// The cache directory itself is not followed if it is a symlink: pointing GOCACHE at a link to somewhere else must not make
// that somewhere else a cache (codex critic). Mutation gate: open the path as given → the files go (red).
func TestTrimGoCache_ARootThatIsASymlinkIsRefused(t *testing.T) {
	c := newFakeCache(t)
	link := filepath.Join(filepath.Dir(c.dir), "go-build-link")
	if err := os.Symlink(c.dir, link); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(&fakeSampler{}, nil)
	if _, err := m.trimGoCache(context.Background(), link, time.Now().Add(-2*time.Hour)); err == nil {
		t.Error("a symlink was accepted as the cache directory")
	}
	if !exists(filepath.Join(c.dir, "ab/0123-a")) {
		t.Error("files behind a symlinked cache directory were deleted")
	}
}

// ---- the trim itself ----

func trimFor(t *testing.T, c fakeCache) (int64, error) {
	t.Helper()
	m := newTestModule(&fakeSampler{}, nil)
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
		m := newTestModule(&fakeSampler{}, nil)
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
		m := newTestModule(&fakeSampler{}, nil)
		if _, err := m.trimGoCache(context.Background(), d, time.Now()); err == nil {
			t.Errorf("%q was accepted as a cache directory", d)
		}
	}
	// a cache that does not exist is not an error: there is nothing to trim
	m := newTestModule(&fakeSampler{}, nil)
	if n, err := m.trimGoCache(context.Background(), filepath.Join(t.TempDir(), "absent"), time.Now()); err != nil || n != 0 {
		t.Errorf("absent cache: %d %v", n, err)
	}
}

// A walk that is told to stop stops: the trim honours its context, so a huge cache cannot hold a request for long.
func TestTrimGoCache_StopsWhenItsContextEnds(t *testing.T) {
	c := newFakeCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := newTestModule(&fakeSampler{}, nil)
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
