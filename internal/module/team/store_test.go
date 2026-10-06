package teammod

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
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

// closeFields is the part of an Approval a Close writes; it is what every
// contender in the CAS race must agree on afterwards.
type closeFields struct {
	State     team.State
	DecidedAt int64
	DecidedBy *team.Client
	Grant     *team.Grant
}

func closeFieldsOf(a team.Approval) closeFields {
	return closeFields{State: a.State, DecidedAt: a.DecidedAt, DecidedBy: a.DecidedBy, Grant: a.Grant}
}

func TestStore_CloseIfOpenExactlyOneWinner(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("id-1", "sid-1", 1000), "h1"); err != nil {
		t.Fatal(err)
	}
	// The four ways a request closes under contention (spec §15): the
	// sweeper's timeout, a denial, the requester's cancel, and an approval
	// that carries a grant. Every contender has a distinct DecidedAt so a
	// leaked field from the wrong close is detectable.
	const n = 16
	type attempt struct {
		in     Close
		after  team.Approval
		closed bool
	}
	results := make([]attempt, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var c Close
			switch i % 4 {
			case 0:
				c = Close{State: team.StateTimeout, DecidedAt: int64(2000 + i)}
			case 1:
				c = Close{State: team.StateDenied, DecidedAt: int64(2000 + i),
					DecidedBy: &team.Client{Kind: "app", Label: fmt.Sprintf("Purdex.app @ test-%d", i)}}
			case 2:
				c = Close{State: team.StateCancelled, DecidedAt: int64(2000 + i)}
			case 3:
				c = Close{State: team.StateApproved, DecidedAt: int64(2000 + i),
					DecidedBy: &team.Client{Kind: "app", Label: fmt.Sprintf("Purdex.app @ test-%d", i)},
					Grant:     &team.Grant{MaxMembers: 2, Roots: []string{"/w"}}}
			}
			after, closed, err := s.CloseIfOpen("id-1", c)
			if err != nil {
				t.Errorf("close %d: %v", i, err)
				return
			}
			results[i] = attempt{in: c, after: after, closed: closed}
		}(i)
	}
	wg.Wait()
	if t.Failed() {
		t.Fatal("a close attempt errored")
	}

	winner := -1
	for i, r := range results {
		if r.closed {
			if winner >= 0 {
				t.Fatalf("two winners: %d and %d", winner, i)
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatal("no contender won")
	}
	won := results[winner]
	// The winner's returned row is its own close, field by field.
	want := closeFields{State: won.in.State, DecidedAt: won.in.DecidedAt, DecidedBy: won.in.DecidedBy, Grant: won.in.Grant}
	if got := closeFieldsOf(won.after); !reflect.DeepEqual(got, want) {
		t.Fatalf("winner %d returned row %+v, want its own close %+v", winner, got, want)
	}
	// Every loser is handed the winner's close whole (spec §6.2: a late
	// decide answers 409 already_decided carrying the closed request with
	// decided_by — and the grant, when the winner approved).
	for i, r := range results {
		if i == winner {
			continue
		}
		if r.closed {
			t.Fatalf("loser %d reports closed=true", i)
		}
		if got := closeFieldsOf(r.after); !reflect.DeepEqual(got, want) {
			t.Fatalf("loser %d (%s) returned row %+v, want winner %d's row %+v", i, r.in.State, got, winner, want)
		}
	}
	final, ok, err := s.Get("id-1")
	if err != nil || !ok {
		t.Fatalf("get after race: ok=%v err=%v", ok, err)
	}
	if got := closeFieldsOf(final); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored row %+v, want winner %d's row %+v", got, winner, want)
	}

	// Deterministic counterpart of the race (the winner above is random): an
	// approval wins, then a late deny loses and must be handed the approval
	// whole — decided_by and grant included — regardless of its own input.
	if _, _, _, err := s.Create(openApproval("id-2", "sid-2", 1000), "h2"); err != nil {
		t.Fatal(err)
	}
	approve := Close{State: team.StateApproved, DecidedAt: 5000,
		DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ test"},
		Grant:     &team.Grant{MaxMembers: 2, Roots: []string{"/w"}}}
	first, closed, err := s.CloseIfOpen("id-2", approve)
	if err != nil || !closed {
		t.Fatalf("approve: closed=%v err=%v", closed, err)
	}
	wantApproved := closeFields{State: approve.State, DecidedAt: approve.DecidedAt, DecidedBy: approve.DecidedBy, Grant: approve.Grant}
	if got := closeFieldsOf(first); !reflect.DeepEqual(got, wantApproved) {
		t.Fatalf("approve returned %+v, want %+v", got, wantApproved)
	}
	late, closed, err := s.CloseIfOpen("id-2", Close{State: team.StateDenied, DecidedAt: 6000,
		DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ late"}})
	if err != nil || closed {
		t.Fatalf("late deny: closed=%v err=%v, want a loss", closed, err)
	}
	if got := closeFieldsOf(late); !reflect.DeepEqual(got, wantApproved) {
		t.Fatalf("late deny returned %+v, want the approval %+v", got, wantApproved)
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

// CloseIfExpired is the sweeper's CAS (review F2): besides state='open' it
// requires the row to still be overdue in the same statement — deadline_at
// for a timeout, lease_until for an abandonment — so a lease renewed after
// the sweeper's read makes the close lose and leaves the row open.
func TestStore_CloseIfExpiredGuardsOnDeadlineOrLease(t *testing.T) {
	s := openTestStore(t)
	if _, _, _, err := s.Create(openApproval("a", "sid-1", 1000), "h"); err != nil { // lease 31000, deadline 541000
		t.Fatal(err)
	}
	// Lease not yet out: an abandonment loses and the row stays open.
	a, won, err := s.CloseIfExpired("a", 30_999, Close{State: team.StateAbandoned, DecidedAt: 30_999})
	if err != nil || won || a.State != team.StateOpen {
		t.Fatalf("abandon before the lease ran out: won=%v err=%v row=%+v", won, err, a)
	}
	// Lease out, but renewed in between: still loses.
	if err := s.RenewLease("a", 60_000); err != nil {
		t.Fatal(err)
	}
	a, won, err = s.CloseIfExpired("a", 31_000, Close{State: team.StateAbandoned, DecidedAt: 31_000})
	if err != nil || won || a.State != team.StateOpen || a.LeaseUntil != 60_000 {
		t.Fatalf("abandon after a renewal: won=%v err=%v row=%+v", won, err, a)
	}
	// Deadline not yet passed: a timeout loses too.
	a, won, err = s.CloseIfExpired("a", 540_999, Close{State: team.StateTimeout, DecidedAt: 540_999})
	if err != nil || won || a.State != team.StateOpen {
		t.Fatalf("timeout before the deadline: won=%v err=%v row=%+v", won, err, a)
	}
	// Only the sweeper's two states have a guard.
	if _, _, err := s.CloseIfExpired("a", 999_999, Close{State: team.StateCancelled, DecidedAt: 999_999}); err == nil {
		t.Fatal("CloseIfExpired must refuse a state without an expiry guard")
	}
	// Lease out: abandoned, once.
	a, won, err = s.CloseIfExpired("a", 60_000, Close{State: team.StateAbandoned, DecidedAt: 60_000})
	if err != nil || !won || a.State != team.StateAbandoned || a.DecidedAt != 60_000 {
		t.Fatalf("abandon after the renewed lease ran out: won=%v err=%v row=%+v", won, err, a)
	}
	if _, won, err := s.CloseIfExpired("a", 999_999, Close{State: team.StateTimeout, DecidedAt: 999_999}); err != nil || won {
		t.Fatalf("closed row: won=%v err=%v", won, err)
	}
	if _, _, err := s.CloseIfExpired("nope", 1, Close{State: team.StateTimeout, DecidedAt: 1}); !errors.Is(err, ErrNoSuchApproval) {
		t.Fatalf("unknown id: %v, want ErrNoSuchApproval", err)
	}
	// Deadline passed: timeout wins whatever the lease says.
	if _, _, _, err := s.Create(openApproval("b", "sid-2", 1000), "h"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenewLease("b", 999_999_999); err != nil {
		t.Fatal(err)
	}
	if a, won, err := s.CloseIfExpired("b", 541_000, Close{State: team.StateTimeout, DecidedAt: 541_000}); err != nil || !won || a.State != team.StateTimeout {
		t.Fatalf("timeout at the deadline: won=%v err=%v row=%+v", won, err, a)
	}
}
