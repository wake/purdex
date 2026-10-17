// Package fastlane is the local fast lane of `pdx test` (spec §5.2): a task that allows it runs at once in the caller's own
// working tree — no snapshot, no queue, no other host — holding one of the gate's light slots, and leaves a job record with the
// result. Single-file tests always take this lane and never leave the host (D12). The job is registered with the local wwd
// before the command starts, so the locks it holds are visible to the worker on this host (§4.1, §5 Locks), and a lock that is
// busy sends the call to the normal queue instead of failing it.
package fastlane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/summary"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrNotLocal: the task, or this many arguments, may not take the fast lane (the manifest's `local`).
var ErrNotLocal = errors.New("fastlane: the task does not allow the local fast lane for these arguments")

// ErrLocksBusy: another job on this host holds a lock the task needs. The caller sends the task through the queue.
var ErrLocksBusy = errors.New("fastlane: a lock of the task is held by another job on this host")

// ExitTimeout is the exit status for a command that ran out of time (spec §8.1).
const ExitTimeout = 124

// Gater runs a command under a gate slot; *gate.Gate is one.
type Gater interface {
	Run(ctx context.Context, o gate.Options) (int, error)
}

// Recorder registers a fast-lane run with the local wwd (its fast-lane entry checks the locks against every job on this host
// and records the run as running). Register returns ErrLocksBusy when a lock is taken.
type Recorder interface {
	Register(ctx context.Context, body submit.Body) (Handle, error)
}

// Handle is one registered run. Its lease is renewed like a worker's (spec §4.2): a lease that cannot be renewed ends the
// command, and a lease that lapses is never requeued (a local run is not run again by a worker).
type Handle interface {
	JobID() string
	// Lease is how long a renewal keeps the run; RenewEvery how often to renew.
	Lease() time.Duration
	RenewEvery() time.Duration
	// Heartbeat renews the lease; cancel is true when the job was cancelled (pdx jobs cancel).
	Heartbeat(ctx context.Context) (cancel bool, err error)
	// Finish stores the outcome; the log file is uploaded with it.
	Finish(ctx context.Context, o Outcome) error
}

// Outcome is what a finished run reports to its record.
type Outcome struct {
	State      wire.JobState
	Exit       *int
	Summary    wire.Summary
	Reason     string
	LogPath    string
	StartedAt  time.Time
	FinishedAt time.Time
}

// Request is one fast-lane run.
type Request struct {
	Root string         // the repo's top level (the caller's working tree)
	Task *manifest.Task // the declared task
	Args []string
	// Body is the record to register (submit.Prepare with Local set).
	Body submit.Body
	Gate Gater
	// Recorder may be nil: the run is then not recorded and holds no lock.
	Recorder Recorder
	Env      []string // the caller's environment; the command keeps it (warm caches are the point of the lane)
	// OutDir holds the log and report files; a temporary directory when empty (removed by the caller via Result.Cleanup).
	OutDir string
}

// Result is how the run ended.
type Result struct {
	JobID    string
	State    wire.JobState
	Exit     int // the command's exit status; ExitTimeout when it ran out of time
	Summary  wire.Summary
	Reason   string
	LogPath  string
	Err      error // a failure of the lane itself (the gate refused, the record could not be made): no test result
	Cleanup  func()
	SumErr   error // why the summary could not be built, when it could not
	FinishAt time.Time
}

// Run runs the task in req.Root now. ErrNotLocal and ErrLocksBusy come back as the error with nothing run.
func Run(ctx context.Context, req Request) (Result, error) {
	task := req.Task
	if !task.Local.Permits(len(req.Args)) {
		return Result{}, ErrNotLocal
	}
	out := req.OutDir
	cleanup := func() {}
	if out == "" {
		d, err := os.MkdirTemp("", "wwd-fastlane-")
		if err != nil {
			return Result{}, err
		}
		out = d
		cleanup = func() { os.RemoveAll(d) }
	}
	res := Result{Cleanup: cleanup}
	fail := func(err error) (Result, error) { cleanup(); return Result{}, err }

	report, junit := filepath.Join(out, "report.json"), filepath.Join(out, "junit.xml")
	argv, err := task.Render(req.Args, manifest.RenderEnv{
		Workspace: out, Report: report, Junit: junit,
		Exists: func(rel string) bool { return submit.TreeFileExists(req.Root, rel) },
	})
	if err != nil {
		return fail(err)
	}

	env := withTaskEnv(req.Env, task.Env)
	var handle Handle
	if req.Recorder != nil {
		h, err := req.Recorder.Register(ctx, req.Body)
		if err != nil {
			return fail(err)
		}
		handle = h
		res.JobID = h.JobID()
		env = withMark(env, h.JobID())
	}

	cp, err := run.OpenCapture(filepath.Join(out, "log"), filepath.Join(out, "stdout"), 0)
	if err != nil {
		finishFailed(handle, "infra", time.Now())
		return fail(err)
	}
	// The task's time starts when the gate admits the command, not while it waits for a slot: the gate prints a mark on the
	// command's stderr right before it starts the command (AdmittedMarkEnv), and that line is taken out of the output here.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var admitted, timedOut atomic.Bool
	var timer atomic.Pointer[time.Timer]
	nonce := newNonce()
	env = append(env, gate.AdmittedMarkEnv+"="+nonce)
	stderr := &admitFilter{w: cp.Stderr(), mark: gate.AdmittedMark(nonce), onAdmitted: func() {
		admitted.Store(true)
		timer.Store(time.AfterFunc(task.Timeout, func() { timedOut.Store(true); stop() }))
	}}
	// The lease: renewed while the command runs and until the result is delivered; cancelled by the origin, or not renewable
	// for a whole lease, ends the command.
	var cancelled, leaseLost atomic.Bool
	hbCtx, hbStop := context.WithCancel(context.WithoutCancel(ctx))
	defer hbStop()
	hbDone := make(chan struct{})
	if handle != nil {
		go func() {
			defer close(hbDone)
			heartbeat(hbCtx, handle, &cancelled, &leaseLost, stop)
		}()
	} else {
		close(hbDone)
	}
	started := time.Now()
	code, gateErr := req.Gate.Run(runCtx, gate.Options{
		Argv: argv, Class: gate.ClassLight, ClassSource: "kind", Env: env,
		Dir:     filepath.Join(req.Root, filepath.FromSlash(task.Cwd)),
		WaitMax: task.QueueTimeout,
		Stdout:  cp.Stdout(), Stderr: stderr,
	})
	finished := time.Now()
	if t := timer.Load(); t != nil {
		t.Stop()
	}
	tail, _, capErr := cp.Close()
	res.FinishAt, res.LogPath, res.Exit = finished, filepath.Join(out, "log"), code

	notRun := !admitted.Load() && (gateErr != nil || code == gate.ExitRefused || code == gate.ExitOrphaned)
	switch {
	case leaseLost.Load():
		res.State, res.Reason, res.Exit = wire.StateFailed, "lost", -1
	case ctx.Err() != nil || cancelled.Load():
		res.State, res.Reason, res.Exit = wire.StateCancelled, "", -1
	case timedOut.Load():
		res.State, res.Reason, res.Exit = wire.StateFailed, "timeout", ExitTimeout
	case notRun && code == gate.ExitRefused && gateErr == nil && finished.Sub(started) >= task.QueueTimeout-time.Second:
		res.State, res.Reason, res.Exit = wire.StateExpired, "queue_timeout", -1 // waited for a slot as long as a queued job may
	case notRun:
		res.State, res.Reason = wire.StateFailed, "infra"
		res.Err = gateErr
		if res.Err == nil {
			res.Err = fmt.Errorf("the gate did not run the command (exit %d)", code)
		}
	case code == 0:
		res.State = wire.StateSucceeded
	default:
		res.State = wire.StateFailed
	}
	if capErr != nil && res.Err == nil && res.State != wire.StateCancelled {
		res.Err = fmt.Errorf("the output could not be stored: %w", capErr)
		res.Reason = "infra"
	}

	if res.Err == nil && res.State != wire.StateCancelled && res.State != wire.StateExpired {
		res.Summary, res.SumErr = buildSummary(task, out, report, junit, tail)
	} else {
		res.Summary = wire.Summary{Failures: []wire.Failure{}, Tail: lines(tail)}
	}
	if handle != nil {
		var exit *int
		if res.State == wire.StateSucceeded || (res.State == wire.StateFailed && res.Reason == "") || res.Reason == "timeout" {
			e := res.Exit
			exit = &e
		}
		o := Outcome{State: res.State, Exit: exit, Summary: res.Summary, Reason: res.Reason, LogPath: res.LogPath, StartedAt: started, FinishedAt: finished}
		if ferr := deliver(context.WithoutCancel(ctx), handle, o, &leaseLost); ferr != nil && res.Err == nil {
			res.Err = fmt.Errorf("the result could not be recorded: %w", ferr)
		}
	}
	hbStop()
	<-hbDone
	return res, nil
}

// How a finished run's result is delivered when the origin is briefly unreachable (spec §4.2): retried with a growing pause
// while the lease is still renewed, for up to deliverFor; a claim that is no longer valid (ErrStale, or a lease lost meanwhile)
// discards the result.
var (
	deliverFor     = 10 * time.Minute
	deliverBackoff = 500 * time.Millisecond
)

// ErrStale is what a Handle's Finish returns when the origin no longer accepts this run's claim.
var ErrStale = errors.New("fastlane: the run's claim is no longer valid")

func deliver(ctx context.Context, h Handle, o Outcome, leaseLost *atomic.Bool) error {
	deadline := time.Now().Add(deliverFor)
	pause := deliverBackoff
	for {
		err := h.Finish(ctx, o)
		if err == nil || errors.Is(err, ErrStale) {
			return err
		}
		if leaseLost.Load() {
			return fmt.Errorf("%w (the lease was lost meanwhile: %v)", ErrStale, err)
		}
		if time.Now().Add(pause).After(deadline) {
			return err
		}
		time.Sleep(pause)
		if pause *= 2; pause > 10*time.Second {
			pause = 10 * time.Second
		}
	}
}

// admitFilter takes the gate's admitted mark out of the command's stderr and says when it saw it. The gate writes the mark
// in one write before the command starts, so nothing of the command's can come before it.
type admitFilter struct {
	w          io.Writer
	mark       string
	onAdmitted func()
	seen       atomic.Bool
}

func (f *admitFilter) Write(p []byte) (int, error) {
	if !f.seen.Load() && string(p) == f.mark {
		f.seen.Store(true)
		f.onAdmitted()
		return len(p), nil
	}
	return f.w.Write(p)
}

func newNonce() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// heartbeat renews the lease every RenewEvery until ctx ends. A cancel answer, or no successful renewal for a whole lease
// (judged on this process's monotonic clock), stops the command through stop.
func heartbeat(ctx context.Context, h Handle, cancelled, leaseLost *atomic.Bool, stop context.CancelFunc) {
	every, lease := h.RenewEvery(), h.Lease()
	if every <= 0 || lease <= 0 {
		return
	}
	last := time.Now()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cancel, err := h.Heartbeat(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err == nil && cancel:
			cancelled.Store(true)
			stop()
			return
		case err == nil:
			last = time.Now()
		case time.Since(last) > lease:
			leaseLost.Store(true)
			stop()
			return
		}
	}
}

func finishFailed(h Handle, reason string, at time.Time) {
	if h != nil {
		_ = h.Finish(context.Background(), Outcome{State: wire.StateFailed, Reason: reason, StartedAt: at, FinishedAt: time.Now()})
	}
}

// withTaskEnv is the caller's environment with the task's fixed values (NAME=value entries of its env list) set over it. The
// fast lane runs in the caller's own tree and environment — warm caches and tool setup are the point of the lane — so the allow
// list of a worker's clean environment does not apply here; only values the task fixes do. Names the runner reserves
// (WWD_*, PDX_*) are never taken from a manifest.
func withTaskEnv(env, taskEnv []string) []string {
	out := append([]string(nil), env...)
	for _, e := range taskEnv {
		name, value, fixed := strings.Cut(e, "=")
		if !fixed || strings.HasPrefix(name, "WWD_") || strings.HasPrefix(name, "PDX_") {
			continue
		}
		kept := out[:0]
		for _, kv := range out {
			if !strings.HasPrefix(kv, name+"=") {
				kept = append(kept, kv)
			}
		}
		out = append(kept, name+"="+value)
	}
	return out
}

func withMark(env []string, id string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if len(kv) >= len(run.MarkEnv)+1 && kv[:len(run.MarkEnv)+1] == run.MarkEnv+"=" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, run.MarkEnv+"="+id)
}

func buildSummary(task *manifest.Task, out, report, junit string, tail []byte) (wire.Summary, error) {
	in := summary.Input{Log: tail, Root: out}
	in.Stdout, _ = os.ReadFile(filepath.Join(out, "stdout"))
	in.Report, _ = os.ReadFile(report)
	in.Junit, _ = os.ReadFile(junit)
	return summary.Parse(task.Summary, in)
}

func lines(b []byte) []string {
	s, _ := summary.Parse("plain", summary.Input{Log: b})
	return s.Tail
}
