//go:build darwin

package facts

import (
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

// The real sources against the machine: the idle time must agree with ioreg's
// HIDIdleTime (the figure M-S2 measured) to within a few seconds, and the lock
// state must be readable.
func TestDarwinOwnerSourcesAgreeWithIoreg(t *testing.T) {
	src, err := DarwinOwnerSources()
	if err != nil {
		t.Fatal(err)
	}
	idle, err := src.IdleSeconds()
	if err != nil {
		t.Skipf("no window server session in this environment: %v", err)
	}
	out, err := exec.Command("/usr/sbin/ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		t.Skipf("ioreg: %v", err)
	}
	m := regexp.MustCompile(`"HIDIdleTime" = (\d+)`).FindSubmatch(out)
	if m == nil {
		t.Skip("no HIDIdleTime in ioreg output")
	}
	ns, _ := strconv.ParseFloat(string(m[1]), 64)
	want := ns / 1e9
	if d := idle - want; d > 5 || d < -5 {
		t.Errorf("idle %v s, ioreg says %v s", idle, want)
	}
	t.Logf("idle %.1f s (ioreg %.1f s)", idle, want)
	if _, err := src.Locked(); err != nil {
		t.Errorf("locked: %v", err)
	}
}
