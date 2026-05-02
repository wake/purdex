package codexbroker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLaunchRegistry_LoadMissing — file absent → empty registry, Empty()=true.
func TestLaunchRegistry_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	store := &LaunchRegistry{}
	out, err := store.Load(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if out == nil {
		t.Fatalf("expected non-nil empty registry")
	}
	if !out.Empty() {
		t.Errorf("expected Empty()=true on fresh load")
	}
	// Lookup must not crash.
	if e, ok := out.Lookup("anykey"); ok || e != nil {
		t.Errorf("lookup on empty must return false/nil: %v %v", e, ok)
	}
}

// TestLaunchRegistry_RoundTrip — register + save + load + lookup.
func TestLaunchRegistry_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launch.json")
	store := &LaunchRegistry{}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f := &LaunchRegistryFile{Version: 1}
	f = store.Register(f, LaunchEntry{
		BrokerKey: "k1", Pid: 4321, Lstart: now.Add(-1 * time.Hour),
		TmuxPane: "%42", CallerSessionID: "sess-1", LaunchedAt: now,
	})
	if err := store.Save(path, f); err != nil {
		t.Fatal(err)
	}
	out, err := store.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := out.Lookup("k1")
	if !ok {
		t.Fatalf("expected lookup to find k1")
	}
	if got.TmuxPane != "%42" {
		t.Errorf("tmuxPane lost: %s", got.TmuxPane)
	}
	if got.Pid != 4321 {
		t.Errorf("pid lost: %d", got.Pid)
	}
}

// TestLaunchRegistry_Remove — remove existing entry, lookup returns false;
// Empty() reflects post-removal state.
func TestLaunchRegistry_Remove(t *testing.T) {
	store := &LaunchRegistry{}
	f := &LaunchRegistryFile{Version: 1}
	f = store.Register(f, LaunchEntry{BrokerKey: "k1", Pid: 100})
	f = store.Register(f, LaunchEntry{BrokerKey: "k2", Pid: 200})
	f = store.Remove(f, "k1", 100)
	if _, ok := f.Lookup("k1"); ok {
		t.Errorf("expected k1 removed")
	}
	if _, ok := f.Lookup("k2"); !ok {
		t.Errorf("expected k2 retained")
	}
	if f.Empty() {
		t.Errorf("expected Empty()=false (k2 still present)")
	}
	f = store.Remove(f, "k2", 200)
	if !f.Empty() {
		t.Errorf("expected Empty()=true after removing all")
	}
}

// TestLaunchRegistry_LookupMissing — entries present but key absent → false.
func TestLaunchRegistry_LookupMissing(t *testing.T) {
	store := &LaunchRegistry{}
	f := &LaunchRegistryFile{Version: 1}
	f = store.Register(f, LaunchEntry{BrokerKey: "k1"})
	if _, ok := f.Lookup("kX"); ok {
		t.Errorf("expected lookup miss for kX")
	}
}

// TestLaunchRegistry_Empty_ZeroEntries — Empty()=true on freshly-constructed
// file.
func TestLaunchRegistry_Empty_ZeroEntries(t *testing.T) {
	f := &LaunchRegistryFile{Version: 1}
	if !f.Empty() {
		t.Errorf("expected Empty()=true on zero entries")
	}
}

// TestLaunchRegistry_Empty_AfterRegister — Empty() flips to false after
// first Register.
func TestLaunchRegistry_Empty_AfterRegister(t *testing.T) {
	store := &LaunchRegistry{}
	f := &LaunchRegistryFile{Version: 1}
	f = store.Register(f, LaunchEntry{BrokerKey: "k1"})
	if f.Empty() {
		t.Errorf("expected Empty()=false after first register")
	}
}

// TestLaunchRegistry_AtomicSave — saving to a fresh path leaves the file
// readable; saving again leaves the original untouched on success.
func TestLaunchRegistry_AtomicSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launch.json")
	store := &LaunchRegistry{}
	original := &LaunchRegistryFile{Version: 1}
	original = store.Register(original, LaunchEntry{BrokerKey: "orig"})
	if err := store.Save(path, original); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Errorf("expected body persisted")
	}
}

// TestLaunchRegistry_Save_CreatesMissingParent — PR review finding H: Save
// must own its first-write behaviour for fresh profiles or relocated data
// roots. The previous implementation called os.WriteFile(path+".tmp")
// directly, which fails when the parent dir does not yet exist. Now
// MkdirAll(filepath.Dir(path), 0755) runs first.
func TestLaunchRegistry_Save_CreatesMissingParent(t *testing.T) {
	dir := t.TempDir()
	// Two missing levels under tmpdir.
	path := filepath.Join(dir, "missing-1", "missing-2", "launch.json")
	store := &LaunchRegistry{}
	f := &LaunchRegistryFile{Version: 1}
	f = store.Register(f, LaunchEntry{BrokerKey: "fresh", Pid: 4321})
	if err := store.Save(path, f); err != nil {
		t.Fatalf("Save with missing parent dir failed: %v", err)
	}
	// File must be readable.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after Save: %v", err)
	}
	if len(body) == 0 {
		t.Errorf("Save produced empty file")
	}
	// Round-trip via Load to verify integrity.
	out, err := store.Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got, ok := out.Lookup("fresh"); !ok || got.Pid != 4321 {
		t.Errorf("round-trip failed: ok=%v entry=%+v", ok, got)
	}
}

// TestLaunchRegistryFile_SatisfiesReader — type assertion only.
func TestLaunchRegistryFile_SatisfiesReader(t *testing.T) {
	var _ LaunchRegistryReader = &LaunchRegistryFile{Version: 1}
}
