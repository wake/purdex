package push

import (
	"testing"

	pushpkg "github.com/wake/purdex/internal/push"
)

// The load watcher's patrol asks the registry whether the person is at a computer: the module publishes that once it is ready.
func TestAtComputerKey_RegisteredByTheReadyModule(t *testing.T) {
	te := newTriggerEnv(t, nil)
	svc, ok := te.mod.core.Registry.Get(pushpkg.AtComputerKey)
	if !ok {
		t.Fatal("the push module did not publish AtComputer")
	}
	r, ok := svc.(pushpkg.AtComputerReader)
	if !ok {
		t.Fatalf("%T is not an AtComputerReader", svc)
	}
	if r.AtComputer() {
		t.Fatal("no Mac has reported: the person is not at a computer")
	}
	// WK-3b: the same value has the three-valued face, which is what the watcher's user_back asks for. No Mac has reported: not known.
	sr, ok := svc.(pushpkg.PresenceStateReader)
	if !ok {
		t.Fatalf("%T is not a PresenceStateReader", svc)
	}
	if at, known := sr.PresenceState(); at || known {
		t.Fatalf("no Mac has reported: at %v known %v, want false false", at, known)
	}
}
