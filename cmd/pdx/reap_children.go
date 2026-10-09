package main

import (
	"errors"
	"syscall"
)

// reapInheritedChildren collects the children the PREVIOUS image of this process left behind (#2137).
//
// A restart is an exec in place, so the pid never changes and every child the old image had started and was
// still waiting for becomes a child of the new image that nobody waits for: os/exec's Wait goroutines died with
// the old image, and a child that exits afterwards stays a zombie of this pid for as long as the daemon lives
// (68 of them in three days on mlab, their start times matching the restarts to the second — each is a `ps` or
// `tmux` call that was in flight when the exec happened).
//
// It must run before this image starts a child of its own: wait4(-1) would take the exit status away from an
// os/exec Wait that owns it. WNOHANG ends the loop at the first child that is still running (or none left), so
// it never blocks, and an interrupted call is retried. It returns how many it collected. No lock is held.
func reapInheritedChildren() int {
	n := 0
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil || pid <= 0: // ECHILD: none left; 0: some left, none exited yet
			return n
		}
		n++
	}
}
