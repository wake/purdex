package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The real store, the real manifest source and the real watcher: a label earned on node 24.14.0 goes when node moves, the entries this
// host made under it go with it, and what the reference host made under the same label stays.
func TestADriftedEarnedLabelIsRevokedWithItsOwnEntriesOnly(t *testing.T) {
	src, _ := refRepo(t, refManifest)
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const label = "task:purdex/tsc"
	earnedOn := map[string]string{"os_version": "Ubuntu 26.04", "node": "24.14.0"}
	if err := st.SaveEarnedLabel(wire.EarnedLabel{Label: label, State: wire.EarnedLabelEarned, Fingerprint: earnedOn, GrantedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	zero := 0
	now := time.Now()
	put := func(key, host string) {
		e := store.CacheEntry{Key: key, Result: wire.Result{Exit: &zero, Host: host, Log: "l"}, HostFingerprint: earnedOn, Label: label, CreatedAt: now}
		if err := st.PutCacheEntry(e, now); err != nil {
			t.Fatal(err)
		}
	}
	put("mine", "mserv")
	put("the-reference-s", "air26")

	var told int
	w := newDriftWatcher("mserv", st, newRefTaskSource(src), func() { told++ })
	w.OnFacts(earnedOn)
	if rows, _ := st.EarnedLabels(); len(rows) != 1 || told != 0 {
		t.Fatalf("nothing moved: %+v %d", rows, told)
	}
	w.OnFacts(map[string]string{"os_version": "Ubuntu 26.04", "node": "24.15.0"})
	if rows, _ := st.EarnedLabels(); len(rows) != 0 {
		t.Fatalf("the label stayed: %+v", rows)
	}
	if told != 1 {
		t.Fatalf("the daemon was told %d times", told)
	}
	if _, ok, _ := st.GetCacheEntry("mine", now); ok {
		t.Fatal("an entry this host made under the revoked label stayed")
	}
	if _, ok, _ := st.GetCacheEntry("the-reference-s", now); !ok {
		t.Fatal("the reference host's entry went with it: it is not this host's to invalidate")
	}
}

// A host that is the reference for a task earns nothing and loses nothing on drift; its own entries go.
func TestAReferenceHostsDriftInTheRealWiring(t *testing.T) {
	src, _ := refRepo(t, refManifest)
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	zero := 0
	now := time.Now()
	for key, host := range map[string]string{"own": "air26", "from-mserv": "mserv"} {
		e := store.CacheEntry{Key: key, Result: wire.Result{Exit: &zero, Host: host, Log: "l"}, HostFingerprint: map[string]string{"node": "24.14.0"}, Label: "task:purdex/tsc", CreatedAt: now}
		if err := st.PutCacheEntry(e, now); err != nil {
			t.Fatal(err)
		}
	}
	var told int
	w := newDriftWatcher("air26", st, newRefTaskSource(src), func() { told++ }) // refManifest names air26 as tsc's reference
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "24.14.0"})
	w.OnFacts(map[string]string{"os_version": "26.4", "node": "25.0.0"})
	if _, ok, _ := st.GetCacheEntry("own", now); ok {
		t.Fatal("the reference's own entry stayed after its node moved")
	}
	if _, ok, _ := st.GetCacheEntry("from-mserv", now); !ok {
		t.Fatal("another host's entry was dropped by the reference's drift")
	}
	if told != 0 {
		t.Fatal("a reference's drift told the daemon a label was revoked")
	}
}
