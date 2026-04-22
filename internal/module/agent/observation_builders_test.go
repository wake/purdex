package agent

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/store"
)

// --- helpers -----------------------------------------------------------------

func fixedNow() time.Time {
	return time.Date(2026, 4, 22, 12, 0, 0, 123456789, time.UTC)
}

// findEvidence returns the first EvidenceRef with key k or (zero, false).
func findEvidence(ev []observation.EvidenceRef, k string) (observation.EvidenceRef, bool) {
	for _, ref := range ev {
		if ref.Key == k {
			return ref, true
		}
	}
	return observation.EvidenceRef{}, false
}

// evidenceString pulls a string value from the evidence slice.
func evidenceString(t *testing.T, ev []observation.EvidenceRef, k string) string {
	t.Helper()
	ref, ok := findEvidence(ev, k)
	if !ok {
		t.Fatalf("evidence key %q missing; keys=%v", k, evidenceKeys(ev))
	}
	s, ok := ref.Value.(string)
	if !ok {
		t.Fatalf("evidence key %q: want string, got %T (%v)", k, ref.Value, ref.Value)
	}
	return s
}

// evidenceInt64 pulls an int64 value from the evidence slice.
func evidenceInt64(t *testing.T, ev []observation.EvidenceRef, k string) int64 {
	t.Helper()
	ref, ok := findEvidence(ev, k)
	if !ok {
		t.Fatalf("evidence key %q missing; keys=%v", k, evidenceKeys(ev))
	}
	switch v := ref.Value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	default:
		t.Fatalf("evidence key %q: want int/int64, got %T (%v)", k, ref.Value, ref.Value)
		return 0
	}
}

func evidenceKeys(ev []observation.EvidenceRef) []string {
	keys := make([]string, 0, len(ev))
	for _, ref := range ev {
		keys = append(keys, ref.Key)
	}
	return keys
}

func assertIdentityTriple(t *testing.T, ev []observation.EvidenceRef, wantPID int64, wantPane, wantStart string) {
	t.Helper()
	if got := evidenceInt64(t, ev, "pid"); got != wantPID {
		t.Errorf("evidence.pid = %d, want %d", got, wantPID)
	}
	if got := evidenceString(t, ev, "pane_id"); got != wantPane {
		t.Errorf("evidence.pane_id = %q, want %q", got, wantPane)
	}
	if got := evidenceString(t, ev, "start_time"); got != wantStart {
		t.Errorf("evidence.start_time = %q, want %q", got, wantStart)
	}
}

func assertRoleResolution(t *testing.T, ev []observation.EvidenceRef, want observation.RoleResolutionState) {
	t.Helper()
	state, ok := observation.RoleResolutionFromEvidence(ev)
	if !ok {
		t.Fatalf("role_resolution missing or invalid; evidence keys=%v", evidenceKeys(ev))
	}
	if state != want {
		t.Errorf("role_resolution = %q, want %q", state, want)
	}
}

// --- hook builder ------------------------------------------------------------

func TestBuildHookObservation_FullShape_IncludesIdentityTriple_AndRoleResolution(t *testing.T) {
	now := fixedNow()
	obs := buildHookObservation(
		"sess-a", "%1", 4242, "1700000000.00",
		4242, "cc", "PostToolUse",
		3,    // observedGeneration
		true, // accepted
		"",
		now,
	)

	if obs.TraceID == "" {
		t.Fatal("TraceID must not be empty")
	}
	if obs.SourceKind != observation.SourceHook {
		t.Errorf("SourceKind = %q, want %q", obs.SourceKind, observation.SourceHook)
	}
	if obs.Action != "PostToolUse" {
		t.Errorf("Action = %q, want PostToolUse", obs.Action)
	}
	if obs.Phase != observation.PhaseCommitted {
		t.Errorf("Phase = %q, want PhaseCommitted", obs.Phase)
	}
	if obs.SessionID != "sess-a" {
		t.Errorf("SessionID = %q, want sess-a", obs.SessionID)
	}
	if obs.ObservedGeneration != 3 {
		t.Errorf("ObservedGeneration = %d, want 3", obs.ObservedGeneration)
	}
	if obs.Proposal.ActorKey.SessionID != "sess-a" ||
		obs.Proposal.ActorKey.Generation != 3 ||
		obs.Proposal.ActorKey.ActorID != "primary:cc" {
		t.Errorf("ActorKey = %+v, want {sess-a, 3, primary:cc}", obs.Proposal.ActorKey)
	}
	if !obs.ObservedAt.Equal(now) {
		t.Errorf("ObservedAt = %v, want %v", obs.ObservedAt, now)
	}
	if obs.Seq != now.UnixNano() {
		t.Errorf("Seq = %d, want %d", obs.Seq, now.UnixNano())
	}

	assertIdentityTriple(t, obs.Evidence, 4242, "%1", "1700000000.00")
	assertRoleResolution(t, obs.Evidence, observation.RoleResolved)

	if got := evidenceString(t, obs.Evidence, "event_name"); got != "PostToolUse" {
		t.Errorf("evidence.event_name = %q, want PostToolUse", got)
	}
}

func TestBuildHookObservation_ProvisionalTraceID_NonEmpty(t *testing.T) {
	obs := buildHookObservation(
		"sess-b", "%2", 1, "st", 1, "cc", "PreToolUse",
		0, true, "", fixedNow(),
	)
	if obs.TraceID == "" {
		t.Fatal("TraceID empty — builder would be rejected by observation.Builder validation")
	}
	// different calls must yield different provisional UUIDs
	obs2 := buildHookObservation(
		"sess-b", "%2", 1, "st", 1, "cc", "PreToolUse",
		0, true, "", fixedNow(),
	)
	if obs.TraceID == obs2.TraceID {
		t.Errorf("two hook observations share TraceID %q — should be independent UUIDs", obs.TraceID)
	}
}

func TestBuildHookObservation_SessionStart_SuggestStatusActive(t *testing.T) {
	obs := buildHookObservation(
		"sess-c", "%3", 11, "st", 11, "cc", "SessionStart",
		5, true, "", fixedNow(),
	)
	if obs.Proposal.SuggestStatus != "active" {
		t.Errorf("SessionStart SuggestStatus = %q, want active", obs.Proposal.SuggestStatus)
	}
	if obs.Proposal.EndLifecycle {
		t.Errorf("SessionStart EndLifecycle = true, want false")
	}
}

func TestBuildHookObservation_SessionEnd_EndLifecycleTrue(t *testing.T) {
	obs := buildHookObservation(
		"sess-d", "%4", 77, "st", 77, "cc", "SessionEnd",
		2, true, "", fixedNow(),
	)
	if !obs.Proposal.EndLifecycle {
		t.Errorf("SessionEnd EndLifecycle = false, want true")
	}
	if obs.Proposal.EndReason != "session_end" {
		t.Errorf("SessionEnd EndReason = %q, want session_end", obs.Proposal.EndReason)
	}
	if obs.Proposal.SuggestStatus != "ended" {
		t.Errorf("SessionEnd SuggestStatus = %q, want ended", obs.Proposal.SuggestStatus)
	}
}

func TestBuildHookObservation_VerifyFailed_RoleResolutionRetryable(t *testing.T) {
	retryableReasons := []string{"pane_unresolvable", "sender_uncertain", "identify_mismatch"}
	for _, reason := range retryableReasons {
		t.Run(reason, func(t *testing.T) {
			obs := buildHookObservation(
				"sess-e", "%5", 99, "st", 99, "cc", "PostToolUse",
				1, false, reason, fixedNow(),
			)
			if obs.Phase != observation.PhaseProposed {
				t.Errorf("reject Phase = %q, want PhaseProposed", obs.Phase)
			}
			assertRoleResolution(t, obs.Evidence, observation.RoleRetryableUnresolved)
			if got := evidenceString(t, obs.Evidence, "verify_reason"); got != reason {
				t.Errorf("evidence.verify_reason = %q, want %q", got, reason)
			}
		})
	}
}

func TestBuildHookObservation_VerifyFailed_RoleResolutionTerminal_IfTerminalReason(t *testing.T) {
	terminalReasons := []string{"session_not_found", "", "unknown_wacky_reason"}
	for _, reason := range terminalReasons {
		t.Run(reason, func(t *testing.T) {
			obs := buildHookObservation(
				"sess-f", "%6", 13, "st", 13, "cc", "PostToolUse",
				1, false, reason, fixedNow(),
			)
			assertRoleResolution(t, obs.Evidence, observation.RoleTerminalUnresolved)
		})
	}
}

func TestBuildHookObservation_StartTimeUnknown_WhenCallerPassesEmpty(t *testing.T) {
	obs := buildHookObservation(
		"sess-g", "%7", 21, "", // caller couldn't resolve start_time
		21, "cc", "PostToolUse",
		1, true, "", fixedNow(),
	)
	if got := evidenceString(t, obs.Evidence, "start_time"); got != "unknown" {
		t.Errorf("hook empty start_time should map to \"unknown\", got %q", got)
	}
}

func TestBuildHookObservation_SenderPID_EmittedWhenDifferentFromPID(t *testing.T) {
	obs := buildHookObservation(
		"sess-h", "%8", 10, "st",
		200, // senderPID != pid
		"cc", "PostToolUse", 1, true, "", fixedNow(),
	)
	if v := evidenceInt64(t, obs.Evidence, "sender_pid"); v != 200 {
		t.Errorf("sender_pid = %d, want 200", v)
	}

	// when sender_pid == pid, builder may omit (not required); but if present must equal.
	obs2 := buildHookObservation(
		"sess-h", "%8", 10, "st",
		10, // senderPID == pid
		"cc", "PostToolUse", 1, true, "", fixedNow(),
	)
	if ref, ok := findEvidence(obs2.Evidence, "sender_pid"); ok {
		if v, _ := ref.Value.(int64); v != 10 {
			t.Errorf("sender_pid when equal: got %v, want 10", ref.Value)
		}
	}
}

// --- probe builder -----------------------------------------------------------

func TestBuildProbeObservation_OutcomeMapping_DeadToEnded(t *testing.T) {
	now := fixedNow()
	obs := buildProbeObservation(
		"sess-p", "%9", 55, "st",
		"cc", 7,
		"probe-liveness-1", "tok-abc",
		"liveness", "dead",
		now,
	)
	if obs.SourceKind != observation.SourceProbe {
		t.Errorf("SourceKind = %q, want SourceProbe", obs.SourceKind)
	}
	if obs.WatcherToken != "tok-abc" {
		t.Errorf("WatcherToken = %q, want tok-abc", obs.WatcherToken)
	}
	if obs.Phase != observation.PhaseProposed {
		t.Errorf("Phase = %q, want PhaseProposed", obs.Phase)
	}
	if obs.Action != "probe.liveness.dead" {
		t.Errorf("Action = %q, want probe.liveness.dead", obs.Action)
	}
	if obs.Proposal.SuggestStatus != "ended" {
		t.Errorf("dead SuggestStatus = %q, want ended", obs.Proposal.SuggestStatus)
	}
	if !obs.Proposal.EndLifecycle {
		t.Errorf("dead EndLifecycle = false, want true")
	}
	if obs.Proposal.EndReason != "probe_dead" {
		t.Errorf("dead EndReason = %q, want probe_dead", obs.Proposal.EndReason)
	}
	if obs.Proposal.ActorKey.ActorID != "primary:cc" {
		t.Errorf("ActorKey.ActorID = %q, want primary:cc", obs.Proposal.ActorKey.ActorID)
	}
	if obs.TraceID == "" {
		t.Fatal("TraceID must not be empty")
	}
}

func TestBuildProbeObservation_OutcomeMapping_IdentifyMismatch_RoleRetryable(t *testing.T) {
	obs := buildProbeObservation(
		"sess-p", "%9", 55, "st",
		"cc", 1,
		"probe-1", "tok-1",
		"liveness", "identify_mismatch",
		fixedNow(),
	)
	assertRoleResolution(t, obs.Evidence, observation.RoleRetryableUnresolved)
}

func TestBuildProbeObservation_WatcherTokenSet_IdentityTriplePresent(t *testing.T) {
	obs := buildProbeObservation(
		"sess-p2", "%10", 300, "1700000111.11",
		"codex", 2,
		"probe-activity-7", "tok-xyz",
		"activity", "active",
		fixedNow(),
	)
	if obs.WatcherToken != "tok-xyz" {
		t.Errorf("WatcherToken = %q, want tok-xyz", obs.WatcherToken)
	}
	assertIdentityTriple(t, obs.Evidence, 300, "%10", "1700000111.11")
	assertRoleResolution(t, obs.Evidence, observation.RoleResolved)

	if got := evidenceString(t, obs.Evidence, "probe_id"); got != "probe-activity-7" {
		t.Errorf("probe_id = %q", got)
	}
	if got := evidenceString(t, obs.Evidence, "probe_kind"); got != "activity" {
		t.Errorf("probe_kind = %q", got)
	}
	if got := evidenceString(t, obs.Evidence, "probe_outcome"); got != "active" {
		t.Errorf("probe_outcome = %q", got)
	}
	// active -> SuggestStatus "active"; no EndLifecycle.
	if obs.Proposal.SuggestStatus != "active" {
		t.Errorf("active SuggestStatus = %q, want active", obs.Proposal.SuggestStatus)
	}
	if obs.Proposal.EndLifecycle {
		t.Errorf("active EndLifecycle = true, want false")
	}
}

// --- sweep builder -----------------------------------------------------------

func TestBuildSweepObservation_PidDead_ProposalEndLifecycle_IdentityTriplePresent_RoleResolved(t *testing.T) {
	now := fixedNow()
	frame := store.Frame{
		FrameID:          "frame-1",
		PaneID:           "%11",
		PID:              999,
		ProcessStartTime: "1700000222.22",
		AgentType:        "cc",
	}
	obs := buildSweepObservation(frame, "pid_dead", 4, now)

	if obs.SourceKind != observation.SourceSweep {
		t.Errorf("SourceKind = %q, want SourceSweep", obs.SourceKind)
	}
	if obs.Action != "sweep.frame_cleared" {
		t.Errorf("Action = %q, want sweep.frame_cleared", obs.Action)
	}
	if obs.Phase != observation.PhaseProposed {
		t.Errorf("Phase = %q, want PhaseProposed", obs.Phase)
	}
	if !obs.Proposal.EndLifecycle {
		t.Errorf("sweep EndLifecycle = false, want true")
	}
	if obs.Proposal.EndReason != "pid_dead" {
		t.Errorf("EndReason = %q, want pid_dead", obs.Proposal.EndReason)
	}
	if obs.Proposal.ActorKey.ActorID != "primary:cc" {
		t.Errorf("ActorKey.ActorID = %q, want primary:cc", obs.Proposal.ActorKey.ActorID)
	}
	if obs.ObservedGeneration != 4 {
		t.Errorf("ObservedGeneration = %d, want 4", obs.ObservedGeneration)
	}
	if obs.TraceID == "" {
		t.Fatal("TraceID must not be empty")
	}
	assertIdentityTriple(t, obs.Evidence, 999, "%11", "1700000222.22")
	assertRoleResolution(t, obs.Evidence, observation.RoleResolved)

	if got := evidenceString(t, obs.Evidence, "reason"); got != "pid_dead" {
		t.Errorf("evidence.reason = %q, want pid_dead", got)
	}
	if got := evidenceString(t, obs.Evidence, "frame_id"); got != "frame-1" {
		t.Errorf("evidence.frame_id = %q, want frame-1", got)
	}
}

func TestBuildSweepObservation_FrameMissingStartTime_RoleTerminalUnresolved(t *testing.T) {
	frame := store.Frame{
		FrameID:          "frame-2",
		PaneID:           "%12",
		PID:              321,
		ProcessStartTime: "", // identity missing
		AgentType:        "cc",
	}
	obs := buildSweepObservation(frame, "pid_reused", 2, fixedNow())
	assertRoleResolution(t, obs.Evidence, observation.RoleTerminalUnresolved)

	// identity triple still emitted (empty string for start_time, since it's missing)
	if got := evidenceString(t, obs.Evidence, "start_time"); got != "" {
		t.Errorf("sweep empty start_time should remain \"\", got %q", got)
	}
}
