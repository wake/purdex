package observation_test

import (
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// captureLog redirects the default logger to a buffer for the duration of t.
// It restores the original writer via t.Cleanup.
func captureLog(t *testing.T) *strings.Builder {
	t.Helper()
	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

// minimalBuilder returns a Builder with all required fields set to valid
// non-zero values for use as a base in tests that only care about one field.
func minimalBuilder() *observation.Builder {
	return observation.NewBuilder().
		TraceID("trace-1").
		SessionID("sess-1").
		SourceKind(observation.SourceHook).
		Action("window-changed").
		Phase(observation.PhaseProposed).
		ObservedAt(time.Now())
}

// ── 1: Minimal happy path ────────────────────────────────────────────────────

func TestBuilder_Build_Minimal(t *testing.T) {
	now := time.Now()
	obs, err := observation.NewBuilder().
		TraceID("trace-abc").
		SessionID("sess-xyz").
		SourceKind(observation.SourceHook).
		Action("pane-created").
		Phase(observation.PhaseProposed).
		ObservedAt(now).
		Build()

	if err != nil {
		t.Fatalf("Build() error = %v; want nil", err)
	}
	if obs.TraceID != "trace-abc" {
		t.Errorf("TraceID = %q; want %q", obs.TraceID, "trace-abc")
	}
	if obs.SessionID != "sess-xyz" {
		t.Errorf("SessionID = %q; want %q", obs.SessionID, "sess-xyz")
	}
	if obs.SourceKind != observation.SourceHook {
		t.Errorf("SourceKind = %q; want %q", obs.SourceKind, observation.SourceHook)
	}
	if obs.Action != "pane-created" {
		t.Errorf("Action = %q; want %q", obs.Action, "pane-created")
	}
	if obs.Phase != observation.PhaseProposed {
		t.Errorf("Phase = %q; want %q", obs.Phase, observation.PhaseProposed)
	}
	if !obs.ObservedAt.Equal(now) {
		t.Errorf("ObservedAt = %v; want %v", obs.ObservedAt, now)
	}
	if len(obs.DecisionPorts) != 0 {
		t.Errorf("DecisionPorts len = %d; want 0", len(obs.DecisionPorts))
	}
}

// ── 2: Missing TraceID ───────────────────────────────────────────────────────

func TestBuilder_MissingTraceID_Error(t *testing.T) {
	_, err := minimalBuilder().TraceID("").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingRequiredField")
	}
	if !errors.Is(err, observation.ErrMissingRequiredField) {
		t.Errorf("errors.Is(err, ErrMissingRequiredField) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "trace_id") {
		t.Errorf("err.Error() = %q; want to contain %q", err.Error(), "trace_id")
	}
}

// ── 3: Missing SessionID ─────────────────────────────────────────────────────

func TestBuilder_MissingSessionID_Error(t *testing.T) {
	_, err := minimalBuilder().SessionID("").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingRequiredField")
	}
	if !errors.Is(err, observation.ErrMissingRequiredField) {
		t.Errorf("errors.Is(err, ErrMissingRequiredField) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("err.Error() = %q; want to contain %q", err.Error(), "session_id")
	}
}

// ── 4: Missing SourceKind ────────────────────────────────────────────────────

func TestBuilder_MissingSourceKind_Error(t *testing.T) {
	_, err := minimalBuilder().SourceKind("").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingRequiredField")
	}
	if !errors.Is(err, observation.ErrMissingRequiredField) {
		t.Errorf("errors.Is(err, ErrMissingRequiredField) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "source_kind") {
		t.Errorf("err.Error() = %q; want to contain %q", err.Error(), "source_kind")
	}
}

// ── 5: Missing Action ────────────────────────────────────────────────────────

func TestBuilder_MissingAction_Error(t *testing.T) {
	_, err := minimalBuilder().Action("").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingRequiredField")
	}
	if !errors.Is(err, observation.ErrMissingRequiredField) {
		t.Errorf("errors.Is(err, ErrMissingRequiredField) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "action") {
		t.Errorf("err.Error() = %q; want to contain %q", err.Error(), "action")
	}
}

// ── 6: Missing Phase ─────────────────────────────────────────────────────────

func TestBuilder_MissingPhase_Error(t *testing.T) {
	_, err := minimalBuilder().Phase("").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingRequiredField")
	}
	if !errors.Is(err, observation.ErrMissingRequiredField) {
		t.Errorf("errors.Is(err, ErrMissingRequiredField) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "phase") {
		t.Errorf("err.Error() = %q; want to contain %q", err.Error(), "phase")
	}
}

// ── 7: Invalid Phase ─────────────────────────────────────────────────────────

func TestBuilder_InvalidPhase_Error(t *testing.T) {
	_, err := minimalBuilder().Phase("bogus").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrInvalidPhase")
	}
	if !errors.Is(err, observation.ErrInvalidPhase) {
		t.Errorf("errors.Is(err, ErrInvalidPhase) = false; err = %v", err)
	}
}

// ── 8: Zero ObservedAt ───────────────────────────────────────────────────────

func TestBuilder_ZeroObservedAt_Error(t *testing.T) {
	_, err := minimalBuilder().ObservedAt(time.Time{}).Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingRequiredField")
	}
	if !errors.Is(err, observation.ErrMissingRequiredField) {
		t.Errorf("errors.Is(err, ErrMissingRequiredField) = false; err = %v", err)
	}
	if !strings.Contains(err.Error(), "observed_at") {
		t.Errorf("err.Error() = %q; want to contain %q", err.Error(), "observed_at")
	}
}

// ── 8b: No-proposal case (zero Proposal.ActorKey with non-zero SessionID/Generation)
//
// Reconcile-only observations (spec §3.4.5) emit trace without a proposal.
// Zero ActorKey must skip mismatch checks even when SessionID/ObservedGeneration
// on the observation are non-zero. This guards against the comment in builder.go
// becoming a regression over time.

func TestBuilder_NoProposal_OK(t *testing.T) {
	obs, err := observation.NewBuilder().
		TraceID("trace-reconcile").
		SessionID("sess-abc").
		SourceKind(observation.SourceReconcile).
		Action("actor.stale_detected").
		Phase(observation.PhaseProposed).
		ObservedGeneration(5).
		ObservedAt(time.Now()).
		Build()

	if err != nil {
		t.Fatalf("Build() error = %v; want nil (no-proposal path)", err)
	}
	if (obs.Proposal.ActorKey != observation.ActorKey{}) {
		t.Errorf("Proposal.ActorKey = %+v; want zero", obs.Proposal.ActorKey)
	}
}

// ── 9: Invalid SourceKind ────────────────────────────────────────────────────

func TestBuilder_InvalidSourceKind_Error(t *testing.T) {
	_, err := minimalBuilder().SourceKind("bogus").Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrInvalidSourceKind")
	}
	if !errors.Is(err, observation.ErrInvalidSourceKind) {
		t.Errorf("errors.Is(err, ErrInvalidSourceKind) = false; err = %v", err)
	}
}

// ── 10: Probe without WatcherToken ──────────────────────────────────────────

func TestBuilder_ProbeWithoutWatcherToken_Error(t *testing.T) {
	_, err := minimalBuilder().
		SourceKind(observation.SourceProbe).
		WatcherToken("").
		Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrMissingWatcherToken")
	}
	if !errors.Is(err, observation.ErrMissingWatcherToken) {
		t.Errorf("errors.Is(err, ErrMissingWatcherToken) = false; err = %v", err)
	}
}

// ── 11: Hook without WatcherToken is OK ─────────────────────────────────────

func TestBuilder_HookWithoutWatcherToken_OK(t *testing.T) {
	_, err := minimalBuilder().
		SourceKind(observation.SourceHook).
		WatcherToken("").
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v; want nil (hook does not require watcher_token)", err)
	}
}

// ── 12: ActorKey SessionID mismatch ─────────────────────────────────────────

func TestBuilder_ActorKeySessionMismatch_Error(t *testing.T) {
	_, err := minimalBuilder().
		SessionID("sess-1").
		Proposal(observation.StateProposal{
			ActorKey: observation.ActorKey{
				SessionID:  "other-session",
				Generation: 0,
				ActorID:    "primary:main",
			},
		}).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrActorKeySessionMismatch")
	}
	if !errors.Is(err, observation.ErrActorKeySessionMismatch) {
		t.Errorf("errors.Is(err, ErrActorKeySessionMismatch) = false; err = %v", err)
	}
}

// ── 13: ActorKey Generation mismatch ────────────────────────────────────────

func TestBuilder_ActorKeyGenerationMismatch_Error(t *testing.T) {
	_, err := minimalBuilder().
		SessionID("sess-1").
		ObservedGeneration(5).
		Proposal(observation.StateProposal{
			ActorKey: observation.ActorKey{
				SessionID:  "sess-1",
				Generation: 7, // mismatch
				ActorID:    "primary:main",
			},
		}).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil; want ErrActorKeyGenerationMismatch")
	}
	if !errors.Is(err, observation.ErrActorKeyGenerationMismatch) {
		t.Errorf("errors.Is(err, ErrActorKeyGenerationMismatch) = false; err = %v", err)
	}
}

// ── 14: Zero ObservedGeneration with matching ActorKey.Generation = 0 ──────

func TestBuilder_ZeroObservedGeneration_OK(t *testing.T) {
	_, err := minimalBuilder().
		SessionID("sess-1").
		ObservedGeneration(0).
		Proposal(observation.StateProposal{
			ActorKey: observation.ActorKey{
				SessionID:  "sess-1",
				Generation: 0,
				ActorID:    "primary:main",
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v; want nil (zero generation is allowed)", err)
	}
}

// ── 15: Exactly 16 DecisionPorts — OK ───────────────────────────────────────

func TestBuilder_DecisionPorts_Exactly16_OK(t *testing.T) {
	b := minimalBuilder()
	for i := 0; i < 16; i++ {
		b.AddDecisionPort(observation.DecisionPort{PortID: "p"})
	}
	obs, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error = %v; want nil", err)
	}
	if len(obs.DecisionPorts) != 16 {
		t.Errorf("DecisionPorts len = %d; want 16", len(obs.DecisionPorts))
	}
}

// ── 16: 17 DecisionPorts in prod → truncate + log ───────────────────────────

func TestBuilder_DecisionPorts_17_ProdTruncate(t *testing.T) {
	buf := captureLog(t)

	b := minimalBuilder() // prod mode (default)
	for i := 0; i < 17; i++ {
		b.AddDecisionPort(observation.DecisionPort{PortID: "p"})
	}
	obs, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error = %v; want nil (prod truncates, not errors)", err)
	}
	if len(obs.DecisionPorts) != 16 {
		t.Errorf("DecisionPorts len = %d; want 16", len(obs.DecisionPorts))
	}
	logged := buf.String()
	if !strings.Contains(logged, "[agent][observation] decision_ports truncated from 17 to 16") {
		t.Errorf("log output = %q; want to contain truncation message", logged)
	}
}

// ── 17: 17 DecisionPorts in dev mode → panic ────────────────────────────────

func TestBuilder_DecisionPorts_17_DevPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Build() did not panic; want panic in dev mode with 17 DecisionPorts")
		}
	}()

	b := observation.NewBuilder(observation.WithDevMode(true)).
		TraceID("trace-1").
		SessionID("sess-1").
		SourceKind(observation.SourceHook).
		Action("window-changed").
		Phase(observation.PhaseProposed).
		ObservedAt(time.Now())

	for i := 0; i < 17; i++ {
		b.AddDecisionPort(observation.DecisionPort{PortID: "p"})
	}
	b.Build() //nolint:errcheck // expecting panic
}

// ── 18: Empty DecisionPorts — OK ────────────────────────────────────────────

func TestBuilder_DecisionPorts_Empty_OK(t *testing.T) {
	obs, err := minimalBuilder().Build()
	if err != nil {
		t.Fatalf("Build() error = %v; want nil", err)
	}
	if len(obs.DecisionPorts) != 0 {
		t.Errorf("DecisionPorts len = %d; want 0", len(obs.DecisionPorts))
	}
}

// ── 19: Fluent chaining does not nil-deref ──────────────────────────────────

func TestBuilder_Chaining(t *testing.T) {
	now := time.Now()
	obs, err := observation.NewBuilder().
		TraceID("t").
		SpanID("s").
		ParentSpanID("p").
		SessionID("sess").
		ObservedGeneration(3).
		SourceKind(observation.SourceSweep).
		WatcherToken("tok").
		Action("sweep-cycle").
		Phase(observation.PhaseCommitted).
		ReasonCode("rc").
		ReasonText("all good").
		Evidence([]observation.EvidenceRef{{Key: "k", Value: "v"}}).
		ObservedAt(now).
		Seq(42).
		Build()

	if err != nil {
		t.Fatalf("Build() error = %v; want nil", err)
	}
	if obs.SpanID != "s" {
		t.Errorf("SpanID = %q; want %q", obs.SpanID, "s")
	}
	if obs.ParentSpanID != "p" {
		t.Errorf("ParentSpanID = %q; want %q", obs.ParentSpanID, "p")
	}
	if obs.ObservedGeneration != 3 {
		t.Errorf("ObservedGeneration = %d; want 3", obs.ObservedGeneration)
	}
	if obs.WatcherToken != "tok" {
		t.Errorf("WatcherToken = %q; want %q", obs.WatcherToken, "tok")
	}
	if obs.ReasonCode != "rc" {
		t.Errorf("ReasonCode = %q; want %q", obs.ReasonCode, "rc")
	}
	if obs.ReasonText != "all good" {
		t.Errorf("ReasonText = %q; want %q", obs.ReasonText, "all good")
	}
	if obs.Seq != 42 {
		t.Errorf("Seq = %d; want 42", obs.Seq)
	}
	if len(obs.Evidence) != 1 || obs.Evidence[0].Key != "k" {
		t.Errorf("Evidence = %+v; want [{k v}]", obs.Evidence)
	}
}
