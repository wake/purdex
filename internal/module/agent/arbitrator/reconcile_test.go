package arbitrator

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// fakeLiveness is an in-test implementation of actorLivenessView used to
// decouple reconcile_test from frame_state.go (Task 4). It captures the
// LastActivity at construction and supports an assertion helper to verify
// reconcile did not mutate it.
type fakeLiveness struct {
	lastActivity time.Time
	mutated      bool
}

func (f *fakeLiveness) GetLastActivity() time.Time {
	return f.lastActivity
}

// setLastActivity is a test-only helper; reconcile must NEVER call this.
func (f *fakeLiveness) setLastActivity(t time.Time) {
	f.lastActivity = t
	f.mutated = true
}

// TestReconcile_FlushesPending verifies a tick invokes the flushPendingDue
// callback exactly once with the current clock value.
func TestReconcile_FlushesPending(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	var flushCalls []time.Time
	flush := func(t time.Time) { flushCalls = append(flushCalls, t) }

	r := newReconciler(reconcilerDeps{
		now:             func() time.Time { return now },
		flushPendingDue: flush,
		allActiveActors: func() map[observation.ActorKey]actorLivenessView {
			return nil
		},
		emitStaleTrace: func(observation.ActorKey) {},
		staleThreshold: 30 * time.Second,
	})

	r.reconcile()

	if len(flushCalls) != 1 {
		t.Fatalf("flushPendingDue calls = %d, want 1", len(flushCalls))
	}
	if !flushCalls[0].Equal(now) {
		t.Errorf("flushPendingDue arg = %v, want %v", flushCalls[0], now)
	}
}

// TestReconcile_StaleActor_EmitsTraceOnly verifies an actor older than the
// stale threshold triggers emitStaleTrace exactly once, and its LastActivity
// is NOT mutated.
func TestReconcile_StaleActor_EmitsTraceOnly(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	key := observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "actor-1"}
	// 31s stale.
	actor := &fakeLiveness{lastActivity: now.Add(-31 * time.Second)}
	before := actor.lastActivity

	var emitted []observation.ActorKey
	r := newReconciler(reconcilerDeps{
		now:             func() time.Time { return now },
		flushPendingDue: func(time.Time) {},
		allActiveActors: func() map[observation.ActorKey]actorLivenessView {
			return map[observation.ActorKey]actorLivenessView{key: actor}
		},
		emitStaleTrace: func(k observation.ActorKey) { emitted = append(emitted, k) },
		staleThreshold: 30 * time.Second,
	})

	r.reconcile()

	if len(emitted) != 1 {
		t.Fatalf("emitStaleTrace calls = %d, want 1", len(emitted))
	}
	if emitted[0] != key {
		t.Errorf("emitted key = %v, want %v", emitted[0], key)
	}
	// Critical invariant: reconcile must NOT mutate actor.LastActivity.
	if actor.mutated {
		t.Error("actor.LastActivity was mutated — reconcile must never mutate actors")
	}
	if !actor.lastActivity.Equal(before) {
		t.Errorf("actor.LastActivity = %v, want unchanged %v", actor.lastActivity, before)
	}
}

// TestReconcile_FreshActor_NoTrace verifies an actor inside the stale window
// does not trigger emitStaleTrace.
func TestReconcile_FreshActor_NoTrace(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	key := observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "actor-1"}
	// 15s old — well under 30s threshold.
	actor := &fakeLiveness{lastActivity: now.Add(-15 * time.Second)}

	var emitted int
	r := newReconciler(reconcilerDeps{
		now:             func() time.Time { return now },
		flushPendingDue: func(time.Time) {},
		allActiveActors: func() map[observation.ActorKey]actorLivenessView {
			return map[observation.ActorKey]actorLivenessView{key: actor}
		},
		emitStaleTrace: func(observation.ActorKey) { emitted++ },
		staleThreshold: 30 * time.Second,
	})

	r.reconcile()

	if emitted != 0 {
		t.Errorf("emitStaleTrace calls = %d, want 0 (actor is fresh)", emitted)
	}
}

// TestReconcile_NoActors_Noop verifies reconcile with an empty actor map still
// flushes pending (which has its own side effects) but does not call
// emitStaleTrace.
func TestReconcile_NoActors_Noop(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	flushed := false
	var emitted int

	r := newReconciler(reconcilerDeps{
		now:             func() time.Time { return now },
		flushPendingDue: func(time.Time) { flushed = true },
		allActiveActors: func() map[observation.ActorKey]actorLivenessView {
			return map[observation.ActorKey]actorLivenessView{}
		},
		emitStaleTrace: func(observation.ActorKey) { emitted++ },
		staleThreshold: 30 * time.Second,
	})

	r.reconcile()

	if !flushed {
		t.Error("flushPendingDue not called — reconcile must always flush, even with zero actors")
	}
	if emitted != 0 {
		t.Errorf("emitStaleTrace calls = %d, want 0", emitted)
	}
}

// TestReconcile_DoesNotMutateActor asserts that reconcile never calls
// setLastActivity on the actor implementation. Uses both a stale and a fresh
// actor to cover both code paths.
func TestReconcile_DoesNotMutateActor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	staleKey := observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "stale"}
	freshKey := observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "fresh"}
	stale := &fakeLiveness{lastActivity: now.Add(-31 * time.Second)}
	fresh := &fakeLiveness{lastActivity: now.Add(-5 * time.Second)}
	staleBefore := stale.lastActivity
	freshBefore := fresh.lastActivity

	// Reset mutated flag set by constructor to isolate reconcile's behaviour.
	stale.mutated = false
	fresh.mutated = false

	r := newReconciler(reconcilerDeps{
		now:             func() time.Time { return now },
		flushPendingDue: func(time.Time) {},
		allActiveActors: func() map[observation.ActorKey]actorLivenessView {
			return map[observation.ActorKey]actorLivenessView{
				staleKey: stale,
				freshKey: fresh,
			}
		},
		emitStaleTrace: func(observation.ActorKey) {},
		staleThreshold: 30 * time.Second,
	})

	r.reconcile()

	if stale.mutated {
		t.Error("stale actor LastActivity mutated")
	}
	if fresh.mutated {
		t.Error("fresh actor LastActivity mutated")
	}
	if !stale.lastActivity.Equal(staleBefore) {
		t.Errorf("stale LastActivity = %v, want %v", stale.lastActivity, staleBefore)
	}
	if !fresh.lastActivity.Equal(freshBefore) {
		t.Errorf("fresh LastActivity = %v, want %v", fresh.lastActivity, freshBefore)
	}
}

// TestReconcile_ThresholdBoundary_ExactlyStaleNotEmitted verifies the
// threshold comparison is strict greater-than (> not >=): an actor exactly at
// the threshold is NOT considered stale.
func TestReconcile_ThresholdBoundary_ExactlyStaleNotEmitted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	key := observation.ActorKey{SessionID: "sess-A", Generation: 1, ActorID: "a"}
	// Exactly 30s old.
	actor := &fakeLiveness{lastActivity: now.Add(-30 * time.Second)}

	var emitted int
	r := newReconciler(reconcilerDeps{
		now:             func() time.Time { return now },
		flushPendingDue: func(time.Time) {},
		allActiveActors: func() map[observation.ActorKey]actorLivenessView {
			return map[observation.ActorKey]actorLivenessView{key: actor}
		},
		emitStaleTrace: func(observation.ActorKey) { emitted++ },
		staleThreshold: 30 * time.Second,
	})

	r.reconcile()

	if emitted != 0 {
		t.Errorf("emitStaleTrace calls = %d, want 0 (boundary must be strict >)", emitted)
	}
}
