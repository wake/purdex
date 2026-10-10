package main

import (
	"errors"
	"fmt"
	"syscall"
)

// killProcessGroup SIGKILLs the whole process group `pdx start` put the daemon in (Setpgid: the pgid is the child's
// pid), so a daemon that failed to come up leaves no grandchildren behind (#958). A group with no member left is not
// an error. It runs after the child exited too, which is when stragglers matter; the pid cannot name a group of
// someone else unless it was recycled into a new group leader within milliseconds. The tmux server a daemon starts
// leaves the group on its own (it daemonizes), so this does not reach the user's tmux (see the test).
func killProcessGroup(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal process group %d", pid)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
