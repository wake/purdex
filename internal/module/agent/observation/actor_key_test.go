package observation_test

import (
	"testing"

	"github.com/wake/purdex/internal/module/agent/observation"
)

func TestActorKey_String_Format(t *testing.T) {
	k := observation.ActorKey{SessionID: "s1", Generation: 7, ActorID: "primary:main"}
	want := "s1/7/primary:main"
	if got := k.String(); got != want {
		t.Errorf("ActorKey.String() = %q; want %q", got, want)
	}
}

func TestActorKey_Equal(t *testing.T) {
	k1 := observation.ActorKey{SessionID: "s1", Generation: 3, ActorID: "primary:main"}
	k2 := observation.ActorKey{SessionID: "s1", Generation: 3, ActorID: "primary:main"}
	if k1 != k2 {
		t.Error("identical ActorKey structs should be equal")
	}

	k3 := observation.ActorKey{SessionID: "s2", Generation: 3, ActorID: "primary:main"}
	if k1 == k3 {
		t.Error("different SessionID should not be equal")
	}

	k4 := observation.ActorKey{SessionID: "s1", Generation: 4, ActorID: "primary:main"}
	if k1 == k4 {
		t.Error("different Generation should not be equal")
	}

	k5 := observation.ActorKey{SessionID: "s1", Generation: 3, ActorID: "subagent:explore-42"}
	if k1 == k5 {
		t.Error("different ActorID should not be equal")
	}
}

func TestActorKey_UsableAsMapKey(t *testing.T) {
	key1 := observation.ActorKey{SessionID: "s1", Generation: 1, ActorID: "primary:main"}
	key2 := observation.ActorKey{SessionID: "s1", Generation: 2, ActorID: "primary:main"}

	m := map[observation.ActorKey]string{}
	m[key1] = "x"
	m[key2] = "y"

	got := m[key1]
	if got != "x" {
		t.Errorf("map[key1] = %q; want \"x\"", got)
	}
	if m[key2] != "y" {
		t.Errorf("map[key2] = %q; want \"y\"", m[key2])
	}
}
