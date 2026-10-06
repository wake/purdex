package teammod

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openApproval(id, sid string, createdAt int64) team.Approval {
	payload, _ := json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3, Roots: []string{"/w"}})
	return team.Approval{
		ID: id, Kind: team.KindLead, HostID: "h:1",
		Origin:  team.Origin{SessionID: sid, Ref: "_abc123", PID: 10, Cwd: "/w"},
		Payload: payload, State: team.StateOpen,
		CreatedAt: createdAt, DeadlineAt: createdAt + 540_000, LeaseUntil: createdAt + 30_000,
	}
}

func TestStore_CreateIsIdempotentAndReportsHashMismatch(t *testing.T) {
	s := openTestStore(t)
	a := openApproval("id-1", "sid-1", 1000)
	stored, hash, inserted, err := s.Create(a, "h1")
	if err != nil || !inserted || hash != "h1" || stored.State != team.StateOpen || stored.LeaseUntil != 31000 {
		t.Fatalf("first create: stored=%+v hash=%q inserted=%v err=%v", stored, hash, inserted, err)
	}
	retry := a
	retry.LeaseUntil = 99 // must NOT overwrite the stored row
	stored2, hash2, inserted2, err := s.Create(retry, "h1")
	if err != nil || inserted2 || hash2 != "h1" || stored2.LeaseUntil != 31000 {
		t.Fatalf("retry: stored=%+v hash=%q inserted=%v err=%v", stored2, hash2, inserted2, err)
	}
	_, hash3, inserted3, err := s.Create(a, "h2") // same id, different request
	if err != nil || inserted3 || hash3 != "h1" {
		t.Fatalf("conflict: hash=%q inserted=%v err=%v (caller compares h2 != h1 → id_conflict)", hash3, inserted3, err)
	}
	got, ok, err := s.Get("id-1")
	if err != nil || !ok || got.Origin.SessionID != "sid-1" || string(got.Payload) != string(a.Payload) {
		t.Fatalf("get: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.Get("nope"); err != nil || ok {
		t.Fatalf("get unknown: ok=%v err=%v", ok, err)
	}
}

func TestStore_CloseIfOpenExactlyOneWinner(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	wins := make(chan team.State, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// The three ways a request closes under contention (spec §15):
			// a decision, the sweeper's timeout, the requester's cancel.
			var c Close
			switch i % 3 {
			case 0:
				c = Close{State: team.StateTimeout, DecidedAt: int64(2000 + i)}
			case 1:
				c = Close{State: team.StateDenied, DecidedAt: int64(2000 + i), DecidedBy: &team.Client{Kind: "app", Label: fmt.Sprintf("app-%d", i)}}
			case 2:
				c = Close{State: team.StateCancelled, DecidedAt: int64(2000 + i)}
			}
			after, won, err := s.CloseIfOpen("id-1", c)
			if err != nil {
				t.Errorf("close %d: %v", i, err)
				return
			}
			if after.State == team.StateOpen {
				t.Errorf("close %d: row still open after the attempt", i)
			}
			if won {
				wins <- after.State
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	var winners []team.State
	for st := range wins {
		winners = append(winners, st)
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %v, want exactly one", winners)
	}
	final, _, _ := s.Get("id-1")
	if final.State != winners[0] {
		t.Fatalf("final state %s, winner %s", final.State, winners[0])
	}
	if final.State == team.StateDenied && (final.DecidedBy == nil || final.DecidedBy.Kind != "app") {
		t.Fatalf("denied without decided_by: %+v", final)
	}
	if final.State != team.StateDenied && final.DecidedBy != nil {
		t.Fatalf("%s must not carry decided_by: %+v", final.State, final)
	}
	if _, _, err := s.CloseIfOpen("nope", Close{State: team.StateCancelled}); err != ErrNoSuchApproval {
		t.Fatalf("unknown id: err=%v, want ErrNoSuchApproval", err)
	}
}

func TestStore_LeasesAndListing(t *testing.T) {
	s := openTestStore(t)
	for _, a := range []team.Approval{openApproval("b", "sid-2", 2000), openApproval("a", "sid-1", 1000), openApproval("c", "sid-1", 1000)} {
		if _, _, _, err := s.Create(a, "h"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RenewLease("a", 50_000); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewLease("a", 40_000); err != nil { // never backwards
		t.Fatal(err)
	}
	if a, _, _ := s.Get("a"); a.LeaseUntil != 50_000 {
		t.Fatalf("lease after renew = %d, want 50000", a.LeaseUntil)
	}
	n, err := s.ExtendOpenLeases(45_000) // boot grace: a stays at 50000, b and c move up
	if err != nil || n != 2 {
		t.Fatalf("extend: n=%d err=%v", n, err)
	}
	if b, _, _ := s.Get("b"); b.LeaseUntil != 45_000 {
		t.Fatalf("b lease = %d", b.LeaseUntil)
	}
	open, err := s.ListOpen()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 3 || open[0].ID != "a" || open[1].ID != "c" || open[2].ID != "b" {
		t.Fatalf("ListOpen order = %v, want a c b (created_at, id)", open)
	}
	if _, _, err := s.CloseIfOpen("a", Close{State: team.StateCancelled, DecidedAt: 3000}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewLease("a", 99_000); err != nil { // closed: ignored
		t.Fatal(err)
	}
	if a, _, _ := s.Get("a"); a.LeaseUntil != 50_000 || a.DecidedAt != 3000 {
		t.Fatalf("closed row changed: %+v", a)
	}
	got, ok, err := s.OpenByOrigin("sid-1", team.KindLead)
	if err != nil || !ok || got.ID != "c" {
		t.Fatalf("OpenByOrigin sid-1 = %+v ok=%v err=%v, want c", got, ok, err)
	}
	if _, ok, _ := s.OpenByOrigin("sid-1", team.KindSelfRelay); ok {
		t.Fatal("OpenByOrigin must filter by kind")
	}
	if open, _ := s.ListOpen(); len(open) != 2 {
		t.Fatalf("ListOpen after close = %d rows", len(open))
	}
}
