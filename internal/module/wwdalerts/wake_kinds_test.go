package wwdalerts

import (
	"testing"

	"github.com/wake/purdex/internal/watchwake"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The App's checklist of kinds (watchwake.KnownKinds, served as known_kinds by the wake settings) names every kind the wake note names, in the
// same words: a kind added to one table and not the other would show in the note and not in the settings, or the reverse.
func TestWakeKindNamesAreTheKnownKindsOfTheWakeSettings(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range watchwake.KnownKinds {
		seen[k.Kind] = true
		if got := wakeKindNames[wire.AlertKind(k.Kind)]; got != k.Label {
			t.Errorf("%s: the note says %q, the settings say %q", k.Kind, got, k.Label)
		}
	}
	for kind, name := range wakeKindNames {
		if !seen[string(kind)] {
			t.Errorf("%s (%s) is in the note's table and not in known_kinds", kind, name)
		}
	}
}
