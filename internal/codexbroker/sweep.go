package codexbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
	//
	// PR review finding C: when KillSequence.Run returns identityMismatchErr
	// the handler appends a fresh QuarantineEntry to this in-memory file via
	// QuarantineStore.AddEntry and persists it through QuarantineStore.Save.
	// Subsequent sweeps see the entry via IsQuarantined and skip the broker
	// before any further kill attempt — preventing the indefinite-retry hole.
	Quarantine *QuarantineFile

	// QuarantineStore + QuarantinePath persist E2 entries. Both must be set
	// for finding-C persistence to engage; if either is nil the handler
	// still updates the in-memory Quarantine but logs a warning rather than
	// silently dropping forensic data.
	QuarantineStore *QuarantineStore
	QuarantinePath  string

	// Lister captures the live (pid, lstart, cmdline) tuple at quarantine
	// time so the persisted QuarantineEntry contains the *suspicious*
	// fingerprint (the new process that took the PID), not the dead
	// broker's recorded identity. Production wires NewPsLister(); tests
	// inject a fake.
	Lister ProcessLister

	// QuarantineRetentionDays defaults to 7 (spec §5.3). Tests can shorten.
	QuarantineRetentionDays int

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

	// ApplyDisabled, if non-empty, causes mode=apply requests to return
	// 503 Service Unavailable with the supplied reason in the body and a
	// Retry-After hint. dry-run remains allowed so operators can still
	// inspect the inventory while the daemon is in degraded mode.
	//
	// PR review finding D: set by Module.Init to "quarantine_load_failed"
	// when quarantine.json on disk was corrupt and the in-memory state
	// cannot be trusted. Operator clears the situation by deleting the
	// renamed .bak file and restarting the daemon.
	ApplyDisabled string

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

	// PR review finding D: degraded mode short-circuits apply BEFORE any
	// lock is taken. dry-run is still allowed so operators can investigate
	// the inventory.
	if mode == "apply" && h.ApplyDisabled != "" {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "apply disabled: " + h.ApplyDisabled,
		})
		return
	}

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

	// PR review finding F: re-check ctx after acquisition. A request whose
	// deadline expired while the lock acquisition goroutine was racing
	// would otherwise still run the full scan + decision evaluation under
	// a doomed budget.
	if err := ctx.Err(); err != nil {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sweep ctx expired"})
		return
	}

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
				if IsIdentityMismatch(killErr) {
					// PR review finding C — translate Step 0 identity drift
					// into an E2 quarantine entry instead of silently looping
					// next sweep. AddEntry is idempotent on brokerKey so a
					// re-quarantine on subsequent sweep is a no-op.
					if quarantined, qErr := h.recordQuarantine(ctx, rec); qErr != nil {
						resp.Errors = append(resp.Errors, rec.Key+": quarantine save failed: "+qErr.Error())
					} else if quarantined {
						resp.Quarantined = append(resp.Quarantined, rec.Key)
					}
				} else {
					resp.Errors = append(resp.Errors, rec.Key+": "+killErr.Error())
				}
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
	//
	// PR review finding F: previously this called pm.Lock() / pm.RLock()
	// directly, ignoring ctx. A queued same-broker apply could stall
	// indefinitely while waiting on the per-broker lock; the request
	// context's deadline + client cancel were both ineffective. Now both
	// branches use lockWriteCtx / lockReadCtx so an expired ctx triggers
	// 503 + Retry-After at the handler entry instead.
	pm := h.brokerMu(brokerKey)
	if mode == "apply" {
		if err := lockWriteCtx(ctx, pm); err != nil {
			h.globalApplyMu.RUnlock()
			return nil, err
		}
		return func() { pm.Unlock(); h.globalApplyMu.RUnlock() }, nil
	}
	// Filtered dry-run.
	if err := lockReadCtx(ctx, pm); err != nil {
		h.globalApplyMu.RUnlock()
		return nil, err
	}
	return func() { pm.RUnlock(); h.globalApplyMu.RUnlock() }, nil
}

// brokerMu returns (creating if needed) the *sync.RWMutex for the given
// brokerKey. sync.Map.LoadOrStore guarantees one-shot init.
func (h *SweepHandler) brokerMu(key string) *sync.RWMutex {
	v, _ := h.perBrokerMu.LoadOrStore(key, &sync.RWMutex{})
	return v.(*sync.RWMutex)
}

// recordQuarantine appends an E2 entry for rec.Key to the in-memory
// QuarantineFile, snapshots the live (pid, lstart, cmdline) tuple as the
// suspicious fingerprint, and atomically saves quarantine.json. Used by
// HandleSweep when KillSequence.Run reports identityMismatchErr (PR review
// finding C).
//
// Returns (added, error). added=true means the entry was appended in
// memory (regardless of save outcome — the entry is in-memory consistent).
// error is non-nil only when the disk save fails after the in-memory
// append; the caller surfaces the save error in resp.Errors so operators
// can investigate without the broker being silently un-quarantined.
//
// If h.QuarantineStore or h.QuarantinePath is nil/empty, persistence is
// skipped (added=true, error=nil); the in-memory append still protects
// the current daemon's lifetime against retry-loops.
func (h *SweepHandler) recordQuarantine(ctx context.Context, rec BrokerRecord) (bool, error) {
	if h.Quarantine == nil {
		h.Quarantine = &QuarantineFile{Version: 1}
	}
	now := time.Now().UTC()
	retention := h.QuarantineRetentionDays
	if retention <= 0 {
		retention = 7
	}
	fp := h.snapshotFingerprint(ctx, rec)
	entry := QuarantineEntry{
		BrokerKey:     rec.Key,
		PID:           rec.PID,
		Lstart:        rec.Lstart,
		QuarantinedAt: now,
		Reason:        "pid_reuse_suspicion",
		Fingerprint:   fp,
		ExpiresAt:     now.Add(time.Duration(retention) * 24 * time.Hour),
	}
	store := h.QuarantineStore
	if store == nil {
		store = &QuarantineStore{}
	}
	h.Quarantine = store.AddEntry(h.Quarantine, entry)
	if h.QuarantinePath == "" {
		// In-memory only; warn through return but treat as added.
		return true, nil
	}
	if err := store.Save(h.QuarantinePath, h.Quarantine); err != nil {
		return true, err
	}
	return true, nil
}

// snapshotFingerprint fetches the *current* row for rec.PID and returns it
// as a BrokerFingerprint. The fields capture the *suspicious* identity (the
// process that the PID now points to), which is what an operator needs to
// triage. Falls back to rec's recorded values when the lister is missing
// or the row vanished between detection and snapshot.
func (h *SweepHandler) snapshotFingerprint(ctx context.Context, rec BrokerRecord) BrokerFingerprint {
	fp := BrokerFingerprint{
		Cmdline: "",
		PidFile: rec.PidFile,
	}
	if h.Lister == nil {
		return fp
	}
	listCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	rows, err := h.Lister.List(listCtx)
	if err != nil {
		return fp
	}
	for _, row := range rows {
		if row.PID == rec.PID {
			fp.Cmdline = row.Cmdline
			// Best-effort: parse executable as the first cmdline token.
			if idx := strings.IndexByte(row.Cmdline, ' '); idx > 0 {
				fp.Executable = row.Cmdline[:idx]
			} else {
				fp.Executable = row.Cmdline
			}
			break
		}
	}
	return fp
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
