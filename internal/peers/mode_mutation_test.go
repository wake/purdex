package peers

import (
	"strings"
	"testing"
)

// TestMutationDefaultModeBackToPrompting_MustFail is a mutation test that
// verifies the fix is not accidental. It simulates what would happen if
// someone changed ValidateMode("") back to returning ModePrompting instead
// of ModeUnknown — it should make this test (and others) fail.
//
// This test exists to detect accidental regressions: if a reviewer or
// contributor changes the default mode and runs `go test`, they should see
// this test fail loudly, not silently revert the fix.
//
// To verify this test works: temporarily edit ValidateMode to return
// ModePrompting when s == "" (the old behavior), run this test, and see it
// fail. Then restore the code and verify it passes.
func TestMutationDefaultModeBackToPrompting_MustFail(t *testing.T) {
	// If ValidateMode("") ever returns ModePrompting instead of ModeUnknown,
	// this test should fail.
	mode, err := ValidateMode("")
	if err != nil {
		t.Fatalf("ValidateMode(\"\") failed: %v", err)
	}

	// This assertion MUST fail if the mutation happens (default changed to ModePrompting).
	if mode == ModePrompting {
		t.Errorf("MUTATION DETECTED: ValidateMode(\"\") returned ModePrompting. "+
			"This is the bug from issue #1124 — the default should be ModeUnknown. "+
			"Restore ValidateMode to return ModeUnknown for empty input.",
		)
	}

	// Positive assertion: it must be ModeUnknown.
	if mode != ModeUnknown {
		t.Errorf("ValidateMode(\"\") = %q, want %q. "+
			"The default mode when caller cannot determine it should be unknown (#1124).",
			mode, ModeUnknown,
		)
	}
}

// TestMutationUnknownModeIsValid_MustFail verifies that ModeUnknown is a
// recognized mode and can be used in the wire protocol. If someone removes
// support for ModeUnknown, this test should fail.
func TestMutationUnknownModeIsValid_MustFail(t *testing.T) {
	mode, err := ValidateMode(ModeUnknown)
	if err != nil {
		t.Errorf("MUTATION DETECTED: ValidateMode(ModeUnknown) failed: %v. "+
			"ModeUnknown should be a valid, recognized mode.",
			err,
		)
	}
	if mode != ModeUnknown {
		t.Errorf("ValidateMode(ModeUnknown) = %q, want ModeUnknown", mode)
	}
}

// TestMutationExampleBadMutationComment demonstrates how to comment a
// mutation test when you want to keep a deliberate old case for compatibility
// (this is NOT that case — ModeUnknown is new in this fix).
func TestMutationExampleBadMutationComment(t *testing.T) {
	// If someone tries to "fix" ValidateMode by changing it back to the old
	// behavior, this test names the issue and gives them context:
	// - the old behavior was: "" → ModePrompting
	// - the new behavior is: "" → ModeUnknown
	// The old behavior is why #1124 exists: pdx couldn't express what it
	// actually knew (nothing) about the mode, so it guessed. This led to
	// messages being delivered without a hold when they should have been held.

	const oldBuggyBehavior = ModePrompting
	const fixedBehavior = ModeUnknown

	mode, _ := ValidateMode("")
	if mode == oldBuggyBehavior {
		t.Logf("Mutation test would catch this: reverting to the old, wrong default")
		t.Fail()
	}
	if mode != fixedBehavior {
		t.Logf("Default mode is not the fix: got %q, want %q", mode, fixedBehavior)
		t.Fail()
	}
}

// testModeInFrameContent verifies that ModeUnknown appears correctly in
// generated frames. This is a helper for frame integration tests.
func testModeInFrameContent(t *testing.T, mode string) bool {
	// This is a helper function for testing that frames correctly carry
	// the mode value. If someone changes how modes are serialized into frames,
	// this utility helps verify the change doesn't break the wire protocol.
	if mode == "" {
		return false
	}
	knownModes := []string{ModePrompting, ModeBypass, ModeUnknown}
	for _, known := range knownModes {
		if mode == known {
			return true
		}
	}
	return false
}

// TestModeConstantsExist verifies that all expected mode constants are
// defined. If someone accidentally deletes or renames ModeUnknown, this
// test should fail.
func TestModeConstantsExist(t *testing.T) {
	modes := []string{ModePrompting, ModeBypass, ModeUnknown}
	for _, mode := range modes {
		if mode == "" {
			t.Errorf("Found an empty mode constant; all modes must be non-empty strings")
		}
		if strings.Contains(mode, " ") {
			t.Errorf("Mode %q contains spaces; modes must be alphanumeric", mode)
		}
	}

	// Sanity check: all three must be different
	uniqueModes := make(map[string]int)
	for _, mode := range modes {
		uniqueModes[mode]++
	}
	if len(uniqueModes) != len(modes) {
		t.Errorf("Mode constants are not unique: %v", modes)
	}

	// Verify ModeUnknown is specifically "unknown"
	if ModeUnknown != "unknown" {
		t.Errorf("ModeUnknown = %q, want %q", ModeUnknown, "unknown")
	}
}

// TestMutationDocstringConsistency checks that the wire protocol's
// documented supported modes match what ValidateMode actually accepts.
// This catches cases where the code and docs drift.
func TestMutationDocstringConsistency(t *testing.T) {
	// ValidateMode's docstring says it accepts "", ModePrompting, ModeBypass, ModeUnknown.
	// Verify that claim by attempting to validate each.
	testCases := []string{"", ModePrompting, ModeBypass, ModeUnknown}
	for _, testCase := range testCases {
		_, err := ValidateMode(testCase)
		if err != nil {
			t.Errorf("ValidateMode(%q) failed, but the docstring says it should be accepted: %v", testCase, err)
		}
	}
}
