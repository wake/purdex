package jobexec

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shared build cache is kept by how long an entry has gone unused (#3748): Go touches an entry's file time when it uses it (at most
// once an hour), so an entry whose time is old is one nobody has asked for. A pass removes those; over its bound the cache is trimmed
// from the oldest entry down, and only a cache that cannot be trimmed is emptied. A trim does not need the cache to be free: Go's own
// trim removes files while other go processes run, and a file that went away is a miss.

// cacheFile writes an entry of n bytes in the cache with its file time age before now.
func cacheFile(t *testing.T, cache, rel string, n int, age time.Duration) string {
	t.Helper()
	p := filepath.Join(cache, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, make([]byte, n), 0o644))
	at := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(p, at, at))
	return p
}

// firstJobCache runs a job to make the cache, lets it go, and returns the cache directory.
func firstJobCache(t *testing.T, r *goRig, repo, sha string) string {
	t.Helper()
	e := r.exec(t, repo, sha)
	j := r.job(t, e, "j_first")
	cache := envValue2(j.env, "GOCACHE")
	require.NoError(t, j.ws.Cleanup())
	return cache
}

func TestAnEntryUnusedForLongerThanTheLimitIsRemovedAndAFreshOneStays(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	old := cacheFile(t, cache, "ab/old-a", 1000, 48*time.Hour)
	older := cacheFile(t, cache, "cd/old-d", 1000, 26*time.Hour)
	fresh := cacheFile(t, cache, "ab/fresh-a", 1000, 2*time.Hour)
	trim := cacheFile(t, cache, "trim.txt", 20, 90*24*time.Hour) // Go's own bookkeeping is not an entry
	readme := cacheFile(t, cache, "README", 20, 90*24*time.Hour)

	j := r.job(t, r.exec(t, repo, sha), "j_second")
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"))
	assert.NoFileExists(t, old)
	assert.NoFileExists(t, older)
	assert.FileExists(t, fresh)
	assert.FileExists(t, trim, "Go's trim.txt is left to Go")
	assert.FileExists(t, readme)
}

func TestTheUnusedLimitIsTheWorkersSetting(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCachePruneH = 2
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	three := cacheFile(t, cache, "ab/three", 1000, 4*time.Hour)
	one := cacheFile(t, cache, "ab/one", 1000, time.Hour)
	r.job(t, r.exec(t, repo, sha), "j_second")
	assert.NoFileExists(t, three)
	assert.FileExists(t, one)
}

// Go updates an entry's file time only when it is an hour old, so an entry used a moment ago can show a time almost an hour old: the limit
// counts from the use, and the hour is added. With the smallest setting (1) an entry 90 minutes old may have been used 30 minutes ago.
func TestTheUnusedLimitAllowsForGosHourOfFileTimeResolution(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCachePruneH = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	recentUse := cacheFile(t, cache, "ab/ninety", 1000, 90*time.Minute)
	idle := cacheFile(t, cache, "ab/three", 1000, 3*time.Hour)
	r.job(t, r.exec(t, repo, sha), "j_second")
	assert.FileExists(t, recentUse, "90 minutes on the file may be a use half an hour ago")
	assert.NoFileExists(t, idle)
}

// The age pass runs at most once an hour (Go's own file times are that coarse); the size is looked at more often.
func TestTheAgePassRunsAtMostOnceAnHour(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	e := r.exec(t, repo, sha)
	clock := time.Now()
	e.goCache.now = func() time.Time { return clock }
	j1 := r.job(t, e, "j_one") // the first pass
	require.NoError(t, j1.ws.Cleanup())
	late := cacheFile(t, cache, "ab/aged-in", 1000, 30*time.Hour) // it became old after the pass (its time is set old here)

	clock = clock.Add(5 * time.Minute) // looked at again (the size), but the age pass is not due
	j2 := r.job(t, e, "j_two")
	require.NoError(t, j2.ws.Cleanup())
	assert.FileExists(t, late, "no age pass within the hour")

	clock = clock.Add(time.Hour)
	r.job(t, e, "j_three")
	assert.NoFileExists(t, late, "the next one after the hour")
}

// Over its bound, nothing is old enough to go, and the cache is trimmed from the oldest entry to 80% of the bound: the newest stay.
func TestAnOverBoundCacheIsTrimmedFromTheOldestNotEmptied(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	var files []string
	for i, age := range []time.Duration{5 * time.Hour, 4 * time.Hour, 3 * time.Hour, 2 * time.Hour, time.Hour} {
		files = append(files, cacheFile(t, cache, filepath.Join("ab", "e"+string(rune('0'+i))), 300<<10, age)) // 5 x 300 KiB over a 1 MiB bound
	}
	j := r.job(t, r.exec(t, repo, sha), "j_second")
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"))
	for _, f := range files[:3] {
		assert.NoFileExists(t, f, "the three oldest go: 600 KiB is under 80% of 1 MiB")
	}
	for _, f := range files[3:] {
		assert.FileExists(t, f, "the two newest stay")
	}
}

// A job holds the cache: it is trimmed all the same (no free lock is needed), and the new job gets the cache.
func TestABusyCacheOverItsBoundIsTrimmedWhileAJobHoldsIt(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	holder := r.job(t, r.exec(t, repo, sha), "j_busy")
	cache := envValue2(holder.env, "GOCACHE")
	oldest := cacheFile(t, cache, "ab/oldest", 2<<20, 5*time.Hour)
	newest := cacheFile(t, cache, "ab/newest", 200<<10, time.Hour)
	j := r.job(t, r.exec(t, repo, sha), "j_new") // another worker process: its own throttle
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"), "trimmed under 2x, so it is given")
	assert.NoFileExists(t, oldest)
	assert.FileExists(t, newest)
}

// A cache that cannot be trimmed under its bound (a file that will not go) is emptied, as before, when nobody holds it.
func TestACacheThatCannotBeTrimmedIsEmptied(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	big := cacheFile(t, cache, "ab/big", 3<<20, time.Hour)
	old := goCacheRemoveFile
	goCacheRemoveFile = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { goCacheRemoveFile = old })
	j := r.job(t, r.exec(t, repo, sha), "j_second")
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"))
	assert.NoFileExists(t, big, "emptied")
	assert.DirExists(t, cache)
}

// A file that went away between the walk and the delete (another worker, Go's own trim) is nothing.
func TestAFileThatIsGoneBeforeItIsRemovedIsNotAnError(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	cacheFile(t, cache, "ab/vanishing", 1000, 48*time.Hour)
	old := goCacheRemoveFile
	goCacheRemoveFile = func(p string) error { _ = os.Remove(p); return os.Remove(p) } // the second remove finds nothing
	t.Cleanup(func() { goCacheRemoveFile = old })
	j := r.job(t, r.exec(t, repo, sha), "j_second")
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"))
	assert.DirExists(t, cache, "not emptied for it")
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	}))
	return n
}

func withRemovalBudget(t *testing.T, n int) {
	old := goCacheMaxRemovals
	goCacheMaxRemovals = n
	t.Cleanup(func() { goCacheMaxRemovals = old })
}

// One pass removes at most so many files (a pass is in the preparation of a job); the rest go in the next passes, two minutes on, and the
// age pass is taken up again until it has gone through the list.
func TestAPassRemovesAtMostABudgetOfFilesAndTheNextOnesGoOn(t *testing.T) {
	withRemovalBudget(t, 100)
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	for i := 0; i < 400; i++ {
		cacheFile(t, cache, filepath.Join("ab", "old-"+strconv.Itoa(i)), 10, 48*time.Hour)
	}
	fresh := cacheFile(t, cache, "cd/fresh", 10, time.Hour)
	before := countFiles(t, cache)
	e := r.exec(t, repo, sha)
	clock := time.Now()
	e.goCache.now = func() time.Time { return clock }
	for pass := 1; pass <= 4; pass++ {
		j := r.job(t, e, "j_"+strconv.Itoa(pass))
		require.NoError(t, j.ws.Cleanup())
		assert.Equal(t, before-100*pass, countFiles(t, cache), "pass %d removes 100", pass)
		clock = clock.Add(3 * time.Minute) // the size is looked at again; the hour is not up
	}
	assert.FileExists(t, fresh)
	assert.DirExists(t, cache, "not emptied")
	j := r.job(t, e, "j_after")
	require.NoError(t, j.ws.Cleanup())
	assert.Equal(t, before-400, countFiles(t, cache), "nothing left to remove")
}

// Over its bound with more to remove than a pass may: the cache is not emptied for it, it is trimmed over the next passes.
func TestAnOverBoundCacheIsTrimmedOverSeveralPassesNotEmptied(t *testing.T) {
	withRemovalBudget(t, 10)
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	for i := 0; i < 30; i++ { // 30 x 100 KiB = 3 MiB over a 1 MiB bound: each pass removes 10, and a trim that began goes on to 80% (8 files) even when the bound is no longer passed
		cacheFile(t, cache, filepath.Join("ab", "e"+strconv.Itoa(i)), 100<<10, time.Duration(31-i)*time.Minute)
	}
	e := r.exec(t, repo, sha)
	clock := time.Now()
	e.goCache.now = func() time.Time { return clock }
	want := []int{20, 10, 8}
	for pass, n := range want {
		j := r.job(t, e, "j_"+strconv.Itoa(pass))
		require.NoError(t, j.ws.Cleanup())
		assert.Equal(t, n, countFiles(t, cache), "after pass %d", pass+1)
		clock = clock.Add(3 * time.Minute)
	}
	assert.FileExists(t, filepath.Join(cache, "ab", "e29"), "the newest stays")
	assert.NoFileExists(t, filepath.Join(cache, "ab", "e0"), "the oldest went first")
}

// Files that will not go use up a pass's budget as much as files that do: a cache over its bound where nothing could be removed is not
// left to "the next pass" for ever; it is emptied.
func TestACacheWhoseRemovalsAllFailPastTheBudgetIsStillEmptied(t *testing.T) {
	withRemovalBudget(t, 5)
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	var files []string
	for i := 0; i < 20; i++ { // 20 x 200 KiB over a 1 MiB bound, more files than the budget
		files = append(files, cacheFile(t, cache, filepath.Join("ab", "e"+strconv.Itoa(i)), 200<<10, time.Hour))
	}
	old := goCacheRemoveFile
	goCacheRemoveFile = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { goCacheRemoveFile = old })
	j := r.job(t, r.exec(t, repo, sha), "j_second")
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"))
	for _, f := range files {
		assert.NoFileExists(t, f, "emptied")
	}
	assert.DirExists(t, cache)
}

// The state of a look (when it was, whether an age pass is due, whether a trim is on, whether the cache was refused) belongs to the cache
// directory it was about: a worker that goes on to another Go version does not carry it over (#3760 attack).
func TestTheStateOfALookBelongsToItsCacheDirectory(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	e := r.exec(t, repo, sha)
	holder := r.job(t, e, "j_busy")
	a := envValue2(holder.env, "GOCACHE")
	cacheFile(t, a, "ab/big", 3<<20, 5*time.Hour)
	old := goCacheRemoveFile
	goCacheRemoveFile = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { goCacheRemoveFile = old })
	clock := time.Now().Add(10 * time.Minute) // past the look the first job made
	e.goCache.now = func() time.Time { return clock }
	cold := r.job(t, e, "j_cold")
	require.Empty(t, envValue2(cold.env, "GOCACHE"), "the first cache is over twice its bound and cannot be trimmed: refused")

	e.goCache.versionOverride = "go version go1.27.0 darwin/arm64" // another Go: another directory, a minute later
	clock = clock.Add(time.Minute)
	next := r.job(t, e, "j_next")
	b := envValue2(next.env, "GOCACHE")
	assert.NotEmpty(t, b, "the other directory is looked at for itself, not refused with the first one's answer")
	assert.NotEqual(t, a, b)
}

// Go's fuzz corpus under the cache is not an entry: it is Go's to clean (go clean -fuzzcache), and no age or size takes it.
func TestTheFuzzCorpusIsNotAnEntry(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	corpus := cacheFile(t, cache, "fuzz/pkg/Fuzz/abc", 1000, 90*24*time.Hour)
	r.job(t, r.exec(t, repo, sha), "j_second")
	assert.FileExists(t, corpus)
}

// A clock that went back does not hold the looks off until it catches up: a look and an age pass whose time is in the future are due.
func TestAClockThatWentBackDoesNotStopTheLooks(t *testing.T) {
	r := newGoRig(t)
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	cache := firstJobCache(t, r, repo, sha)
	e := r.exec(t, repo, sha)
	clock := time.Now()
	e.goCache.now = func() time.Time { return clock }
	j := r.job(t, e, "j_one")
	require.NoError(t, j.ws.Cleanup())
	aged := cacheFile(t, cache, "ab/aged", 1000, 48*time.Hour+6*time.Hour)

	clock = clock.Add(-6 * time.Hour) // the clock goes back
	r.job(t, e, "j_two")
	assert.NoFileExists(t, aged, "looked at again, and the age pass done")
}

// Without the exclusive lock (a job holds the cache) the cache is trimmed only of entries unused for a while, so that a trim does not take
// an entry another job read a moment ago; entries it just used stay, and a cache that cannot be brought under its bound that way is refused
// to a new job when over twice it. With nobody holding it the whole cache may be trimmed.
func TestATrimWhileAJobHoldsTheCacheLeavesTheEntriesUsedRecently(t *testing.T) {
	r := newGoRig(t)
	r.cfgGoCacheMB = 1
	repo, sha := goRepo(t, "module x\n\ngo 1.26\n")
	holder := r.job(t, r.exec(t, repo, sha), "j_busy")
	cache := envValue2(holder.env, "GOCACHE")
	recent := cacheFile(t, cache, "ab/recent", 3<<20, 30*time.Minute)
	cold := r.job(t, r.exec(t, repo, sha), "j_cold")
	assert.FileExists(t, recent, "used half an hour ago: not taken while a job holds the cache")
	assert.Empty(t, envValue2(cold.env, "GOCACHE"), "and over twice the bound, so it is not given")

	require.NoError(t, os.Remove(recent))
	stale := cacheFile(t, cache, "ab/stale", 3<<20, 3*time.Hour)
	recent = cacheFile(t, cache, "ab/recent2", 900<<10, 30*time.Minute)
	j := r.job(t, r.exec(t, repo, sha), "j_new")
	assert.NoFileExists(t, stale, "unused for three hours: trimmed")
	assert.FileExists(t, recent, "used half an hour ago: left")
	assert.Equal(t, cache, envValue2(j.env, "GOCACHE"), "and under its bound again, so it is given")
}
