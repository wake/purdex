package codexbroker

import (
	"sync"
	"time"
)

// DefaultE1ConfirmationGap is the spec §5.3 line 399 minimum interval
// between two definitive ENOENT observations before E1 fires.
const DefaultE1ConfirmationGap = 60 * time.Second

// E1Tracker accumulates per-broker ENOENT observations across daemon
// lifetime. The spec §5.3 rule "two consecutive scans ≥ 60 s apart"
// CANNOT be satisfied by per-sweep state because each POST /sweep is an
// independent HTTP request and any sweep-local map dies with the
// response. The tracker is therefore owned by Module (task R) and
// injected into the decision composer + sweep handler.
//
// Daemon restart drops the in-memory state — acceptable per spec which
// only requires two scans ≥ 60 s apart, not persistence (a fresh start
// conservatively restarts the 60 s clock; no false-positive E1 ever
// fires from this).
type E1Tracker struct {
	mu     sync.Mutex
	states map[string]*E1State
}

// NewE1Tracker returns a ready-to-use empty tracker.
func NewE1Tracker() *E1Tracker {
	return &E1Tracker{states: map[string]*E1State{}}
}

// Observe ingests one BrokerRecord scan result and reports whether E1 is
// triggered.
//
// Behaviour matrix:
//
//   - rec carries AnomalyCwdMissing (definitive ENOENT, not transient):
//       no prior state → store first-seen, return (false, ...).
//       prior state present, gap ≥ DefaultE1ConfirmationGap → set
//         FirstConfirmedAt = now and return (true, *prior).
//       prior state present, gap < DefaultE1ConfirmationGap → return
//         (false, *prior) without mutation (keeps the original
//         FirstSeenAt as the clock anchor).
//
//   - rec carries AnomalyCwdTransientStatError (or no anomaly): clear any
//     prior state for this brokerKey so a future ENOENT chain restarts
//     cleanly. Return (false, zero E1State).
//
// Concurrent-safe: a single sync.Mutex serialises every map mutation.
func (t *E1Tracker) Observe(rec BrokerRecord, now time.Time) (bool, E1State) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if recordHasAnomaly(rec, AnomalyCwdMissing) {
		prior, ok := t.states[rec.Key]
		if !ok {
			st := &E1State{BrokerKey: rec.Key, FirstSeenAt: now}
			t.states[rec.Key] = st
			return false, *st
		}
		if now.Sub(prior.FirstSeenAt) >= DefaultE1ConfirmationGap {
			confirmed := now
			prior.FirstConfirmedAt = &confirmed
			return true, *prior
		}
		return false, *prior
	}

	// Not ENOENT — clear any prior state so a fresh ENOENT chain restarts.
	delete(t.states, rec.Key)
	return false, E1State{}
}

// Snapshot returns a copy of the current state map. Callers must not
// mutate the returned values; the copy is shallow but the *E1State values
// are dereferenced before insertion to prevent aliasing.
func (t *E1Tracker) Snapshot() map[string]E1State {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]E1State, len(t.states))
	for k, v := range t.states {
		out[k] = *v
	}
	return out
}

// Reset removes the state for one brokerKey. Called from the sweep handler
// after a successful kill so a respawned broker doesn't inherit a stale
// 60 s clock from the killed predecessor (plan task G last bullet).
func (t *E1Tracker) Reset(brokerKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, brokerKey)
}

// EvalE2 implements spec §5.3 row E2: the live (pid, lstart, executable,
// cmdline) fingerprint mismatches the most-recently-known fingerprint for
// this brokerKey → quarantine, not kill.
//
// knownFingerprint may be nil when no historical fingerprint is available
// for the broker (P2 ships before the spawn-time write path; the
// fingerprint store land in a follow-up). When nil, E2 cannot fire — return
// false with a "no-baseline" detail so the audit trace explains why.
//
// The match check uses Executable + Cmdline + PidFile (PID/Lstart are
// already part of the runtime identity tuple checked in Step 0; the
// fingerprint is the on-disk shape that survives PID rotation).
func EvalE2(rec BrokerRecord, live BrokerFingerprint, knownFingerprint *BrokerFingerprint) (bool, string) {
	if knownFingerprint == nil {
		return false, "no-baseline"
	}
	if live.Executable == knownFingerprint.Executable &&
		live.Cmdline == knownFingerprint.Cmdline &&
		live.PidFile == knownFingerprint.PidFile {
		return false, "match"
	}
	return true, "mismatch"
}
