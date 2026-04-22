package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/arbitrator"
	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// makeArbitratorModule builds a Module with an Arbitrator wired in-place for
// admission tests. inChCap controls the input channel capacity so tests can
// force a full-channel state. Returns the Module and a cleanup helper.
func makeArbitratorModule(t *testing.T, inChCap int) *Module {
	t.Helper()
	arbitrator.ResetForTesting()

	m := &Module{}
	minter, _ := observation.NewTraceIDRegistry()
	m.traceMinter = minter
	m.arbmodeMgr = arbmode.NewManager("", "")
	m.arbitrator = arbitrator.NewArbitrator(arbitrator.Options{
		Minter:      minter,
		Arbmode:     m.arbmodeMgr,
		TraceWriter: &nopTraceSubmitter{},
		InChCap:     inChCap,
	})
	return m
}

// nopTraceSubmitter satisfies arbitrator.TraceSubmitter; records are dropped.
type nopTraceSubmitter struct{}

func (*nopTraceSubmitter) Submit(arbitrator.TraceRecord)     {}
func (*nopTraceSubmitter) Shutdown(context.Context) error    { return nil }

func committedObs() observation.Observation {
	return observation.Observation{
		SessionID:    "sess-committed",
		Phase:        observation.PhaseCommitted,
		SourceKind:   observation.SourceHook,
		Action:       "test.committed",
		ObservedAt:   time.Now(),
	}
}

func proposedObs() observation.Observation {
	return observation.Observation{
		SessionID:    "sess-proposed",
		Phase:        observation.PhaseProposed,
		SourceKind:   observation.SourceProbe,
		Action:       "test.proposed",
		ObservedAt:   time.Now(),
	}
}

// fillInCh drains from the arbitrator's InCh to free a slot only when the test
// asks. Here we want to BLOCK so we never read. Helper pushes until the channel
// is full.
func fillInCh(t *testing.T, m *Module, n int) {
	t.Helper()
	ch := m.arbitrator.InChForTesting()
	for i := 0; i < n; i++ {
		select {
		case ch <- observation.Observation{SessionID: "filler", Phase: observation.PhaseProposed}:
		default:
			t.Fatalf("fillInCh: channel rejected push %d (expected to have space)", i)
		}
	}
}

func TestSubmitObservation_Committed_BlocksUpTo100ms(t *testing.T) {
	m := makeArbitratorModule(t, 1)
	fillInCh(t, m, 1) // saturate — no consumer pulling

	start := time.Now()
	m.SubmitObservation(committedObs())
	elapsed := time.Since(start)

	// Allow generous lower bound (90ms) to survive scheduler jitter while still
	// proving that we actually blocked rather than dropping immediately.
	if elapsed < 90*time.Millisecond {
		t.Fatalf("committed SubmitObservation returned too fast: %v (expected ~100ms block)", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("committed SubmitObservation returned too slow: %v (expected ~100ms)", elapsed)
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=committed"); got != 1 {
		t.Fatalf("lights_arb_in_dropped{priority=committed} = %d, want 1", got)
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=proposed"); got != 0 {
		t.Fatalf("lights_arb_in_dropped{priority=proposed} = %d, want 0", got)
	}
}

func TestSubmitObservation_Committed_NoBlock_WhenSpace(t *testing.T) {
	m := makeArbitratorModule(t, 2)

	obs := committedObs()
	start := time.Now()
	m.SubmitObservation(obs)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Fatalf("committed SubmitObservation into non-full channel blocked: %v", elapsed)
	}
	// Read back and confirm the same observation was enqueued.
	select {
	case received := <-m.arbitrator.InChForTesting():
		if received.SessionID != obs.SessionID || received.Phase != obs.Phase {
			t.Fatalf("channel received different observation: %+v", received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("nothing enqueued on InCh")
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=committed"); got != 0 {
		t.Fatalf("lights_arb_in_dropped{priority=committed} = %d, want 0 (no drop expected)", got)
	}
}

func TestSubmitObservation_Proposed_NonBlocking_DropsWhenFull(t *testing.T) {
	m := makeArbitratorModule(t, 1)
	fillInCh(t, m, 1)

	start := time.Now()
	m.SubmitObservation(proposedObs())
	elapsed := time.Since(start)

	if elapsed > 20*time.Millisecond {
		t.Fatalf("proposed SubmitObservation blocked: %v (expected immediate drop)", elapsed)
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=proposed"); got != 1 {
		t.Fatalf("lights_arb_in_dropped{priority=proposed} = %d, want 1", got)
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=committed"); got != 0 {
		t.Fatalf("lights_arb_in_dropped{priority=committed} = %d, want 0", got)
	}
}

func TestSubmitObservation_Rejected_NonBlocking_DropsWhenFull(t *testing.T) {
	m := makeArbitratorModule(t, 1)
	fillInCh(t, m, 1)

	obs := observation.Observation{
		SessionID:  "sess-rejected",
		Phase:      observation.PhaseRejected,
		SourceKind: observation.SourceHook,
		Action:     "test.rejected",
	}
	start := time.Now()
	m.SubmitObservation(obs)
	elapsed := time.Since(start)

	if elapsed > 20*time.Millisecond {
		t.Fatalf("rejected SubmitObservation blocked: %v (expected immediate drop)", elapsed)
	}
	// Rejected is treated as proposed-tier per spec §3.5.1.
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=proposed"); got != 1 {
		t.Fatalf("lights_arb_in_dropped{priority=proposed} = %d, want 1 (rejected treated as proposed tier)", got)
	}
}

func TestSubmitObservation_NilArbitrator_NoOp(t *testing.T) {
	arbitrator.ResetForTesting()
	m := &Module{} // arbitrator is nil
	// Must not panic.
	m.SubmitObservation(committedObs())
	m.SubmitObservation(proposedObs())

	if got := arbitrator.Value("lights_arb_in_dropped", "priority=committed"); got != 0 {
		t.Fatalf("lights_arb_in_dropped{priority=committed} = %d, want 0 (nil arbitrator should not emit metrics)", got)
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=proposed"); got != 0 {
		t.Fatalf("lights_arb_in_dropped{priority=proposed} = %d, want 0", got)
	}
}

// TestSubmitObservation_Committed_RaceyReaderDrainsBeforeTimer verifies that a
// late consumer that appears within the 100ms window unblocks the committed
// submit instead of letting the timer fire.
func TestSubmitObservation_Committed_RaceyReaderDrainsBeforeTimer(t *testing.T) {
	m := makeArbitratorModule(t, 1)
	fillInCh(t, m, 1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Delay a bit, then drain one entry so the committed submit can land
		// before the 100ms timer fires.
		time.Sleep(30 * time.Millisecond)
		<-m.arbitrator.InChForTesting() // filler
	}()

	start := time.Now()
	m.SubmitObservation(committedObs())
	elapsed := time.Since(start)
	wg.Wait()

	if elapsed > 90*time.Millisecond {
		t.Fatalf("committed SubmitObservation did not take the fast path after drain: %v", elapsed)
	}
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=committed"); got != 0 {
		t.Fatalf("lights_arb_in_dropped{priority=committed} = %d, want 0 (drained before timer)", got)
	}
}
