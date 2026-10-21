package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

const (
	// DefaultKillAfter is how long a group gets between TERM and KILL (spec §5).
	DefaultKillAfter = 10 * time.Second
	// pipeDrain is how long Wait lets output pipes drain after the command itself is gone, so a stray child that kept a pipe
	// open cannot hold the job.
	pipeDrain = 3 * time.Second
)

// Outcome says how the command ended.
type Outcome string

const (
	Exited      Outcome = "exited"       // it ended by itself: ExitCode (or Signal) says how
	TimedOut    Outcome = "timed_out"    // the deadline passed and the group was ended
	Cancelled   Outcome = "cancelled"    // the context was cancelled and the group was ended
	StartFailed Outcome = "start_failed" // the command never started (not found, workspace missing)
)

// Spec is one command to run.
type Spec struct {
	JobID string
	// Origin is the tag of the connection the job came in on (worker/ident.OriginTag of the source), put into the mark as WWD_ORIGIN;
	// "" for a run with none (the mark is then the old kind, which any origin's sweep also matches during the transition).
	Origin    string
	Argv      []string // already rendered; started directly, never through a shell
	Dir       string   // the working directory (absolute)
	Workspace string   // the workspace root; HOME, TMPDIR and XDG_* point inside it
	TaskEnv   []string // the manifest's env entries
	ParentEnv []string // what allowed names are read from (os.Environ() in the worker)
	Timeout   time.Duration
	KillAfter time.Duration // TERM to KILL; DefaultKillAfter when zero
	OutDir    string        // where log and stdout files are written (created if missing)
	MaxLog    int64         // per-file capture cap; DefaultMaxLog when zero
	// Procs finds processes by job mark for the sweep after the run; DefaultProcSource() when nil.
	Procs ProcSource
	// Sandbox, when set (Linux hosts), runs the command as a transient systemd service instead of a process group.
	Sandbox *Sandbox
}

// Result is what the run left behind.
type Result struct {
	Outcome    Outcome
	ExitCode   int    // the exit status for Exited; -1 otherwise
	Signal     string // the signal that ended it, for Exited by signal (an OOM kill shows as KILL)
	Err        error  // why a StartFailed run did not start; a sandboxed run of any Outcome can carry one that says the unit was not proven stopped (an infrastructure failure: do not reuse the workspace)
	Reason     string // "oom" or "timeout" when the sandbox's unit says so; empty otherwise
	StartedAt  time.Time
	FinishedAt time.Time
	LogPath    string // stdout and stderr in arrival order
	StdoutPath string // standard output alone (go-json reads it)
	Tail       []byte // the end of the log, for the summary
	LogCut     bool   // the log reached the capture cap
	// CaptureErr is a failure to write the log or stdout file (disk full, quota). The command's own result stands, but the
	// files and the summary built from them are incomplete: the worker treats it as an infrastructure failure (spec §5.1).
	CaptureErr error
	// Escaped are the processes found outside the process group by the job mark and ended after it: a task that has any
	// leaks processes, and the log says so. SweepErr is why the scan could not run (the job's result is unaffected).
	Escaped  []int
	SweepErr error
	// Usage is what the run used (CPU time; for a sandbox unit also the memory peak). Recording only: it is the zero value when
	// nothing was measured, and nothing reads it to decide what a run's result is.
	Usage Usage
}

// Run starts the command, waits for it, and ends its whole process group on timeout or cancel (TERM, then KILL after
// KillAfter). When it returns, no process of the group is left.
func Run(ctx context.Context, spec Spec) Result {
	res := Result{ExitCode: -1}
	env, err := BuildEnv(spec.JobID, spec.Origin, spec.Workspace, spec.TaskEnv, spec.ParentEnv)
	if err != nil {
		return failStart(res, err)
	}
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return failStart(res, fmt.Errorf("run: empty command"))
	}
	if spec.Sandbox != nil {
		if err := spec.Sandbox.validate(spec); err != nil {
			return failStart(res, err)
		}
		if err := spec.Sandbox.checkExtraPaths(); err != nil {
			return failStart(res, err)
		}
	}
	if !filepath.IsAbs(spec.Dir) {
		return failStart(res, fmt.Errorf("run: the working directory %q must be absolute", spec.Dir))
	}
	if err := os.MkdirAll(spec.OutDir, 0o700); err != nil {
		return failStart(res, err)
	}
	for _, d := range []string{"HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "PDX_GATE_DIR", "PDX_CODEX_STATE_ROOT"} {
		if v, ok := envValue(env, d); ok {
			if err := os.MkdirAll(v, 0o700); err != nil {
				return failStart(res, err)
			}
		}
	}
	prog, err := lookPath(spec.Argv[0], spec.Dir, env)
	if err != nil {
		return failStart(res, err)
	}
	res.LogPath = filepath.Join(spec.OutDir, "log")
	res.StdoutPath = filepath.Join(spec.OutDir, "stdout")
	cp, err := newCapture(res.LogPath, res.StdoutPath, spec.MaxLog)
	if err != nil {
		return failStart(res, err)
	}
	if spec.Sandbox != nil {
		return runSandboxed(ctx, spec, env, prog, cp, res)
	}

	cmd := execstat.Command("wwd.run", prog, spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = env
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
	pgid := cmd.Process.Pid // Setpgid makes the child its own group leader

	killAfter := spec.KillAfter
	if killAfter <= 0 {
		killAfter = DefaultKillAfter
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var deadline <-chan time.Time
	if spec.Timeout > 0 {
		t := time.NewTimer(spec.Timeout)
		defer t.Stop()
		deadline = t.C
	}

	res.Outcome = Exited
	var waitErr error
	select {
	case waitErr = <-done:
	case <-deadline:
		res.Outcome = TimedOut
		waitErr = endGroup(pgid, killAfter, done)
	case <-ctx.Done():
		res.Outcome = Cancelled
		waitErr = endGroup(pgid, killAfter, done)
	}
	// The leader is gone; anything it left behind in the group goes too.
	strays := syscall.Kill(-pgid, 0) == nil // someone is still in the group: what it used from here on is in no figure
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	// Then whatever left the group (a daemonized tmux server, vite, playwright) but kept the job mark.
	src := spec.Procs
	if src == nil {
		src = DefaultProcSource()
	}
	res.Escaped, res.SweepErr = sweepBounded(src, spec.Origin, spec.JobID, killAfter)
	if len(res.Escaped) > 0 {
		cp.note(escapedNote(res.Escaped))
	}
	res.FinishedAt = time.Now()
	res.Tail, res.LogCut, res.CaptureErr = cp.close()
	res.Usage = rusageUsage(cmd.ProcessState) // set once Wait returned, which every branch above waited for
	res.Usage.Leaked = strays || len(res.Escaped) > 0

	if res.Outcome == Exited {
		var ee *exec.ExitError
		switch {
		case waitErr == nil:
			res.ExitCode = 0
		case errors.As(waitErr, &ee):
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				res.Signal = ws.Signal().String()
			} else {
				res.ExitCode = ee.ExitCode()
			}
		case errors.Is(waitErr, exec.ErrWaitDelay):
			res.ExitCode = 0 // the command succeeded; only a stray held the pipes open
		default:
			res.Err = waitErr
		}
	}
	return res
}

func failStart(res Result, err error) Result {
	res.Outcome = StartFailed
	res.Err = err
	if res.FinishedAt.IsZero() {
		res.FinishedAt = time.Now()
	}
	return res
}

// endGroup sends TERM to the group and gives the whole group killAfter to go away: KILL comes only when that time has passed
// with a process still in the group, even when the leader itself left at once (a child that is still cleaning up keeps its
// grace period). It returns the leader's Wait error.
func endGroup(pgid int, killAfter time.Duration, done <-chan error) error {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(killAfter)
	var waitErr error
	leaderGone := false
	for {
		if !leaderGone {
			select {
			case waitErr = <-done:
				leaderGone = true
			default:
			}
		}
		if leaderGone && syscall.Kill(-pgid, 0) != nil { // nobody left in the group
			return waitErr
		}
		if !time.Now().Before(deadline) {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			if !leaderGone {
				waitErr = <-done
			}
			return waitErr
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// LookPath is lookPath for a caller outside the package that needs to start a program as a job would find it (the worker asking a tool
// its version for a cache key): the PATH of env, not that of this process.
func LookPath(name, dir string, env []string) (string, error) { return lookPath(name, dir, env) }

// lookPath finds the program the way the command's own environment would: a name with a slash is relative to dir, a bare name
// is searched in the PATH of env, not in the PATH of the process that runs the job.
func lookPath(name, dir string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		p := name
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		return p, checkExec(p)
	}
	path, _ := envValue(env, "PATH")
	for _, d := range filepath.SplitList(path) {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		p := filepath.Join(d, name)
		if checkExec(p) == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("run: %q not found in PATH: %w", name, exec.ErrNotFound)
}

func checkExec(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st.IsDir() || st.Mode()&0o111 == 0 {
		return fmt.Errorf("run: %s is not an executable file", p)
	}
	return nil
}
