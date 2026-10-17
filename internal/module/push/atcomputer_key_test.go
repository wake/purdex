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
}
