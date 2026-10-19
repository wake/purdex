package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

var cacheNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func okResult(host string) wire.Result {
	zero := 0
	return wire.Result{Exit: &zero, Host: host, Log: "log-1"}
}

func cacheEntry(key string, created time.Time, label string) CacheEntry {
	return CacheEntry{Key: key, Result: okResult("mserv"), HostFingerprint: map[string]string{"go": "1.26.0"}, Label: label, CreatedAt: created}
}

// cache retention <= log retention <= job retention: an entry never outlives the log or the job it points at. It goes red when any of
// the three is changed on its own.
func TestCacheRetentionNeverOutlivesLogAndJob(t *testing.T) {
	if !(CacheRetention <= queue.LogRetention && queue.LogRetention <= queue.JobRetention) {
		t.Fatalf("cache %v, log %v, job %v", CacheRetention, queue.LogRetention, queue.JobRetention)
	}
	if CacheRetention != 3*24*time.Hour {
		t.Fatalf("cache retention %v, the decision of 10-19 is 3 days", CacheRetention)
	}
}

func TestCacheLimitIsFixed(t *testing.T) {
	if MaxCacheEntries != 10000 {
		t.Fatalf("MaxCacheEntries %d, spec §4.5 says 10,000", MaxCacheEntries)
	}
	s := openTemp(t)
	for _, n := range []int{0, -1} {
		if err := s.putCacheEntry(cacheEntry("k", cacheNow, ""), cacheNow, n); err == nil {
			t.Fatalf("limit %d accepted", n)
		}
	}
	if n, _ := s.CountCache(); n != 0 {
		t.Fatalf("a refused write left %d rows", n)
	}
}

func TestCacheEntryRoundTripAndReplace(t *testing.T) {
	s := openTemp(t)
	if err := s.PutCacheEntry(cacheEntry("k", cacheNow.Add(-time.Hour), "task:a/b"), cacheNow); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetCacheEntry("k", cacheNow)
	if err != nil || !ok || c.Result.Host != "mserv" || *c.Result.Exit != 0 || c.Label != "task:a/b" || c.HostFingerprint["go"] != "1.26.0" || !c.CreatedAt.Equal(cacheNow.Add(-time.Hour)) {
		t.Fatalf("got %+v %v %v", c, ok, err)
	}
	if err := s.PutCacheEntry(cacheEntry("k", cacheNow, "task:a/b"), cacheNow); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != 1 {
		t.Fatalf("same key twice: %d rows", n)
	}
	if _, ok, _ := s.GetCacheEntry("none", cacheNow); ok {
		t.Fatal("missing key found")
	}
}

// Only the result of a command that ran and exited 0, with host and log, that is not a reuse and was not superseded, is stored.
func TestCacheRefusesWhatIsNotAReusableResult(t *testing.T) {
	s := openTemp(t)
	one := 1
	super := "job-2"
	for name, mod := range map[string]func(*CacheEntry){
		"no key":        func(c *CacheEntry) { c.Key = "" },
		"no exit":       func(c *CacheEntry) { c.Result.Exit = nil },
		"exit 1":        func(c *CacheEntry) { c.Result.Exit = &one },
		"no host":       func(c *CacheEntry) { c.Result.Host = "" },
		"no log":        func(c *CacheEntry) { c.Result.Log = "" },
		"a reuse":       func(c *CacheEntry) { c.Result.CacheHit = true },
		"superseded":    func(c *CacheEntry) { c.Result.SupersededBy = &super },
		"a reason":      func(c *CacheEntry) { c.Result.Reason = "infra" },
		"a bad digest":  func(c *CacheEntry) { c.Result.Summary = &wire.Summary{PassedDigest: "nope"} },
		"in the future": func(c *CacheEntry) { c.CreatedAt = cacheNow.Add(cacheClockSlack + time.Second) },
	} {
		c := cacheEntry("k", cacheNow, "")
		mod(&c)
		if err := s.PutCacheEntry(c, cacheNow); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if n, _ := s.CountCache(); n != 0 {
		t.Fatalf("refused writes left %d rows", n)
	}
	// within the clock slack is fine, and is taken as now: the entry never lives past the log it points at
	if err := s.PutCacheEntry(cacheEntry("k", cacheNow.Add(cacheClockSlack), ""), cacheNow); err != nil {
		t.Fatalf("a small clock step: %v", err)
	}
	c, ok, err := s.GetCacheEntry("k", cacheNow)
	if err != nil || !ok || !c.CreatedAt.Equal(cacheNow) {
		t.Fatalf("stored as %v ok=%v err=%v, want %v", c.CreatedAt, ok, err, cacheNow)
	}
	if _, ok, _ := s.GetCacheEntry("k", cacheNow.Add(CacheRetention)); !ok {
		t.Fatal("an entry made a minute ahead must still end exactly CacheRetention after now")
	}
	if _, ok, _ := s.GetCacheEntry("k", cacheNow.Add(CacheRetention+time.Millisecond)); ok {
		t.Fatal("an entry made a minute ahead outlived CacheRetention after now")
	}
}

// A row that is valid JSON but not a reusable result, or whose host column disagrees with the result, is an error, never a hit.
func TestCacheDamagedRowIsAnErrorNotAHit(t *testing.T) {
	s := openTemp(t)
	for _, c := range []struct{ key, rec, fp, host string }{
		{"syntax", `{`, `{}`, "h"},
		{"badfp", `{"exit":0,"host":"h","log":"l"}`, `{`, "h"},
		{"null", `null`, `{}`, "h"},
		{"scalar", `"x"`, `{}`, "h"},
		{"empty object", `{}`, `{}`, "h"},
		{"failed exit", `{"exit":1,"host":"h","log":"l"}`, `{}`, "h"},
		{"no log", `{"exit":0,"host":"h"}`, `{}`, "h"},
		{"host column differs", `{"exit":0,"host":"h","log":"l"}`, `{}`, "other"},
		{"malformed digest", `{"exit":0,"host":"h","log":"l","summary":{"passed":1,"failed":0,"skipped":0,"failures":[],"tail":[],"passed_digest":"nope"}}`, `{}`, "h"},
	} {
		if _, err := s.DB().Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES (?, ?, ?, ?, '', ?)`,
			c.key, c.rec, c.host, c.fp, cacheNow.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.GetCacheEntry(c.key, cacheNow); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v", c.key, ok, err)
		}
	}
}

// A hit just before day 3 and just after it: at exactly 3 days the entry still answers (like a log at its limit), one millisecond more
// and it does not.
func TestCacheHitJustBeforeAndAfterDayThree(t *testing.T) {
	s := openTemp(t)
	made := cacheNow
	if err := s.PutCacheEntry(cacheEntry("k", made, ""), made); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		age time.Duration
		hit bool
	}{
		{CacheRetention - time.Millisecond, true},
		{CacheRetention, true},
		{CacheRetention + time.Millisecond, false},
	} {
		if _, ok, err := s.GetCacheEntry("k", made.Add(c.age)); err != nil || ok != c.hit {
			t.Errorf("age %v: hit=%v err=%v, want %v", c.age, ok, err, c.hit)
		}
	}
}

// A row that claims to be made after now (a clock that was set forward, a bad write) never answers and is pruned: it would outlive
// the log it points at.
func TestCacheFutureRowNeverAnswersAndIsPruned(t *testing.T) {
	s := openTemp(t)
	future := cacheNow.Add(48 * time.Hour)
	if _, err := s.DB().Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES ('f', '{"exit":0,"host":"h","log":"l"}', 'h', '{}', '', ?)`, future.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetCacheEntry("f", cacheNow); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, err := s.DB().Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES ('s', '{"exit":0,"host":"h","log":"l"}', 'h', '{}', '', ?)`, cacheNow.Add(time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetCacheEntry("s", cacheNow); ok {
		t.Fatal("a row one second ahead answered")
	}
	if n, err := s.PruneCache(cacheNow); err != nil || n != 2 {
		t.Fatalf("pruned %d %v", n, err)
	}
}

func TestPruneCacheDropsOnlyThePast(t *testing.T) {
	s := openTemp(t)
	for _, c := range []CacheEntry{
		cacheEntry("old", cacheNow.Add(-CacheRetention-time.Second), ""),
		cacheEntry("edge", cacheNow.Add(-CacheRetention), ""),
		cacheEntry("new", cacheNow, ""),
	} {
		if err := s.PutCacheEntry(c, cacheNow); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.PruneCache(cacheNow); err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	if n, _ := s.CountCache(); n != 2 {
		t.Fatalf("left %d", n)
	}
}

func fillCache(t *testing.T, s *Store, n int) {
	t.Helper()
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES (?, '{"exit":0,"host":"h","log":"l"}', 'h', '{}', '', ?)`,
			fmt.Sprintf("k%05d", i), cacheNow.UnixMilli()+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The 10,000th entry stays and the 10,001st pushes out the oldest.
func TestCacheKeepsMaxAndDropsOldest(t *testing.T) {
	s := openTemp(t)
	fillCache(t, s, MaxCacheEntries)
	if err := s.PutCacheEntry(cacheEntry("k00000", cacheNow, ""), cacheNow); err != nil { // an existing key again: replaces itself
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != MaxCacheEntries {
		t.Fatalf("at the limit: %d", n)
	}
	if err := s.PutCacheEntry(cacheEntry("new", cacheNow.Add(time.Second), ""), cacheNow); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != MaxCacheEntries {
		t.Fatalf("after the 10,001st: %d", n)
	}
	if _, ok, _ := s.GetCacheEntry("k00000", cacheNow); ok { // the oldest (created at cacheNow, the lowest key)
		t.Fatal("the oldest stayed")
	}
	if _, ok, _ := s.GetCacheEntry("new", cacheNow.Add(time.Second)); !ok {
		t.Fatal("the new one is gone")
	}
}

// An entry older than all the others of a full table is the one that goes out: the write succeeds, and the table stays at the limit.
func TestCacheOlderThanEveryOtherGoesOutItself(t *testing.T) {
	s := openTemp(t)
	fillCache(t, s, MaxCacheEntries)
	if err := s.PutCacheEntry(cacheEntry("ancient", cacheNow.Add(-time.Hour), ""), cacheNow); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != MaxCacheEntries {
		t.Fatalf("%d", n)
	}
	if _, ok, _ := s.GetCacheEntry("ancient", cacheNow); ok {
		t.Fatal("the oldest entry stayed in a full table")
	}
}

// Equal created_ms: the same entry goes out every time (the lowest key first).
func TestCacheTrimTieBreaksByKey(t *testing.T) {
	for run := 0; run < 3; run++ {
		s := openTemp(t)
		for _, k := range []string{"c", "a", "b"} {
			if err := s.putCacheEntry(cacheEntry(k, cacheNow, ""), cacheNow, 3); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.putCacheEntry(cacheEntry("d", cacheNow, ""), cacheNow, 3); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.GetCacheEntry("a", cacheNow); ok {
			t.Fatalf("run %d: a stayed", run)
		}
		for _, k := range []string{"b", "c", "d"} {
			if _, ok, _ := s.GetCacheEntry(k, cacheNow); !ok {
				t.Fatalf("run %d: %s gone", run, k)
			}
		}
	}
}

func TestCacheDeleteByLabel(t *testing.T) {
	s := openTemp(t)
	for _, c := range []CacheEntry{cacheEntry("a", cacheNow, "task:x/1"), cacheEntry("b", cacheNow, "task:x/1"), cacheEntry("c", cacheNow, "task:x/2"), cacheEntry("d", cacheNow, "")} {
		if err := s.PutCacheEntry(c, cacheNow); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.DeleteCacheByLabel("task:x/1"); err != nil || n != 2 {
		t.Fatalf("by label: %d %v", n, err)
	}
	if n, _ := s.CountCache(); n != 2 {
		t.Fatalf("left %d", n)
	}
}
