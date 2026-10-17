package watch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// ---- #2965: a resident app of /Applications is not a heavy command ----

// herd is an app that launchd started (pid 100, ppid 1) with a worker below it: cores in all, rssMB in all.
func herd(cores float64, rssMB uint64) []resources.Proc {
	return []resources.Proc{
		p(1, 0, "launchd", 0, 10),
		p(100, 1, "Herd", cores*100/2, rssMB/2),
		p(101, 100, "php-fpm", cores*100/2, rssMB/2),
	}
}

const herdPath = "/Applications/Herd.app/Contents/MacOS/Herd"

func residentRig(t *testing.T, argv0 string, age time.Duration) (*rig, *[]string) {
	r := newRig(t)
	r.argv[100] = []string{argv0}
	r.started[100] = t0.Add(-age)
	var diags []string
	r.d.Diag = func(source, problem string) {
		if problem != "" {
			diags = append(diags, source+": "+problem)
		}
	}
	return r, &diags
}

// Herd at 0.2 cores and 1.05 GB, up for days: no event, and the diagnostic says why.
// Mutation gates: the check left out → the event fires (red); no diagnostic → red.
func TestE1_AResidentAppOverOnlyInMemoryRaisesNoEvent(t *testing.T) {
	r, diags := residentRig(t, herdPath, 4*24*time.Hour)
	r.procs = herd(0.2, 1058)
	assert.Empty(t, r.run(3*time.Minute))
	require.Len(t, *diags, 1)
	assert.Contains(t, (*diags)[0], "Herd")
	assert.Contains(t, (*diags)[0], "resident app")
}

// The same tree is an event when any one of the four conditions is not so.
// Mutation gates: each guard dropped in turn → its case fires no event (red).
func TestE1_AResidentLookingTreeThatIsNotStillRaisesTheEvent(t *testing.T) {
	cases := []struct {
		name  string
		argv0 string
		age   time.Duration
		procs func() []resources.Proc
		known bool // the start time is known
	}{
		{"a young app", herdPath, 5 * time.Minute, func() []resources.Proc { return herd(0.2, 1058) }, true},
		{"just under ten windows", herdPath, 500 * time.Second, func() []resources.Proc { return herd(0.2, 1058) }, true},
		{"not of /Applications", "/Users/x/bin/server", 4 * 24 * time.Hour, func() []resources.Proc { return herd(0.2, 1058) }, true},
		{"its start is unknown", herdPath, 0, func() []resources.Proc { return herd(0.2, 1058) }, false},
		{"it burns cores", herdPath, 4 * 24 * time.Hour, func() []resources.Proc { return herd(2.5, 1058) }, true},
		{"not a child of launchd", herdPath, 4 * 24 * time.Hour, func() []resources.Proc {
			ps := herd(0.2, 1058)
			ps[1].PPID = 10 // below a shell
			return append(ps, p(10, 1, "zsh", 0, 5))
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := residentRig(t, c.argv0, c.age)
			if !c.known {
				delete(r.started, 100)
			}
			r.procs = c.procs()
			if c.name == "not a child of launchd" {
				r.argv[100] = []string{c.argv0}
			}
			ev := r.run(3 * time.Minute)
			require.NotEmpty(t, ev, "an event is raised")
			assert.Equal(t, KindUnmanagedHeavy, ev[0].Kind)
		})
	}
}

// A resident app's event is made once its CPU crosses, though its memory alone was quiet: the rule is about memory only.
func TestE1_AResidentAppThatLaterBurnsCoresIsReported(t *testing.T) {
	r, _ := residentRig(t, herdPath, 4*24*time.Hour)
	r.procs = herd(0.2, 1058)
	assert.Empty(t, r.run(3*time.Minute))
	r.procs = herd(2.5, 1058)
	assert.NotEmpty(t, r.run(3*time.Minute))
}

// The episode that fired on memory and was set aside does not hold the cores back: a resident app that burns cores from the very next
// pass is reported once the window of cores has been seen. Mutation gate: the episode kept → no event, ever (red).
func TestE1_AResidentAppThatBurnsCoresRightAfterTheSkippedEventIsReported(t *testing.T) {
	r, _ := residentRig(t, herdPath, 4*24*time.Hour)
	r.procs = herd(0.2, 1058)
	for i := 0; i < 20 && r.now.Sub(t0) <= 61*time.Second; i++ {
		assert.Empty(t, r.pass()) // up to and including the pass that sets the memory event aside
	}
	r.procs = herd(2.5, 1058)
	assert.NotEmpty(t, r.run(3*time.Minute))
}
