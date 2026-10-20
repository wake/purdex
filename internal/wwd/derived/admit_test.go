package derived

import (
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func admitFacts(zone wire.Zone) wire.Facts {
	var f wire.Facts
	f.Worker.Slots = wire.Slots{Heavy: 1, Light: 2}
	f.Derived.Zone = zone
	return f
}

func TestAdmitLocal(t *testing.T) {
	owner := admitFacts(wire.ZoneNormal)
	owner.Worker.OwnerRule, owner.Owner = "keyboard", &wire.OwnerState{Active: true}
	off := admitFacts(wire.ZoneNormal)
	off.Worker.Enabled = false
	held := admitFacts(wire.ZoneNormal)
	held.Exception = []wire.FactExc{{Name: "release"}}
	cases := []struct {
		name    string
		f       wire.Facts
		heavy   bool
		running wire.Slots
		want    []string
	}{
		{"light room", admitFacts(wire.ZoneNormal), false, wire.Slots{}, nil},
		{"heavy room", admitFacts(wire.ZoneNormal), true, wire.Slots{}, nil},
		{"owner active does not stop either", owner, true, wire.Slots{}, nil},
		{"worker off does not stop either", off, false, wire.Slots{}, nil},
		{"light warning passes", admitFacts(wire.ZoneWarning), false, wire.Slots{}, nil},
		{"heavy warning stops", admitFacts(wire.ZoneWarning), true, wire.Slots{}, []string{ReasonZoneWarning}},
		{"light danger stops", admitFacts(wire.ZoneDanger), false, wire.Slots{}, []string{ReasonZoneDanger}},
		{"light slots full", admitFacts(wire.ZoneNormal), false, wire.Slots{Light: 2}, []string{ReasonSlotsFull}},
		{"light ignores heavy running", admitFacts(wire.ZoneNormal), false, wire.Slots{Heavy: 1}, nil},
		{"heavy slots full", admitFacts(wire.ZoneNormal), true, wire.Slots{Heavy: 1}, []string{ReasonSlotsFull}},
		{"heavy hold", held, true, wire.Slots{}, []string{ReasonExceptionP + "release"}},
		{"light ignores a hold", held, false, wire.Slots{}, nil},
	}
	for _, c := range cases {
		if got := AdmitLocal(c.f, c.heavy, c.running); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
