package fastlane_test

import (
	"testing"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/fastlane"
)

// The old gate is still running until WWD-9 step 9a, and the marks are the two sides' conversation between processes: a change on
// either side has to turn this red at once. Delete this file together with internal/gate in 9a.
func TestWireValuesEqualTheGates(t *testing.T) {
	if string(fastlane.ClassLight) != string(gate.ClassLight) || string(fastlane.ClassHeavy) != string(gate.ClassHeavy) {
		t.Fatalf("classes: fastlane %q %q, gate %q %q", fastlane.ClassLight, fastlane.ClassHeavy, gate.ClassLight, gate.ClassHeavy)
	}
	if fastlane.AdmittedMarkEnv != gate.AdmittedMarkEnv {
		t.Fatalf("AdmittedMarkEnv: fastlane %q, gate %q", fastlane.AdmittedMarkEnv, gate.AdmittedMarkEnv)
	}
	if fastlane.AdmittedMark("x7") != gate.AdmittedMark("x7") {
		t.Fatalf("AdmittedMark: fastlane %q, gate %q", fastlane.AdmittedMark("x7"), gate.AdmittedMark("x7"))
	}
	if fastlane.NotStartedMark("x7") != gate.NotStartedMark("x7") {
		t.Fatalf("NotStartedMark: fastlane %q, gate %q", fastlane.NotStartedMark("x7"), gate.NotStartedMark("x7"))
	}
	if fastlane.ExitRefused != gate.ExitRefused || fastlane.ExitOrphaned != gate.ExitOrphaned {
		t.Fatalf("exit codes: fastlane %d %d, gate %d %d", fastlane.ExitRefused, fastlane.ExitOrphaned, gate.ExitRefused, gate.ExitOrphaned)
	}
}
