package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

// Sandbox runs the command as a transient systemd service instead of a process group (spec §5, M-L2, Linux hosts such as
// mserv). A service, never a scope: a scope rejects the sandbox properties. The unit is named wwd-<job id>, so a failed one is
// ours to clear and mserv's failed-unit alert never sees an unnamed run-*.service; its Result is read before reset-failed.
type Sandbox struct {
	User              string   // the account the job runs as (--uid); required
	Slice             string   // default wwd.slice (no "-" in a slice name: a "-" makes a hierarchy)
	CPUQuota          string   // default 200%
	MemoryMax         string   // default 4G (with no MemoryHigh: MemoryMax alone kills at once, MemoryHigh only throttles)
	MemorySwapMax     string   // default 0
	ReadWritePaths    []string // writable besides the workspace (a composer cache)
	InaccessiblePaths []string // hidden besides /run/docker.sock, which is always hidden when it exists
	SudoPath          string   // default "sudo"
	SystemctlPath     string   // default "systemctl" (show needs no root)
}

// the "-" makes a missing path (a host with no docker) harmless: without it systemd refuses to set up the unit's namespace
const dockerSocket = "-/run/docker.sock"

func (s Sandbox) withDefaults() Sandbox {
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&s.Slice, "wwd.slice")
	def(&s.CPUQuota, "200%")
	def(&s.MemoryMax, "4G")
	def(&s.MemorySwapMax, "0")
	def(&s.SudoPath, "sudo")
	def(&s.SystemctlPath, "systemctl")
	s.InaccessiblePaths = append([]string{dockerSocket}, s.InaccessiblePaths...)
	return s
}

// unitSafe is what a job id may contain to become part of a unit name.
var unitSafe = regexp.MustCompile(`^[A-Za-z0-9_.]{1,100}$`)

func (s Sandbox) validate(jobID string) error {
	if s.User == "" {
		return errors.New("run: the sandbox needs a user")
	}
	if !unitSafe.MatchString(jobID) {
		return fmt.Errorf("run: job id %q cannot be a unit name", jobID)
	}
	return nil
}

func unitName(jobID string) string { return "wwd-" + jobID }

// sandboxArgv is the systemd-run command line (without the leading sudo). The environment is cleared with env -i and then set
// from env, so nothing of the unit's default environment reaches the job.
func sandboxArgv(sb Sandbox, unit, workspace, dir string, timeout, killAfter time.Duration, env []string, prog string, args []string) []string {
	argv := []string{"systemd-run", "--wait", "--pipe", "--uid=" + sb.User, "--unit=" + unit, "--slice=" + sb.Slice,
		"-p", "CPUQuota=" + sb.CPUQuota, "-p", "MemoryMax=" + sb.MemoryMax, "-p", "MemorySwapMax=" + sb.MemorySwapMax}
	if timeout > 0 {
		// the fuse that holds even if wwd is gone: the worker's own deadline comes first, one minute earlier
		argv = append(argv, "-p", "RuntimeMaxSec="+strconv.Itoa(ceilSeconds(timeout)+60))
	}
	argv = append(argv, "-p", "TimeoutStopSec="+strconv.Itoa(ceilSeconds(killAfter)), // TERM, then KILL after this: as on macOS
		"-p", "PrivateTmp=yes", "-p", "ProtectHome=read-only",
		"-p", "ReadWritePaths="+strings.Join(append([]string{workspace}, sb.ReadWritePaths...), " "),
		"-p", "WorkingDirectory="+dir, "-p", "NoNewPrivileges=yes",
		"-p", "InaccessiblePaths="+strings.Join(sb.InaccessiblePaths, " "))
	argv = append(argv, "env", "-i")
	argv = append(argv, env...)
	argv = append(argv, "nice", "-n", "10", "ionice", "-c3", prog)
	return append(argv, args...)
}

func ceilSeconds(d time.Duration) int {
	n := int(d / time.Second)
	if d%time.Second > 0 {
		n++
	}
	return n
}

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

// runSandboxed is Run for a Sandbox: the same log files, the outcome from the unit.
func runSandboxed(ctx context.Context, spec Spec, env []string, prog string, cp *capture, res Result) Result {
	sb := spec.Sandbox.withDefaults()
	unit := unitName(spec.JobID)
	killAfter := spec.KillAfter
	if killAfter <= 0 {
		killAfter = DefaultKillAfter
	}
	argv := sandboxArgv(sb, unit, spec.Workspace, spec.Dir, spec.Timeout, killAfter, env, prog, spec.Argv[1:])
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
	var waitErr error
	select {
	case waitErr = <-done:
	case <-deadline:
		stop = stopDeadline
	case <-ctx.Done():
		stop = stopCancel
	}
	var runErr error
	if stop != stopNone {
		// Killing the systemd-run client leaves the service running: the unit itself is stopped.
		sb.control(unit, "stop")
		select {
		case waitErr = <-done:
		case <-time.After(killAfter + 10*time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			waitErr = <-done
			runErr = fmt.Errorf("run: unit %s did not stop in time", unit)
		}
	}
	show := sb.show(unit)
	if st := show["ActiveState"]; stop == stopNone && (st == "active" || st == "activating" || st == "deactivating") {
		// the client went away but the service is still there: it is not left running
		sb.control(unit, "stop")
		runErr = fmt.Errorf("run: systemd-run ended but unit %s was still %s", unit, st)
		stop = stopCancel
	}
	sb.control(unit, "reset-failed") // after reading Result; harmless when the unit is already gone
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

// control runs `sudo systemctl <verb> <unit>` and ignores a failure: stop and reset-failed are best effort, the outcome is read
// from the unit afterwards.
func (sb Sandbox) control(unit, verb string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := execstat.CommandContext(ctx, "wwd.run.sandbox", sb.SudoPath, "systemctl", verb, unit)
	_ = cmd.Run()
}

func (sb Sandbox) show(unit string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := execstat.CommandContext(ctx, "wwd.run.sandbox", sb.SystemctlPath, "show", unit, "-p", "ActiveState,Result,ExecMainStatus,ExecMainCode")
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return parseShow(out.String())
}
