package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The previous image's children (#2137).
//
// A restart is an exec in place, so the pid never changes and every child the old image had started and was
// still waiting for becomes a child of the new image that nobody waits for: os/exec's Wait goroutines died with
// the old image, and a child that exits afterwards stays a zombie of this pid for as long as the daemon lives
// (68 of them in three days on mlab: a `tmux wait-for` the shutdown killed but had not yet reaped when the exec
// came, and calls in flight at the exec).
//
// Two steps, both before this image starts a child of its own and neither holding a lock:
//   - reapInheritedChildren collects the ones that already exited (wait4(-1, WNOHANG) in a loop: -1 is safe only
//     now, since later it would take the exit status away from an os/exec Wait that owns it), and returns the
//     pids of the ones still running;
//   - reapSurvivors then waits for exactly those pids (wait4(pid, WNOHANG): targeted, so it can never touch a
//     child of this image), once a second, until each has been collected.

// reapInheritedChildren returns how many exited children it collected and the pids of the children that are
// still running. It never blocks.
func reapInheritedChildren() (reaped int, survivors []int) {
	listed := directChildren() // before the loop: the zombies it collects are in it too
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil || pid <= 0: // ECHILD: none left; 0: some left, none exited yet
			for _, p := range listed {
				// listed but not collected above: still running, or exited since the loop ended
				if collected, alive := probeChild(p); collected {
					reaped++
				} else if alive {
					survivors = append(survivors, p)
				}
			}
			return reaped, survivors
		}
		reaped++
		// a pid collected by the loop is no longer a child: probeChild answers "gone" for it
	}
}

// probeChild asks wait4(pid, WNOHANG): collected says it had exited and has been collected just now; alive that
// it is still a running child; neither: it is not our child (any more).
func probeChild(pid int) (collected, alive bool) {
	for {
		var ws syscall.WaitStatus
		got, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil:
			return false, false // ECHILD
		case got == pid:
			return true, false
		default:
			return false, true
		}
	}
}

// directChildren lists the pids whose parent is this process, through ps. The ps child it starts is waited for
// by os/exec and is not in the result.
func directChildren() []int {
	cmd := exec.Command("/bin/ps", "-A", "-o", "pid=,ppid=")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil || ppid != self || pid == cmd.Process.Pid {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// reapSurvivors collects each of pids once it has exited, polling every interval for at most maxWait; it
// returns how many it collected (maxWait 0: until every one has been collected, however long that takes — one
// wait4 per pid per second). Only the given pids are waited for (they are the previous image's children,
// nobody else waits for them, and a pid cannot be reused while its zombie is uncollected).
func reapSurvivors(pids []int, interval, maxWait time.Duration, logf func(string, ...any)) int {
	n := 0
	deadline := time.Now().Add(maxWait)
	for len(pids) > 0 && (maxWait == 0 || time.Now().Before(deadline)) {
		time.Sleep(interval)
		rest := pids[:0]
		for _, p := range pids {
			collected, alive := probeChild(p)
			switch {
			case collected:
				n++
			case alive:
				rest = append(rest, p)
			}
		}
		pids = rest
	}
	if len(pids) > 0 {
		logf("startup: %d child process(es) of the previous image still running after %v; no longer watched", len(pids), maxWait)
	}
	return n
}
