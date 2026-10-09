package teammod

import (
	"fmt"
	"path/filepath"
	"testing"
)

// Mutation gates: skip the upsert → the restart case red; skip the eviction → the cap case red.
func TestModHello_SurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertModHello("sid-a", helloInfo{ModVersion: "2", Agent: "cc", At: 10}, 512); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertModHello("sid-a", helloInfo{ModVersion: "3", Agent: "cc", At: 20}, 512); err != nil { // an update, not a second row
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.LoadModHello(512)
	if err != nil || len(got) != 1 || got["sid-a"] != (helloInfo{ModVersion: "3", Agent: "cc", At: 20}) {
		t.Fatalf("after reopen: %+v err=%v", got, err)
	}
}

func TestModHello_KeepsTheNewestOnly(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 5; i++ {
		if err := s.UpsertModHello(fmt.Sprintf("s-%d", i), helloInfo{ModVersion: "2", Agent: "cc", At: int64(i + 1)}, 3); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoadModHello(512)
	if err != nil || len(got) != 3 {
		t.Fatalf("kept %d, want 3: %+v err=%v", len(got), got, err)
	}
	for _, sid := range []string{"s-2", "s-3", "s-4"} {
		if _, ok := got[sid]; !ok {
			t.Fatalf("%s must be kept: %+v", sid, got)
		}
	}
}

// The hello route writes through to the store, and a module that starts on the same file sees it (Init loads it).
func TestModHello_HelloRoutePersistsAndInitLoads(t *testing.T) {
	f := newFixture(t)
	if code, body := f.do("POST", "/api/relay/hello", map[string]any{"session_id": "sid-1", "mod_version": "2", "agent": "cc"}); code != 200 {
		t.Fatalf("hello: %d %s", code, body)
	}
	g := New()
	if err := g.Init(f.core); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	if !g.modPresent("sid-1") || g.modSeen["sid-1"].ModVersion != "2" {
		t.Fatalf("a restarted module lost the hello: %+v", g.modSeen)
	}
}

// Memory and the table drop the same session when several hellos share one millisecond: oldest At first, the greatest
// session id among ties. Mutation gate: evict by At only (Go map order) → the two sets differ (red, flaky-proof by size).
func TestModHello_EvictionAgreesWithTheTableOnTies(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < modSeenCap+20; i++ { // every hello at the same clock value
		if code, body := f.do("POST", "/api/relay/hello", map[string]any{"session_id": fmt.Sprintf("s-%04d", i), "mod_version": "2", "agent": "cc"}); code != 200 {
			t.Fatalf("hello %d: %d %s", i, code, body)
		}
	}
	rows, err := f.m.store.LoadModHello(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if len(rows) != modSeenCap || len(f.m.modSeen) != modSeenCap {
		t.Fatalf("table %d, memory %d, want %d each", len(rows), len(f.m.modSeen), modSeenCap)
	}
	for sid := range rows {
		if _, ok := f.m.modSeen[sid]; !ok {
			t.Fatalf("%s is in the table but not in memory", sid)
		}
	}
}
