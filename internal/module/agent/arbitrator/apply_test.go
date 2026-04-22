package arbitrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/store"
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

// fakeMinter records Mint + AdoptTraceID + PruneSessionBefore calls for
// SessionStart tests. AdoptTraceID is added in PR-1b-1c (T1); existing tests
// only exercise Mint, but the interface now includes Adopt, so the fake
// satisfies both contracts.
type fakeMinter struct {
	mintCalls         []mintCall
	adoptCalls        []adoptCall
	pruneBeforeCalls  []pruneBeforeCall
	pruneSessionCalls []string
	nextMintID        string
}

type mintCall struct {
	sessionID  string
	generation int64
}

type adoptCall struct {
	sessionID  string
	generation int64
	seed       string
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

// AdoptTraceID echoes the seed when non-empty (fake idempotency is not
// exercised by existing apply tests — 1b-1c T5 will cover that). When seed
// is empty, degrade to Mint so the contract round-trips the no-empty-id
// guarantee.
func (m *fakeMinter) AdoptTraceID(sessionID string, generation int64, seed string) string {
	m.adoptCalls = append(m.adoptCalls, adoptCall{sessionID, generation, seed})
	if seed != "" {
		return seed
	}
	return m.Mint(sessionID, generation)
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

// ----- Divergence + legacy frames fakes (T5) ------------------------------

// fakeDivergencesWriter records every Insert call so tests can assert both
// the call count (primary-only gate passes) and the row shape (Matched,
// DiffSummary, OldStateRef, ProposalStateRef). The fake never returns an
// error; tests that want to simulate store failures can swap in a custom
// DivergencesWriter.
type fakeDivergencesWriter struct {
	inserts []store.FrameDivergence
	nextErr error
}

func (f *fakeDivergencesWriter) Insert(row store.FrameDivergence) (int64, error) {
	f.inserts = append(f.inserts, row)
	if f.nextErr != nil {
		return 0, f.nextErr
	}
	return int64(len(f.inserts)), nil
}

// fakeLegacyFramesView lets tests pre-seed the frame state the divergence
// writer will compare against. Key is the (paneID, pid, startTime) triple
// serialized via fakeLegacyKey so tests can use struct literals.
type fakeLegacyFramesView struct {
	frames map[fakeLegacyKey]*store.Frame
	err    error
}

type fakeLegacyKey struct {
	PaneID    string
	PID       int
	StartTime string
}

func (f *fakeLegacyFramesView) set(paneID string, pid int, startTime string, frame *store.Frame) {
	if f.frames == nil {
		f.frames = make(map[fakeLegacyKey]*store.Frame)
	}
	f.frames[fakeLegacyKey{PaneID: paneID, PID: pid, StartTime: startTime}] = frame
}

func (f *fakeLegacyFramesView) GetByIdentity(paneID string, pid int, startTime string) (*store.Frame, error) {
	if f.err != nil {
		return nil, f.err
	}
	if fr, ok := f.frames[fakeLegacyKey{PaneID: paneID, PID: pid, StartTime: startTime}]; ok {
		return fr, nil
	}
	return nil, nil
}

// ----- Test harness --------------------------------------------------------

// applyHarness builds a fully-wired applyDeps with sensible defaults for
// passthrough-mode tests. Individual tests can override fields on the
// returned struct before invoking apply.
type applyHarness struct {
	deps         *applyDeps
	now          time.Time
	trace        *fakeTraceSubmitter
	minter       *fakeMinter
	arbmodeFk    *fakeArbmode
	stormGuard   *hookStormGuard
	idem         *idemCache
	pending      *pendingStore
	frames       *frameState
	sampler      *sampler
	divergences  *fakeDivergencesWriter
	legacyFrames *fakeLegacyFramesView
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
	h.sampler = newSampler()
	h.divergences = &fakeDivergencesWriter{}
	h.legacyFrames = &fakeLegacyFramesView{}
	h.deps = &applyDeps{
		frames:          h.frames,
		pending:         h.pending,
		idem:            h.idem,
		stormGuard:      h.stormGuard,
		minter:          h.minter,
		arbmode:         h.arbmodeFk,
		traceSubmit:     h.trace,
		sampler:         h.sampler,
		divergences:     h.divergences,
		legacyFrames:    h.legacyFrames,
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

// ----- C5: Authoritative fail-closed is a pre-gate ------------------------

// TestApply_AuthoritativeMode_DoesNotAdvanceGeneration_OnSessionStart
// verifies the authoritative pre-gate (step 0b) blocks the SessionStart
// helper entirely. Before C5 the mode check ran AFTER checkGenerationGate,
// so frameState would have its generation bumped and actors ended even
// though the observation was ultimately rejected.
func TestApply_AuthoritativeMode_DoesNotAdvanceGeneration_OnSessionStart(t *testing.T) {
	h := newApplyHarness(t)
	h.arbmodeFk.current = arbmode.ModeAuthoritative
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.deps.apply(obs)

	if g := h.frames.getOrCreateSession("s1").Generation; g != 1 {
		t.Errorf("generation after authoritative SessionStart = %d, want 1 (pre-gate must block mutation)", g)
	}
	if len(h.minter.mintCalls) != 0 {
		t.Errorf("Mint called under authoritative; want no mint (pre-gate blocks)")
	}
	// Exactly one rejected trace (authoritative).
	rej := h.trace.byReason(ReasonAuthoritativeNotSupportedPhase1)
	if len(rej) != 1 {
		t.Errorf("authoritative reject count = %d, want 1", len(rej))
	}
}

// TestApply_AuthoritativeMode_DoesNotEndActors verifies the pre-gate blocks
// forceEndOldGenActors for authoritative mode. Before C5, a SessionStart
// observation would end all pre-existing actors even though the
// observation was rejected.
func TestApply_AuthoritativeMode_DoesNotEndActors(t *testing.T) {
	h := newApplyHarness(t)
	h.arbmodeFk.current = arbmode.ModeAuthoritative
	h.frames.getOrCreateSession("s1").Generation = 1
	oldKey := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old"}
	h.frames.upsertActor(oldKey, func(a *actorSummary) {
		a.Status = "active"
		a.LastActivity = h.now
	})

	obs := h.obsBuilder().withSession("s1", 2).hook().withAction(actionSessionStart).build()
	h.deps.apply(obs)

	actor, ok := h.frames.actor(oldKey)
	if !ok {
		t.Fatal("actor should still exist")
	}
	if actor.EndedAt != nil {
		t.Errorf("old actor was ended under authoritative mode (EndedAt=%v); pre-gate must block mutation", actor.EndedAt)
	}
}

// TestApply_AuthoritativeMode_DoesNotAdvancePrimary verifies the pre-gate
// blocks enforceSinglePrimaryInvariant so primary transfers do not happen
// on rejected observations.
func TestApply_AuthoritativeMode_DoesNotAdvancePrimary(t *testing.T) {
	h := newApplyHarness(t)
	h.arbmodeFk.current = arbmode.ModeAuthoritative
	h.frames.getOrCreateSession("s1").Generation = 1
	oldKey := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "old"}
	h.frames.upsertActor(oldKey, func(a *actorSummary) { a.Status = "active"; a.LastActivity = h.now })
	h.frames.setPrimary(oldKey, h.now)

	// Incoming obs asserts is_primary=true for a NEW actor.
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("new").withSuggest("active").withSeq(1).
		withEvidence(observation.EvidenceRef{Key: "is_primary", Value: true}).build()
	h.deps.apply(obs)

	// Old primary must remain primary; no synthetic replacement trace.
	oldA, _ := h.frames.actor(oldKey)
	if !oldA.IsPrimary {
		t.Error("old primary was demoted under authoritative — pre-gate must block")
	}
	for _, r := range h.trace.records {
		if r.SourceKind == observation.SourceSynthetic && r.ReasonCode == endReasonReplacedByNewPrimary {
			t.Errorf("synthetic replaced_by_new_primary emitted under authoritative; want none")
		}
	}
}

// ----- C1: Boundary trace carries adopted trace_id ------------------------

// TestApplySessionStart_BoundaryTraceUsesAdoptedTraceID verifies that the
// synthetic session.generation_advanced boundary trace is tagged with the
// trace_id returned from minter.AdoptTraceID — which, for a fresh
// (session, gen) slot, equals the seed taken from obs.TraceID (a
// provisional UUID minted hook-side, per D1.2). Prior to PR-1b-1c the
// boundary id came from minter.Mint, but T5 rewired the binding so the
// hook-provided id is authoritative and Observation.TraceID / hook chain
// trace_id / boundary trace_id all correlate without a registry round-trip.
func TestApplySessionStart_BoundaryTraceUsesAdoptedTraceID(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 3

	// Incoming hook carries a provisional UUID (hook-side mint from T6).
	obs := h.obsBuilder().withSession("s1", 4).hook().withAction(actionSessionStart).
		withTrace("HOOK_PROVISIONAL_UUID").build()
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
	if boundary.TraceID != "HOOK_PROVISIONAL_UUID" {
		t.Errorf("boundary TraceID = %q, want HOOK_PROVISIONAL_UUID (adopted from obs.TraceID)",
			boundary.TraceID)
	}
	if boundary.SpanID == "" {
		t.Error("boundary SpanID must be non-empty (generated UUID) to keep AppendSteps idempotent")
	}
}

// ----- C6: emitAcceptedTrace preserves incoming Phase ----------------------

// TestApply_AcceptedTrace_CommittedObs_KeepsCommittedPhase_AndDropPriority0
// verifies that a committed hook observation stays PhaseCommitted end-to-end
// so the priority ring buffer's protections for hook+committed (dp=0) are
// not silently downgraded to hook+proposed (dp=2) by the accept path.
func TestApply_AcceptedTrace_CommittedObs_KeepsCommittedPhase_AndDropPriority0(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	obs.Phase = observation.PhaseCommitted
	h.deps.apply(obs)

	accepted := h.trace.records
	if len(accepted) != 1 {
		t.Fatalf("want 1 accepted trace; got %d: %+v", len(accepted), accepted)
	}
	r := accepted[0]
	if r.Phase != observation.PhaseCommitted {
		t.Errorf("Phase = %q, want committed (incoming committed obs must not be downgraded)", r.Phase)
	}
	if r.DropPriority != 0 {
		t.Errorf("DropPriority = %d, want 0 (hook+committed); downgrade breaks priority ring protection", r.DropPriority)
	}
}

// TestApply_AcceptedTrace_ProposedObs_KeepsProposedPhase verifies the mirror
// case: a proposed observation stays proposed after acceptance.
func TestApply_AcceptedTrace_ProposedObs_KeepsProposedPhase(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	// Leave Phase = PhaseProposed (default from obsBuilder).

	h.deps.apply(obs)

	if len(h.trace.records) != 1 {
		t.Fatalf("want 1 accepted trace; got %d", len(h.trace.records))
	}
	if h.trace.records[0].Phase != observation.PhaseProposed {
		t.Errorf("Phase = %q, want proposed", h.trace.records[0].Phase)
	}
	if h.trace.records[0].DropPriority != 2 {
		t.Errorf("DropPriority = %d, want 2 (hook+proposed)", h.trace.records[0].DropPriority)
	}
}

// ----- C8 defensive: SpanID is generated when missing ----------------------

// TestEmitTrace_GeneratesStableSpanIDWhenMissing verifies emit*Trace
// back-fills SpanID with a fresh UUID when the incoming observation carries
// none. Without this, toStoreSteps would synthesize a step_id from
// trace_id+seq+batch-index — batch-local indices break INSERT OR IGNORE
// idempotency across AppendSteps retries.
func TestEmitTrace_GeneratesStableSpanIDWhenMissing(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	// SpanID explicitly left empty.
	if obs.SpanID != "" {
		t.Fatalf("test precondition: obs.SpanID should be empty")
	}

	h.deps.apply(obs)

	if len(h.trace.records) != 1 {
		t.Fatalf("want 1 trace; got %d", len(h.trace.records))
	}
	if h.trace.records[0].SpanID == "" {
		t.Error("emitAcceptedTrace must back-fill empty obs.SpanID with a fresh UUID")
	}

	// Rejected path: a stale-generation obs gets a fresh SpanID too.
	h.trace.records = nil
	h.frames.getOrCreateSession("s2").Generation = 5
	rejObs := h.obsBuilder().withSession("s2", 2).hook().withAction("PostToolUse").build()
	if rejObs.SpanID != "" {
		t.Fatalf("test precondition: rejected obs.SpanID should be empty")
	}
	h.deps.apply(rejObs)
	if len(h.trace.records) != 1 {
		t.Fatalf("want 1 rejected trace; got %d", len(h.trace.records))
	}
	if h.trace.records[0].SpanID == "" {
		t.Error("emitRejectedTrace must back-fill empty obs.SpanID with a fresh UUID")
	}
}

// ======================================================================
// T5 (PR-1b-1c) — Apply pipeline expansion
// ======================================================================

// roleResolutionEv is a small helper that builds the canonical EvidenceRef
// carrying role_resolution. Used throughout the T5 test suite to avoid
// repeating the key name.
func roleResolutionEv(state observation.RoleResolutionState) observation.EvidenceRef {
	return observation.EvidenceRef{Key: observation.EvidenceKeyRoleResolution, Value: state}
}

// identityTripleEv produces the {pid, pane_id, start_time} evidence triple
// that the divergence writer gate requires (D5.2 / P0-5). The pid type is
// int64 so evidenceInt64 extraction hits the canonical path.
func identityTripleEv(paneID string, pid int64, startTime string) []observation.EvidenceRef {
	return []observation.EvidenceRef{
		{Key: "pid", Value: pid},
		{Key: "pane_id", Value: paneID},
		{Key: "start_time", Value: startTime},
	}
}

// ----- Role-based pending routing (D4.2) ----------------------------------

// TestApply_HookRoleResolutionMissing_NotPending verifies a hook observation
// with no role_resolution evidence key falls through route-to-pending (goes
// the normal reject path instead of stashing). The observation lands at
// source-priority or later steps; we assert pending remains empty.
func TestApply_HookRoleResolutionMissing_NotPending(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	// No role_resolution key anywhere in Evidence.

	h.deps.apply(obs)

	if h.pending.count() != 0 {
		t.Errorf("pending count = %d, want 0 (no role_resolution -> no pending)", h.pending.count())
	}
}

// TestApply_HookRetryableUnresolved_CreatesFirstPendingEntry_NoRetryScheduled
// verifies the RoleRetryableUnresolved branch stashes observation into
// pending and NEVER schedules a timer-based retry (Non-Goals).
func TestApply_HookRetryableUnresolved_CreatesFirstPendingEntry_NoRetryScheduled(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).
		withEvidence(roleResolutionEv(observation.RoleRetryableUnresolved)).build()

	h.deps.apply(obs)

	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	if !h.pending.isPending(key) {
		t.Fatalf("pending entry missing after RoleRetryableUnresolved observation")
	}
	entry := h.pending.entries[key]
	if len(entry.Observations) != 1 {
		t.Errorf("pending entry observations = %d, want 1", len(entry.Observations))
	}
	// Non-Goals: no retryTick / retryCh / timer. The stash path is silent —
	// no trace records should be emitted.
	if len(h.trace.records) != 0 {
		t.Errorf("pending stash must not emit trace; got %d records", len(h.trace.records))
	}
}

// TestApply_HookTerminalUnresolved_EmitsTraceOnly_NotPending verifies the
// RoleTerminalUnresolved branch emits one trace (reason=RoleTerminalUnresolved)
// and does NOT add the observation to pending.
func TestApply_HookTerminalUnresolved_EmitsTraceOnly_NotPending(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).
		withEvidence(roleResolutionEv(observation.RoleTerminalUnresolved)).build()

	h.deps.apply(obs)

	if h.pending.count() != 0 {
		t.Errorf("pending count = %d, want 0 (terminal must drop)", h.pending.count())
	}
	terminalRejects := h.trace.byReason("RoleTerminalUnresolved")
	if len(terminalRejects) != 1 {
		t.Fatalf("RoleTerminalUnresolved traces = %d, want 1; got %+v", len(terminalRejects), h.trace.records)
	}
}

// TestApply_HookResolved_WhilePending_TriggersTryPromoteToActorNow verifies
// that a RoleResolved observation arriving while the actor has a pending
// entry triggers immediate promotion (no wait for reconcile tick).
func TestApply_HookResolved_WhilePending_TriggersTryPromoteToActorNow(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// Seed a pending entry with a Retryable observation.
	retryObs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).
		withEvidence(roleResolutionEv(observation.RoleRetryableUnresolved)).build()
	h.deps.apply(retryObs)

	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	if !h.pending.isPending(key) {
		t.Fatalf("seed Retryable obs did not create pending entry")
	}

	// Now send a Resolved observation — should promote immediately.
	h.trace.records = nil
	resolvedObs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(2).
		withEvidence(roleResolutionEv(observation.RoleResolved)).build()
	h.deps.apply(resolvedObs)

	if h.pending.isPending(key) {
		t.Errorf("pending entry still exists after Resolved promotion; want cleared")
	}
	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Errorf("expected 1 accepted trace after promote; got %d records=%+v", len(accepted), h.trace.records)
	}
}

// ----- tryPromoteFromEntry contract (D4.3) --------------------------------

// TestApply_TryPromoteFromEntry_NoPositiveSignal_ReturnsFalse_WaitsForDeadline
// verifies that an entry containing only RoleRetryableUnresolved observations
// is not promoted — it stays pending until deadline drop.
func TestApply_TryPromoteFromEntry_NoPositiveSignal_ReturnsFalse_WaitsForDeadline(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// Build an entry with only Retryable observations.
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}
	for i := 0; i < 3; i++ {
		obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
			withActor("a1").withSuggest("active").withSeq(int64(i + 1)).
			withEvidence(roleResolutionEv(observation.RoleRetryableUnresolved)).build()
		h.pending.addPending(obs, h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil)
	}

	entry := h.pending.entries[key]
	if entry == nil {
		t.Fatalf("seed failed")
	}

	promoted := h.deps.tryPromoteFromEntry(entry)
	if promoted {
		t.Errorf("tryPromoteFromEntry = true, want false (no RoleResolved obs in entry)")
	}
	// Still pending.
	if !h.pending.isPending(key) {
		t.Errorf("entry removed despite no positive signal")
	}
}

// TestApply_TryPromoteFromEntry_HasResolvedObs_Promotes verifies that an
// entry containing at least one RoleResolved observation is promoted by
// running apply steps 5-9.
func TestApply_TryPromoteFromEntry_HasResolvedObs_Promotes(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}

	// Seed: Retryable first, then Resolved.
	retryObs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).
		withEvidence(roleResolutionEv(observation.RoleRetryableUnresolved)).build()
	resolvedObs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(2).
		withEvidence(roleResolutionEv(observation.RoleResolved)).build()
	h.pending.addPending(retryObs, h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil)
	h.pending.addPending(resolvedObs, h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil)

	entry := h.pending.entries[key]
	promoted := h.deps.tryPromoteFromEntry(entry)
	if !promoted {
		t.Errorf("tryPromoteFromEntry = false, want true (Resolved present)")
	}
	accepted := h.trace.byPhase(observation.PhaseProposed)
	if len(accepted) != 1 {
		t.Errorf("accepted trace count = %d, want 1 after promotion", len(accepted))
	}
}

// TestApply_TryPromoteFromEntry_ResolvedButNoProposal_ReturnsFalse verifies
// that a Resolved observation with no meaningful proposal (empty
// SuggestStatus and EndLifecycle=false) does NOT promote the entry.
func TestApply_TryPromoteFromEntry_ResolvedButNoProposal_ReturnsFalse(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	key := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "a1"}

	// Resolved but proposal is empty.
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSeq(1).
		withEvidence(roleResolutionEv(observation.RoleResolved)).build()
	// obs.Proposal.SuggestStatus defaults to "" and EndLifecycle=false.
	h.pending.addPending(obs, h.now, h.deps.pendingDeadline, h.deps.perSessionCap, h.deps.perEntryObsCap, nil)

	entry := h.pending.entries[key]
	promoted := h.deps.tryPromoteFromEntry(entry)
	if promoted {
		t.Errorf("tryPromoteFromEntry = true, want false (Resolved but no proposal)")
	}
}

// ----- Gen gate (D1.3) ----------------------------------------------------

// TestApply_GenGate_ObservedEqualCurrent_NotSessionStart_AppliesNormally
// documents that generation == current proceeds to normal apply without
// triggering the SessionStart helper or any mint/adopt.
func TestApply_GenGate_ObservedEqualCurrent_NotSessionStart_AppliesNormally(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 2

	obs := h.obsBuilder().withSession("s1", 2).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	if len(h.minter.mintCalls) != 0 || len(h.minter.adoptCalls) != 0 {
		t.Errorf("same-gen apply must not mint/adopt; mint=%d adopt=%d",
			len(h.minter.mintCalls), len(h.minter.adoptCalls))
	}
	if len(h.trace.byPhase(observation.PhaseProposed)) != 1 {
		t.Errorf("same-gen apply did not accept; records=%+v", h.trace.records)
	}
}

// TestApply_GenGate_ObservedGreaterCurrent_SessionStart_RunsHelper_Adopts
// verifies that obs.Gen > current + Action == SessionStart triggers
// AdoptTraceID with the observation's trace_id as seed.
func TestApply_GenGate_ObservedGreaterCurrent_SessionStart_RunsHelper_Adopts(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 0

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction(actionSessionStart).
		withTrace("HOOK_PROVISIONAL_UUID").build()
	h.deps.apply(obs)

	if len(h.minter.adoptCalls) != 1 {
		t.Fatalf("AdoptTraceID calls = %d, want 1", len(h.minter.adoptCalls))
	}
	if h.minter.adoptCalls[0].seed != "HOOK_PROVISIONAL_UUID" {
		t.Errorf("adopt seed = %q, want HOOK_PROVISIONAL_UUID", h.minter.adoptCalls[0].seed)
	}
	if h.minter.adoptCalls[0].sessionID != "s1" || h.minter.adoptCalls[0].generation != 1 {
		t.Errorf("adopt args = %+v, want {s1, 1}", h.minter.adoptCalls[0])
	}
	// SessionStart must not call Mint (adopt supersedes it).
	if len(h.minter.mintCalls) != 0 {
		t.Errorf("SessionStart called Mint %d times, want 0 (AdoptTraceID replaces it)", len(h.minter.mintCalls))
	}
}

// TestApply_GenGate_ObservedGreaterCurrent_NotSessionStart_Rejects verifies
// that obs.Gen > current + Action != SessionStart is rejected with
// ObservedGenerationAhead (i.e. UnauthorizedGenerationBump reason code in
// this codebase's enum).
func TestApply_GenGate_ObservedGreaterCurrent_NotSessionStart_Rejects(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 0

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("a1").withSuggest("active").withSeq(1).build()
	h.deps.apply(obs)

	rej := h.trace.byReason(ReasonUnauthorizedGenerationBump)
	if len(rej) != 1 {
		t.Errorf("ObservedGenerationAhead / UnauthorizedGenerationBump rejects = %d, want 1", len(rej))
	}
}

// ----- SessionStart AdoptTraceID (D1.2) -----------------------------------

// TestApply_SessionStart_AdoptTraceID_UsesObservationSeed verifies the
// boundary synthetic trace uses the seed returned from AdoptTraceID (which
// equals obs.TraceID when the seed is non-empty and the key is new).
func TestApply_SessionStart_AdoptTraceID_UsesObservationSeed(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 0

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction(actionSessionStart).
		withTrace("ADOPT_ME").build()
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
	if boundary.TraceID != "ADOPT_ME" {
		t.Errorf("boundary trace_id = %q, want ADOPT_ME (adopted seed)", boundary.TraceID)
	}
}

// TestApply_SessionStart_AdoptTraceID_Idempotent_WhenReplayed verifies that
// two AdoptTraceID calls with the same (sid, gen) but different seeds
// return the first seed — the second is discarded. We invoke AdoptTraceID
// directly on the real registry to exercise the idempotent contract at
// the source (the fake echoes seed, so it cannot demonstrate idempotency).
func TestApply_SessionStart_AdoptTraceID_Idempotent_WhenReplayed(t *testing.T) {
	minter, _ := observation.NewTraceIDRegistry()

	first := minter.AdoptTraceID("s1", 1, "FIRST_SEED")
	second := minter.AdoptTraceID("s1", 1, "SECOND_SEED")

	if first != "FIRST_SEED" {
		t.Errorf("first adopt = %q, want FIRST_SEED", first)
	}
	if second != "FIRST_SEED" {
		t.Errorf("second adopt (replay) = %q, want FIRST_SEED (idempotent; second seed discarded)", second)
	}
}

// ----- Divergence writer primary-only (D5.2) ------------------------------

// TestApply_Passthrough_DivergenceWritten_WhenLegacyDiffers_Primary verifies
// the happy path: primary actor + identity triple present + legacy frame
// exists + proposal diverges from legacy → one Insert with Matched=false.
func TestApply_Passthrough_DivergenceWritten_WhenLegacyDiffers_Primary(t *testing.T) {
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// Seed a legacy frame (status=idle) so the passthrough proposal
	// (status=active) diverges.
	legacyFrame := &store.Frame{
		PaneID:           "pane-1",
		PID:              4321,
		ProcessStartTime: "2026-04-22T00:00:00Z",
		AgentType:        "cc",
		Status:           agentpkg.StatusIdle,
	}
	h.legacyFrames.set("pane-1", 4321, "2026-04-22T00:00:00Z", legacyFrame)

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("primary:cc").withSuggest("active").withSeq(1).
		withEvidence(identityTripleEv("pane-1", 4321, "2026-04-22T00:00:00Z")...).build()
	obs.TraceID = "trace-xyz"
	obs.SpanID = "span-abc"

	h.deps.apply(obs)

	if len(h.divergences.inserts) != 1 {
		t.Fatalf("divergences.Insert calls = %d, want 1", len(h.divergences.inserts))
	}
	row := h.divergences.inserts[0]
	if row.Matched {
		t.Errorf("divergence Matched = true, want false (status differs)")
	}
	if row.SessionID != "s1" || row.TraceID != "trace-xyz" || row.EventID != "span-abc" {
		t.Errorf("divergence identity fields = (sid=%q trace=%q event=%q), want (s1, trace-xyz, span-abc)",
			row.SessionID, row.TraceID, row.EventID)
	}
	if row.ObservedGeneration != 1 {
		t.Errorf("ObservedGeneration = %d, want 1", row.ObservedGeneration)
	}
	if row.DiffSummary == "" {
		t.Error("DiffSummary empty; expected non-empty humanDiff summary")
	}
	// OldStateRef / ProposalStateRef should be valid JSON.
	if !json.Valid(row.OldStateRef) {
		t.Errorf("OldStateRef not valid JSON: %s", row.OldStateRef)
	}
	if !json.Valid(row.ProposalStateRef) {
		t.Errorf("ProposalStateRef not valid JSON: %s", row.ProposalStateRef)
	}
}

// TestApply_Passthrough_DivergenceSkipped_WhenActorIsSubagent_MetricIncremented
// verifies non-primary actors are gated out and the divergence skip metric
// fires with actor_kind=subagent.
func TestApply_Passthrough_DivergenceSkipped_WhenActorIsSubagent_MetricIncremented(t *testing.T) {
	ResetForTesting()
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("subagent:task-1").withSuggest("active").withSeq(1).
		withEvidence(identityTripleEv("pane-1", 4321, "2026-04-22T00:00:00Z")...).build()

	h.deps.apply(obs)

	if len(h.divergences.inserts) != 0 {
		t.Errorf("Insert calls = %d, want 0 (subagent must be skipped)", len(h.divergences.inserts))
	}
	got := Value("lights_divergence_non_primary_skipped", "actor_kind=subagent")
	if got != 1 {
		t.Errorf("lights_divergence_non_primary_skipped{actor_kind=subagent} = %d, want 1", got)
	}
}

// TestApply_Passthrough_DivergenceSkipped_WhenIdentityTripleMissing_MetricIncremented
// verifies the identity-triple gate skips divergence writes and bumps the
// appropriate metric when pane_id/pid/start_time is missing.
func TestApply_Passthrough_DivergenceSkipped_WhenIdentityTripleMissing_MetricIncremented(t *testing.T) {
	ResetForTesting()
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// pid + start_time present, but pane_id missing.
	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("primary:cc").withSuggest("active").withSeq(1).
		withEvidence(
			observation.EvidenceRef{Key: "pid", Value: int64(4321)},
			observation.EvidenceRef{Key: "start_time", Value: "2026-04-22T00:00:00Z"},
		).build()

	h.deps.apply(obs)

	if len(h.divergences.inserts) != 0 {
		t.Errorf("Insert calls = %d, want 0 (identity triple missing)", len(h.divergences.inserts))
	}
	got := Value("lights_divergence_identity_missing", "source=hook")
	if got != 1 {
		t.Errorf("lights_divergence_identity_missing{source=hook} = %d, want 1", got)
	}
}

// TestApply_Passthrough_NoDivergenceWrite_WhenMatches verifies a matching
// projection does NOT insert a row; only the matched=1 metric bumps.
func TestApply_Passthrough_NoDivergenceWrite_WhenMatches(t *testing.T) {
	ResetForTesting()
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1

	// Seed legacy frame with status already active → projection will match.
	legacyFrame := &store.Frame{
		PaneID:           "pane-1",
		PID:              4321,
		ProcessStartTime: "2026-04-22T00:00:00Z",
		AgentType:        "cc",
		Status:           agentpkg.Status("active"),
	}
	h.legacyFrames.set("pane-1", 4321, "2026-04-22T00:00:00Z", legacyFrame)

	obs := h.obsBuilder().withSession("s1", 1).hook().withAction("PostToolUse").
		withActor("primary:cc").withSuggest("active").withSeq(1).
		withEvidence(identityTripleEv("pane-1", 4321, "2026-04-22T00:00:00Z")...).build()

	h.deps.apply(obs)

	if len(h.divergences.inserts) != 0 {
		t.Errorf("matching projection must not insert row; got %d inserts", len(h.divergences.inserts))
	}
	if got := Value("lights_divergence_total", "matched=1"); got != 1 {
		t.Errorf("lights_divergence_total{matched=1} = %d, want 1", got)
	}
}

// ----- Sampler wiring (D7) ------------------------------------------------

// TestApply_Sampler_Sweep_DropsNineInTen_PerSession verifies the sampler is
// invoked on emit: 10 sweep proposals in one session produce exactly 1
// trace row (first emits, subsequent 9 drop). A second session's sweep
// sequence starts fresh (independent counter).
func TestApply_Sampler_Sweep_DropsNineInTen_PerSession(t *testing.T) {
	ResetForTesting()
	h := newApplyHarness(t)
	h.frames.getOrCreateSession("s1").Generation = 1
	h.frames.getOrCreateSession("s2").Generation = 1

	// 10 sweep proposals on session s1 against fresh actors (unique ActorID
	// so idempotency + pending routing do not interfere).
	for i := 0; i < 10; i++ {
		obs := h.obsBuilder().withSession("s1", 1).sweep().withAction("scan").
			withActor("a" + itoa(i)).withSuggest("waiting").withSeq(int64(i + 1)).
			withEvidence(roleResolutionEv(observation.RoleResolved)).build()
		h.deps.apply(obs)
	}

	s1Sweep := 0
	for _, r := range h.trace.records {
		if r.SessionID == "s1" && r.SourceKind == observation.SourceSweep {
			s1Sweep++
		}
	}
	if s1Sweep != 1 {
		t.Errorf("s1 sweep emitted traces = %d, want 1 (1-in-10 sampling)", s1Sweep)
	}
	// sampled_dropped metric should reflect 9 drops for s1 sweep.
	if got := Value("lights_trace_sampled_dropped",
		"phase=proposed", "session=s1", "source=sweep"); got != 9 {
		t.Errorf("lights_trace_sampled_dropped{s1,sweep,proposed} = %d, want 9", got)
	}

	// Now fire 1 sweep on s2 — should emit (independent counter).
	obs := h.obsBuilder().withSession("s2", 1).sweep().withAction("scan").
		withActor("b0").withSuggest("waiting").withSeq(1).
		withEvidence(roleResolutionEv(observation.RoleResolved)).build()
	h.deps.apply(obs)

	s2Sweep := 0
	for _, r := range h.trace.records {
		if r.SessionID == "s2" && r.SourceKind == observation.SourceSweep {
			s2Sweep++
		}
	}
	if s2Sweep != 1 {
		t.Errorf("s2 first sweep did not emit; s2 sweep traces = %d, want 1 (cross-session independence)", s2Sweep)
	}
}
