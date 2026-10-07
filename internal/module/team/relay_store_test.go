package teammod

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func selfOp(id, sid, ref string, createdAt int64) team.RelayOp {
	pct := 72.4
	return team.RelayOp{
		ID: id, Kind: team.RelayKindSelf, HostID: "h:1", SessionID: sid, Ref: ref, RequestID: "req-" + id,
		State: team.RelayAwaitingApproval, HandoffPath: "/d/relay/" + id + ".md", UsedPercentage: &pct,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func mustReport(t *testing.T, s *Store, id string, r RelayReport) team.RelayOp {
	t.Helper()
	op, res, err := s.ReportRelay(id, r)
	if err != nil || res != ReportApplied {
		t.Fatalf("report %s → %s: res=%v err=%v op=%+v", id, r.State, res, err, op)
	}
	return op
}

func TestRelayStore_CreateGetAndOpenBySession(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op-1", "sid-old", "_aaaaaa", 1000)
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRelayOp(op); err == nil {
		t.Fatal("a duplicate op id must be an error (ids are daemon-minted, never retried)")
	}
	got, ok, err := s.GetRelayOp("op-1")
	if err != nil || !ok || !reflect.DeepEqual(got, op) {
		t.Fatalf("get: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := s.GetRelayOp("nope"); err != nil || ok {
		t.Fatalf("get unknown: ok=%v err=%v", ok, err)
	}
	open, ok, err := s.OpenRelayOpBySession("sid-old")
	if err != nil || !ok || open.ID != "op-1" {
		t.Fatalf("open by session: %+v ok=%v err=%v", open, ok, err)
	}
	byReq, ok, err := s.RelayOpByRequest("req-op-1")
	if err != nil || !ok || byReq.ID != "op-1" {
		t.Fatalf("by request: %+v ok=%v err=%v", byReq, ok, err)
	}
	active, err := s.ListActiveRelayOps()
	if err != nil || len(active) != 1 {
		t.Fatalf("active = %+v err=%v", active, err)
	}
	mustReport(t, s, "op-1", RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonDenied, At: 2000})
	if _, ok, _ := s.OpenRelayOpBySession("sid-old"); ok {
		t.Fatal("a cancelled op is not open")
	}
	if active, _ := s.ListActiveRelayOps(); len(active) != 0 || active == nil {
		t.Fatalf("active after cancel = %+v (must be [] not nil)", active)
	}
}

// Spec §8.3: reports are idempotent per (op, state); §8.1: the forward path
// awaiting_approval → claimed → writing → written → cleared → done.
func TestRelayStore_ReportTransitionsAndIdempotency(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateRelayOp(selfOp("op-1", "sid-old", "_aaaaaa", 1000)); err != nil {
		t.Fatal(err)
	}
	// A report of a state the op is not in and cannot reach: bad_transition, row unchanged.
	op, res, err := s.ReportRelay("op-1", RelayReport{State: team.RelayWriting, At: 1500})
	if err != nil || res != ReportBadTransition || op.State != team.RelayAwaitingApproval {
		t.Fatalf("awaiting → writing: res=%v err=%v op=%+v", res, err, op)
	}
	mustReport(t, s, "op-1", RelayReport{State: team.RelayClaimed, At: 2000})
	// Same state again: no-op, 200-equivalent, updated_at untouched.
	op, res, err = s.ReportRelay("op-1", RelayReport{State: team.RelayClaimed, At: 2500})
	if err != nil || res != ReportNoop || op.UpdatedAt != 2000 {
		t.Fatalf("claimed twice: res=%v err=%v op=%+v", res, err, op)
	}
	mustReport(t, s, "op-1", RelayReport{State: team.RelayWriting, At: 3000})
	mustReport(t, s, "op-1", RelayReport{State: team.RelayWritten, At: 4000})
	// A stale re-send of an earlier state is refused, not applied.
	if _, res, _ := s.ReportRelay("op-1", RelayReport{State: team.RelayWriting, At: 4500}); res != ReportBadTransition {
		t.Fatalf("written → writing: res=%v, want bad_transition", res)
	}
	cleared := mustReport(t, s, "op-1", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_bbbbbb", At: 5000})
	if cleared.NewSessionID != "sid-new" || cleared.NewRef != "_bbbbbb" {
		t.Fatalf("cleared = %+v", cleared)
	}
	done := mustReport(t, s, "op-1", RelayReport{State: team.RelayDone, At: 6000})
	if !done.State.Terminal() {
		t.Fatalf("done = %+v", done)
	}
	// Terminal: nothing leads out of it.
	if _, res, _ := s.ReportRelay("op-1", RelayReport{State: team.RelayFailed, Reason: "x", At: 7000}); res != ReportBadTransition {
		t.Fatalf("done → failed: res=%v, want bad_transition", res)
	}
	if _, _, err := s.ReportRelay("nope", RelayReport{State: team.RelayDone}); !errors.Is(err, ErrNoSuchRelayOp) {
		t.Fatalf("unknown op: err=%v", err)
	}
}

// Spec §8.4 / §15 "Lineage": cleared writes session_lineage in the same
// transaction, and an uncapped chain still resolves the oldest ref after 11
// or more relays. PreviousRefs is newest first.
func TestRelayStore_ClearedWritesLineageAndChainIsUncapped(t *testing.T) {
	s := openTestStore(t)
	const n = 12
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("op-%d", i)
		old, next := fmt.Sprintf("sid-%d", i), fmt.Sprintf("sid-%d", i+1)
		oldRef, nextRef := fmt.Sprintf("_r%05d", i), fmt.Sprintf("_r%05d", i+1)
		op := selfOp(id, old, oldRef, int64(1000*(i+1)))
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
		mustReport(t, s, id, RelayReport{State: team.RelayCleared, NewSessionID: next, NewRef: nextRef, At: int64(1000*(i+1) + 500)})
	}
	refs, err := s.PreviousRefs()
	if err != nil {
		t.Fatal(err)
	}
	head := refs[fmt.Sprintf("sid-%d", n)]
	if len(head) != n {
		t.Fatalf("head chain has %d refs, want %d (uncapped): %v", len(head), n, head)
	}
	if head[0] != fmt.Sprintf("_r%05d", n-1) || head[n-1] != "_r00000" {
		t.Fatalf("chain must be newest first: %v", head)
	}
	// An intermediate session is also a head of its own (shorter) chain.
	if mid := refs["sid-3"]; !reflect.DeepEqual(mid, []string{"_r00002", "_r00001", "_r00000"}) {
		t.Fatalf("sid-3 chain = %v", mid)
	}
	// A second cleared report for the same op is a no-op and writes no second lineage row.
	op, res, err := s.ReportRelay("op-0", RelayReport{State: team.RelayCleared, NewSessionID: "other", NewRef: "_zzzzzz", At: 9})
	if err != nil || res != ReportNoop || op.NewSessionID != "sid-1" {
		t.Fatalf("cleared twice: res=%v err=%v op=%+v", res, err, op)
	}
	if refs2, _ := s.PreviousRefs(); !reflect.DeepEqual(refs2, refs) {
		t.Fatal("a no-op report must not change the lineage")
	}
}

// Two concurrent reports of different next states: exactly one applies; the
// loser sees bad_transition against the winner's state, never an error.
func TestRelayStore_ConcurrentReportsOneWins(t *testing.T) {
	s := openTestStore(t)
	op := selfOp("op-1", "sid", "_aaaaaa", 1000)
	op.State = team.RelayClaimed
	if err := s.CreateRelayOp(op); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]ReportResult, 2)
	errs := make([]error, 2)
	for i, st := range []team.RelayState{team.RelayFailed, team.RelayCancelled} {
		wg.Add(1)
		go func(i int, st team.RelayState) {
			defer wg.Done()
			_, results[i], errs[i] = s.ReportRelay("op-1", RelayReport{State: st, Reason: "r", At: 2000})
		}(i, st)
	}
	wg.Wait()
	applied := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("report %d: %v", i, errs[i])
		}
		if results[i] == ReportApplied {
			applied++
		} else if results[i] != ReportBadTransition {
			t.Fatalf("report %d: res=%v", i, results[i])
		}
	}
	if applied != 1 {
		t.Fatalf("%d reports applied, want exactly 1", applied)
	}
}

func TestRelayStore_SelfRelayPause(t *testing.T) {
	s := openTestStore(t)
	if p, err := s.SelfRelayPaused("sid"); err != nil || p {
		t.Fatalf("default paused=%v err=%v", p, err)
	}
	if err := s.SetSelfRelayPaused("sid", true, 10); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.SelfRelayPaused("sid"); !p {
		t.Fatal("paused must read back true")
	}
	if err := s.SetSelfRelayPaused("sid", false, 20); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.SelfRelayPaused("sid"); p {
		t.Fatal("on lifts the pause")
	}
}

// The database holds "at most one open relay per session" (spec §8.7) by
// itself: a second non-terminal op for a session is refused with
// ErrRelayOpOpen whatever the caller checked first; once the open op is
// terminal a new one may be created, and terminal history never collides.
func TestRelayStore_OneOpenOpPerSessionIsEnforcedByTheTable(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateRelayOp(selfOp("op-1", "sid-1", "_aaaaaa", 1000)); err != nil {
		t.Fatal(err)
	}
	second := selfOp("op-2", "sid-1", "_aaaaaa", 1001)
	second.State = team.RelayRequested
	if err := s.CreateRelayOp(second); !errors.Is(err, ErrRelayOpOpen) {
		t.Fatalf("second open op for sid-1: err = %v, want ErrRelayOpOpen", err)
	}
	if _, ok, _ := s.GetRelayOp("op-2"); ok {
		t.Fatal("the refused op must not have been inserted")
	}
	// A different session is unaffected.
	if err := s.CreateRelayOp(selfOp("op-3", "sid-2", "_bbbbbb", 1002)); err != nil {
		t.Fatal(err)
	}
	// Once op-1 is terminal, sid-1 may open again; two terminal rows coexist.
	mustReport(t, s, "op-1", RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonDenied, At: 1003})
	if err := s.CreateRelayOp(second); err != nil {
		t.Fatalf("after op-1 cancelled: %v", err)
	}
	mustReport(t, s, "op-2", RelayReport{State: team.RelayCancelled, Reason: team.RelayReasonDenied, At: 1004})
	if err := s.CreateRelayOp(selfOp("op-4", "sid-1", "_aaaaaa", 1005)); err != nil {
		t.Fatalf("third op after two terminal ones: %v", err)
	}
	// Racing creators: exactly one of n concurrent inserts for one fresh
	// session wins, the others see ErrRelayOpOpen, none errors otherwise.
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.CreateRelayOp(selfOp(fmt.Sprintf("race-%d", i), "sid-race", "_cccccc", int64(2000+i)))
		}(i)
	}
	wg.Wait()
	won, open := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrRelayOpOpen):
			open++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if won != 1 || open != n-1 {
		t.Fatalf("won = %d, refused = %d, want 1 and %d", won, open, n-1)
	}
}

// A `cleared` report that would corrupt the lineage is refused inside the
// transaction and leaves the op and the lineage exactly as they were: empty
// new session or ref, new == old, two ops clearing into one new session,
// and a cycle (A→B, then B→A).
func TestRelayStore_ClearedRefusesCorruptLineage(t *testing.T) {
	s := openTestStore(t)
	mk := func(id, sid, ref string) {
		t.Helper()
		op := selfOp(id, sid, ref, 1000)
		op.State = team.RelayClaimed
		if err := s.CreateRelayOp(op); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(id string, r RelayReport, why string) {
		t.Helper()
		before, _, _ := s.GetRelayOp(id)
		got, res, err := s.ReportRelay(id, r)
		if !errors.Is(err, ErrBadRelayReport) || res != ReportBadTransition {
			t.Fatalf("%s: err = %v, res = %v, want ErrBadRelayReport", why, err, res)
		}
		if got.State != before.State || got.NewSessionID != before.NewSessionID {
			t.Fatalf("%s: returned op %+v differs from the stored one %+v", why, got, before)
		}
		after, _, _ := s.GetRelayOp(id)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: the op changed: %+v → %+v", why, before, after)
		}
	}
	mk("op-a", "A", "_aaaaaa")
	refuse("op-a", RelayReport{State: team.RelayCleared, NewRef: "_bbbbbb", At: 1}, "empty new session")
	refuse("op-a", RelayReport{State: team.RelayCleared, NewSessionID: "B", At: 1}, "empty new ref")
	refuse("op-a", RelayReport{State: team.RelayCleared, NewSessionID: "A", NewRef: "_aaaaaa", At: 1}, "new == old")
	refs, _ := s.PreviousRefs()
	if len(refs) != 0 {
		t.Fatalf("lineage written by a refused report: %v", refs)
	}

	// A → C accepted; then B → C must be refused (C already heads op-a's lineage).
	mustReport(t, s, "op-a", RelayReport{State: team.RelayCleared, NewSessionID: "C", NewRef: "_cccccc", At: 2})
	mk("op-b", "B", "_bbbbbb")
	refuse("op-b", RelayReport{State: team.RelayCleared, NewSessionID: "C", NewRef: "_cccccc", At: 3}, "two ops into one new session")
	refs, _ = s.PreviousRefs()
	if !reflect.DeepEqual(refs["C"], []string{"_aaaaaa"}) {
		t.Fatalf("C's lineage must still be op-a's alone: %v", refs)
	}

	// C → A would close a cycle (A is C's ancestor).
	mk("op-c", "C", "_cccccc")
	refuse("op-c", RelayReport{State: team.RelayCleared, NewSessionID: "A", NewRef: "_aaaaaa", At: 4}, "cycle")
	// C → D is fine, and the chain reads D ← C ← A.
	mustReport(t, s, "op-c", RelayReport{State: team.RelayCleared, NewSessionID: "D", NewRef: "_dddddd", At: 5})
	refs, _ = s.PreviousRefs()
	if !reflect.DeepEqual(refs["D"], []string{"_cccccc", "_aaaaaa"}) {
		t.Fatalf("D's chain = %v, want [_cccccc _aaaaaa]", refs["D"])
	}
	// Reporting cleared again on op-c is the idempotent no-op, lineage untouched.
	if _, res, err := s.ReportRelay("op-c", RelayReport{State: team.RelayCleared, NewSessionID: "D", NewRef: "_dddddd", At: 6}); err != nil || res != ReportNoop {
		t.Fatalf("second cleared: res = %v err = %v", res, err)
	}
}
