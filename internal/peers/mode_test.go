package peers

import (
	"testing"
)

// TestValidateModeDefaultsToUnknown confirms that ValidateMode("") normalises
// to ModeUnknown, not ModePrompting. This is the fix for #1124: when pdx msg
// send is called without --mode, it now says "unknown" instead of guessing
// "prompting", so the receiver can treat it as a mismatch (Option 2).
func TestValidateModeDefaultsToUnknown(t *testing.T) {
	mode, err := ValidateMode("")
	if err != nil {
		t.Fatalf("ValidateMode(\"\") failed: %v", err)
	}
	if mode != ModeUnknown {
		t.Errorf("ValidateMode(\"\") returned %q, want %q", mode, ModeUnknown)
	}
}

// TestValidateModeAcceptsKnownModes confirms that ValidateMode accepts all
// three known modes: prompting, bypass, and unknown.
func TestValidateModeAcceptsKnownModes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ModeUnknown},
		{ModePrompting, ModePrompting},
		{ModeBypass, ModeBypass},
		{ModeUnknown, ModeUnknown},
	}
	for _, tt := range tests {
		mode, err := ValidateMode(tt.input)
		if err != nil {
			t.Errorf("ValidateMode(%q) failed: %v", tt.input, err)
		}
		if mode != tt.want {
			t.Errorf("ValidateMode(%q) = %q, want %q", tt.input, mode, tt.want)
		}
	}
}

// TestValidateModeRejectsInvalid confirms that ValidateMode rejects any value
// other than "", ModePrompting, ModeBypass, or ModeUnknown.
func TestValidateModeRejectsInvalid(t *testing.T) {
	invalid := []string{"invalid", "PROMPTING", "bypass ", " bypass", "?"}
	for _, s := range invalid {
		_, err := ValidateMode(s)
		if err == nil {
			t.Errorf("ValidateMode(%q) should have failed but didn't", s)
		}
	}
}
