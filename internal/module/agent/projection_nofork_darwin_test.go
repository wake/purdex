package agent

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/store"
)

// c933b0-128 (from the c933b0-127 report): the identity check a projection read makes for EVERY live frame forked ps 5 to 13
// times, inside the emit slot. With the production seams it asks the kernel and forks none.
// Mutation: restore the ps-based probe.ProcessStartTime / PidAncestorIncludes → forks > 0 → red.
func TestProjectionFrameEligible_ForksNoPS(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LANG", "en_US.UTF-8")
	pid := os.Getpid()
	start, err := processStartTimeFn(pid) // also runs the one-time start-text calibration, outside the count
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(os.Getppid()), "-o", "ppid=").Output()
	if err != nil {
		t.Fatal(err)
	}
	grand, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	for name, pane := range map[string]int{"self": pid, "parent": os.Getppid(), "grandparent": grand} {
		base := execstat.Take()
		if !projectionFrameEligibleForPane(store.Frame{PID: pid, ProcessStartTime: start}, pane) {
			t.Fatalf("%s: the frame is eligible (its start time matches and the pane pid is on its chain)", name)
		}
		if d := execstat.Take().Sub(base); d.PSN != 0 {
			t.Fatalf("%s: eligibility forked ps %d times, want 0", name, d.PSN)
		}
	}
	if projectionFrameEligibleForPane(store.Frame{PID: pid, ProcessStartTime: "Thu Jan  1 00:00:00 1970"}, pid) {
		t.Fatal("a frame whose recorded start time differs is another process, not eligible")
	}
}
