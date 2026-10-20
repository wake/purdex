package fastlane

import (
	"context"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/wwd/manifest"
)

func TestADeclaredTaskTakesALightSlotAndAnUndeclaredCommandAHeavyOne(t *testing.T) {
	g := &fakeGate{}
	res, err := Run(context.Background(), Request{Root: tree(t), Task: task(t, "one-file"), Args: []string{"a.test.ts"}, Gate: g})
	if err != nil {
		t.Fatal(err)
	}
	res.Cleanup()
	if g.calls[0].Class != ClassLight {
		t.Fatalf("a declared task took %q", g.calls[0].Class)
	}

	g = &fakeGate{}
	res, err = Run(context.Background(), Request{Root: tree(t), Task: manifest.AdHocTask(), Args: []string{"go", "vet", "./..."}, GateClass: ClassHeavy, Gate: g})
	if err != nil {
		t.Fatal(err)
	}
	res.Cleanup()
	if g.calls[0].Class != ClassHeavy || !reflect.DeepEqual(g.calls[0].Argv, []string{"go", "vet", "./..."}) {
		t.Fatalf("an undeclared command: class %q argv %q", g.calls[0].Class, g.calls[0].Argv)
	}
}
