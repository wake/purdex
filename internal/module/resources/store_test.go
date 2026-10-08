package resourcesmod

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/wake/purdex/internal/resources"
)

func newTestStore(t *testing.T) *leaseStore {
	t.Helper()
	s, err := openLeaseStore(filepath.Join(t.TempDir(), "resources.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Codex attack (medium): the lease rows name sessions, pids and tool use ids,
// and DataDir is 0755, so resources.db and its WAL sidecars must be private
// whatever the umask or the mode of a file left by an earlier run.
func TestStore_FilesArePrivate(t *testing.T) {
	check := func(t *testing.T, path string) {
		t.Helper()
		s, err := openLeaseStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		mustCreate(t, s, baseRow("a", "c1")) // a write, so the WAL exists
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			if got := fi.Mode().Perm(); got != 0o600 {
				t.Errorf("%s mode = %o, want 600", filepath.Base(p), got)
			}
		}
	}
	t.Run("fresh file under umask 022", func(t *testing.T) {
		old := syscall.Umask(0o022)
		t.Cleanup(func() { syscall.Umask(old) })
		check(t, filepath.Join(t.TempDir(), "resources.db"))
	})
	t.Run("a loose file from an earlier run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "resources.db")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+"-wal", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		check(t, path)
	})
}

// Re-review (P1): a loose file is tightened before SQLite touches it, not
// only after a successful migration. A file that is not a database makes the
// migration fail, and it still must not stay readable.
func TestStore_LooseFileIsTightenedEvenWhenTheMigrationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database, padded to be longer than a header......"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := openLeaseStore(path); err == nil {
		_ = s.Close()
		t.Fatal("a garbage file must fail to open")
	}
	for _, p := range []string{path, path + "-wal"} {
		fi, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue // SQLite may delete a stale sidecar; gone is private enough
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o after a failed open, want 600", filepath.Base(p), got)
		}
	}
}

func baseRow(id, client string) leaseRow {
	return leaseRow{
		ID: id, ClientID: client, Kind: "test-full", Weight: 35, SessionID: "sid-1",
		HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026", Scope: resources.ScopeProcess,
		CreatedAt: 1000, DeadlineAt: 301000, LeaseUntil: 31000,
	}
}

func mustCreate(t *testing.T, s *leaseStore, r leaseRow) leaseRow {
	t.Helper()
	got, created, err := s.Create(r)
	if err != nil || !created {
		t.Fatalf("create %s: created=%v err=%v", r.ID, created, err)
	}
	return got
}

func TestStore_CreateIdempotent(t *testing.T) {
	s := newTestStore(t)
	first, created, err := s.Create(baseRow("a", "c1"))
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	if first.State != resources.StateWaiting || first.Kind != "test-full" || first.Weight != 35 || first.GrantedAt != 0 || first.EndedAt != 0 {
		t.Fatalf("row = %+v", first)
	}

	// Same client id, different id and weight: nothing inserted, the first row comes back.
	dup := baseRow("b", "c1")
	dup.Weight = 99
	again, created, err := s.Create(dup)
	if err != nil || created {
		t.Fatalf("replay: created=%v err=%v", created, err)
	}
	if again.ID != "a" || again.Weight != 35 {
		t.Fatalf("replay returned %+v, want the first row", again)
	}
	// ... also once the first row moved on.
	if ok, err := s.Grant("a", 2000, false, false); err != nil || !ok {
		t.Fatal(ok, err)
	}
	again, created, _ = s.Create(dup)
	if created || again.State != resources.StateHeld {
		t.Fatalf("replay after grant = %+v created=%v", again, created)
	}
	if rows, _ := s.Waiting(); len(rows) != 0 {
		t.Fatalf("a replay must not queue a second row: %+v", rows)
	}
}

func TestStore_RenewMaxOnly(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, baseRow("a", "c1"))
	renew := func(until int64) int64 {
		if err := s.RenewLease("a", until); err != nil {
			t.Fatal(err)
		}
		r, _, _ := s.Get("a")
		return r.LeaseUntil
	}
	if got := renew(60000); got != 60000 {
		t.Fatalf("renew forward = %d", got)
	}
	if got := renew(45000); got != 60000 {
		t.Fatalf("renew must never move back: %d", got)
	}
	// Only waiting rows renew.
	if ok, _ := s.Grant("a", 2000, false, false); !ok {
		t.Fatal("grant")
	}
	if got := renew(99000); got != 60000 {
		t.Fatalf("a held row must not be renewed: %d", got)
	}
	// An unknown id is not an error.
	if err := s.RenewLease("nope", 1); err != nil {
		t.Fatal(err)
	}
}

func TestStore_GrantCAS(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, baseRow("a", "c1"))
	mustCreate(t, s, baseRow("b", "c2"))

	if ok, err := s.Grant("a", 5000, true, true); err != nil || !ok {
		t.Fatalf("grant a: %v %v", ok, err)
	}
	a, _, _ := s.Get("a")
	if a.State != resources.StateHeld || a.GrantedAt != 5000 || a.WaitedMS != 4000 || !a.Overrun || !a.WouldWait {
		t.Fatalf("granted row = %+v", a)
	}
	if ok, _ := s.Grant("a", 6000, false, false); ok {
		t.Fatal("a second grant must lose")
	}
	if a2, _, _ := s.Get("a"); a2.GrantedAt != 5000 {
		t.Fatal("a lost grant must not touch the row")
	}

	// Grant after end loses.
	if ok, _ := s.End("b", resources.EndCancelled, 3000); !ok {
		t.Fatal("end b")
	}
	if ok, _ := s.Grant("b", 4000, false, false); ok {
		t.Fatal("grant after end must be false")
	}
	b, _, _ := s.Get("b")
	if b.State != resources.StateEnded || b.EndReason != resources.EndCancelled || b.EndedAt != 3000 || b.WaitedMS != 2000 {
		t.Fatalf("ended row = %+v", b)
	}
	// End is a one-shot too, and keeps a recorded wait.
	if ok, _ := s.End("b", resources.EndReleased, 9000); ok {
		t.Fatal("a second end must lose")
	}
	if ok, _ := s.End("a", resources.EndReleased, 9000); !ok {
		t.Fatal("end a")
	}
	if a3, _, _ := s.Get("a"); a3.WaitedMS != 4000 || a3.EndReason != resources.EndReleased {
		t.Fatalf("end must keep the recorded wait: %+v", a3)
	}
}

func TestStore_CloseIfExpiredLosesToRenew(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, baseRow("a", "c1")) // lease_until 31000
	mustCreate(t, s, baseRow("h", "c2"))
	if ok, _ := s.Grant("h", 2000, false, false); !ok {
		t.Fatal("grant h")
	}

	// The sweeper read the row as overdue at now=40000 ...
	// ... but a poll renewed it before the close landed.
	if err := s.RenewLease("a", 70000); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CloseIfExpired("a", 40000); err != nil || ok {
		t.Fatalf("a renewed row must not be closed: ok=%v err=%v", ok, err)
	}
	if a, _, _ := s.Get("a"); a.State != resources.StateWaiting {
		t.Fatalf("row = %+v", a)
	}

	if ok, err := s.CloseIfExpired("a", 70000); err != nil || !ok {
		t.Fatalf("an overdue row must close: ok=%v err=%v", ok, err)
	}
	a, _, _ := s.Get("a")
	if a.State != resources.StateEnded || a.EndReason != resources.EndAbandoned || a.EndedAt != 70000 || a.WaitedMS != 69000 {
		t.Fatalf("abandoned row = %+v", a)
	}
	// A held row has no lease to expire.
	if ok, _ := s.CloseIfExpired("h", 1<<40); ok {
		t.Fatal("a held row must not be closed as abandoned")
	}
}

func TestStore_BootGrace(t *testing.T) {
	s := newTestStore(t)
	short := baseRow("short", "c1")
	short.LeaseUntil = 10000
	long := baseRow("long", "c2")
	long.LeaseUntil = 90000
	mustCreate(t, s, short)
	mustCreate(t, s, long)
	mustCreate(t, s, baseRow("held", "c3"))
	if ok, _ := s.Grant("held", 2000, false, false); !ok {
		t.Fatal("grant")
	}

	n, err := s.ExtendWaiting(50000)
	if err != nil || n != 1 {
		t.Fatalf("extended %d rows (err %v), want 1", n, err)
	}
	get := func(id string) leaseRow { r, _, _ := s.Get(id); return r }
	if get("short").LeaseUntil != 50000 {
		t.Fatalf("short = %d, want 50000", get("short").LeaseUntil)
	}
	if get("long").LeaseUntil != 90000 {
		t.Fatalf("long = %d: the grace must never shorten a lease", get("long").LeaseUntil)
	}
	if get("held").LeaseUntil != 31000 {
		t.Fatalf("held = %d: only waiting rows are extended", get("held").LeaseUntil)
	}
}

func TestStore_Retention(t *testing.T) {
	s := newTestStore(t)
	for i, id := range []string{"old", "fresh", "waiting", "held"} {
		mustCreate(t, s, baseRow(id, "c"+id))
		_ = i
	}
	if ok, _ := s.End("old", resources.EndReleased, 1000); !ok {
		t.Fatal("end old")
	}
	if ok, _ := s.End("fresh", resources.EndReleased, 900000); !ok {
		t.Fatal("end fresh")
	}
	if ok, _ := s.Grant("held", 2000, false, false); !ok {
		t.Fatal("grant")
	}
	n, err := s.Prune(500000)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d (err %v), want 1", n, err)
	}
	if _, ok, _ := s.Get("old"); ok {
		t.Fatal("an old ended row must be deleted")
	}
	for _, id := range []string{"fresh", "waiting", "held"} {
		if _, ok, _ := s.Get(id); !ok {
			t.Fatalf("%s must survive", id)
		}
	}
}

// Codex attack (high): the lists /api/resources shows are one view. A writer
// keeps creating rows and moving each waiting -> held -> ended while Listing
// runs. Every row created before a call must be in exactly one of its three
// lists; three separate queries lose a row granted between the first two, or
// show an ended one in two lists.
func TestStore_ListingIsOneConsistentView(t *testing.T) {
	s := newTestStore(t)
	const total = 120
	var created atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			id := fmt.Sprintf("r%d", i)
			if _, _, err := s.Create(baseRow(id, "c"+id)); err != nil {
				return
			}
			created.Add(1)
			_, _ = s.Grant(id, int64(2000+i), false, false)
			_, _ = s.End(id, resources.EndReleased, int64(3000+i))
		}
	}()

	polls := 0
	for finished := false; !finished; {
		select {
		case <-done:
			finished = true // one last poll after the writer is done
		default:
		}
		polls++
		before := int(created.Load())
		active, waiting, recent, err := s.Listing(1_000_000, 0)
		if err != nil {
			t.Fatal(err)
		}
		after := int(created.Load())
		seen := map[string]int{}
		for _, list := range [][]leaseRow{active, waiting, recent} {
			for _, r := range list {
				seen[r.ID]++
			}
		}
		for id, c := range seen {
			if c != 1 {
				t.Fatalf("poll %d: %s is in %d lists", polls, id, c)
			}
		}
		// The writer may have created one more row than it has counted yet.
		if len(seen) < before || len(seen) > after+1 {
			t.Fatalf("poll %d: %d rows listed, but %d existed before the call and %d after (active %d waiting %d recent %d)",
				polls, len(seen), before, after, len(active), len(waiting), len(recent))
		}
	}
	if polls < 10 {
		t.Fatalf("only %d polls overlapped the writer: the test did not exercise anything", polls)
	}
}

func TestStore_ListsAndRecent(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"w2", "w1", "h1", "e1", "e2"} {
		r := baseRow(id, "c"+id)
		if id == "w2" {
			r.CreatedAt = 3000
		}
		mustCreate(t, s, r)
	}
	if ok, _ := s.Grant("h1", 2000, false, false); !ok {
		t.Fatal("grant")
	}
	s.End("e1", resources.EndReleased, 7000)
	s.End("e2", resources.EndCancelled, 8000)

	ids := func(rows []leaseRow) (out []string) {
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return
	}
	if got := ids(must(s.Waiting())); len(got) != 2 || got[0] != "w1" || got[1] != "w2" {
		t.Fatalf("waiting = %v, want queue order [w1 w2]", got)
	}
	if got := ids(must(s.Active())); len(got) != 1 || got[0] != "h1" {
		t.Fatalf("active = %v", got)
	}
	if got := ids(must(s.Recent(10, 0))); len(got) != 2 || got[0] != "e2" {
		t.Fatalf("recent = %v, want newest first", got)
	}
	if got := ids(must(s.Recent(1, 0))); len(got) != 1 {
		t.Fatalf("recent limit = %v", got)
	}
	if got := ids(must(s.Recent(10, 7500))); len(got) != 1 || got[0] != "e2" {
		t.Fatalf("recent since = %v", got)
	}
}

func must(rows []leaseRow, err error) []leaseRow {
	if err != nil {
		panic(err)
	}
	return rows
}

func TestStore_UpdateUsePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	s, err := openLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, baseRow("a", "c1"))
	if ok, _ := s.Grant("a", 2000, false, false); !ok {
		t.Fatal("grant")
	}
	if err := s.UpdateUse("a", 30, 55, 28.5, 9, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := openLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	a, ok, _ := s2.Get("a")
	if !ok || a.EWMA != 30 || a.PeakUse != 55 || a.MeanUse != 28.5 || a.Samples != 9 || a.EmptySamples != 1 {
		t.Fatalf("reopened row = %+v", a)
	}
}
