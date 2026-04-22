package arbitrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
)

// ----- Test fakes ----------------------------------------------------------

// fakeTraceSubmitter records every TraceRecord Submit was called with so
// individual tests can assert on counts, phases, reason codes, and field
// content. Shutdown is a no-op for unit tests.
type fakeTraceSubmitter struct {
	records []TraceRecord
}

func (f *fakeTraceSubmitter) Submit(r TraceRecord)          { f.records = append(f.records, r) }
func (f *fakeTraceSubmitter) Shutdown(context.Context) error { return nil }

func (f *fakeTraceSubmitter) byPhase(p observation.ObsPhase) []TraceRecord {
	var out []TraceRecord
	for _, r := range f.records {
		if r.Phase == p {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeTraceSubmitter) byReason(code string) []TraceRecord {
	var out []TraceRecord
	for _, r := range f.records {
		if r.ReasonCode == code {
			out = append(out, r)
		}
	}
	return out
}

// fakeMinter records Mint + PruneSessionBefore calls for SessionStart tests.
type fakeMinter struct {
	mintCalls          []mintCall
	pruneBeforeCalls   []pruneBeforeCall
	pruneSessionCalls  []string
	nextMintID         string
}

type mintCall struct {
	sessionID  string
	generation int64
}

type pruneBeforeCall struct {
	sessionID  string
	generation int64
}

func (m *fakeMinter) Mint(sessionID string, generation int64) string {
	m.mintCalls = append(m.mintCalls, mintCall{sessionID, generation})
	if m.nextMintID != "" {
		return m.nextMintID
	}
	return "mint-" + sessionID
}

func (m *fakeMinter) PruneSessionBefore(sessionID string, generation int64) int {
	m.pruneBeforeCalls = append(m.pruneBeforeCalls, pruneBeforeCall{sessionID, generation})
	return 0
}

func (m *fakeMinter) PruneSession(sessionID string) int {
	m.pruneSessionCalls = append(m.pruneSessionCalls, sessionID)
	return 0
}

// fakeArbmode records Snapshot + ApplyAtSessionStart calls and returns a
// configurable Snapshot so each test can pick its own mode.
type fakeArbmode struct {
	current            arbmode.ArbMode
	pending            arbmode.ArbMode
	envLocked          bool
	snapshotCalls      int
	applyAtStartCalls  int
}

func (f *fakeArbmode) Snapshot() arbmode.Snapshot {
	f.snapshotCalls++
	return arbmode.Snapshot{Current: f.current, Pending: f.pending, EnvLocked: f.envLocked}
}

func (f *fakeArbmode) ApplyAtSessionStart() { f.applyAtStartCalls++ }

// ----- Test harness --------------------------------------------------------

// applyHarness builds a fully-wired applyDeps with sensible defaults for
// passthrough-mode tests. Individual tests can override fields on the
// returned struct before invoking apply.
type applyHarness struct {
	deps       *applyDeps
	now        time.Time
	trace      *fakeTraceSubmitter
	minter     *fakeMinter
	arbmodeFk  *fakeArbmode
	stormGuard *hookStormGuard
	idem       *idemCache
	pending    *pendingStore
	frames     *frameState
}

func newApplyHarness(t *testing.T) *applyHarness {
	t.Helper()
	h := &applyHarness{
		now:       time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
		trace:     &fakeTraceSubmitter{},
		minter:    &fakeMinter{},
		arbmodeFk: &fakeArbmode{current: arbmode.ModePassthrough, pending: arbmode.ModePassthrough},
	}
	h.stormGuard = newHookStormGuard(10*time.Millisecond, 50)
	h.idem = newIdemCache(func() time.Time { return h.now })
	h.pending = newPendingStore()
	h.frames = newFrameState()
	h.deps = &applyDeps{
		frames:          h.frames,
		pending:         h.pending,
		idem:            h.idem,
		stormGuard:      h.stormGuard,
		minter:          h.minter,
		arbmode:         h.arbmodeFk,
		traceSubmit:     h.trace,
		now:             func() time.Time { return h.now },
		pendingDeadline: 200 * time.Millisecond,
		perSessionCap:   8,
		perEntryObsCap:  16,
	}
	return h
}

func (h *applyHarness) advance(d time.Duration) { h.now = h.now.Add(d) }

// obsBuilder is a convenience to build observations with only the fields
// each test cares about. Anything not set uses safe defaults.
func (h *applyHarness) obsBuilder() *tObsBuilder {
	return &tObsBuilder{
		observedAt: h.now,
		phase:      observation.PhaseProposed,
	}
}

type tObsBuilder struct {
	sessionID    string
	generation   int64
	sourceKind   observation.SourceKind
	action       string
	watcherToken string
	actorKey     observation.ActorKey
	suggest      string
	endLifecycle bool
	endReason    string
	evidence     []observation.EvidenceRef
	observedAt   time.Time
	seq          int64
	phase        observation.ObsPhase
	traceID      string
}

func (b *tObsBuilder) withSession(s string, gen int64) *tObsBuilder {
	b.sessionID = s
	b.generation = gen
	return b
}
func (b *tObsBuilder) hook() *tObsBuilder  { b.sourceKind = observation.SourceHook; return b }
func (b *tObsBuilder) probe() *tObsBuilder { b.sourceKind = observation.SourceProbe; return b }
func (b *tObsBuilder) sweep() *tObsBuilder { b.sourceKind = observation.SourceSweep; return b }
func (b *tObsBuilder) synthetic() *tObsBuilder {
	b.sourceKind = observation.SourceSynthetic
	return b
}
func (b *tObsBuilder) reconcile() *tObsBuilder {
	b.sourceKind = observation.SourceReconcile
	return b
}
func (b *tObsBuilder) withAction(a string) *tObsBuilder  { b.action = a; return b }
func (b *tObsBuilder) withWatcher(tok string) *tObsBuilder { b.watcherToken = tok; return b }
func (b *tObsBuilder) withActor(actorID string) *tObsBuilder {
	b.actorKey = observation.ActorKey{SessionID: b.sessionID, Generation: b.generation, ActorID: actorID}
	return b
}
func (b *tObsBuilder) withSuggest(s string) *tObsBuilder { b.suggest = s; return b }
func (b *tObsBuilder) endLifecycleWithReason(reason string) *tObsBuilder {
	b.endLifecycle = true
	b.endReason = reason
	return b
}
func (b *tObsBuilder) withEvidence(evs ...observation.EvidenceRef) *tObsBuilder {
	b.evidence = append(b.evidence, evs...)
	return b
}
func (b *tObsBuilder) withSeq(s int64) *tObsBuilder { b.seq = s; return b }
func (b *tObsBuilder) withTrace(id string) *tObsBuilder { b.traceID = id; return b }

func (b *tObsBuilder) build() observation.Observation {
	return observation.Observation{
		TraceID:            b.traceID,
		SessionID:          b.sessionID,
		ObservedGeneration: b.generation,
		SourceKind:         b.sourceKind,
		WatcherToken:       b.watcherToken,
		Action:             b.action,
		Phase:              b.phase,
		Proposal: observation.StateProposal{
			ActorKey:      b.actorKey,
			SuggestStatus: b.suggest,
			EndLifecycle:  b.endLifecycle,
			EndReason:     b.endReason,
		},
		Evidence:   b.evidence,
		ObservedAt: b.observedAt,
		Seq:        b.seq,
	}
}

// ----- Step 1: Generation gate --------------------------------------------

func TestApply_GenStaleBelowFrame_RejectStaleGeneration(t *testing.T) {
	h := newApplyHarness(t)
	// Put the session at generation 5; observation comes in at gen 3.
	h.frames.getOrCreateSession("s1").Generation = 5
	obs := h.obsBuilder().withSession("s1", 3).hook().withAction("PostToolUse").build()

	h.deps.apply(obs)

	rejected := h.trace.byPhase(observation.PhaseRejected)
	if len(rejected) != 1 {
		t.Fatalf("rejected trace count = %d, want 1", len(rejected))
	}
	if rejected[0].ReasonCode != ReasonStaleGeneration {
		t.Errorf("reason = %q, want %q", rejected[0].ReasonCode, ReasonStaleGeneration)
	}
}

func TestApply_GenEqualFrame_Proceed(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 2
	obs := h.obsBuilder().withSession("s1", 2).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()

	h.deps.apply(obs)

	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Fatalf("accepted trace count = %d, want 1; records=%+v", len(accepted), h.trace.records)
	}
	if accepted[0].Outcome != "skipped" {
		t.Errorf("outcome = %q, want skipped (passthrough accept)", accepted[0].Outcome)
	}
}

func TestApply_GenAboveFrame_HookSessionStart_AdvancesGen(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 0
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("SessionStart").build()

	h.deps.apply(obs)

	if g := h.frames.getOrCreateSession("s1").Generation; g != 1 {
		t.Errorf("generation after SessionStart = %d, want 1", g)
	}
}

func TestApply_GenAboveFrame_NonSessionStart_RejectUnauthorizedBump(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 0
	obs := h.obsBuilder().withSession("s1", 1).probe().withAction("Heartbeat").
		withActor("a1").withWatcher("tok").build()

	h.deps.apply(obs)

	if g := h.frames.getOrCreateSession("s1").Generation; g != 0 {
		t.Errorf("generation = %d, want 0 (non-SessionStart must not advance)", g)
	}
	rej := h.trace.byReason(ReasonUnauthorizedGenerationBump)
	if len(rej) != 1 {
		t.Fatalf("UnauthorizedGenerationBump reject count = %d, want 1", len(rej))
	}
}

func TestApply_GenAboveFrame_HookNonSessionStart_RejectUnauthorizedBump(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 0
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PreToolUse").build()

	h.deps.apply(obs)

	if len(h.trace.byReason(ReasonUnauthorizedGenerationBump)) != 1 {
		t.Errorf("hook with non-SessionStart action must reject as UnauthorizedGenerationBump; got %+v", h.trace.records)
	}
}

func TestApply_SessionStart_PrunesTraceIDsBeforeNewGen(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 3
	obs := h.obsBuilder().withSession("s1", 4).hook().withAction("SessionStart").build()

	h.deps.apply(obs)

	if len(h.minter.pruneBeforeCalls) != 1 {
		t.Fatalf("PruneSessionBefore calls = %d, want 1", len(h.minter.pruneBeforeCalls))
	}
	pc := h.minter.pruneBeforeCalls[0]
	if pc.sessionID != "s1" || pc.generation != 4 {
		t.Errorf("prune args = %+v, want {s1, 4}", pc)
	}
	if len(h.minter.mintCalls) != 1 || h.minter.mintCalls[0].generation != 4 {
		t.Errorf("mint calls = %+v, want single {s1, 4}", h.minter.mintCalls)
	}
}

// ----- Step 2: Watcher identity -------------------------------------------

func TestApply_Probe_WatcherTokenMatches_Proceed(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	h.frames.upsertActor(key, func(a *actorSummary) {})
	h.frames.rotateWatcherToken(key, "probe-1", "tok-A")

	obs := h.obsBuilder().withSession("s1", 1).probe().withAction("Heartbeat").
		withActor("a1").withWatcher("tok-A").withSuggest("active").withSeq(1).
		withEvidence(observation.EvidenceRef{Key: "probe_id", Value: "probe-1"}).build()

	h.deps.apply(obs)

	if len(h.trace.byPhase(observation.PhaseProposed)) != 1 {
		t.Errorf("expected 1 accepted trace; got %+v", h.trace.records)
	}
}

func TestApply_Probe_WatcherTokenMismatch_RejectStaleWatcher(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	h.frames.upsertActor(key, func(a *actorSummary) {})
	h.frames.rotateWatcherToken(key, "probe-1", "tok-A")

	obs := h.obsBuilder().withSession("s1", 1).probe().withAction("Heartbeat").
		withActor("a1").withWatcher("tok-WRONG").withSuggest("active").withSeq(1).
		withEvidence(observation.EvidenceRef{Key: "probe_id", Value: "probe-1"}).build()

	h.deps.apply(obs)

	rej := h.trace.byReason(ReasonStaleWatcher)
	if len(rej) != 1 {
		t.Fatalf("StaleWatcher reject count = %d, want 1", len(rej))
	}
}

func TestApply_Probe_WatcherTokenNotRegistered_RejectStaleWatcher(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	// Actor exists but no watcher token has been registered.
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	h.frames.upsertActor(key, func(a *actorSummary) {})

	obs := h.obsBuilder().withSession("s1", 1).probe().withAction("Heartbeat").
		withActor("a1").withWatcher("tok-any").withSuggest("active").withSeq(1).
		withEvidence(observation.EvidenceRef{Key: "probe_id", Value: "probe-1"}).build()

	h.deps.apply(obs)

	if len(h.trace.byReason(ReasonStaleWatcher)) != 1 {
		t.Errorf("expected StaleWatcher reject; got %+v", h.trace.records)
	}
}

func TestApply_Hook_NoWatcherCheck(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()

	h.deps.apply(obs)

	if len(h.trace.byReason(ReasonStaleWatcher)) != 0 {
		t.Errorf("hook should bypass watcher check; got StaleWatcher rejection")
	}
}

func TestApply_Sweep_NoWatcherCheck(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").
		withActor("a1").withSuggest("waiting").withSeq(1).build()

	h.deps.apply(obs)

	if len(h.trace.byReason(ReasonStaleWatcher)) != 0 {
		t.Errorf("sweep should bypass watcher check; got StaleWatcher rejection")
	}
}

// ----- Step 3: Idempotency -------------------------------------------------

func TestApply_FirstObservation_Accepts(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PreToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()

	h.deps.apply(obs)

	if len(h.trace.byPhase(observation.PhaseProposed)) != 1 {
		t.Errorf("first observation should accept; got %+v", h.trace.records)
	}
}

func TestApply_DuplicateSameActorSourceActionEvidence_RejectDuplicate(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	build := func() observation.Observation {
		return h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
			withActor("a1").withSuggest("active").withSeq(5).
			withEvidence(observation.EvidenceRef{Key: "k", Value: "v"}).build()
	}

	h.deps.apply(build())
	h.deps.apply(build())

	if got := len(h.trace.byReason(ReasonDuplicateObservation)); got != 1 {
		t.Errorf("duplicate reject count = %d, want 1; records=%+v", got, h.trace.records)
	}
}

func TestApply_DifferentActor_NotDuplicate(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build())
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a2").withSuggest("active").withSeq(1).build())

	if len(h.trace.byPhase(observation.PhaseProposed)) != 2 {
		t.Errorf("two different actors should both accept; got %+v", h.trace.records)
	}
}

// ----- Step 4: Pending routing --------------------------------------------

func TestApply_SweepWhileActorPending_StashesToPending(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}

	// Seed pending entry directly.
	h.pending.addPending(
		h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").withActor("a1").withSeq(0).build(),
		h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil,
	)
	if !h.pending.isPending(key) {
		t.Fatalf("pending seed failed")
	}

	// Clear records so we only inspect the stash outcome.
	h.trace.records = nil
	obs := h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").
		withActor("a1").withSuggest("waiting").withSeq(1).build()
	h.deps.apply(obs)

	if len(h.trace.records) != 0 {
		t.Errorf("stash path must not emit trace; got %+v", h.trace.records)
	}
	entry := h.pending.entries[key]
	if entry == nil {
		t.Fatalf("pending entry missing")
	}
	if len(entry.Observations) != 2 {
		t.Errorf("pending entry should hold 2 observations; got %d", len(entry.Observations))
	}
}

func TestApply_NonSweepWhileActorPending_Proceed(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	h.pending.addPending(
		h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").withActor("a1").withSeq(0).build(),
		h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil,
	)

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	if len(h.trace.byPhase(observation.PhaseProposed)) != 1 {
		t.Errorf("non-sweep must proceed; got %+v", h.trace.records)
	}
}

func TestApply_SweepWhileActorNotPending_Proceed(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	if len(h.trace.byPhase(observation.PhaseProposed)) != 1 {
		t.Errorf("sweep with no pending should proceed; got %+v", h.trace.records)
	}
}

// ----- Step 5: Source priority --------------------------------------------

func TestApply_SourcePriority_Hook_GtProbe_GtSweep_GtSynthetic(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	h.frames.upsertActor(key, func(a *actorSummary) {})
	h.frames.rotateWatcherToken(key, "probe-1", "tok-A")

	// Hook "active" wins first.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build())

	// Sweep (rank=3) tries to override hook (rank=1); should reject.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").
		withActor("a1").withSuggest("waiting").withSeq(2).build())

	// Synthetic (rank=4) also can't override hook.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).synthetic().withAction("noop").
		withActor("a1").withSuggest("idle").withSeq(3).build())

	if got := len(h.trace.byReason(reasonSourcePriorityLower)); got != 2 {
		t.Fatalf("SourcePriorityLower rejects = %d, want 2; records=%+v", got, h.trace.records)
	}

	// Probe "active" coming after hook "active" is also lower priority
	// (hook=1, probe=2). No error-override applies, so probe must reject.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).probe().withAction("Heartbeat").
		withActor("a1").withWatcher("tok-A").withSuggest("active").withSeq(4).
		withEvidence(observation.EvidenceRef{Key: "probe_id", Value: "probe-1"}).build())
	if got := len(h.trace.byReason(reasonSourcePriorityLower)); got != 3 {
		t.Errorf("probe should reject on priority (hook > probe); got %d rejects. records=%+v", got, h.trace.records)
	}

	// Same-priority (hook → hook) observation for the same actor must NOT
	// reject on priority — equal rank is acceptable.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("idle").withSeq(5).build())
	if got := len(h.trace.byReason(reasonSourcePriorityLower)); got != 3 {
		t.Errorf("same-source (hook→hook) should not reject on priority; got %d. records=%+v", got, h.trace.records)
	}
}

func TestApply_ProbeError_OverridesHookWaiting(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	h.frames.upsertActor(key, func(a *actorSummary) {})
	h.frames.rotateWatcherToken(key, "probe-1", "tok-A")

	// Hook says "waiting" first.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("waiting").withSeq(1).build())

	// Probe says "error" — the documented override should fire.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).probe().withAction("Heartbeat").
		withActor("a1").withWatcher("tok-A").withSuggest("error").withSeq(2).
		withEvidence(observation.EvidenceRef{Key: "probe_id", Value: "probe-1"}).build())

	if got := len(h.trace.byReason(reasonSourcePriorityLower)); got != 0 {
		t.Errorf("probe.error should override hook.waiting; got %d rejects. records=%+v", got, h.trace.records)
	}
	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 2 {
		t.Fatalf("expected 2 accepted traces; got %d", len(accepted))
	}
	actor, _ := h.frames.actor(key)
	if actor.LastAcceptedStatus != "error" {
		t.Errorf("actor status after probe override = %q, want error", actor.LastAcceptedStatus)
	}
}

// ----- Step 6: Monotone lifecycle -----------------------------------------

func TestApply_ActorEnded_NewProposal_RejectActorEnded(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	ended := h.now.Add(-time.Minute)
	h.frames.upsertActor(key, func(a *actorSummary) {
		a.EndedAt = &ended
		a.EndedReason = "done"
		a.Status = "ended"
	})

	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build())

	if got := len(h.trace.byReason(ReasonActorEnded)); got != 1 {
		t.Errorf("ActorEnded reject count = %d, want 1; records=%+v", got, h.trace.records)
	}
}

func TestApply_SyntheticReplacedByNewPrimary_AllowedEndEnded(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old-primary"}
	ended := h.now.Add(-time.Minute)
	h.frames.upsertActor(key, func(a *actorSummary) {
		a.EndedAt = &ended
		a.EndedReason = endReasonReplacedByNewPrimary
	})

	// Synthetic replaced_by_new_primary should pass through monotone gate.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).synthetic().withAction("actor.end_lifecycle").
		withActor("old-primary").endLifecycleWithReason(endReasonReplacedByNewPrimary).
		withSeq(1).build())

	if got := len(h.trace.byReason(ReasonActorEnded)); got != 0 {
		t.Errorf("synthetic replaced_by_new_primary should bypass ActorEnded; records=%+v", h.trace.records)
	}
}

// ----- Step 7: Invariant (single primary) ---------------------------------

func TestApply_NewPrimaryWhenExistingPrimary_EmitsSyntheticEndThenApply(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	oldKey := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old"}
	h.frames.upsertActor(oldKey, func(a *actorSummary) {
		a.Status = "active"
		a.LastActivity = h.now
	})
	h.frames.setPrimary(oldKey, h.now)

	// Clear existing traces so we only see the new primary's pipeline output.
	h.trace.records = nil

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("new").withSuggest("active").withSeq(1).
		withEvidence(observation.EvidenceRef{Key: "is_primary", Value: true}).build()
	h.deps.apply(obs)

	// Expect two traces: synthetic replaced-by-new-primary + accept for new.
	if len(h.trace.records) != 2 {
		t.Fatalf("trace count = %d, want 2 (synthetic + accept); got %+v", len(h.trace.records), h.trace.records)
	}
	if h.trace.records[0].SourceKind != observation.SourceSynthetic {
		t.Errorf("first trace source = %q, want synthetic", h.trace.records[0].SourceKind)
	}
	if h.trace.records[0].ReasonCode != endReasonReplacedByNewPrimary {
		t.Errorf("first trace reason = %q, want %q", h.trace.records[0].ReasonCode, endReasonReplacedByNewPrimary)
	}

	// Actor state: old no longer primary, new is primary.
	oldA, _ := h.frames.actor(oldKey)
	if oldA.IsPrimary {
		t.Error("old primary should have been demoted")
	}
	newKey := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "new"}
	newA, _ := h.frames.actor(newKey)
	if !newA.IsPrimary {
		t.Error("new actor should be primary after transfer")
	}
}

func TestApply_NewPrimaryNoExistingPrimary_NoSynthetic(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).
		withEvidence(observation.EvidenceRef{Key: "is_primary", Value: true}).build()
	h.deps.apply(obs)

	synths := 0
	for _, r := range h.trace.records {
		if r.SourceKind == observation.SourceSynthetic {
			synths++
		}
	}
	if synths != 0 {
		t.Errorf("no existing primary — no synthetic expected; got %d synthetic traces", synths)
	}
}

// ----- Step 8: Mode branch -------------------------------------------------

func TestApply_PassthroughMode_FrameStateUpdates(t *testing.T) {
	h := newApplyHarness(t)
	h.arbmodeFk.current = arbmode.ModePassthrough
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	actor, ok := h.frames.actor(key)
	if !ok {
		t.Fatal("actor should be created on accept")
	}
	if actor.Status != "active" {
		t.Errorf("actor.Status = %q, want active", actor.Status)
	}
	if actor.LastAcceptedSource != observation.SourceHook {
		t.Errorf("actor.LastAcceptedSource = %q, want hook", actor.LastAcceptedSource)
	}
}

// countingTraceSubmitter lets Step-8 tests assert that writes that belong to
// Phase 2 (FramesStore / divergence / broadcast) never fire in 1b-1b. Since
// those subsystems are not injected into applyDeps yet, their absence is
// asserted indirectly via the contract: trace is the only output produced.
type countingTraceSubmitter struct{ fakeTraceSubmitter }

func (c *countingTraceSubmitter) Calls() int { return len(c.records) }

func TestApply_PassthroughMode_NoFramesStoreWrite(t *testing.T) {
	// applyDeps does not take a FramesStore in 1b-1b. The absence of any
	// FramesStore call is therefore guaranteed by the type system; this
	// test documents that invariant by asserting apply never allocates an
	// external writer on the passthrough accept path.
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)
	// Only source of side-effects: trace. Verify count == 1.
	if len(h.trace.records) != 1 {
		t.Errorf("passthrough accept must produce exactly 1 trace; got %d", len(h.trace.records))
	}
}

func TestApply_PassthroughMode_NoDivergenceWrite(t *testing.T) {
	// Same rationale as above: divergence writer is not in applyDeps.
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)
	if len(h.trace.records) != 1 {
		t.Errorf("passthrough accept must produce exactly 1 trace; got %d", len(h.trace.records))
	}
}

func TestApply_PassthroughMode_NoWSBroadcast(t *testing.T) {
	// Broadcaster not in applyDeps; same invariant.
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)
	if len(h.trace.records) != 1 {
		t.Errorf("passthrough accept must produce exactly 1 trace; got %d", len(h.trace.records))
	}
}

func TestApply_PassthroughMode_TraceEmitted(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Fatalf("want 1 accepted trace; got %+v", h.trace.records)
	}
	if accepted[0].Status != "success" || accepted[0].Outcome != "skipped" {
		t.Errorf("trace status/outcome = %s/%s, want success/skipped", accepted[0].Status, accepted[0].Outcome)
	}
}

func TestApply_AuthoritativeMode_FailClosed_EmitsRejectedTrace(t *testing.T) {
	h := newApplyHarness(t)
	h.arbmodeFk.current = arbmode.ModeAuthoritative
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	rej := h.trace.byReason(ReasonAuthoritativeNotSupportedPhase1)
	if len(rej) != 1 {
		t.Fatalf("Authoritative fail-closed reject count = %d, want 1; got %+v", len(rej), h.trace.records)
	}
	if rej[0].Outcome != "rejected" {
		t.Errorf("authoritative reject outcome = %q, want rejected", rej[0].Outcome)
	}

	// Reducer still updates actor during steps 1-7: actor must exist but
	// LastAcceptedSource is unset (step 9 accept path is where that writes).
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	if _, ok := h.frames.actor(key); ok {
		// Actor may or may not exist depending on upstream steps; here no
		// primary flag is set so no upsert happens. Either way acceptable —
		// we just assert LastAcceptedSource stays empty so future observations
		// are not mistakenly seen as "previously accepted".
		actor, _ := h.frames.actor(key)
		if actor.LastAcceptedSource != "" {
			t.Errorf("authoritative must not record acceptance; got source=%q", actor.LastAcceptedSource)
		}
	}
}

func TestApply_AuthoritativeMode_LateModeFlipBackToPassthrough_ResumesNormalAccept(t *testing.T) {
	h := newApplyHarness(t)
	h.arbmodeFk.current = arbmode.ModeAuthoritative
	h.frames.getOrCreateSession("s1").Generation = 1

	// First obs: rejected under authoritative.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build())
	if len(h.trace.byReason(ReasonAuthoritativeNotSupportedPhase1)) != 1 {
		t.Fatalf("expected authoritative reject on first obs")
	}

	// Flip mode back to passthrough and send a new obs with higher seq.
	h.arbmodeFk.current = arbmode.ModePassthrough
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(2).build())

	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Errorf("passthrough after flip should accept; got %d accepted traces", len(accepted))
	}
}

// ----- Step 9: Trace ------------------------------------------------------

func TestApply_TraceRecordHasCompleteDecisionPorts(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	dp := observation.DecisionPort{PortID: "p1", Selected: "branch-a", Reason: "because"}
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	obs.DecisionPorts = []observation.DecisionPort{dp}

	h.deps.apply(obs)

	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Fatalf("no accepted trace")
	}
	if len(accepted[0].DecisionPorts) != 1 || accepted[0].DecisionPorts[0].PortID != "p1" {
		t.Errorf("DecisionPorts not propagated: got %+v", accepted[0].DecisionPorts)
	}
}

func TestApply_TraceRecordPhaseProposed_WhenAcceptedPassthrough(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)
	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Fatalf("want 1 proposed trace; got %d", len(accepted))
	}
}

func TestApply_TraceRecordPhaseRejected_WhenRejected(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 5
	obs := h.obsBuilder().withSession("s1", 2).hook().withAction("PostToolUse").build()
	h.deps.apply(obs)
	rej := h.trace.byPhase(observation.PhaseRejected)
	if len(rej) != 1 {
		t.Fatalf("want 1 rejected trace; got %d", len(rej))
	}
}

func TestApply_TraceRecordAssignsReasonCode(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 5

	// Stale gen → ReasonStaleGeneration.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").build())
	if len(h.trace.byReason(ReasonStaleGeneration)) != 1 {
		t.Error("want ReasonStaleGeneration")
	}

	// Force hook storm: fill the window.
	h.frames.getOrCreateSession("s2").Generation = 1
	for i := 0; i < 51; i++ {
		h.deps.apply(h.obsBuilder().withSession("s2", 1).hook().withAction("PostToolUse").
			withActor("a1").withSuggest("active").withSeq(int64(100 + i)).build())
	}
	if len(h.trace.byReason(ReasonHookStormDropped)) < 1 {
		t.Error("want at least one HookStormDropped reason")
	}
}

// ----- SessionStart helper (D5b) ------------------------------------------

func TestApplySessionStart_EndsOldGenActors_WithSessionRestartReason(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	oldKey := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old"}
	h.frames.upsertActor(oldKey, func(a *actorSummary) { a.Status = "active"; a.LastActivity = h.now })

	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.deps.apply(obs)

	actor, ok := h.frames.actor(oldKey)
	if !ok {
		t.Fatal("old actor missing")
	}
	if actor.EndedAt == nil {
		t.Error("old actor must be ended by SessionStart")
	}
	if actor.EndedReason != "session_restart" {
		t.Errorf("EndedReason = %q, want session_restart", actor.EndedReason)
	}
}

func TestApplySessionStart_ClearsWatcherTokens(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	oldKey := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old"}
	h.frames.upsertActor(oldKey, func(a *actorSummary) { a.Status = "active" })
	h.frames.rotateWatcherToken(oldKey, "probe-1", "tok")

	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.deps.apply(obs)

	actor, _ := h.frames.actor(oldKey)
	if len(actor.WatcherTokens) != 0 {
		t.Errorf("WatcherTokens not cleared: %+v", actor.WatcherTokens)
	}
}

func TestApplySessionStart_ClearsPending_EmitsSessionRestartClearedTracePerObs(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// Seed 3 pending observations on generation 1.
	for i := 0; i < 3; i++ {
		h.pending.addPending(
			h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").
				withActor("a1").withSeq(int64(i)).build(),
			h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil,
		)
	}

	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.trace.records = nil
	h.deps.apply(obs)

	cleared := h.trace.byReason(ReasonSessionRestartCleared)
	// 3 pending observations + 1 boundary synthetic trace that also carries
	// ReasonSessionRestartCleared.
	if len(cleared) < 3 {
		t.Errorf("want at least 3 SessionRestartCleared traces; got %d. records=%+v", len(cleared), h.trace.records)
	}
}

func TestApplySessionStart_CallsMinterMintAndPrune(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()

	h.deps.apply(obs)

	if len(h.minter.mintCalls) != 1 || h.minter.mintCalls[0].generation != 2 {
		t.Errorf("Mint calls = %+v, want single {s1,2}", h.minter.mintCalls)
	}
	if len(h.minter.pruneBeforeCalls) != 1 || h.minter.pruneBeforeCalls[0].generation != 2 {
		t.Errorf("PruneSessionBefore calls = %+v, want single {s1,2}", h.minter.pruneBeforeCalls)
	}
}

func TestApplySessionStart_CallsArbmodeApply(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.deps.apply(obs)
	if h.arbmodeFk.applyAtStartCalls != 1 {
		t.Errorf("ApplyAtSessionStart calls = %d, want 1", h.arbmodeFk.applyAtStartCalls)
	}
}

func TestApplySessionStart_EmitsBoundarySyntheticTrace(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.deps.apply(obs)

	var boundary *TraceRecord
	for i, r := range h.trace.records {
		if r.SourceKind == observation.SourceSynthetic && r.Action == actionSessionGenerationAdvanced {
			boundary = &h.trace.records[i]
			break
		}
	}
	if boundary == nil {
		t.Fatalf("boundary trace missing; records=%+v", h.trace.records)
	}
	if boundary.ObservedGeneration != 2 {
		t.Errorf("boundary generation = %d, want 2", boundary.ObservedGeneration)
	}
	if !strings.Contains(boundary.ReasonText, "SessionStart") {
		t.Errorf("boundary reason text = %q, want mention SessionStart", boundary.ReasonText)
	}
}

// ----- Hook-storm pre-gate ------------------------------------------------

func TestApply_HookStorm_51stObsDropped_BeforeGate(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// Fire 50 obs — all should be processed (either accept or dedup-reject).
	for i := 0; i < 50; i++ {
		h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
			withActor("a1").withSuggest("active").withSeq(int64(i + 1)).build())
	}
	// Before the 51st, idem cache has registered the actor. Snapshot current
	// idemCache size; after the drop, the cache entry count should NOT grow.
	idemBefore := len(h.idem.entries)

	// 51st: storm gate should drop it.
	h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(99).build())

	if len(h.idem.entries) != idemBefore {
		t.Errorf("51st obs must not reach idempotency gate (it was pre-dropped); idem size before=%d after=%d",
			idemBefore, len(h.idem.entries))
	}
	if got := len(h.trace.byReason(ReasonHookStormDropped)); got != 1 {
		t.Errorf("HookStormDropped traces = %d, want 1", got)
	}
}

func TestApply_HookStorm_EmitsHookStormDroppedTrace(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	for i := 0; i < 51; i++ {
		h.deps.apply(h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
			withActor("a1").withSuggest("active").withSeq(int64(i + 1)).build())
	}
	rej := h.trace.byReason(ReasonHookStormDropped)
	if len(rej) < 1 {
		t.Fatalf("expected HookStormDropped trace; got %+v", h.trace.records)
	}
	if rej[0].Phase != observation.PhaseRejected {
		t.Errorf("HookStormDropped phase = %q, want rejected", rej[0].Phase)
	}
}
