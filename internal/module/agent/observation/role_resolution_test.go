package observation_test

import (
	"testing"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// TestRoleResolutionFromEvidence_Resolved verifies that a string-valued
// role_resolution entry with the "RoleResolved" value returns the typed
// enum plus ok=true.
func TestRoleResolutionFromEvidence_Resolved(t *testing.T) {
	ev := []observation.EvidenceRef{
		{Key: observation.EvidenceKeyRoleResolution, Value: "RoleResolved"},
	}

	state, ok := observation.RoleResolutionFromEvidence(ev)
	if !ok {
		t.Fatalf("ok = false; want true")
	}
	if state != observation.RoleResolved {
		t.Errorf("state = %q; want %q", state, observation.RoleResolved)
	}
}

// TestRoleResolutionFromEvidence_RetryableUnresolved verifies the retryable
// enum round-trips through a string evidence value.
func TestRoleResolutionFromEvidence_RetryableUnresolved(t *testing.T) {
	ev := []observation.EvidenceRef{
		{Key: "pid", Value: int64(42)},
		{Key: observation.EvidenceKeyRoleResolution, Value: "RoleRetryableUnresolved"},
	}

	state, ok := observation.RoleResolutionFromEvidence(ev)
	if !ok {
		t.Fatalf("ok = false; want true")
	}
	if state != observation.RoleRetryableUnresolved {
		t.Errorf("state = %q; want %q", state, observation.RoleRetryableUnresolved)
	}
}

// TestRoleResolutionFromEvidence_TerminalUnresolved verifies the terminal
// enum round-trips through a string evidence value.
func TestRoleResolutionFromEvidence_TerminalUnresolved(t *testing.T) {
	ev := []observation.EvidenceRef{
		{Key: observation.EvidenceKeyRoleResolution, Value: "RoleTerminalUnresolved"},
	}

	state, ok := observation.RoleResolutionFromEvidence(ev)
	if !ok {
		t.Fatalf("ok = false; want true")
	}
	if state != observation.RoleTerminalUnresolved {
		t.Errorf("state = %q; want %q", state, observation.RoleTerminalUnresolved)
	}
}

// TestRoleResolutionFromEvidence_MissingKey_ReturnsFalse verifies that
// evidence without the role_resolution key returns ("", false).
func TestRoleResolutionFromEvidence_MissingKey_ReturnsFalse(t *testing.T) {
	ev := []observation.EvidenceRef{
		{Key: "pid", Value: int64(42)},
		{Key: "verify_reason", Value: "ok"},
	}

	state, ok := observation.RoleResolutionFromEvidence(ev)
	if ok {
		t.Fatalf("ok = true; want false")
	}
	if state != "" {
		t.Errorf("state = %q; want empty", state)
	}
}

// TestRoleResolutionFromEvidence_InvalidEnum_ReturnsFalse verifies that a
// string value that is not one of the three canonical enum values returns
// ("", false).
func TestRoleResolutionFromEvidence_InvalidEnum_ReturnsFalse(t *testing.T) {
	ev := []observation.EvidenceRef{
		{Key: observation.EvidenceKeyRoleResolution, Value: "RoleFoo"},
	}

	state, ok := observation.RoleResolutionFromEvidence(ev)
	if ok {
		t.Fatalf("ok = true; want false")
	}
	if state != "" {
		t.Errorf("state = %q; want empty", state)
	}
}

// TestRoleResolutionFromEvidence_InvalidValueType_ReturnsFalse verifies that
// non-string / non-RoleResolutionState value types (int, bool, struct, nil)
// are rejected with ("", false).
func TestRoleResolutionFromEvidence_InvalidValueType_ReturnsFalse(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{name: "int", value: 42},
		{name: "int64", value: int64(42)},
		{name: "bool", value: true},
		{name: "nil", value: nil},
		{name: "struct", value: struct{ X int }{X: 1}},
		{name: "bytes", value: []byte("RoleResolved")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := []observation.EvidenceRef{
				{Key: observation.EvidenceKeyRoleResolution, Value: tc.value},
			}

			state, ok := observation.RoleResolutionFromEvidence(ev)
			if ok {
				t.Fatalf("ok = true; want false")
			}
			if state != "" {
				t.Errorf("state = %q; want empty", state)
			}
		})
	}
}

// TestRoleResolutionFromEvidence_AcceptRoleResolutionStateType verifies that
// evidence values whose concrete type is already RoleResolutionState (not
// plain string) are recognized. Builders on the producer side may use the
// typed enum directly.
func TestRoleResolutionFromEvidence_AcceptRoleResolutionStateType(t *testing.T) {
	cases := []struct {
		name string
		in   observation.RoleResolutionState
		want observation.RoleResolutionState
	}{
		{name: "Resolved", in: observation.RoleResolved, want: observation.RoleResolved},
		{name: "Retryable", in: observation.RoleRetryableUnresolved, want: observation.RoleRetryableUnresolved},
		{name: "Terminal", in: observation.RoleTerminalUnresolved, want: observation.RoleTerminalUnresolved},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := []observation.EvidenceRef{
				{Key: observation.EvidenceKeyRoleResolution, Value: tc.in},
			}

			state, ok := observation.RoleResolutionFromEvidence(ev)
			if !ok {
				t.Fatalf("ok = false; want true")
			}
			if state != tc.want {
				t.Errorf("state = %q; want %q", state, tc.want)
			}
		})
	}
}

// TestRoleResolutionFromEvidence_EmptyEvidence_ReturnsFalse verifies that
// both nil and empty slices return ("", false).
func TestRoleResolutionFromEvidence_EmptyEvidence_ReturnsFalse(t *testing.T) {
	cases := []struct {
		name string
		ev   []observation.EvidenceRef
	}{
		{name: "nil", ev: nil},
		{name: "empty", ev: []observation.EvidenceRef{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := observation.RoleResolutionFromEvidence(tc.ev)
			if ok {
				t.Fatalf("ok = true; want false")
			}
			if state != "" {
				t.Errorf("state = %q; want empty", state)
			}
		})
	}
}

// TestRoleResolutionFromEvidence_FirstKeyWins verifies that when multiple
// role_resolution entries are present, the first one is returned (apply
// pipeline must not rely on duplicates, but a stable deterministic behavior
// is required).
func TestRoleResolutionFromEvidence_FirstKeyWins(t *testing.T) {
	ev := []observation.EvidenceRef{
		{Key: observation.EvidenceKeyRoleResolution, Value: "RoleResolved"},
		{Key: observation.EvidenceKeyRoleResolution, Value: "RoleTerminalUnresolved"},
	}

	state, ok := observation.RoleResolutionFromEvidence(ev)
	if !ok {
		t.Fatalf("ok = false; want true")
	}
	if state != observation.RoleResolved {
		t.Errorf("state = %q; want %q (first-wins)", state, observation.RoleResolved)
	}
}
