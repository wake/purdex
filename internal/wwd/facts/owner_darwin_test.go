//go:build darwin

package facts

import (
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

// idleTolerance is how far apart our reading and ioreg's may be beyond the time between them (the two sources round differently).
const idleTolerance = 2.0

// idleAttempts is how many times the readings are taken again when the owner touched the machine inside the window.
const idleAttempts = 5

// The real sources against the machine: the idle time must agree with ioreg's HIDIdleTime (the figure M-S2 measured), and the lock
// state must be readable.
//
// The two are read at different moments and the idle time is a live figure, so they are never compared bare: our reading is taken
// before and after ioreg's, and ioreg's must lie between them (idleAgrees). That holds however long ioreg took to start on a busy host
// (the old test compared one reading with a later one and failed when the fork was late), and when the owner touched the machine inside
// the window (the idle time started again from zero) the readings are not comparable and are taken again; a machine that is in use
// the whole time skips rather than fails.
func TestDarwinOwnerSourcesAgreeWithIoreg(t *testing.T) {
	src, err := DarwinOwnerSources()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.IdleSeconds(); err != nil {
		t.Skipf("no window server session in this environment: %v", err)
	}
	re := regexp.MustCompile(`"HIDIdleTime" = (\d+)`)
	for attempt := 1; attempt <= idleAttempts; attempt++ {
		before, err := src.IdleSeconds()
		if err != nil {
			t.Fatalf("idle: %v", err)
		}
		out, err := exec.Command("/usr/sbin/ioreg", "-c", "IOHIDSystem").Output()
		if err != nil {
			t.Skipf("ioreg: %v", err)
		}
		after, err := src.IdleSeconds()
		if err != nil {
			t.Fatalf("idle: %v", err)
		}
		m := re.FindSubmatch(out)
		if m == nil {
			t.Skip("no HIDIdleTime in ioreg output")
		}
		ns, _ := strconv.ParseFloat(string(m[1]), 64)
		want := ns / 1e9
		agree, comparable := idleAgrees(before, want, after, idleTolerance)
		if !comparable {
			t.Logf("attempt %d: the owner touched the machine inside the window (idle %.1f s then %.1f s); taking the readings again", attempt, before, after)
			continue
		}
		if !agree {
			t.Errorf("ioreg says %.1f s, which is outside our readings %.1f s before and %.1f s after it (tolerance %.0f s)", want, before, after, idleTolerance)
		}
		t.Logf("idle %.1f s .. %.1f s (ioreg %.1f s)", before, after, want)
		if _, err := src.Locked(); err != nil {
			t.Errorf("locked: %v", err)
		}
		return
	}
	t.Skipf("the machine was in use throughout %d attempts: no steady window to compare in", idleAttempts)
}
