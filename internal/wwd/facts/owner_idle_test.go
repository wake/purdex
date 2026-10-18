package facts

import "testing"

// idleAgrees says whether ioreg's HIDIdleTime is consistent with our own idle readings taken just before and just after it.
// The idle time grows with the clock until the owner touches the machine and then starts again from zero, so two readings made at
// different moments are the same figure only if nothing reset it between them, and ioreg's own reading lies between ours however
// long it took to start (a forked process on a busy host can start seconds late): before-tol <= ioreg <= after+tol. comparable is
// false when a reset happened inside the window (after is lower than before): there is nothing to compare, take the readings again.
func idleAgrees(before, ioreg, after, tol float64) (agree, comparable bool) {
	if after < before-tol {
		return false, false
	}
	return ioreg >= before-tol && ioreg <= after+tol, true
}

func TestIdleAgrees(t *testing.T) {
	const tol = 2.0
	for name, c := range map[string]struct {
		before, ioreg, after float64
		agree, comparable    bool
	}{
		"quiet, ioreg started instantly":              {100, 100.1, 100.2, true, true},
		"quiet, ioreg took 8 s to start under load":   {100, 108, 108.5, true, true}, // the old test, comparing 100 with 108, failed here
		"quiet, ioreg a little ahead of us":           {100, 101.5, 101.6, true, true},
		"ioreg disagrees: far above both":             {100, 140, 101, false, true},
		"ioreg disagrees: far below both":             {100, 60, 101, false, true},
		"input between our first read and ioreg":      {600, 0.4, 0.9, false, false}, // the old test compared 600 with 0.4
		"input between ioreg and our second read":     {600, 600.2, 0.3, false, false},
		"input right after the second read is fine":   {600, 600.3, 600.4, true, true},
		"a reset smaller than the tolerance is noise": {100, 99.5, 99.0, true, true},
	} {
		agree, comparable := idleAgrees(c.before, c.ioreg, c.after, tol)
		if agree != c.agree || comparable != c.comparable {
			t.Errorf("%s: idleAgrees(%v, %v, %v) = %v, %v; want %v, %v", name, c.before, c.ioreg, c.after, agree, comparable, c.agree, c.comparable)
		}
	}
}
