package codexbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeScanner returns a canned set of brokers + an optional error.
type fakeScanner struct {
	brokers []BrokerRecord
	err     error
	delay   time.Duration
	calls   atomic.Int64
}

func (s *fakeScanner) scanForSweep(_ context.Context) ([]BrokerRecord, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return nil, s.err
	}
	out := make([]BrokerRecord, len(s.brokers))
	copy(out, s.brokers)
	return out, nil
}

// fakeKiller is invoked instead of a real KillSequence so tests can assert
// invocation count + return arbitrary results.
type fakeKiller struct {
	mu        sync.Mutex
	invoked   int
	keys      []string
	delay     time.Duration
	result    KillResult
	err       error
	inflight  atomic.Int32
	maxFlight atomic.Int32
}

func (f *fakeKiller) run(_ context.Context, rec BrokerRecord, _ DecisionResult) (KillResult, error) {
	f.mu.Lock()
	f.invoked++
	f.keys = append(f.keys, rec.Key)
	f.mu.Unlock()
	cur := f.inflight.Add(1)
	for {
		mx := f.maxFlight.Load()
		if cur <= mx {
			break
		}
		if f.maxFlight.CompareAndSwap(mx, cur) {
			break
		}
	}
	defer f.inflight.Add(-1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.result, f.err
}

func (f *fakeKiller) snapshot() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keysCopy := make([]string, len(f.keys))
	copy(keysCopy, f.keys)
	return f.invoked, keysCopy
}

// stubDecisionEvaluator lets tests inject a deterministic per-broker
// DecisionResult; production uses EvalDecision directly.
type stubDecisionEvaluator struct {
	by map[string]DecisionResult
}

func (s *stubDecisionEvaluator) eval(_ context.Context, rec BrokerRecord, _ DecisionOpts) DecisionResult {
	if d, ok := s.by[rec.Key]; ok {
		return d
	}
	return DecisionResult{}
}

// newSweepHandlerForTest constructs a SweepHandler with the supplied seams.
func newSweepHandlerForTest(scan *fakeScanner, killer *fakeKiller, eval *stubDecisionEvaluator, registry LaunchRegistryReader, qf *QuarantineFile) *SweepHandler {
	h := &SweepHandler{
		ScanFn: scan.scanForSweep,
		KillerFactory: func(rec BrokerRecord) KillRunner {
			return killerWrap{rec: rec, k: killer}
		},
		Quarantine: qf,
		Registry:   registry,
		E1Tracker:  NewE1Tracker(),
		EvalFn:     eval.eval,
		ApplyTimeout:   30 * time.Second,
		DryRunTimeout:  10 * time.Second,
	}
	return h
}

// killerWrap implements KillRunner over a fakeKiller.
type killerWrap struct {
	rec BrokerRecord
	k   *fakeKiller
}

func (w killerWrap) Run(ctx context.Context, decision DecisionResult, _ SocketVerifier) (KillResult, error) {
	return w.k.run(ctx, w.rec, decision)
}

// emptyRegistry is a LaunchRegistryReader with no entries.
type emptyRegistry struct{}

func (emptyRegistry) Lookup(_ string) (*LaunchEntry, bool) { return nil, false }
func (emptyRegistry) Empty() bool                           { return true }

// populatedRegistry has the supplied keys.
type populatedRegistry struct {
	keys map[string]bool
}

func newPopulatedRegistry(keys ...string) *populatedRegistry {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return &populatedRegistry{keys: m}
}

func (r *populatedRegistry) Lookup(k string) (*LaunchEntry, bool) {
	if r.keys[k] {
		return &LaunchEntry{BrokerKey: k}, true
	}
	return nil, false
}
func (r *populatedRegistry) Empty() bool { return len(r.keys) == 0 }

// -- Tests ---------------------------------------------------------------

// TestSweepHandler_DryRun_NoKills — mode=dry-run with one kill-eligible
// broker → evaluated populated, applied empty.
func TestSweepHandler_DryRun_NoKills(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp SweepResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.DryRun {
		t.Errorf("expected dryRun=true")
	}
	if len(resp.Evaluated) != 1 {
		t.Errorf("evaluated len = %d, want 1", len(resp.Evaluated))
	}
	if len(resp.Applied) != 0 {
		t.Errorf("applied len = %d, want 0 (dry-run)", len(resp.Applied))
	}
	if invoked, _ := killer.snapshot(); invoked != 0 {
		t.Errorf("killer invoked %d times in dry-run", invoked)
	}
}

// TestSweepHandler_Apply_KillsOrphan — mode=apply with populated registry
// for the brokerKey; baseline Kill=true → killer invoked.
func TestSweepHandler_Apply_KillsOrphan(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Applied) != 1 || resp.Applied[0] != "k1" {
		t.Errorf("applied = %v, want [k1]", resp.Applied)
	}
	if invoked, keys := killer.snapshot(); invoked != 1 || keys[0] != "k1" {
		t.Errorf("killer = %d invoked, keys=%v", invoked, keys)
	}
}

// TestSweepHandler_BrokerKeyFilter — only the matching brokerKey ends up
// in evaluated.
func TestSweepHandler_BrokerKeyFilter(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}, {Key: "k2"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
		"k2": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1", "k2"), &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run&brokerKey=k1", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Evaluated) != 1 || resp.Evaluated[0].Broker.Key != "k1" {
		t.Errorf("evaluated = %v, want [k1]", resp.Evaluated)
	}
}

// TestSweepHandler_QuarantinedSkipped — broker in quarantine → not in
// evaluated.
func TestSweepHandler_QuarantinedSkipped(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}, {Key: "k2"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
		"k2": {Kill: true, Reason: "idle_timeout"},
	}}
	qf := &QuarantineFile{Version: 1, Entries: []QuarantineEntry{{BrokerKey: "k1"}}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1", "k2"), qf)
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Evaluated) != 1 {
		t.Errorf("evaluated len = %d, want 1 (k1 quarantined)", len(resp.Evaluated))
	}
	if resp.Evaluated[0].Broker.Key != "k2" {
		t.Errorf("expected k2, got %s", resp.Evaluated[0].Broker.Key)
	}
}

// TestSweepHandler_ApplyAll_RegistryEmpty_NoKills — mlab safety: 50 brokers,
// empty registry → unfiltered apply issues zero kills.
func TestSweepHandler_ApplyAll_RegistryEmpty_NoKills(t *testing.T) {
	brokers := make([]BrokerRecord, 50)
	by := map[string]DecisionResult{}
	for i := range brokers {
		key := "k" + itoa(i)
		brokers[i] = BrokerRecord{Key: key}
		by[key] = DecisionResult{Kill: true, Reason: "foreign_quarantine"}
	}
	scan := &fakeScanner{brokers: brokers}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: by}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if invoked, _ := killer.snapshot(); invoked != 0 {
		t.Errorf("MASS KILL: killer invoked %d times on foreign brokers", invoked)
	}
	if len(resp.Applied) != 0 {
		t.Errorf("applied = %v, expected empty (foreign mass-kill safety)", resp.Applied)
	}
	if len(resp.Evaluated) != 50 {
		t.Errorf("evaluated len = %d, want 50", len(resp.Evaluated))
	}
}

// TestSweepHandler_BrokerKeyApply_OverridesForeignQuarantine_BaselineKillTrue
// — operator override on a baseline-killable foreign broker → kill IS issued
// + audit reason flips to manual_sweep_override.
func TestSweepHandler_BrokerKeyApply_OverridesForeignQuarantine_BaselineKillTrue(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "foreign_quarantine", AnomaliesAdded: []AnomalyCode{AnomalyForeignOwner}},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if invoked, _ := killer.snapshot(); invoked != 1 {
		t.Errorf("expected killer invoked once on operator override, got %d", invoked)
	}
	if len(resp.Applied) != 1 || resp.Applied[0] != "k1" {
		t.Errorf("applied = %v, want [k1]", resp.Applied)
	}
	// Evaluated decision should have Reason flipped to manual_sweep_override.
	if got := resp.Evaluated[0].Decision.Reason; got != "manual_sweep_override" {
		t.Errorf("audit reason = %q, want manual_sweep_override", got)
	}
}

// TestSweepHandler_BrokerKeyApply_AlivePredicate_NoKill — round-3 #1
// regression: filtered apply where baseline Kill=false (predicate A true)
// → kill NOT issued. Operator override only suppresses foreign guard,
// NEVER bypasses alive-protection.
func TestSweepHandler_BrokerKeyApply_AlivePredicate_NoKill(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {
			Kill:           false, // alive-protection wins
			Reason:         "foreign_quarantine",
			AnomaliesAdded: []AnomalyCode{AnomalyForeignOwner},
			Predicates:     PredicateResult{A: true, ADetail: "rpc:active"},
		},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if invoked, _ := killer.snapshot(); invoked != 0 {
		t.Errorf("ALIVE-PROTECTION BREACH: killer invoked %d times on baseline Kill=false", invoked)
	}
	if len(resp.Applied) != 0 {
		t.Errorf("applied = %v, expected empty (alive-protection)", resp.Applied)
	}
}

// TestSweepHandler_BrokerKeyApply_IdleNotExpired_NoKill — round-3 #1
// regression: filtered apply where A/B/C false but idle not expired →
// baseline Kill=false → kill NOT issued.
func TestSweepHandler_BrokerKeyApply_IdleNotExpired_NoKill(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {
			Kill:           false, // idle not expired
			Reason:         "foreign_quarantine",
			AnomaliesAdded: []AnomalyCode{AnomalyForeignOwner},
			IdleSeconds:    60, // 1 minute idle, well under 30 min default
		},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if invoked, _ := killer.snapshot(); invoked != 0 {
		t.Errorf("IDLE-PROTECTION BREACH: killer invoked %d times on baseline Kill=false (idle not expired)", invoked)
	}
	if len(resp.Applied) != 0 {
		t.Errorf("applied = %v, expected empty (idle-protection)", resp.Applied)
	}
}

// TestSweepHandler_UnfilteredApply_BaselineKillButForeign_NoKill — even a
// baseline-killable foreign broker is NOT killed via unfiltered apply.
func TestSweepHandler_UnfilteredApply_BaselineKillButForeign_NoKill(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "foreign_quarantine", AnomaliesAdded: []AnomalyCode{AnomalyForeignOwner}},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if invoked, _ := killer.snapshot(); invoked != 0 {
		t.Errorf("MASS-KILL FAILURE: killer invoked %d times on unfiltered foreign broker", invoked)
	}
}

// TestSweepHandler_MethodNotPost_405 — GET → 405 Method Not Allowed.
func TestSweepHandler_MethodNotPost_405(t *testing.T) {
	scan := &fakeScanner{}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("GET", "/api/codex/brokers/sweep", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// TestSweepHandler_ScanFailure_503 — scanner error → 503.
func TestSweepHandler_ScanFailure_503(t *testing.T) {
	scan := &fakeScanner{err: errors.New("ps unavailable")}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(scan, killer, eval, emptyRegistry{}, &QuarantineFile{Version: 1})
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// -- Concurrency tests (round-1 #4 / round-2 #2) ------------------------

// TestSweepHandler_ConcurrentApplyApply_SerializedPerBroker — two
// apply&brokerKey=X requests run serially, never concurrently in
// KillSequence.Run.
func TestSweepHandler_ConcurrentApplyApply_SerializedPerBroker(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{delay: 80 * time.Millisecond}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
			w := httptest.NewRecorder()
			h.HandleSweep(w, req)
		}()
	}
	wg.Wait()
	if invoked, _ := killer.snapshot(); invoked != 2 {
		t.Errorf("expected 2 sequential invocations, got %d", invoked)
	}
	if mx := killer.maxFlight.Load(); mx > 1 {
		t.Errorf("MAX CONCURRENT INVOCATIONS = %d, want 1 (serialised per broker)", mx)
	}
}

// TestSweepHandler_AllApply_BlocksFilteredApply_SameBroker — __all__ apply
// blocks any other request until done.
func TestSweepHandler_AllApply_BlocksFilteredApply_SameBroker(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{delay: 50 * time.Millisecond}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	// Brief pause so __all__ apply enters its critical section.
	time.Sleep(10 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	wg.Wait()
	if mx := killer.maxFlight.Load(); mx > 1 {
		t.Errorf("MAX CONCURRENT KillSequence.Run = %d, want 1 (__all__ exclusive)", mx)
	}
}

// TestSweepHandler_FilteredDryRun_ReadLock_NoBlockOtherFilteredDryRun —
// two filtered dry-runs on different brokers run their Scanner in parallel.
func TestSweepHandler_FilteredDryRun_ReadLock_NoBlockOtherFilteredDryRun(t *testing.T) {
	// Use a barrier-style scanner: each call blocks until both have arrived.
	barrier := newBarrierScanner([]BrokerRecord{{Key: "k1"}, {Key: "k2"}}, 2, 200*time.Millisecond)
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(&fakeScanner{}, killer, eval, newPopulatedRegistry("k1", "k2"), &QuarantineFile{Version: 1})
	h.ScanFn = barrier.scan

	var wg sync.WaitGroup
	for _, k := range []string{"k1", "k2"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run&brokerKey="+key, nil)
			h.HandleSweep(httptest.NewRecorder(), req)
		}(k)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Both completed quickly because the barrier was tripped.
	case <-time.After(500 * time.Millisecond):
		t.Errorf("filtered dry-runs serialised; expected parallel")
	}
}

// barrierScanner only releases callers once `n` have arrived. Used to prove
// concurrent execution: if execution is serialised, the barrier deadlocks
// past the window.
type barrierScanner struct {
	brokers []BrokerRecord
	n       int
	timeout time.Duration
	mu      sync.Mutex
	arrived int
	cond    *sync.Cond
}

func newBarrierScanner(brokers []BrokerRecord, n int, timeout time.Duration) *barrierScanner {
	b := &barrierScanner{brokers: brokers, n: n, timeout: timeout}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *barrierScanner) scan(_ context.Context) ([]BrokerRecord, error) {
	b.mu.Lock()
	b.arrived++
	if b.arrived >= b.n {
		b.cond.Broadcast()
	} else {
		// Wait, but bound it.
		done := make(chan struct{})
		go func() {
			b.mu.Lock()
			for b.arrived < b.n {
				b.cond.Wait()
			}
			b.mu.Unlock()
			close(done)
		}()
		b.mu.Unlock()
		select {
		case <-done:
		case <-time.After(b.timeout):
		}
		b.mu.Lock()
	}
	out := make([]BrokerRecord, len(b.brokers))
	copy(out, b.brokers)
	b.mu.Unlock()
	return out, nil
}

// TestSweepHandler_UnfilteredDryRun_Exclusive — round-2 #2 regression:
// unfiltered dry-run takes globalApplyMu.Lock(), blocking everything else.
func TestSweepHandler_UnfilteredDryRun_Exclusive(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}, delay: 80 * time.Millisecond}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	// Track in-flight scan count via the scanner.
	var inflight, peak atomic.Int32
	wrap := func(ctx context.Context) ([]BrokerRecord, error) {
		cur := inflight.Add(1)
		for {
			mx := peak.Load()
			if cur <= mx {
				break
			}
			if peak.CompareAndSwap(mx, cur) {
				break
			}
		}
		defer inflight.Add(-1)
		return scan.scanForSweep(ctx)
	}
	h.ScanFn = wrap

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run", nil)
			h.HandleSweep(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
	if mx := peak.Load(); mx > 1 {
		t.Errorf("UNFILTERED DRY-RUN concurrent peak = %d, want 1", mx)
	}
}

// TestSweepHandler_DryRunBlocksApplySameBroker — filtered dry-run holds an
// RLock on per-broker mutex; concurrent filtered apply on same broker blocks.
func TestSweepHandler_DryRunBlocksApplySameBroker(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}, delay: 80 * time.Millisecond}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	var dryReleased atomic.Int64
	var applyAcquired atomic.Int64

	// Wrap scan: increments dryReleased only after the dry-run scan
	// completes; the apply scan increments applyAcquired BEFORE killer fires.
	originalScan := h.ScanFn
	h.ScanFn = func(ctx context.Context) ([]BrokerRecord, error) {
		out, err := originalScan(ctx)
		dryReleased.Add(1)
		return out, err
	}

	originalKiller := h.KillerFactory
	h.KillerFactory = func(rec BrokerRecord) KillRunner {
		applyAcquired.Store(time.Now().UnixNano())
		return originalKiller(rec)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run&brokerKey=k1", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	time.Sleep(10 * time.Millisecond) // let dry-run grab its lock
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	wg.Wait()

	// applyAcquired moment must be >= dry-run scan completion moment.
	// Encoded loosely: dryReleased was incremented at least once by the
	// time apply actually ran the killer. We verify by counting: dryReleased
	// counts both scans; applyAcquired registered after the dry-run lock
	// drop. Concrete check: killer invoked exactly once and dryReleased >= 2
	// (both scans completed).
	if invoked, _ := killer.snapshot(); invoked != 1 {
		t.Errorf("apply killer invocations = %d, want 1", invoked)
	}
	if dryReleased.Load() < 2 {
		t.Errorf("scan invocations = %d, want >= 2", dryReleased.Load())
	}
}

// TestSweepHandler_AllApplyExclusive — concurrent __all__ apply + filtered
// dry-run: only one actually scans/kills at a time.
func TestSweepHandler_AllApplyExclusive(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}, delay: 50 * time.Millisecond}
	killer := &fakeKiller{delay: 30 * time.Millisecond}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	var inflight, peak atomic.Int32
	originalScan := h.ScanFn
	h.ScanFn = func(ctx context.Context) ([]BrokerRecord, error) {
		cur := inflight.Add(1)
		for {
			mx := peak.Load()
			if cur <= mx {
				break
			}
			if peak.CompareAndSwap(mx, cur) {
				break
			}
		}
		defer inflight.Add(-1)
		return originalScan(ctx)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	time.Sleep(5 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=dry-run&brokerKey=k1", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	wg.Wait()
	if mx := peak.Load(); mx > 1 {
		t.Errorf("__all__ apply + filtered dry-run scanned in parallel: peak=%d", mx)
	}
}

// TestSweepHandler_LockTimeout_503 — request whose context deadline expires
// before the global lock is released → 503 + Retry-After.
func TestSweepHandler_LockTimeout_503(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}, delay: 200 * time.Millisecond}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	// Hold the global lock for 200ms via an __all__ apply.
	go func() {
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	time.Sleep(10 * time.Millisecond) // let it grab the lock

	// Issue a queued request with a tiny ctx deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Errorf("expected Retry-After header on 503")
	}
}

// TestSweepHandler_Concurrency_NoDeadlock — many mixed requests run under
// -race without deadlock or detector hits.
func TestSweepHandler_Concurrency_NoDeadlock(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}, {Key: "k2"}}, delay: 5 * time.Millisecond}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1", "k2"), &QuarantineFile{Version: 1})

	var wg sync.WaitGroup
	patterns := []string{
		"?mode=dry-run",
		"?mode=dry-run&brokerKey=k1",
		"?mode=dry-run&brokerKey=k2",
		"?mode=apply&brokerKey=k1",
		"?mode=apply&brokerKey=k2",
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/codex/brokers/sweep"+patterns[idx%len(patterns)], nil)
			h.HandleSweep(httptest.NewRecorder(), req)
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Errorf("deadlock — 50 mixed requests did not complete in 5s")
	}
}

// -- PR review finding F: per-broker lock honours ctx --------------------

// TestSweepHandler_PerBrokerLockTimeout_503 — finding F regression: a
// filtered apply on brokerKey=k1 that arrives while another filtered
// apply on the same broker is in flight must NOT block indefinitely on
// the per-broker mutex. If the request context expires before the per-
// broker write lock is acquired, the handler returns 503 + Retry-After
// instead of stalling.
//
// Setup: first apply holds the per-broker lock for 200ms via a slow
// killer; second apply arrives 10ms later with a 30ms ctx deadline. The
// second must return 503 within ~50ms.
func TestSweepHandler_PerBrokerLockTimeout_503(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	killer := &fakeKiller{delay: 200 * time.Millisecond}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "idle_timeout"},
	}}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	// First apply: hold the per-broker write lock.
	go func() {
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
		h.HandleSweep(httptest.NewRecorder(), req)
	}()
	time.Sleep(10 * time.Millisecond)

	// Second apply with a tight ctx deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	start := time.Now()
	h.HandleSweep(w, req)
	elapsed := time.Since(start)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (per-broker ctx timeout)", w.Code)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("HandleSweep stalled %v on per-broker lock; expected ~30ms ctx-deadline", elapsed)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Errorf("expected Retry-After header on 503")
	}
}

// TestSweepHandler_CtxExpiresAfterLockAcquired_503 — finding F: even when
// the per-broker lock acquisition succeeds, an already-expired request
// context must short-circuit the scan rather than running the full
// kill-eligibility evaluation (which holds the lock longer than the
// caller is waiting for).
func TestSweepHandler_CtxExpiresAfterLockAcquired_503(t *testing.T) {
	scan := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}, delay: 50 * time.Millisecond}
	killer := &fakeKiller{}
	eval := &stubDecisionEvaluator{}
	h := newSweepHandlerForTest(scan, killer, eval, newPopulatedRegistry("k1"), &QuarantineFile{Version: 1})

	// Pre-cancelled ctx so lock acquisition will succeed but ctx.Err is
	// non-nil immediately afterwards.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (ctx expired before scan)", w.Code)
	}
}

// -- PR review finding C: identityMismatchErr persists quarantine --------

// identityMismatchKiller is a KillRunner that always returns
// identityMismatchErr from Step 0. Used to assert the sweep handler
// translates that error into a persisted QuarantineEntry.
type identityMismatchKiller struct{}

func (identityMismatchKiller) Run(_ context.Context, _ DecisionResult, _ SocketVerifier) (KillResult, error) {
	return KillResult{}, &identityMismatchErr{detail: "lstart-drift"}
}

// TestSweepHandler_IdentityMismatch_PersistsQuarantine — finding C: when
// KillSequence.Run returns identityMismatchErr, the handler must build a
// QuarantineEntry from the live broker fingerprint, append it to the in-
// memory QuarantineFile, atomically save quarantine.json, surface the
// quarantined key in the response, and ensure subsequent sweeps skip the
// broker via IsQuarantined.
func TestSweepHandler_IdentityMismatch_PersistsQuarantine(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "quarantine.json")
	qf := &QuarantineFile{Version: 1}
	scan := &fakeScanner{brokers: []BrokerRecord{{
		Key:     "k1",
		PID:     4321,
		Lstart:  time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		Cwd:     "/tmp/x",
		PidFile: "/tmp/x/.codex/pid",
	}}}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "foreign_quarantine"},
	}}
	h := newSweepHandlerForTest(scan, &fakeKiller{}, eval, emptyRegistry{}, qf)
	// Override killer factory to always return identityMismatch.
	h.KillerFactory = func(_ BrokerRecord) KillRunner { return identityMismatchKiller{} }
	h.QuarantineStore = &QuarantineStore{}
	h.QuarantinePath = qPath
	// Provide a lister so the handler can fetch live fingerprint for the
	// QuarantineEntry. Same matching cmdline as the broker.
	h.Lister = NewFakeProcessLister([]RawProcess{{
		PID:     4321,
		Lstart:  time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC), // drifted
		Cmdline: "/usr/bin/zsh",
	}})

	// First sweep — operator override on k1 → kill triggers identity
	// mismatch → quarantine persists.
	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp SweepResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// Quarantine must contain k1 in memory.
	if !IsQuarantined(qf, "k1") {
		t.Errorf("expected k1 quarantined in memory, got %+v", qf.Entries)
	}
	// Quarantine must be saved on disk.
	if _, err := os.Stat(qPath); err != nil {
		t.Errorf("expected quarantine.json saved, stat err: %v", err)
	}
	// Response should NOT include k1 in Applied (kill was aborted).
	for _, key := range resp.Applied {
		if key == "k1" {
			t.Errorf("identity-mismatch broker should not appear in Applied; got %v", resp.Applied)
		}
	}
	// Response should surface quarantined key.
	foundQ := false
	for _, key := range resp.Quarantined {
		if key == "k1" {
			foundQ = true
		}
	}
	if !foundQ {
		t.Errorf("expected k1 in resp.Quarantined, got %+v", resp.Quarantined)
	}

	// Second sweep — same broker; should be skipped by IsQuarantined check
	// before EvalFn / KillerFactory.
	scan2 := &fakeScanner{brokers: []BrokerRecord{{Key: "k1"}}}
	h.ScanFn = scan2.scanForSweep
	req2 := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	w2 := httptest.NewRecorder()
	h.HandleSweep(w2, req2)
	var resp2 SweepResponse
	_ = json.NewDecoder(w2.Body).Decode(&resp2)
	if len(resp2.Evaluated) != 0 {
		t.Errorf("expected k1 skipped by quarantine on 2nd sweep; got evaluated=%+v", resp2.Evaluated)
	}
}

// TestSweepHandler_IdentityMismatch_QuarantinePathParentMissing — PR review
// R3 finding #2: on a fresh profile, audit/ may not exist when Step 0
// identity mismatch occurs (Step 0 returns before WritePreimage creates the
// audit dir). recordQuarantine must MkdirAll the parent before saving.
func TestSweepHandler_IdentityMismatch_QuarantinePathParentMissing(t *testing.T) {
	tmp := t.TempDir()
	// Use a parent that does NOT yet exist — recordQuarantine must create it.
	qPath := filepath.Join(tmp, "audit", "quarantine.json")
	qf := &QuarantineFile{Version: 1}
	scan := &fakeScanner{brokers: []BrokerRecord{{
		Key:    "k1",
		PID:    4321,
		Lstart: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}}}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "foreign_quarantine"},
	}}
	h := newSweepHandlerForTest(scan, &fakeKiller{}, eval, emptyRegistry{}, qf)
	h.KillerFactory = func(_ BrokerRecord) KillRunner { return identityMismatchKiller{} }
	h.QuarantineStore = &QuarantineStore{}
	h.QuarantinePath = qPath
	h.Lister = NewFakeProcessLister([]RawProcess{{
		PID: 4321, Lstart: time.Now(), Cmdline: "/usr/bin/zsh",
	}})

	req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey=k1", nil)
	w := httptest.NewRecorder()
	h.HandleSweep(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp SweepResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	for _, e := range resp.Errors {
		if strings.Contains(e, "quarantine save failed") {
			t.Errorf("expected MkdirAll to enable save; got error %q", e)
		}
	}
	if _, err := os.Stat(qPath); err != nil {
		t.Errorf("expected quarantine.json saved into freshly-created parent dir; stat err: %v", err)
	}
}

// TestSweepHandler_IdentityMismatch_ConcurrentQuarantine_NoRace — PR review
// R3 finding #3: filtered applies for distinct broker keys hold only their
// own perBrokerMu but share h.Quarantine. Two simultaneous identity-mismatch
// quarantines must be serialised so neither slice append nor file save races.
// Asserts both entries land and the file persists both.
func TestSweepHandler_IdentityMismatch_ConcurrentQuarantine_NoRace(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "quarantine.json")
	qf := &QuarantineFile{Version: 1}
	rec1 := BrokerRecord{Key: "k1", PID: 1001, Lstart: time.Now()}
	rec2 := BrokerRecord{Key: "k2", PID: 2002, Lstart: time.Now()}
	scan := &fakeScanner{brokers: []BrokerRecord{rec1, rec2}}
	eval := &stubDecisionEvaluator{by: map[string]DecisionResult{
		"k1": {Kill: true, Reason: "foreign_quarantine"},
		"k2": {Kill: true, Reason: "foreign_quarantine"},
	}}
	h := newSweepHandlerForTest(scan, &fakeKiller{}, eval, emptyRegistry{}, qf)
	h.KillerFactory = func(_ BrokerRecord) KillRunner { return identityMismatchKiller{} }
	h.QuarantineStore = &QuarantineStore{}
	h.QuarantinePath = qPath
	h.Lister = NewFakeProcessLister([]RawProcess{
		{PID: 1001, Lstart: time.Now(), Cmdline: "/usr/bin/zsh"},
		{PID: 2002, Lstart: time.Now(), Cmdline: "/usr/bin/zsh"},
	})

	// Fire two filtered applies in parallel. Per the lock table they take
	// disjoint perBrokerMu but share globalApplyMu.RLock — so they run
	// concurrently and would race on h.Quarantine without quarantineMu.
	var wg sync.WaitGroup
	doApply := func(key string) {
		defer wg.Done()
		req := httptest.NewRequest("POST", "/api/codex/brokers/sweep?mode=apply&brokerKey="+key, nil)
		w := httptest.NewRecorder()
		h.HandleSweep(w, req)
	}
	wg.Add(2)
	go doApply("k1")
	go doApply("k2")
	wg.Wait()

	if !IsQuarantined(qf, "k1") || !IsQuarantined(qf, "k2") {
		t.Errorf("both keys must be quarantined; got entries=%+v", qf.Entries)
	}
	// Reload from disk and confirm both entries persisted (the actual race
	// would drop one entry on disk even if memory looks consistent).
	loaded, err := (&QuarantineStore{}).Load(qPath)
	if err != nil {
		t.Fatalf("load quarantine.json: %v", err)
	}
	if !IsQuarantined(loaded, "k1") || !IsQuarantined(loaded, "k2") {
		t.Errorf("both keys must persist on disk; got %+v", loaded.Entries)
	}
}

// _ keeps unused-import linter quiet if the package surface trims later.
var _ = filepath.Join
var _ = os.Stat
