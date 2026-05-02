package codexbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// DefaultApplyTimeout caps mode=apply requests so a stuck KillSequence
// doesn't hold the global lock forever. 30s covers graceful (5s) +
// SIGTERM (5s) + SIGKILL (2s) + verify/cleanup with margin.
const DefaultApplyTimeout = 30 * time.Second

// DefaultDryRunTimeout caps mode=dry-run requests; 10s covers Scanner +
// EvalDecision per record under reasonable inventory size.
const DefaultDryRunTimeout = 10 * time.Second

// KillRunner is the seam over KillSequence.Run so the sweep handler tests
// can inject a fake without spawning a real signaller. Production wiring
// constructs a *KillSequence per-broker and returns it (KillSequence
// satisfies KillRunner via its Run method).
type KillRunner interface {
	Run(ctx context.Context, decision DecisionResult, verifier SocketVerifier) (KillResult, error)
}

// SweepHandler implements POST /api/codex/brokers/sweep per spec §6.1
// "Manual API" + plan §4 task Q.
//
// Concurrency:
//
//   - globalApplyMu serialises __all__ apply (and unfiltered dry-run, per
//     round-2 #2) against everything else. Held in WRITE mode for those
//     two cases; held in READ mode by every other request.
//   - perBrokerMu (sync.Map of *sync.RWMutex) serialises filtered apply
//     per brokerKey against itself + against same-broker dry-run. Filtered
//     apply takes WRITE; filtered dry-run takes READ. __all__ apply does
//     NOT touch per-broker mutexes — the global write-lock excludes
//     everything else.
//
// Lock order is fixed: globalApplyMu -> perBrokerMu. Deadlock is
// impossible by construction.
type SweepHandler struct {
	// ScanFn returns the current broker inventory. Production injects
	// Scanner.Scan; tests inject a fake. The function should respect ctx.
	ScanFn func(ctx context.Context) ([]BrokerRecord, error)

	// EvalFn evaluates one BrokerRecord into a DecisionResult. Production
	// injects EvalDecision; tests inject a stub.
	EvalFn func(ctx context.Context, rec BrokerRecord, opts DecisionOpts) DecisionResult

	// KillerFactory builds a per-broker KillRunner. Production wraps a
	// concrete *KillSequence; tests inject a fake to assert invocation.
	KillerFactory func(rec BrokerRecord) KillRunner

	// SocketVerifier is passed to each KillRunner.Run. Defaults to
	// NewSocketVerifier() if nil.
	SocketVerifier SocketVerifier

	// Quarantine is consulted before any kill: a quarantined broker is
	// excluded from evaluation entirely.
	Quarantine *QuarantineFile

	// Registry is read-only in P2 (spawn-time write deferred to PR-G).
	Registry LaunchRegistryReader

	// E1Tracker is daemon-lifetime; passed into EvalDecision via
	// DecisionOpts and Reset() after a successful kill.
	E1Tracker *E1Tracker

	// DecisionOpts seam — production fills FS, Lister, Dialer, Panes,
	// IdleTimeout, ResultWindow, StaleThreshold; tests can leave nil
	// because EvalFn is stubbed.
	BaseDecisionOpts DecisionOpts

	// Timeouts — defaulted in HandleSweep when zero.
	ApplyTimeout  time.Duration
	DryRunTimeout time.Duration

	// Concurrency primitives. Initialised lazily in HandleSweep.
	globalApplyMu sync.RWMutex
	perBrokerMu   sync.Map // map[brokerKey]*sync.RWMutex
}

// HandleSweep is the HTTP entry point.
func (h *SweepHandler) HandleSweep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "dry-run"
	}
	if mode != "dry-run" && mode != "apply" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mode"})
		return
	}
	brokerKey := r.URL.Query().Get("brokerKey")

	// Apply per-mode timeout to the request context. The lock acquisition
	// path also honours this deadline — a queued request whose deadline
	// expires before the global lock is released gets a 503 + Retry-After.
	timeout := h.DryRunTimeout
	if mode == "apply" {
		timeout = h.ApplyTimeout
	}
	if timeout <= 0 {
		if mode == "apply" {
			timeout = DefaultApplyTimeout
		} else {
			timeout = DefaultDryRunTimeout
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Acquire locks per the kill-semantics table in plan §4 task Q.
	releaseLocks, lockErr := h.acquireLocks(ctx, mode, brokerKey)
	if lockErr != nil {
		// Context expired waiting for the global lock.
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sweep busy"})
		return
	}
	defer releaseLocks()

	brokers, scanErr := h.ScanFn(ctx)
	if scanErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": scanErr.Error()})
		return
	}

	resp := SweepResponse{
		DryRun:    mode == "dry-run",
		Evaluated: []BrokerDecision{},
		Applied:   []string{},
	}

	opts := h.BaseDecisionOpts
	if opts.Registry == nil {
		opts.Registry = h.Registry
	}
	if opts.E1Tracker == nil {
		opts.E1Tracker = h.E1Tracker
	}

	for _, rec := range brokers {
		if brokerKey != "" && rec.Key != brokerKey {
			continue
		}
		if IsQuarantined(h.Quarantine, rec.Key) {
			continue
		}

		decision := h.EvalFn(ctx, rec, opts)
		isForeign := decision.Reason == "foreign_quarantine"

		// Combine baseline Kill + foreign flag per the kill-semantics
		// table in plan §4 task Q.
		shouldKill := false
		auditReason := decision.Reason
		switch {
		case mode != "apply":
			shouldKill = false
		case !decision.Kill:
			// Baseline alive-protection wins for ANY mode/brokerKey
			// combination. Round-3 finding #1: operator override does
			// NOT bypass alive-protection.
			shouldKill = false
		case brokerKey == "" && isForeign:
			// Mass-kill safety: unfiltered apply NEVER kills foreign.
			shouldKill = false
		case brokerKey != "" && isForeign:
			// Operator-explicit override per spec §5.1 line 371: filter
			// IS the override; baseline Kill=true is honoured.
			shouldKill = true
			auditReason = "manual_sweep_override"
		case isForeign:
			shouldKill = false
		default:
			shouldKill = true
		}

		// Reflect the audit reason flip back into the evaluated decision
		// so the API surface and audit dump agree.
		decision.Reason = auditReason

		if shouldKill {
			runner := h.KillerFactory(rec)
			verifier := h.SocketVerifier
			if verifier == nil {
				verifier = NewSocketVerifier()
			}
			_, killErr := runner.Run(ctx, decision, verifier)
			if killErr != nil {
				resp.Errors = append(resp.Errors, rec.Key+": "+killErr.Error())
			} else {
				resp.Applied = append(resp.Applied, rec.Key)
				if h.E1Tracker != nil {
					h.E1Tracker.Reset(rec.Key)
				}
			}
		}

		resp.Evaluated = append(resp.Evaluated, BrokerDecision{Broker: rec, Decision: decision})
	}

	writeJSON(w, http.StatusOK, resp)
}

// acquireLocks implements the lock-acquisition matrix in plan §4 task Q.
// Returns a release function that MUST be called to drop everything.
//
// On context-deadline expiration while waiting for globalApplyMu, returns
// a non-nil error; the handler maps that to a 503 + Retry-After.
func (h *SweepHandler) acquireLocks(ctx context.Context, mode, brokerKey string) (func(), error) {
	wantsGlobalWrite := (mode == "apply" && brokerKey == "") || (mode == "dry-run" && brokerKey == "")

	if wantsGlobalWrite {
		if err := lockWriteCtx(ctx, &h.globalApplyMu); err != nil {
			return nil, err
		}
		return func() { h.globalApplyMu.Unlock() }, nil
	}

	// All other paths take the global RLock first.
	if err := lockReadCtx(ctx, &h.globalApplyMu); err != nil {
		return nil, err
	}

	// Then per-broker mutex (always required for filtered paths).
	pm := h.brokerMu(brokerKey)
	if mode == "apply" {
		pm.Lock()
		return func() { pm.Unlock(); h.globalApplyMu.RUnlock() }, nil
	}
	// Filtered dry-run.
	pm.RLock()
	return func() { pm.RUnlock(); h.globalApplyMu.RUnlock() }, nil
}

// brokerMu returns (creating if needed) the *sync.RWMutex for the given
// brokerKey. sync.Map.LoadOrStore guarantees one-shot init.
func (h *SweepHandler) brokerMu(key string) *sync.RWMutex {
	v, _ := h.perBrokerMu.LoadOrStore(key, &sync.RWMutex{})
	return v.(*sync.RWMutex)
}

// lockWriteCtx attempts to acquire a write-lock on mu, but bails out if ctx
// expires first. Implementation: try the lock in a goroutine and select.
func lockWriteCtx(ctx context.Context, mu *sync.RWMutex) error {
	if mu.TryLock() {
		return nil
	}
	got := make(chan struct{})
	go func() {
		mu.Lock()
		close(got)
	}()
	select {
	case <-got:
		return nil
	case <-ctx.Done():
		// Spawn a "release-on-acquire" goroutine so the eventually-acquired
		// lock doesn't leak.
		go func() {
			<-got
			mu.Unlock()
		}()
		return ctx.Err()
	}
}

func lockReadCtx(ctx context.Context, mu *sync.RWMutex) error {
	if mu.TryRLock() {
		return nil
	}
	got := make(chan struct{})
	go func() {
		mu.RLock()
		close(got)
	}()
	select {
	case <-got:
		return nil
	case <-ctx.Done():
		go func() {
			<-got
			mu.RUnlock()
		}()
		return ctx.Err()
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// _ ensures strconv stays available for future Retry-After numeric tuning;
// keeping the import wired prevents lint flapping when the constant is
// inlined.
var _ = strconv.Itoa
var _ = errors.New
