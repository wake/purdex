package codexbroker

import (
	"context"
	"sync"
	"testing"
	"time"
)

func recordWithENOENT(key string) BrokerRecord {
	return BrokerRecord{
		Key:       key,
		Cwd:       "/missing/" + key,
		CwdExists: false,
		Anomalies: []Anomaly{{Code: AnomalyCwdMissing}},
	}
}

func recordWithTransient(key string) BrokerRecord {
	return BrokerRecord{
		Key:       key,
		Cwd:       "/maybe/" + key,
		CwdExists: false,
		Anomalies: []Anomaly{{Code: AnomalyCwdTransientStatError}},
	}
}

func recordHealthy(key string) BrokerRecord {
	return BrokerRecord{
		Key:       key,
		Cwd:       "/ok/" + key,
		CwdExists: true,
		Anomalies: nil,
	}
}

// TestE1Tracker_FirstObservation_NotTriggered — first ENOENT observe stores
// state but does not trigger.
func TestE1Tracker_FirstObservation_NotTriggered(t *testing.T) {
	tr := NewE1Tracker()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	triggered, _ := tr.Observe(recordWithENOENT("k1"), now)
	if triggered {
		t.Errorf("expected not triggered on first observe")
	}
	snap := tr.Snapshot()
	if _, ok := snap["k1"]; !ok {
		t.Errorf("expected state for k1 after first observe, got %+v", snap)
	}
}

// TestE1Tracker_SecondObservation_TooSoon — two observes 30s apart → not
// triggered, state retained.
func TestE1Tracker_SecondObservation_TooSoon(t *testing.T) {
	tr := NewE1Tracker()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	tr.Observe(recordWithENOENT("k1"), t0)
	triggered, st := tr.Observe(recordWithENOENT("k1"), t0.Add(30*time.Second))
	if triggered {
		t.Errorf("expected not triggered with 30s gap")
	}
	if st.FirstSeenAt.IsZero() {
		t.Errorf("expected state retained, got zero")
	}
}

// TestE1Tracker_DryRunThenApplyAfter60s_Triggers — two observes ≥60s apart
// → second returns triggered=true. Core regression test for the
// daemon-lifetime promotion.
func TestE1Tracker_DryRunThenApplyAfter60s_Triggers(t *testing.T) {
	tr := NewE1Tracker()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	tr.Observe(recordWithENOENT("k1"), t0)
	triggered, st := tr.Observe(recordWithENOENT("k1"), t0.Add(60*time.Second))
	if !triggered {
		t.Errorf("expected triggered after ≥60s gap")
	}
	if st.FirstConfirmedAt == nil {
		t.Errorf("expected FirstConfirmedAt populated on trigger")
	}
}

// TestE1Tracker_TransientError_NoE1 — Observe with transient stat error
// only → tracker does NOT record state.
func TestE1Tracker_TransientError_NoE1(t *testing.T) {
	tr := NewE1Tracker()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	triggered, _ := tr.Observe(recordWithTransient("k1"), t0)
	if triggered {
		t.Errorf("transient should not trigger")
	}
	snap := tr.Snapshot()
	if _, ok := snap["k1"]; ok {
		t.Errorf("transient should not record state")
	}
}

// TestE1Tracker_CwdRecovered_StateCleared — ENOENT then recovery → state
// cleared; subsequent ENOENT restarts the 60s clock fresh.
func TestE1Tracker_CwdRecovered_StateCleared(t *testing.T) {
	tr := NewE1Tracker()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	tr.Observe(recordWithENOENT("k1"), t0)
	tr.Observe(recordHealthy("k1"), t0.Add(30*time.Second))
	snap := tr.Snapshot()
	if _, ok := snap["k1"]; ok {
		t.Errorf("expected state cleared after recovery")
	}
	// Now ENOENT again — must NOT trigger immediately even though wall time is past 60s.
	triggered, _ := tr.Observe(recordWithENOENT("k1"), t0.Add(120*time.Second))
	if triggered {
		t.Errorf("expected restart of 60s clock, not immediate trigger")
	}
}

// TestE1Tracker_ConcurrentObserves_Safe — go test -race: 100 concurrent
// Observes for distinct brokerKeys must not corrupt the map.
func TestE1Tracker_ConcurrentObserves_Safe(t *testing.T) {
	tr := NewE1Tracker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tr.Observe(recordWithENOENT("k"+itoa(i)), time.Now())
		}(i)
	}
	wg.Wait()
	if len(tr.Snapshot()) != 100 {
		t.Errorf("expected 100 distinct keys, got %d", len(tr.Snapshot()))
	}
}

// TestE1Tracker_Reset_ClearsState — Reset(key) drops the state; subsequent
// Observe is fresh-first.
func TestE1Tracker_Reset_ClearsState(t *testing.T) {
	tr := NewE1Tracker()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	tr.Observe(recordWithENOENT("k1"), t0)
	tr.Reset("k1")
	snap := tr.Snapshot()
	if _, ok := snap["k1"]; ok {
		t.Errorf("expected state dropped after Reset")
	}
	triggered, _ := tr.Observe(recordWithENOENT("k1"), t0.Add(120*time.Second))
	if triggered {
		t.Errorf("expected fresh-first after Reset, got immediate trigger")
	}
}

// TestEvalDecision_E1Tracker_OverrideE1True — task G integration: when
// E1Tracker is supplied via DecisionOpts and triggers, OverrideE1=true on
// the result.
func TestEvalDecision_E1Tracker_OverrideE1True(t *testing.T) {
	tr := NewE1Tracker()
	rec := brokerWithLastJobAge(2 * time.Hour)
	rec.Cwd = "/missing"
	rec.CwdExists = false
	rec.Anomalies = []Anomaly{{Code: AnomalyCwdMissing}}
	// First observe at t0 — populates state.
	tr.Observe(rec, time.Now().Add(-90*time.Second))
	// Second observe at "now" via EvalDecision — should trigger.
	opts := decisionOptsAllFalse(false)
	opts.E1Tracker = tr
	opts.Registry = &fakeRegistry{entries: map[string]LaunchEntry{rec.Key: {BrokerKey: rec.Key, TmuxPane: "%1"}}}
	res := EvalDecision(context.Background(), rec, opts)
	if !res.OverrideE1 {
		t.Errorf("expected OverrideE1=true, got %+v", res)
	}
}

// ----- Task H — E2 fingerprint mismatch quarantine -----------------------

// TestEvalE2_FingerprintMatch_NoTrigger — same fingerprint → not triggered.
func TestEvalE2_FingerprintMatch_NoTrigger(t *testing.T) {
	rec := BrokerRecord{
		Key:    "k1",
		PID:    4321,
		Lstart: time.Now().Add(-2 * time.Hour),
	}
	known := BrokerFingerprint{
		Executable: "/usr/bin/node",
		Cmdline:    "node app-server-broker.mjs",
		PidFile:    "/tmp/cxc-X/broker.pid",
	}
	live := known
	triggered, _ := EvalE2(rec, live, &known)
	if triggered {
		t.Errorf("expected not triggered on match")
	}
}

// TestEvalE2_FingerprintMismatch_Triggered — mismatch → triggered.
func TestEvalE2_FingerprintMismatch_Triggered(t *testing.T) {
	rec := BrokerRecord{Key: "k1", PID: 4321}
	known := BrokerFingerprint{Executable: "/usr/bin/node", Cmdline: "node a"}
	live := BrokerFingerprint{Executable: "/usr/bin/python3", Cmdline: "python3 b"}
	triggered, _ := EvalE2(rec, live, &known)
	if !triggered {
		t.Errorf("expected triggered on mismatch")
	}
}

// TestEvalE2_NoKnownFingerprint_NoTrigger — no historical fingerprint to
// compare against → cannot fire E2.
func TestEvalE2_NoKnownFingerprint_NoTrigger(t *testing.T) {
	rec := BrokerRecord{Key: "k1"}
	live := BrokerFingerprint{Executable: "/usr/bin/node", Cmdline: "node a"}
	triggered, _ := EvalE2(rec, live, nil)
	if triggered {
		t.Errorf("nil knownFingerprint must not trigger")
	}
}
