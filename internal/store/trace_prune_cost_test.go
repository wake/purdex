package store

// A save at a full table must not read the table (#2887: every hook event read all 10000 chains inside the write
// transaction).

import (
	"fmt"
	"testing"
)

func saveOneStepChain(t *testing.T, s *TraceStore, n int) {
	t.Helper()
	id := fmt.Sprintf("c-%06d", n)
	err := s.SaveChain(TraceRecord{
		Chain: TraceChain{ChainID: id, StartedAt: int64(n + 1), CompletedAt: int64(n + 1), TerminalStatus: "done"},
		Steps: []TraceStep{{StepID: id + "-1", ChainID: id, Seq: 1, Kind: "root", EventName: "Stop", CreatedAt: int64(n + 1)}},
	})
	if err != nil {
		t.Fatalf("SaveChain %d: %v", n, err)
	}
}

func countTraceChains(t *testing.T, s *TraceStore) (chains, steps int) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(step_count), 0) FROM agent_trace_chains`).Scan(&chains, &steps); err != nil {
		t.Fatal(err)
	}
	return
}

func TestTraceStore_SavesAtAFullTableDoNotReadTheTable(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 1000
	s.maxSteps = 100000
	for i := 0; i < 1000; i++ {
		saveOneStepChain(t, s, i)
	}
	if c, _ := countTraceChains(t, s); c != 1000 {
		t.Fatalf("a table at its limit holds %d chains, want 1000", c)
	}
	s.totalsOf().mu.Lock()
	scanned0, syncs0 := s.totalsOf().scanned, s.totalsOf().syncs
	s.totalsOf().mu.Unlock()

	// 50 more saves: one pass over the oldest 101 (down to 900) and room left for the rest; never a read per save.
	for i := 1000; i < 1050; i++ {
		saveOneStepChain(t, s, i)
	}
	s.totalsOf().mu.Lock()
	scanned, syncs := s.totalsOf().scanned-scanned0, s.totalsOf().syncs-syncs0
	s.totalsOf().mu.Unlock()
	if scanned > 150 {
		t.Errorf("50 saves at a full table read %d chains, want one eviction pass (about 100)", scanned)
	}
	if syncs > 1 { // the periodic recount (every traceTotalsResyncEvery saves) may fall in the window, once
		t.Errorf("the table was counted %d times, want at most 1 (the totals are kept)", syncs)
	}
	chains, steps := countTraceChains(t, s)
	if chains > 1000 || chains < 900 {
		t.Errorf("%d chains kept, want between 900 and 1000", chains)
	}
	if steps != chains {
		t.Errorf("%d steps for %d one-step chains", steps, chains)
	}
	// the oldest went first
	var oldest int
	if err := s.db.QueryRow(`SELECT MIN(started_at) FROM agent_trace_chains`).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if oldest != 1051-chains {
		t.Errorf("oldest kept chain starts at %d, want %d", oldest, 1051-chains)
	}
}

// The totals follow a chain that is saved again with another number of steps, and a count taken from the table is the
// same as the totals kept.
func TestTraceStore_KeptTotalsMatchTheTable(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 1000
	for i := 0; i < 5; i++ {
		saveOneStepChain(t, s, i)
	}
	// c-000002 again, with three steps
	id := "c-000002"
	if err := s.SaveChain(TraceRecord{
		Chain: TraceChain{ChainID: id, StartedAt: 2, CompletedAt: 2, TerminalStatus: "done"},
		Steps: []TraceStep{
			{StepID: "x1", ChainID: id, Seq: 1, Kind: "root", EventName: "Stop", CreatedAt: 2},
			{StepID: "x2", ChainID: id, Seq: 2, Kind: "decision", EventName: "Stop", CreatedAt: 2},
			{StepID: "x3", ChainID: id, Seq: 3, Kind: "decision", EventName: "Stop", CreatedAt: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}
	chains, steps := countTraceChains(t, s)
	s.totalsOf().mu.Lock()
	kc, ks := s.totalsOf().chains, s.totalsOf().steps
	s.totalsOf().mu.Unlock()
	if kc != chains || ks != steps || chains != 5 || steps != 7 {
		t.Errorf("kept %d/%d, table %d/%d, want 5/7", kc, ks, chains, steps)
	}
}

// Two handles of one store share the totals: one that pruned must not leave the other counting a table that is gone.
func TestTraceStore_HandlesOfOneStoreShareTheTotals(t *testing.T) {
	st, err := OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Traces()
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Traces()
	if err != nil {
		t.Fatal(err)
	}
	if a.totalsOf() != b.totalsOf() {
		t.Fatal("two handles of one store keep separate totals")
	}
}
