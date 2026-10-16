package main

import (
	"os/exec"
	"sync/atomic"
)

// hookForks counts the child processes this `pdx hook` run has started (tmux, ps). The CLI lives for one event, so the count
// is sent with it (hookPayload.Forks) and the daemon keeps it under "hook-cli"; a counter held here would die with the process.
var hookForks atomic.Int64

// outputCounted is cmd.Output() that counts the fork once the child has started: a command that could not be started (no tmux,
// no ps) did not fork and is not counted.
func outputCounted(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.Output()
	if cmd.Process != nil {
		hookForks.Add(1)
	}
	return out, err
}
