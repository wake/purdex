package watch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

// #3049: a slot that is held with no record read as a holder with no start time and no pid; the detector measured it against the
// zero time and made a slot_long of 292 years (9223372036 s). Such a holder has nothing to measure: no event, however long, and a
// diagnostic that says it is there.
func TestE4_AHolderWithNoStartTimeIsNeverASlotLong(t *testing.T) {
	for _, c := range []struct {
		name string
		h    gate.Holder
	}{
		{"no record (read as zero)", gate.Holder{Class: gate.ClassLight, Slot: 2, NoRecord: true}},
		{"a zero start", gate.Holder{Class: gate.ClassLight, Slot: 2, PID: 500, Argv: []string{"go", "test"}}},
		{"pid 0 with a start", gate.Holder{Class: gate.ClassLight, Slot: 2, Started: t0, Argv: []string{"go", "test"}}},
		{"a negative pid", gate.Holder{Class: gate.ClassLight, Slot: 2, PID: -1, Started: t0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			var told []string
			r.d.Diag = func(source, problem string) { told = append(told, source+": "+problem) }
			r.holders = []gate.Holder{c.h}
			assert.Empty(t, r.run(3*time.Hour), "no event for it, however long it stays")
			require.NotEmpty(t, told)
			assert.Equal(t, "slot records: held with no readable start or pid, not measured: light-2", told[len(told)-1])
		})
	}
}

// A holder with a start and a pid is measured as before, and the diagnostic is cleared when the unreadable one is gone.
func TestE4_TheDiagnosticClearsWhenTheHolderIsReadable(t *testing.T) {
	r := newRig(t)
	var last string
	r.d.Diag = func(_, problem string) { last = problem }
	r.holders = []gate.Holder{{Class: gate.ClassLight, Slot: 2, NoRecord: true}}
	r.pass()
	assert.NotEmpty(t, last)
	r.holders = []gate.Holder{holder(2, 500, r.now, false)}
	r.pass()
	assert.Empty(t, last, "recovered")
}

// The unreadable holder does not hide a real one: the real one is still reported when it has been held long enough.
func TestE4_AnUnreadableHolderBesideARealOne(t *testing.T) {
	r := newRig(t)
	r.holders = []gate.Holder{{Class: gate.ClassLight, Slot: 1, NoRecord: true}, holder(2, 500, t0, false)}
	got := filterKind(r.run(61*time.Minute), KindSlotLong)
	require.Len(t, got, 1)
	assert.Equal(t, "heavy-2", got[0].Slot)
	assert.Equal(t, 500, got[0].HolderPID)
	assert.Less(t, got[0].Duration, 2*time.Hour)
}

// A detector with no Diag still works.
func TestE4_NoDiagIsFine(t *testing.T) {
	r := newRig(t)
	r.holders = []gate.Holder{{Class: gate.ClassLight, Slot: 2, NoRecord: true}}
	assert.NotPanics(t, func() { r.run(2 * time.Hour) })
}
