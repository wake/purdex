package fastlane

import "testing"

// The marks, exit codes and class names are spoken between this package and the old gate (a run sent from another host is read by
// the same marks), so their values are fixed here as literals: a change on either side is a protocol change, not a rename.
func TestWireValuesAreFixed(t *testing.T) {
	if AdmittedMarkEnv != "PDX_GATE_ADMITTED_MARK" {
		t.Fatalf("AdmittedMarkEnv = %q", AdmittedMarkEnv)
	}
	if got, want := AdmittedMark("n1"), "\x1epdx-gate-admitted:n1\n"; got != want {
		t.Fatalf("AdmittedMark = %q, want %q", got, want)
	}
	if got, want := NotStartedMark("n1"), "\x1epdx-gate-not-started:n1\n"; got != want {
		t.Fatalf("NotStartedMark = %q, want %q", got, want)
	}
	if ExitRefused != 75 || ExitOrphaned != 129 || ExitTimeout != 124 {
		t.Fatalf("exit codes = %d %d %d, want 75 129 124", ExitRefused, ExitOrphaned, ExitTimeout)
	}
	if ClassLight != "light" || ClassHeavy != "heavy" {
		t.Fatalf("classes = %q %q", ClassLight, ClassHeavy)
	}
}
