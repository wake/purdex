package teammod

import (
	"errors"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// The mod read carries exactly compactionView's Effective.
func TestModReader_CarriesTheEffectiveCompaction(t *testing.T) {
	f := newFixture(t)
	f.compact.d = hostconfig.CompactionDefaults{
		Solo: hostconfig.CompactionSet{Auto: true, Threshold: 33, Idle: false},
		Team: hostconfig.CompactionSet{Auto: true, Threshold: 80, Idle: true},
	}
	f.switches.set(hostconfig.RelaySwitches{})
	sid := "sid-lead"
	seedTeam(t, f.m.store, uid(9), sid, f.clock.Load())
	got, err := modReader{f.m}.ModTeamRead(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != team.SeatLead || got.Compaction == nil {
		t.Fatalf("got %+v", got)
	}
	if want := f.view(sid).Effective; !reflect.DeepEqual(*got.Compaction, want) {
		t.Fatalf("compaction %+v, want %+v", *got.Compaction, want)
	}
}

// When the effective settings cannot be read the answer has no compaction (the mod keeps its last value); role stays.
func TestModReader_CompactionErrorLeavesItNilAndTheRestIntact(t *testing.T) {
	f := newFixture(t)
	sid := "sid-lead"
	seedTeam(t, f.m.store, uid(9), sid, f.clock.Load())
	f.switches.mu.Lock()
	f.switches.err = errors.New("config down")
	f.switches.mu.Unlock()
	got, err := modReader{f.m}.ModTeamRead(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != team.SeatLead || got.Compaction != nil {
		t.Fatalf("got %+v", got)
	}
}
