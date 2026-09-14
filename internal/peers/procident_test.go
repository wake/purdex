package peers

import (
	"errors"
	"testing"
)

// TestClassifyProc pins the four outcomes of the shared tri-state
// process identity (R2-G): a dead pid is different without ever asking
// for its start time; a live pid is same, different or unknown by its
// start time — and unknown is never folded into either verdict.
func TestClassifyProc(t *testing.T) {
	const want = "Mon Sep 14 10:00:00 2026"
	cases := []struct {
		name      string
		alive     bool
		ps        string
		psErr     error
		wantAlive bool
		wantID    ProcIdentity
		wantAsked bool // procStart consulted
	}{
		{"dead", false, want, nil, false, ProcDifferent, false},
		{"alive same", true, want, nil, true, ProcSame, true},
		{"alive different", true, "Mon Sep 14 09:30:00 2026", nil, true, ProcDifferent, true},
		{"alive unknown", true, "", errors.New("ps: boom"), true, ProcUnknown, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asked := false
			alive, id := ClassifyProc(4242, want,
				func(pid int) bool {
					if pid != 4242 {
						t.Errorf("alive(%d), want 4242", pid)
					}
					return c.alive
				},
				func(pid int) (string, error) {
					asked = true
					if pid != 4242 {
						t.Errorf("procStart(%d), want 4242", pid)
					}
					return c.ps, c.psErr
				})
			if alive != c.wantAlive || id != c.wantID {
				t.Errorf("ClassifyProc = (%v, %v), want (%v, %v)", alive, id, c.wantAlive, c.wantID)
			}
			if asked != c.wantAsked {
				t.Errorf("procStart consulted = %v, want %v", asked, c.wantAsked)
			}
		})
	}
}

func TestProcIdentity_String(t *testing.T) {
	for id, want := range map[ProcIdentity]string{ProcSame: "same", ProcDifferent: "different", ProcUnknown: "unknown", ProcIdentity(42): "unknown"} {
		if got := id.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(id), got, want)
		}
	}
	var zero ProcIdentity
	if zero != ProcUnknown {
		t.Errorf("zero value = %v, want unknown (the safe default)", zero)
	}
}
