package jobexec

import (
	"os"
	"path/filepath"
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
	older := cacheFile(t, cache, "cd/old-d", 1000, 25*time.Hour)
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
	three := cacheFile(t, cache, "ab/three", 1000, 3*time.Hour)
	one := cacheFile(t, cache, "ab/one", 1000, time.Hour)
	r.job(t, r.exec(t, repo, sha), "j_second")
	assert.NoFileExists(t, three)
	assert.FileExists(t, one)
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
