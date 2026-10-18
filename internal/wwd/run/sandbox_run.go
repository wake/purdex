package run

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

var (
	// stopRetry is how often a stop is sent again while systemd-run is still around: a stop that came before the unit existed
	// found nothing, and the client goes on to start the service afterwards.
	stopRetry = time.Second
	// stopGrace is how long past the kill grace the client gets to end after the stops, before it is killed.
	stopGrace = 10 * time.Second
	// settle is how long, after the client is gone, the unit gets to be seen inactive or gone.
	settle = 15 * time.Second
)

// runSandboxed is Run for a Sandbox: the same log files, the outcome from the unit. When the unit could not be proven stopped
// after a cancel or a deadline, Err is set: the caller must treat that as an infrastructure failure and must not reuse the
// workspace.
func runSandboxed(ctx context.Context, spec Spec, env []string, prog string, cp *capture, res Result) Result {
	sb := spec.Sandbox.withDefaults()
	unit := unitName(spec.JobID)
	killAfter := spec.KillAfter
	if killAfter <= 0 {
		killAfter = DefaultKillAfter
	}
	// A unit of this name left from an earlier run of the same job (a lease lapse, then a re-run) would be read as this run's
	// result: clear it first, and refuse to start next to one that is still running.
	// A failure to ask, or to clear, is a failure to start: a result read next to a unit that could not be cleared could belong to
	// the earlier run.
	before, err := sb.show(unit)
	if err == nil && activeState(before["ActiveState"]) {
		err = fmt.Errorf("run: unit %s is already %s", unit, before["ActiveState"])
	}
	if err == nil && before["LoadState"] != "not-found" {
		// systemctl refuses reset-failed for a unit that is not loaded (exit 1): there is nothing to clear then
		err = sb.control(unit, "reset-failed")
	}
	if err != nil {
		res.Tail, res.LogCut, res.CaptureErr = cp.close()
		return failStart(res, err)
	}
	// the invocation the unit has now, if it is loaded still; a failed query here would leave no base to compare with
	if before, err = sb.show(unit); err != nil {
		res.Tail, res.LogCut, res.CaptureErr = cp.close()
		return failStart(res, err)
	}

	exitFile := filepath.Join(spec.Workspace, ".wwd-exit")
	_ = os.Remove(exitFile)
	argv := sandboxArgv(sb, unit, spec.Workspace, spec.Dir, exitFile, spec.Timeout, killAfter, env, prog, spec.Argv[1:])
	cmd := execstat.Command("wwd.run.sandbox", sb.SudoPath, argv...)
	cmd.Dir = spec.Dir
	cmd.Stdin = nil
	cmd.Stdout = cp.Stdout()
	cmd.Stderr = cp.Stderr()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = pipeDrain

	res.StartedAt = time.Now()
	if err := cmd.Start(); err != nil {
		res.Tail, res.LogCut, res.CaptureErr = cp.close()
		return failStart(res, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var deadline <-chan time.Time
	if spec.Timeout > 0 {
		t := time.NewTimer(spec.Timeout)
		defer t.Stop()
		deadline = t.C
	}

	stop := stopNone
	var waitErr, runErr error
	select {
	case waitErr = <-done:
	case <-deadline:
		stop = stopDeadline
	case <-ctx.Done():
		stop = stopCancel
	}
	if stop != stopNone {
		waitErr, runErr = endUnit(sb, unit, cmd.Process.Pid, done, killAfter)
	}
	show, showErr := sb.show(unit)
	if showErr != nil && runErr == nil {
		runErr = fmt.Errorf("run: cannot read unit %s: %w", unit, showErr)
	}
	if st := show["ActiveState"]; stop == stopNone && activeState(st) {
		// the client went away but the service is still there
		stop = stopCancel
		_, runErr = endUnit(sb, unit, 0, nil, killAfter)
		if runErr == nil {
			runErr = fmt.Errorf("run: systemd-run ended but unit %s was still %s", unit, st)
		}
	}
	if stop == stopNone {
		switch {
		case show["LoadState"] == "not-found":
			// A unit that ended well is unloaded by systemd as soon as it is inactive (only a failed one stays to be read; measured
			// on mserv). What the command's wrapper left in the exit file is then the answer. systemd-run's own exit 0 is not: a
			// unit stopped from outside also leaves it with 0.
			code, got := readExit(exitFile)
			switch {
			case waitErr != nil:
				show = map[string]string{} // classify reads this as "no unit": nothing was started
			case got && code == 0:
				show = map[string]string{"Result": "success", "ExecMainStatus": "0"}
			case got:
				show = map[string]string{"Result": "exit-code", "ExecMainStatus": strconv.Itoa(code)}
			default:
				stop = stopCancel
				if runErr == nil {
					runErr = fmt.Errorf("run: unit %s ended and left no exit status: it was stopped from outside", unit)
				}
			}
		case show["InvocationID"] == "" || show["InvocationID"] == before["InvocationID"]:
			// The Result must belong to the invocation this run started: a unit that is still there with the invocation it had
			// before is an old one.
			show = map[string]string{}
		}
	}
	_ = sb.control(unit, "reset-failed") // after reading Result; harmless when the unit is already gone
	res.FinishedAt = time.Now()
	res.Tail, res.LogCut, res.CaptureErr = cp.close()

	var startErr error
	res.Outcome, res.ExitCode, res.Signal, res.Reason, startErr = classify(show, stop)
	switch {
	case startErr != nil:
		res.Err = startErr
		if waitErr != nil {
			res.Err = fmt.Errorf("%w (systemd-run: %v)", startErr, waitErr)
		}
	case runErr != nil:
		res.Err = runErr
	}
	return res
}

// endUnit stops the unit and waits for the systemd-run client (done; nil when there is none) to end, sending the stop again
// every stopRetry: killing the client leaves the service running, and a stop sent before the unit existed found nothing. Past
// killAfter+stopGrace the client is killed, one more stop is sent, and the unit is polled until it is inactive or gone; if it
// is not, the error says so. It returns the client's Wait error.
func endUnit(sb Sandbox, unit string, clientPid int, done <-chan error, killAfter time.Duration) (waitErr, err error) {
	limit := time.After(killAfter + stopGrace)
	tick := time.NewTicker(stopRetry)
	defer tick.Stop()
	_ = sb.control(unit, "stop")
	for done != nil {
		select {
		case waitErr = <-done:
			done = nil
		case <-tick.C:
			_ = sb.control(unit, "stop")
		case <-limit:
			_ = syscall.Kill(-clientPid, syscall.SIGKILL)
			waitErr = <-done
			done = nil
		}
	}
	for end := time.Now().Add(settle); ; {
		m, err := sb.show(unit)
		st := m["ActiveState"]
		// stopped is only what systemd said: an answer that failed or that has no state proves nothing
		if err == nil && st != "" && !activeState(st) {
			return waitErr, nil
		}
		if !time.Now().Before(end) {
			return waitErr, fmt.Errorf("run: unit %s is not proven stopped after the stop (state %q, query error %v): the job may still be running", unit, st, err)
		}
		_ = sb.control(unit, "stop")
		time.Sleep(stopRetry)
	}
}

// activeState says whether a unit's ActiveState means "something may be running".
func activeState(st string) bool {
	return st == "active" || st == "activating" || st == "deactivating" || st == "reloading"
}

// readExit reads the exit status the wrapper left; got is false when there is none.
func readExit(path string) (code int, got bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// control runs `sudo systemctl <verb> <unit>`; the callers that cannot do anything about a failure ignore it.
func (sb Sandbox) control(unit, verb string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return execstat.CommandContext(ctx, "wwd.run.sandbox", sb.SudoPath, "systemctl", verb, unit).Run()
}

// show reads the unit's properties; a unit that does not exist answers ActiveState=inactive, LoadState=not-found. The error is
// the query's own failure (systemctl missing, timed out, refused).
func (sb Sandbox) show(unit string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := execstat.CommandContext(ctx, "wwd.run.sandbox", sb.SystemctlPath, "show", unit, "-p", "ActiveState,LoadState,InvocationID,Result,ExecMainStatus,ExecMainCode")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return parseShow(out.String()), err
}
