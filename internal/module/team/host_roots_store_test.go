// internal/module/team/host_roots_store_test.go — the lead host's record of the root changes it asked member hosts for, and the cache of
// what those hosts last said (cross-host roots spec 2026-10-12 §3.2, XR-2a).
package teammod

import (
	"errors"
	"reflect"
	"testing"
)

func rootChange(id, host, op, path string, at int64) HostRootChange {
	return HostRootChange{ID: id, HostID: host, Op: op, Path: path, CreatedAt: at}
}

func mustInsertChange(t *testing.T, s *Store, c HostRootChange) {
	t.Helper()
	if err := s.InsertHostRootChange(c); err != nil {
		t.Fatalf("insert %s: %v", c.ID, err)
	}
}

func TestHostRoots_InsertAndPendingByHostInRowidOrder(t *testing.T) {
	s := openTestStore(t)
	mustInsertChange(t, s, rootChange("c1", "hostA", "add", "/w/a", 100))
	mustInsertChange(t, s, rootChange("c2", "hostB", "add", "/w/b", 101))
	mustInsertChange(t, s, rootChange("c3", "hostA", "remove", "/w/old", 102))
	hosts, err := s.PendingRootChangeHosts()
	if err != nil || !reflect.DeepEqual(hosts, []string{"hostA", "hostB"}) {
		t.Fatalf("hosts = %v, %v", hosts, err)
	}
	head, ok, err := s.HeadRootChange("hostA")
	if err != nil || !ok || head.ID != "c1" || head.State != RootChangePending {
		t.Fatalf("head = %+v ok=%v err=%v, want c1 pending", head, ok, err)
	}
	if _, ok, _ := s.HeadRootChange("hostZ"); ok {
		t.Fatal("a host with nothing pending has a head")
	}
}

func TestHostRoots_TheSchemaRefusesWhatItShouldNot(t *testing.T) {
	s := openTestStore(t)
	for _, c := range []HostRootChange{
		rootChange("c1", "hostA", "read", "", 1), // a read is not a change
		rootChange("c2", "hostA", "add", "", 1),  // no path
		rootChange("c3", "", "add", "/w", 1),     // no host
	} {
		if err := s.InsertHostRootChange(c); err == nil {
			t.Errorf("%+v was stored", c)
		}
	}
	mustInsertChange(t, s, rootChange("c4", "hostA", "add", "/w", 1))
	if err := s.InsertHostRootChange(rootChange("c4", "hostA", "add", "/w", 1)); err == nil {
		t.Error("a second row with the same id was stored")
	}
	if _, err := s.db.Exec(`UPDATE host_root_changes SET state = 'weird' WHERE id = 'c4'`); err == nil {
		t.Error("the state CHECK let 'weird' in")
	}
}

// done + canonical + the cache are ONE transaction; a failing cache write rolls the row back.
// Mutation gate: settle the row, then write the cache in a second statement/transaction → the rollback test is red.
func TestHostRoots_SettleDoneWritesTheRowAndTheCacheTogether(t *testing.T) {
	s := openTestStore(t)
	mustInsertChange(t, s, rootChange("c1", "hostA", "add", "/w/link", 100))
	if err := s.SettleRootChangeDone("c1", "/real/w", []string{"/real/w", "/x"}, 7, 200); err != nil {
		t.Fatal(err)
	}
	row, _, _ := s.RootChange("c1")
	if row.State != RootChangeDone || row.Canonical != "/real/w" {
		t.Fatalf("row = %+v", row)
	}
	seen, ok, _ := s.HostRootsSeen("hostA")
	if !ok || seen.Rev != 7 || seen.SeenAt != 200 || !reflect.DeepEqual(seen.Roots, []string{"/real/w", "/x"}) {
		t.Fatalf("cache = %+v ok=%v", seen, ok)
	}
	// atomic: the cache write fails ⇒ the row is still pending
	mustInsertChange(t, s, rootChange("c2", "hostA", "add", "/w/two", 101))
	s.failBeforeRootsCache = func() error { return errors.New("disk full") }
	if err := s.SettleRootChangeDone("c2", "/real/two", []string{"/real/two"}, 8, 300); err == nil {
		t.Fatal("the settle succeeded through an injected cache failure")
	}
	s.failBeforeRootsCache = nil
	if row, _, _ := s.RootChange("c2"); row.State != RootChangePending || row.Canonical != "" {
		t.Fatalf("row = %+v, want it still pending (rolled back)", row)
	}
	if seen, _, _ := s.HostRootsSeen("hostA"); seen.Rev != 7 {
		t.Fatalf("cache = %+v, want it untouched", seen)
	}
}

// A settle of a row that is no longer pending (refused, dropped, dismissed meanwhile) changes nothing, the cache included.
func TestHostRoots_SettleDoneOfARowNoLongerPendingIsANoOp(t *testing.T) {
	s := openTestStore(t)
	mustInsertChange(t, s, rootChange("c1", "hostA", "add", "/w", 100))
	if _, err := s.DropRootChangesOfHost("hostA", 150); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleRootChangeDone("c1", "/w", []string{"/w"}, 3, 200); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := s.RootChange("c1"); row.State != RootChangeDropped {
		t.Fatalf("row = %+v", row)
	}
	if _, ok, _ := s.HostRootsSeen("hostA"); ok {
		t.Fatal("the cache was written for a row that was no longer pending")
	}
}

// The cache only moves forward. Mutation gate: write without the rev guard → the older rev overwrites (red).
func TestHostRoots_TheCacheIsMonotonicInRev(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertHostRootsSeen("hostA", []string{"/new"}, 5, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertHostRootsSeen("hostA", []string{"/old"}, 4, 200); err != nil { // a late, older answer
		t.Fatal(err)
	}
	seen, _, _ := s.HostRootsSeen("hostA")
	if seen.Rev != 5 || seen.SeenAt != 100 || !reflect.DeepEqual(seen.Roots, []string{"/new"}) {
		t.Fatalf("cache = %+v, want rev 5 untouched by the older answer", seen)
	}
	if err := s.UpsertHostRootsSeen("hostA", []string{"/new"}, 5, 300); err != nil { // the same rev: refreshes seen_at
		t.Fatal(err)
	}
	if seen, _, _ = s.HostRootsSeen("hostA"); seen.SeenAt != 300 || seen.Rev != 5 {
		t.Fatalf("cache = %+v, want seen_at 300", seen)
	}
	if err := s.UpsertHostRootsSeen("hostA", []string{"/newer", "/x"}, 6, 400); err != nil {
		t.Fatal(err)
	}
	if seen, _, _ = s.HostRootsSeen("hostA"); seen.Rev != 6 || len(seen.Roots) != 2 {
		t.Fatalf("cache = %+v", seen)
	}
}

func TestHostRoots_RefuseDropAndDismiss(t *testing.T) {
	s := openTestStore(t)
	mustInsertChange(t, s, rootChange("c1", "hostA", "remove", "/w", 100))
	mustInsertChange(t, s, rootChange("c2", "hostA", "add", "/x", 101))
	mustInsertChange(t, s, rootChange("c3", "hostB", "add", "/y", 102))
	if err := s.RefuseRootChange("c1", "roots_in_use", "_abc123,_def456", 200); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := s.RootChange("c1"); row.State != RootChangeRefused || row.Code != "roots_in_use" || row.Detail != "_abc123,_def456" {
		t.Fatalf("row = %+v", row)
	}
	if head, _, _ := s.HeadRootChange("hostA"); head.ID != "c2" {
		t.Fatalf("head = %+v, the refused row no longer blocks the host", head)
	}
	// dropping a host drops only its pending rows
	n, err := s.DropRootChangesOfHost("hostA", 300)
	if err != nil || n != 1 {
		t.Fatalf("dropped %d, %v, want 1 (c2)", n, err)
	}
	if row, _, _ := s.RootChange("c1"); row.State != RootChangeRefused {
		t.Fatalf("a refused row was dropped: %+v", row)
	}
	if row, _, _ := s.RootChange("c3"); row.State != RootChangePending {
		t.Fatalf("another host's row was dropped: %+v", row)
	}
	// dismiss: a refused or dropped row is deleted; a pending one is not
	for _, id := range []string{"c1", "c2"} {
		if err := s.DismissRootChange(id); err != nil {
			t.Fatalf("dismiss %s: %v", id, err)
		}
		if _, ok, _ := s.RootChange(id); ok {
			t.Fatalf("%s still there", id)
		}
	}
	if err := s.DismissRootChange("c3"); !errors.Is(err, ErrRootChangePending) {
		t.Fatalf("dismiss of a pending row = %v, want ErrRootChangePending", err)
	}
	if err := s.DismissRootChange("nope"); !errors.Is(err, ErrNoSuchRootChange) {
		t.Fatalf("dismiss of an unknown row = %v", err)
	}
}

func TestHostRoots_TheFirst401StampSurvivesAndClears(t *testing.T) {
	s := openTestStore(t)
	mustInsertChange(t, s, rootChange("c1", "hostA", "add", "/w", 100))
	if first, err := s.Stamp401OfHead("hostA", 1000); err != nil || first != 1000 {
		t.Fatalf("stamp = %d, %v", first, err)
	}
	if first, err := s.Stamp401OfHead("hostA", 5000); err != nil || first != 1000 { // a later 401 keeps the first
		t.Fatalf("second stamp = %d, %v, want 1000", first, err)
	}
	if row, _, _ := s.RootChange("c1"); row.First401At != 1000 {
		t.Fatalf("row = %+v", row)
	}
	if err := s.Clear401OfHead("hostA"); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := s.RootChange("c1"); row.First401At != 0 {
		t.Fatalf("row = %+v, want it cleared", row)
	}
	// no pending row: nothing to stamp
	if first, err := s.Stamp401OfHead("hostZ", 1); err != nil || first != 0 {
		t.Fatalf("stamp with no row = %d, %v", first, err)
	}
}
