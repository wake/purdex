package wire

import "testing"

// The slot a job is charged (spec §5.2): a declared fast-lane task is light; everything else — a queue job the worker runs, an adhoc
// command of the fast lane — is heavy.
func TestJobSlot(t *testing.T) {
	cases := []struct {
		name string
		job  Job
		want Slot
	}{
		{"a queue job", Job{}, SlotHeavy},
		{"a queue job that is adhoc (never queued, but the rule does not look)", Job{AdHoc: true}, SlotHeavy},
		{"a declared fast-lane task", Job{Local: true}, SlotLight},
		{"an adhoc fast-lane command", Job{Local: true, AdHoc: true}, SlotHeavy},
	}
	for _, c := range cases {
		if got := JobSlot(c.job); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
