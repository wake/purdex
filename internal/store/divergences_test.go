package store

import (
	"fmt"
	"sync"
	"testing"
)

func openTestDivergenceStore(t *testing.T) *DivergenceStore {
	t.Helper()
	events := openTestAgentEventStore(t)
	divs, err := events.Divergences()
	if err != nil {
		t.Fatalf("Divergences: %v", err)
	}
	return divs
}

func TestDivergenceStore_InsertAndFetchRoundtrip(t *testing.T) {
	s := openTestDivergenceStore(t)

	d := FrameDivergence{
		SessionID:          "proj-a",
		TraceID:            "chain-1",
		EventID:            "evt-1",
		ObservedGeneration: 42,
		OldStateRef:        []byte(`{"frame":"old"}`),
		ProposalStateRef:   []byte(`{"frame":"new"}`),
		DiffSummary:        `{"changed":["decision"]}`,
		Matched:            false,
		ReasonCode:         "projection_mismatch",
		CreatedAt:          1700000000,
	}

	id, err := s.Insert(d)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Insert id = %d, want > 0", id)
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != id {
		t.Fatalf("ID = %d, want %d", got.ID, id)
	}
	if got.SessionID != d.SessionID || got.TraceID != d.TraceID || got.EventID != d.EventID {
		t.Fatalf("identifiers mismatch: %+v", got)
	}
	if got.ObservedGeneration != d.ObservedGeneration {
		t.Fatalf("ObservedGeneration = %d, want %d", got.ObservedGeneration, d.ObservedGeneration)
	}
	if string(got.OldStateRef) != string(d.OldStateRef) {
		t.Fatalf("OldStateRef = %s", string(got.OldStateRef))
	}
	if string(got.ProposalStateRef) != string(d.ProposalStateRef) {
		t.Fatalf("ProposalStateRef = %s", string(got.ProposalStateRef))
	}
	if got.DiffSummary != d.DiffSummary {
		t.Fatalf("DiffSummary = %q", got.DiffSummary)
	}
	if got.Matched != d.Matched {
		t.Fatalf("Matched = %v, want %v", got.Matched, d.Matched)
	}
	if got.ReasonCode != d.ReasonCode {
		t.Fatalf("ReasonCode = %q", got.ReasonCode)
	}
	if got.CreatedAt != d.CreatedAt {
		t.Fatalf("CreatedAt = %d", got.CreatedAt)
	}
}

func TestDivergenceStore_MatchedFlagPersistsBothValues(t *testing.T) {
	s := openTestDivergenceStore(t)

	matched := FrameDivergence{
		SessionID: "proj-a", TraceID: "t1", EventID: "e1",
		OldStateRef: []byte("a"), ProposalStateRef: []byte("a"),
		DiffSummary: "{}", Matched: true, CreatedAt: 1,
	}
	mismatched := FrameDivergence{
		SessionID: "proj-a", TraceID: "t2", EventID: "e2",
		OldStateRef: []byte("a"), ProposalStateRef: []byte("b"),
		DiffSummary: `{"k":"v"}`, Matched: false, ReasonCode: "x", CreatedAt: 2,
	}

	mID, err := s.Insert(matched)
	if err != nil {
		t.Fatalf("Insert matched: %v", err)
	}
	unID, err := s.Insert(mismatched)
	if err != nil {
		t.Fatalf("Insert mismatched: %v", err)
	}

	gotMatched, err := s.Get(mID)
	if err != nil || gotMatched == nil {
		t.Fatalf("Get matched: %v", err)
	}
	if !gotMatched.Matched {
		t.Fatalf("Matched = false, want true")
	}
	gotMismatch, err := s.Get(unID)
	if err != nil || gotMismatch == nil {
		t.Fatalf("Get mismatched: %v", err)
	}
	if gotMismatch.Matched {
		t.Fatalf("Matched = true, want false")
	}
}

// Single-conn DB serializes writes; this test verifies no lost updates at the Go layer.
func TestDivergenceStore_ParallelInsertsAllPersist(t *testing.T) {
	s := openTestDivergenceStore(t)

	const goroutines = 8
	const perGoroutine = 12
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				d := FrameDivergence{
					SessionID:          fmt.Sprintf("sess-%d", gid),
					TraceID:            fmt.Sprintf("trace-%d-%d", gid, i),
					EventID:            fmt.Sprintf("evt-%d-%d", gid, i),
					ObservedGeneration: int64(i),
					OldStateRef:        []byte("old"),
					ProposalStateRef:   []byte("new"),
					DiffSummary:        "{}",
					Matched:            i%2 == 0,
					CreatedAt:          int64(gid*1000 + i),
				}
				if _, err := s.Insert(d); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent insert: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM frame_divergences`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if want := goroutines * perGoroutine; count != want {
		t.Fatalf("count = %d, want %d (lost updates)", count, want)
	}
}

func TestDivergenceStore_IndexesAreCreated(t *testing.T) {
	s := openTestDivergenceStore(t)

	wantIndexes := map[string]bool{
		"idx_divergence_session": false,
		"idx_divergence_matched": false,
	}
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='frame_divergences'`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if _, ok := wantIndexes[name]; ok {
			wantIndexes[name] = true
		}
	}
	for name, seen := range wantIndexes {
		if !seen {
			t.Fatalf("index %q not created", name)
		}
	}
}
