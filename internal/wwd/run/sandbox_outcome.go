package run

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// stopReason says who ended the unit: nobody (it ended by itself), the worker's deadline or a cancel.
type stopReason int

const (
	stopNone stopReason = iota
	stopDeadline
	stopCancel
)

// parseShow reads the Key=Value lines of `systemctl show`.
func parseShow(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// classify turns the unit's properties into the run's outcome (spec §5: the outcome comes from Result, not from
// systemd-run's exit code, which is 1 for an OOM kill and for a timeout alike, and 0 for a stop). err is set for StartFailed.
func classify(show map[string]string, stop stopReason) (out Outcome, code int, signal, reason string, err error) {
	switch stop {
	case stopDeadline:
		return TimedOut, -1, "", "", nil
	case stopCancel:
		return Cancelled, -1, "", "", nil
	}
	status, _ := strconv.Atoi(show["ExecMainStatus"])
	sig := func() string { return syscall.Signal(status).String() }
	switch r := show["Result"]; r {
	case "success":
		return Exited, 0, "", "", nil
	case "exit-code":
		return Exited, status, "", "", nil
	case "oom-kill":
		return Exited, -1, syscall.SIGKILL.String(), "oom", nil
	case "signal", "core-dump":
		return Exited, -1, sig(), "", nil
	case "timeout":
		return TimedOut, -1, "", "timeout", nil
	case "":
		return StartFailed, -1, "", "", errors.New("run: the unit does not exist: systemd-run did not start it")
	default:
		return StartFailed, -1, "", "", fmt.Errorf("run: the unit failed to run: Result=%s", r)
	}
}
