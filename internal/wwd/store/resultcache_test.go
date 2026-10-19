package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
)

var cacheNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func cacheEntry(key string, created time.Time, label string) CacheEntry {
	return CacheEntry{Key: key, Rec: json.RawMessage(`{"ok":true}`), Host: "mserv", HostFingerprint: map[string]string{"go": "1.26.0"}, Label: label, CreatedAt: created}
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
		if err := s.putCacheEntry(cacheEntry("k", cacheNow, ""), n); err == nil {
			t.Fatalf("limit %d accepted", n)
		}
	}
	if n, _ := s.CountCache(); n != 0 {
		t.Fatalf("a refused write left %d rows", n)
	}
}

func TestCacheEntryRoundTripReplaceAndBadRec(t *testing.T) {
	s := openTemp(t)
	if err := s.PutCacheEntry(cacheEntry("k", cacheNow.Add(-time.Hour), "task:a/b")); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.GetCacheEntry("k", cacheNow)
	if err != nil || !ok || c.Host != "mserv" || c.Label != "task:a/b" || c.HostFingerprint["go"] != "1.26.0" || !c.CreatedAt.Equal(cacheNow.Add(-time.Hour)) {
		t.Fatalf("got %+v %v %v", c, ok, err)
	}
	if err := s.PutCacheEntry(cacheEntry("k", cacheNow, "task:a/b")); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != 1 {
		t.Fatalf("same key twice: %d rows", n)
	}
	if _, ok, _ := s.GetCacheEntry("none", cacheNow); ok {
		t.Fatal("missing key found")
	}
	bad := cacheEntry("x", cacheNow, "")
	bad.Rec = json.RawMessage(`{`)
	if err := s.PutCacheEntry(bad); err == nil {
		t.Fatal("bad rec accepted")
	}
}

// A hit just before day 3 and just after it: at exactly 3 days the entry still answers (like a log at its limit), one millisecond more
// and it does not.
func TestCacheHitJustBeforeAndAfterDayThree(t *testing.T) {
	s := openTemp(t)
	made := cacheNow
	if err := s.PutCacheEntry(cacheEntry("k", made, "")); err != nil {
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

// A row whose record or fingerprint no longer decodes is an error, never a hit: the submit path must not answer from damage.
func TestCacheDamagedRowIsAnErrorNotAHit(t *testing.T) {
	s := openTemp(t)
	for _, c := range []struct{ key, rec, fp string }{{"badrec", `{`, `{}`}, {"badfp", `{}`, `{`}} {
		if _, err := s.DB().Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES (?, ?, 'h', ?, '', ?)`,
			c.key, c.rec, c.fp, cacheNow.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.GetCacheEntry(c.key, cacheNow); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v", c.key, ok, err)
		}
	}
}

func TestPruneCacheDropsOnlyThePast(t *testing.T) {
	s := openTemp(t)
	for _, c := range []CacheEntry{
		cacheEntry("old", cacheNow.Add(-CacheRetention-time.Second), ""),
		cacheEntry("edge", cacheNow.Add(-CacheRetention), ""),
		cacheEntry("new", cacheNow, ""),
	} {
		if err := s.PutCacheEntry(c); err != nil {
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

// The 10,000th entry stays and the 10,001st pushes out the oldest.
func TestCacheKeepsMaxAndDropsOldest(t *testing.T) {
	s := openTemp(t)
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxCacheEntries; i++ {
		if _, err := tx.Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES (?, '{}', 'h', '{}', '', ?)`,
			fmt.Sprintf("k%05d", i), cacheNow.UnixMilli()+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCacheEntry(cacheEntry("k00000", cacheNow, "")); err != nil { // the oldest key again: replaces itself, stays (now at its old time)
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != MaxCacheEntries {
		t.Fatalf("at the limit: %d", n)
	}
	if err := s.PutCacheEntry(cacheEntry("new", cacheNow.Add(time.Hour), "")); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountCache(); n != MaxCacheEntries {
		t.Fatalf("after the 10,001st: %d", n)
	}
	if _, ok, _ := s.GetCacheEntry("k00000", cacheNow); ok { // created at cacheNow, the same ms as nothing else older; it is the oldest
		t.Fatal("the oldest stayed")
	}
	if _, ok, _ := s.GetCacheEntry("new", cacheNow); !ok {
		t.Fatal("the new one is gone")
	}
}

// Equal created_ms: the same entry goes out every time (the lowest key first).
func TestCacheTrimTieBreaksByKey(t *testing.T) {
	for run := 0; run < 3; run++ {
		s := openTemp(t)
		for _, k := range []string{"c", "a", "b"} {
			if err := s.putCacheEntry(cacheEntry(k, cacheNow, ""), 3); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.putCacheEntry(cacheEntry("d", cacheNow, ""), 3); err != nil {
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
		if err := s.PutCacheEntry(c); err != nil {
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
