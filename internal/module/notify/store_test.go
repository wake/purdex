package notify

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- notify.db: the groups the App wrote back, by (device, root) (notify-title spec §3.2, plan N-2 #2) ----

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func best(t *testing.T, s *Store) map[string]string {
	t.Helper()
	m, err := s.Best()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for root, e := range m {
		out[root] = e.Group
	}
	return out
}

// A write replaces the device's whole set: what it leaves out is gone.
func TestStore_ReplaceTakesTheWholeSetOfADevice(t *testing.T) {
	s := openMem(t)
	if err := s.Replace("dev-a", []Label{{"r1", "Alpha"}, {"r2", "Beta"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Replace("dev-a", []Label{{"r2", "Beta 2"}}); err != nil {
		t.Fatal(err)
	}
	got := best(t, s)
	if len(got) != 1 || got["r2"] != "Beta 2" {
		t.Fatalf("got %v: r1 was left out of the second write and must be gone", got)
	}
}

// Two devices that wrote the same root: the later write wins, in a total order that does not depend on the clock (the same millisecond too).
func TestStore_TheLastWriteWinsEvenInTheSameMillisecond(t *testing.T) {
	s := openMem(t)
	s.now = func() int64 { return 1_000 } // one instant for every write
	if err := s.Replace("dev-a", []Label{{"r1", "From A"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Replace("dev-b", []Label{{"r1", "From B"}}); err != nil {
		t.Fatal(err)
	}
	if got := best(t, s)["r1"]; got != "From B" {
		t.Fatalf("got %q, want the later write", got)
	}
	if err := s.Replace("dev-a", []Label{{"r1", "From A again"}}); err != nil {
		t.Fatal(err)
	}
	if got := best(t, s)["r1"]; got != "From A again" {
		t.Fatalf("got %q: a device that writes again is the latest", got)
	}
	// B's set is untouched by A's second write, and when A leaves the root out B's value is the one that stays
	if err := s.Replace("dev-a", nil); err != nil {
		t.Fatal(err)
	}
	if got := best(t, s)["r1"]; got != "From B" {
		t.Fatalf("got %q, want B's value once A left the root out", got)
	}
}

// The replacement is one transaction: a failure in the middle leaves the device's old set as it was, and a reader never sees an empty or half set.
func TestStore_AFailureInTheMiddleLeavesTheOldSet(t *testing.T) {
	s := openMem(t)
	if err := s.Replace("dev-a", []Label{{"r1", "Old 1"}, {"r2", "Old 2"}}); err != nil {
		t.Fatal(err)
	}
	s.failInsertAt = 1 // the second row of the next write fails
	err := s.Replace("dev-a", []Label{{"r1", "New 1"}, {"r2", "New 2"}, {"r3", "New 3"}})
	if err == nil {
		t.Fatal("the injected failure did not fail the write")
	}
	got := best(t, s)
	if len(got) != 2 || got["r1"] != "Old 1" || got["r2"] != "Old 2" {
		t.Fatalf("got %v: the failed write must leave the old set whole", got)
	}
}

// A device that has not written for the retention is gone with all its rows, and only that device.
func TestStore_SweepRemovesTheDevicesThatWentQuiet(t *testing.T) {
	s := openMem(t)
	now := int64(10_000)
	s.now = func() int64 { return now }
	if err := s.Replace("old", []Label{{"r1", "From old"}, {"r2", "From old 2"}}); err != nil {
		t.Fatal(err)
	}
	now = 20_000
	if err := s.Replace("fresh", []Label{{"r2", "From fresh"}}); err != nil {
		t.Fatal(err)
	}
	n, err := s.Sweep(15_000) // devices whose last write is before 15_000
	if err != nil || n != 2 {
		t.Fatalf("swept %d, %v: want the two rows of the quiet device", n, err)
	}
	got := best(t, s)
	if len(got) != 1 || got["r2"] != "From fresh" {
		t.Fatalf("got %v", got)
	}
}

// The file is made owner-only, and reopening finds the data and does not fail (idempotent migration).
func TestStore_ReopenKeepsTheDataAndTheFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Replace("dev-a", []Label{{"r1", "Alpha"}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if got := best(t, s)["r1"]; got != "Alpha" {
		t.Fatalf("got %q after reopen", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode().Perm(), err)
	}
}

func TestStore_ErrorFromAClosedStoreIsReported(t *testing.T) {
	s := openMem(t)
	s.Close()
	if err := s.Replace("d", []Label{{"r", "g"}}); err == nil {
		t.Fatal("a write to a closed store succeeded")
	}
}
