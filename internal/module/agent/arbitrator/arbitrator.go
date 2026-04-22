package arbitrator

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// Default tunables for the Arbitrator (plan D2).
const (
	defaultInChCap         = 1024
	defaultPendingDeadline = 2 * time.Second
	defaultPerSessionCap   = 8
	defaultPerEntryObsCap  = 16
	defaultReconcileEvery  = 5 * time.Second
	defaultHookStormWindow = 10 * time.Millisecond
	defaultHookStormCap    = 50
	defaultStaleThreshold  = 30 * time.Second
)

// Options bundles every tunable and dependency the Arbitrator needs. Zero
// values fall back to the package defaults. Required fields (Minter, Arbmode,
// TraceWriter) must be supplied by the caller — nil values panic at first use.
//
// Lookup is optional: when non-nil, reconcile uses it to attach a valid
// trace_id to synthesized stale traces. When nil (or when a key has no
// entry), reconcile silently skips emitting for that key so empty-TraceID
// rows never reach the TraceWriter and poison validateLightsRow.
type Options struct {
	Minter          observation.TraceIDMinter
	Lookup          observation.TraceIDLookup
	Arbmode         ArbmodeView
	TraceWriter     TraceSubmitter
	InChCap         int
	PendingDeadline time.Duration
	PerSessionCap   int
	PerEntryObsCap  int
	ReconcileEvery  time.Duration
	HookStormWindow time.Duration
	HookStormCap    int
	StaleThreshold  time.Duration
	Now             func() time.Time
}

func (o Options) withDefaults() Options {
	if o.InChCap <= 0 {
		o.InChCap = defaultInChCap
	}
	if o.PendingDeadline <= 0 {
		o.PendingDeadline = defaultPendingDeadline
	}
	if o.PerSessionCap <= 0 {
		o.PerSessionCap = defaultPerSessionCap
	}
	if o.PerEntryObsCap <= 0 {
		o.PerEntryObsCap = defaultPerEntryObsCap
	}
	if o.ReconcileEvery <= 0 {
		o.ReconcileEvery = defaultReconcileEvery
	}
	if o.HookStormWindow <= 0 {
		o.HookStormWindow = defaultHookStormWindow
	}
	if o.HookStormCap <= 0 {
		o.HookStormCap = defaultHookStormCap
	}
	if o.StaleThreshold == 0 {
		o.StaleThreshold = defaultStaleThreshold
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Arbitrator is the single-writer that owns a goroutine consuming observations
// from inCh and running the 9-step apply pipeline. Run blocks until the ctx
// passed to it is cancelled; construction is independent from lifetime.
type Arbitrator struct {
	opts       Options
	inCh       chan observation.Observation
	deps       *applyDeps
	reconciler *reconciler
}

// NewArbitrator builds a ready-to-Run Arbitrator. All helpers (frameState,
// pendingStore, idemCache, hookStormGuard, reconciler) are wired here so Run
// only has to consume inCh + ticker.
func NewArbitrator(opts Options) *Arbitrator {
	opts = opts.withDefaults()

	frames := newFrameState()
	pending := newPendingStore()
	idem := newIdemCache(opts.Now)
	stormGuard := newHookStormGuard(opts.HookStormWindow, opts.HookStormCap)

	a := &Arbitrator{
		opts: opts,
		inCh: make(chan observation.Observation, opts.InChCap),
	}
	a.deps = &applyDeps{
		frames:          frames,
		pending:         pending,
		idem:            idem,
		stormGuard:      stormGuard,
		minter:          opts.Minter,
		arbmode:         opts.Arbmode,
		traceSubmit:     opts.TraceWriter,
		now:             opts.Now,
		pendingDeadline: opts.PendingDeadline,
		perSessionCap:   opts.PerSessionCap,
		perEntryObsCap:  opts.PerEntryObsCap,
	}

	// Reconciler needs closures bound to this Arbitrator so the reconcile
	// tick can flush pending + emit stale traces through the shared
	// TraceWriter.
	//
	// emitStale skips the record when no trace_id is known for the actor's
	// (session, generation). A missing lookup entry signals the session was
	// never observed through SessionStart — emitting an empty-trace_id row
	// would fail validateLightsRow and roll back the TraceWriter's entire
	// batch. Reconcile is observer-only, so a skipped stale note is
	// preferable to a batch poison pill.
	emitStale := func(key observation.ActorKey) {
		if a.opts.Lookup == nil {
			return
		}
		traceID, ok := a.opts.Lookup.Get(key.SessionID, key.Generation)
		if !ok || traceID == "" {
			return
		}
		now := a.opts.Now()
		a.opts.TraceWriter.Submit(TraceRecord{
			TraceID:            traceID,
			SpanID:             uuid.NewString(),
			SessionID:          key.SessionID,
			ObservedGeneration: key.Generation,
			SourceKind:         observation.SourceReconcile,
			Action:             "actor.stale_detected",
			Phase:              observation.PhaseProposed,
			Status:             "success",
			Outcome:            "skipped",
			ReasonCode:         ReasonReconcileStaleNoted,
			ReasonText:         "actor 30s 無活動，reconcile 不主動改 status",
			StartedAt:          now,
			EndedAt:            now,
			DropPriority:       dropPriority(observation.SourceReconcile, observation.PhaseProposed),
		})
	}

	flushPendingDue := func(now time.Time) {
		// PR-1b-1b wires only the skeleton + happy path. tryPromote is a stub
		// that always returns false so pending entries fall through to the
		// drop path (emitting a PidTreeUnresolvable trace per entry). The
		// authoritative promote logic lands in PR-1b-1c.
		tryPromote := func(_ *PendingEntry) bool { return false }
		emitOnDrop := func(entry *PendingEntry, reason string) {
			for _, pObs := range entry.Observations {
				a.deps.emitRejectedTrace(pObs, reason, "pending entry dropped without promotion")
			}
		}
		a.deps.pending.flushPendingDue(now, tryPromote, emitOnDrop)
	}

	a.reconciler = newReconciler(reconcilerDeps{
		now:             opts.Now,
		flushPendingDue: flushPendingDue,
		allActiveActors: frames.allActiveActorsView,
		emitStaleTrace:  emitStale,
		staleThreshold:  opts.StaleThreshold,
		pruneIdem:       func(now time.Time) { idem.Prune(now) },
	})

	return a
}

// InCh returns the send-only channel Module.SubmitObservation writes to.
// Admission (priority-aware send / drop) is Task 8's responsibility; the
// channel itself is unbiased.
func (a *Arbitrator) InCh() chan<- observation.Observation {
	return a.inCh
}

// InChForTesting returns the bidirectional input channel. Test-only — exposes
// the receive side so admission tests can drain without starting the Run
// goroutine. Never call from production code.
func (a *Arbitrator) InChForTesting() chan observation.Observation {
	return a.inCh
}

// Run is the single-owner goroutine. It blocks until ctx is cancelled.
//
// On ctx cancel, Run drains any observations already queued in inCh before
// exiting so Module.Stop does not silently discard queued work between
// "cancel ctx" and "goroutine exit". New observations arriving from
// producers after cancel may still be sent or dropped (admission in
// module.SubmitObservation handles that side); the drain only covers the
// already-queued backlog.
func (a *Arbitrator) Run(ctx context.Context) {
	ticker := time.NewTicker(a.opts.ReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case obs := <-a.inCh:
			a.deps.apply(obs)
		case <-ticker.C:
			a.reconciler.reconcile()
		case <-ctx.Done():
			// Drain any observations queued when cancel fired so producers
			// that submitted just before Module.Stop don't lose data.
			for {
				select {
				case obs := <-a.inCh:
					a.deps.apply(obs)
				default:
					return
				}
			}
		}
	}
}
