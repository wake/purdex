package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestRingOwnsItsData(t *testing.T) {
	r := NewRing(4)
	disk := []wire.Disk{{Mount: "/", FreeGB: 10}}
	po := 7.0
	swap := 100
	f := rec(t0, 1, 1000, 1, wire.ZoneNormal)
	f.Disk = disk
	f.Mem.SwapUsedMB = &swap
	f.Mem.PageOutRate = &po
	until := t0.Add(time.Hour)
	f.Exception = []wire.FactExc{{Name: "x", Since: t0, Until: &until}}
	f.Procs = []wire.Proc{{PID: 1, Name: "a", Owner: &wire.ProcOwner{Kind: wire.OwnerSystem}}}
	f.Tools.PHP = []string{"8.4"}
	r.Add(f)

	// the caller reuses its buffers after Add
	disk[0].FreeGB = -1
	po, swap = -1, -1
	until = t0
	f.Procs[0].Name = "mutated"
	f.Procs[0].Owner.Kind = wire.OwnerJob
	f.Tools.PHP[0] = "mutated"

	got := r.Since(time.Time{})[0]
	if got.Disk[0].FreeGB != 10 || *got.Mem.PageOutRate != 7 || *got.Mem.SwapUsedMB != 100 || !got.Exception[0].Until.Equal(t0.Add(time.Hour)) ||
		got.Procs[0].Name != "a" || got.Procs[0].Owner.Kind != wire.OwnerSystem || got.Tools.PHP[0] != "8.4" {
		t.Fatalf("Add shares memory with the caller: %+v", got)
	}

	// a reader changing a result must not change the history
	got.Disk[0].FreeGB = -2
	*got.Mem.PageOutRate = -2
	got.Procs[0].Owner.Kind = wire.OwnerJob
	again := r.Since(time.Time{})[0]
	if again.Disk[0].FreeGB != 10 || *again.Mem.PageOutRate != 7 || again.Procs[0].Owner.Kind != wire.OwnerSystem {
		t.Fatalf("Since returns aliases: %+v", again)
	}
	last, _ := r.Last()
	last.Disk[0].FreeGB = -3
	if r2, _ := r.Last(); r2.Disk[0].FreeGB != 10 {
		t.Fatal("Last returns aliases")
	}
}

// Every pointer and slice of wire.Facts must be copied by Clone. This walks the
// struct with reflection so a field added to the contract later fails here
// instead of silently aliasing.
func TestCloneCoversEveryReferenceField(t *testing.T) {
	f := fullFacts()
	c := Clone(f)
	if !sameShape(f, c) {
		t.Fatalf("clone differs: %+v vs %+v", f, c)
	}
	if aliased(f, c) {
		t.Fatal("clone shares a pointer or slice with the original")
	}
}
