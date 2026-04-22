package arbitrator

import (
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// actorLivenessView is the minimal read-only projection of an actor that the
// reconcile loop needs. It is defined here (rather than taking *actorSummary
// directly) so reconcile.go does not take a compile-time dependency on
// Task 4's frame_state.go; any concrete actor type that exposes
// GetLastActivity() time.Time satisfies this interface. Task 6 / Task 7 wire
// in an adapter that exposes frameState.allActiveActors() as a
// map[ActorKey]actorLivenessView.
//
// Reconcile must only READ LastActivity — mutation is explicitly forbidden by
// spec §3.4.5.
type actorLivenessView interface {
	GetLastActivity() time.Time
}

// reconcilerDeps bundles the dependencies reconciler needs. Grouping them in
// a struct keeps the (long) parameter list manageable and makes test wiring
// obvious.
type reconcilerDeps struct {
	// now returns the current clock value. Injected for determinism in tests.
	now func() time.Time
	// flushPendingDue forwards to pendingStore.flushPendingDue with the
	// Arbitrator's tryPromote / emitOnDrop closures already bound.
	flushPendingDue func(now time.Time)
	// allActiveActors returns the live actor map (adapter over
	// frameState.allActiveActors()).
	allActiveActors func() map[observation.ActorKey]actorLivenessView
	// emitStaleTrace is called once per stale actor to produce a
	// ReconcileStaleNoted trace record. Reconcile never mutates the actor.
	emitStaleTrace func(key observation.ActorKey)
	// staleThreshold is the duration after LastActivity at which an actor is
	// considered stale (spec §3.4.5 default: 30s).
	staleThreshold time.Duration
}

// reconciler is a light helper that performs a single reconcile tick when
// invoked. It does not own a goroutine or ticker — that is Task 7's
// responsibility (Arbitrator.Run wires this to a 5s time.Ticker).
type reconciler struct {
	deps reconcilerDeps
}

// newReconciler constructs a reconciler bound to deps. It performs no
// validation — callers (Task 7) are expected to populate all fields.
func newReconciler(deps reconcilerDeps) *reconciler {
	return &reconciler{deps: deps}
}

// reconcile performs one tick of work:
//  1. Flush any pending entries whose deadline has expired.
//  2. Scan active actors; for any actor where now - LastActivity strictly
//     exceeds the stale threshold, emit a ReconcileStaleNoted trace.
//
// reconcile NEVER mutates actor.Status, actor.LastActivity, or actor.EndedAt
// (spec §3.4.5, first rule). It is observer-only.
func (r *reconciler) reconcile() {
	now := r.deps.now()
	if r.deps.flushPendingDue != nil {
		r.deps.flushPendingDue(now)
	}
	if r.deps.allActiveActors == nil || r.deps.emitStaleTrace == nil {
		return
	}
	actors := r.deps.allActiveActors()
	for key, actor := range actors {
		if now.Sub(actor.GetLastActivity()) > r.deps.staleThreshold {
			r.deps.emitStaleTrace(key)
		}
	}
}
