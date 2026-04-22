package probe_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// TestLivenessWatcher_Start_FiresOutcome_WithStableToken verifies the initial
// check fires an Outcome carrying the Watcher's current token, and that
// repeated callbacks without Rotate() keep the same token.
func TestLivenessWatcher_Start_FiresOutcome_WithStableToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var tokens []string
	var outcomes []probe.Outcome

	spec := probe.Spec{Target: "sess:", AgentType: "cc", Interval: 20 * time.Millisecond}

	var tick atomic.Int32
	checker := func(at, tgt string) bool {
		if at != "cc" || tgt != "sess:" {
			t.Errorf("unexpected checker args: agentType=%q target=%q", at, tgt)
		}
		return tick.Add(1) < 2
	}

	w := probe.StartLiveness(ctx, spec, checker, func(outcome probe.Outcome, token string, _ []probe.EvidenceRef) {
		mu.Lock()
		tokens = append(tokens, token)
		outcomes = append(outcomes, outcome)
		mu.Unlock()
	})
	t.Cleanup(w.Stop)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(outcomes)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(outcomes) < 2 {
		t.Fatalf("expected >=2 outcomes, got %d", len(outcomes))
	}
	if outcomes[0] != probe.OutcomeAlive {
		t.Fatalf("expected first outcome alive, got %q", outcomes[0])
	}
	if outcomes[1] != probe.OutcomeDead {
		t.Fatalf("expected second outcome dead, got %q", outcomes[1])
	}
	if tokens[0] == "" {
		t.Fatal("expected non-empty token on first callback")
	}
	if tokens[0] != tokens[1] {
		t.Fatalf("expected stable token across callbacks without rotate, got %q then %q", tokens[0], tokens[1])
	}
	if tokens[0] != w.Token() {
		t.Fatalf("watcher Token() %q does not match callback token %q", w.Token(), tokens[0])
	}
}

// TestLivenessWatcher_Rotate_ReturnsNewToken_OldToken_BecomesInvalid: after
// Rotate() the Watcher reports the new token to subsequent callbacks.
func TestLivenessWatcher_Rotate_ReturnsNewToken_OldToken_BecomesInvalid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var alive atomic.Bool
	alive.Store(true)

	var mu sync.Mutex
	var tokens []string

	spec := probe.Spec{Target: "sess:", AgentType: "cc", Interval: 15 * time.Millisecond}
	w := probe.StartLiveness(ctx, spec, func(string, string) bool { return alive.Load() }, func(_ probe.Outcome, token string, _ []probe.EvidenceRef) {
		mu.Lock()
		tokens = append(tokens, token)
		mu.Unlock()
	})
	t.Cleanup(w.Stop)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(tokens)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	original := w.Token()
	newTok := w.Rotate()
	if newTok == original {
		t.Fatalf("Rotate returned the same token: %q", newTok)
	}
	if w.Token() != newTok {
		t.Fatalf("Token() returned %q, expected rotated %q", w.Token(), newTok)
	}

	alive.Store(false)

	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		last := ""
		if len(tokens) > 0 {
			last = tokens[len(tokens)-1]
		}
		mu.Unlock()
		if last == newTok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("expected a callback carrying rotated token %q, got tokens=%v", newTok, tokens)
}

// TestLivenessWatcher_Stop_Idempotent: calling Stop() multiple times must
// not panic and subsequent callbacks must cease.
func TestLivenessWatcher_Stop_Idempotent(t *testing.T) {
	ctx := context.Background()
	spec := probe.Spec{Target: "sess:", AgentType: "cc", Interval: 10 * time.Millisecond}
	var calls atomic.Int32
	w := probe.StartLiveness(ctx, spec, func(string, string) bool { return true }, func(probe.Outcome, string, []probe.EvidenceRef) {
		calls.Add(1)
	})
	time.Sleep(40 * time.Millisecond)
	w.Stop()
	w.Stop()
	w.Stop()
	before := calls.Load()
	time.Sleep(60 * time.Millisecond)
	after := calls.Load()
	if after != before {
		t.Fatalf("expected no further callbacks after Stop; before=%d after=%d", before, after)
	}
}

// TestActivityWatcher_Start_FiresOnActivity verifies that an ActivitySignal
// gets translated into an OutcomeActive event through the callback.
func TestActivityWatcher_Start_FiresOnActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var called sync.WaitGroup
	called.Add(1)
	var mu sync.Mutex
	var outcomes []probe.Outcome

	var starterInvoked atomic.Int32
	starter := func(target string, cb probe.ActivityCallback) {
		starterInvoked.Add(1)
		if target != "sess:" {
			t.Errorf("unexpected target: %q", target)
		}
		signal := probe.ActivitySignalRunning
		if starterInvoked.Load() > 1 {
			signal = probe.ActivitySignalIdle
		}
		go cb(target, signal)
	}

	w := probe.StartActivity(ctx, probe.Spec{Target: "sess:", AgentType: "cc"}, starter, nil, func(outcome probe.Outcome, _ string, _ []probe.EvidenceRef) {
		mu.Lock()
		outcomes = append(outcomes, outcome)
		mu.Unlock()
		if outcome == probe.OutcomeActive {
			called.Done()
		}
	})
	t.Cleanup(w.Stop)

	done := make(chan struct{})
	go func() { called.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected OutcomeActive within 500ms")
	}
}

// TestReadinessWatcher_Start_FiresOnReady verifies that a registered
// ReadinessPoller returning StatusRunning results in OutcomeReady.
func TestReadinessWatcher_Start_FiresOnReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var called sync.WaitGroup
	called.Add(1)

	poller := func(agentType, target string) (probe.ReadinessResult, bool) {
		return probe.ReadinessResult{Status: agentpkg.StatusRunning}, true
	}

	w := probe.StartReadiness(ctx, probe.Spec{Target: "sess:", AgentType: "cc", Interval: 10 * time.Millisecond}, poller, func(outcome probe.Outcome, _ string, _ []probe.EvidenceRef) {
		if outcome == probe.OutcomeReady {
			called.Done()
		}
	})
	t.Cleanup(w.Stop)

	done := make(chan struct{})
	go func() { called.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected OutcomeReady within 500ms")
	}
}
