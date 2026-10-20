// Package fastlane is the local fast lane of `pdx test` (spec §5.2): a task that allows it runs at once in the caller's own
// working tree — no snapshot, no queue, no other host — holding one of wwd's own slots (taken when the run is registered), and leaves a job record with the
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

// ErrSlotBusy: this host has no slot for the run now (wwd's own, taken when the run is registered). Nothing was run; the caller waits
// and asks again, like for ErrLocksBusy.
var ErrSlotBusy = errors.New("fastlane: this host has no slot for the run now")

// ErrUnrecorded is what a Recorder's Register returns when wwd has no way to record fast-lane runs (it does not know the route,
// yet): the run goes on without a record, and holds no lock.
var ErrUnrecorded = errors.New("fastlane: wwd cannot record fast-lane runs")

// ArgError: the arguments do not fit the task (a file that is not there, a path that leaves the tree, a flag where a file is
// expected). Nothing was run.
type ArgError struct{ Err error }

func (e *ArgError) Error() string { return e.Err.Error() }
func (e *ArgError) Unwrap() error { return e.Err }

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
	// RetakeTree, when set, is called before the registration and its tree replaces Body's (empty when it fails, the error in
	// Result.TreeErr): a run that waited for a lock or a slot is of the directory as it is when it goes on, not as it was before
	// the wait (#3735). The caller sets it from the second attempt on; the first one uses the tree Prepare took.
	RetakeTree func(ctx context.Context) (string, error)
	// TreeNote is what Prepare said about the tree it took (the record has none, and why): it stands unless RetakeTree ran, whose
	// outcome then replaces it.
	TreeNote string
	Gate     Gater
	// Recorder may be nil: the run is then not recorded and holds no lock.
	Recorder Recorder
	Env      []string // the caller's environment; the command keeps it (warm caches are the point of the lane)
	// GateClass is the slot the run takes: "" is the light slot of a declared task, an undeclared command takes a heavy one.
	GateClass gate.Class
	// TeeStdout and TeeStderr, when set, also receive the command's output as it is written (the log keeps it as well): a caller
	// that has to show the output has it live. A tee that fails or stalls never stops the command.
	TeeStdout, TeeStderr io.Writer
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
	TreeNote string // why the registered record has no tree ("" when it has one)
	SumErr   error  // why the summary could not be built, when it could not
	// Tail is the end of the command's output (the last 64 KiB at most, kept in memory whatever the log's cap was).
	Tail []byte
	// TailCut says the output is longer than Tail, so Tail starts somewhere inside a line.
	TailCut  bool
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
		return fail(&ArgError{Err: err})
	}

	env := withTaskEnv(req.Env, task.Env)
	var handle Handle
	if req.Recorder != nil {
		res.TreeNote = req.TreeNote
		if req.RetakeTree != nil {
			tree, terr := req.RetakeTree(ctx)
			req.Body.Snapshot.Tree, res.TreeNote = tree, ""
			if terr != nil {
				res.TreeNote = fmt.Sprintf("the working tree could not be recorded when the run went on (the record has no tree): %v", terr)
			}
		}
		h, err := req.Recorder.Register(ctx, req.Body)
		switch {
		case errors.Is(err, ErrUnrecorded): // runs on with no record, no job id and no mark
		case err != nil:
			return fail(err)
		default:
			handle = h
			res.JobID = h.JobID()
			env = withMark(env, h.JobID())
		}
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
	var admitted, timedOut, notStarted atomic.Bool
	var timer atomic.Pointer[time.Timer]
	nonce := newNonce()
	env = append(env, gate.AdmittedMarkEnv+"="+nonce)
	outTee, errTee := newTee(cp.Stdout(), req.TeeStdout), newTee(cp.Stderr(), req.TeeStderr)
	stderr := &admitFilter{w: errTee, admitted: gate.AdmittedMark(nonce), notStartedMark: gate.NotStartedMark(nonce),
		onAdmitted: func() {
			admitted.Store(true)
			timer.Store(time.AfterFunc(task.Timeout, func() { timedOut.Store(true); stop() }))
		},
		onNotStarted: func() { notStarted.Store(true) }}
	// The lease: renewed while the command runs and until the result is delivered; cancelled by the origin, or not renewable
	// for a whole lease, ends the command.
	var cancelled, leaseLost, stale atomic.Bool
	hbCtx, hbStop := context.WithCancel(context.WithoutCancel(ctx))
	defer hbStop()
	hbDone := make(chan struct{})
	if handle != nil {
		go func() {
			defer close(hbDone)
			heartbeat(hbCtx, handle, &cancelled, &leaseLost, &stale, stop)
		}()
	} else {
		close(hbDone)
	}
	started := time.Now()
	var waitedOut bool // the gate's own word that the wait for a slot ran out, which an exit 75 alone does not say
	code, gateErr := req.Gate.Run(runCtx, gate.Options{
		Argv: argv, Class: gateClass(req.GateClass), ClassSource: "kind", Env: env,
		Dir:     filepath.Join(req.Root, filepath.FromSlash(task.Cwd)),
		WaitMax: task.QueueTimeout, WaitedOut: &waitedOut,
		Stdout: outTee, Stderr: stderr,
	})
	finished := time.Now()
	if t := timer.Load(); t != nil {
		t.Stop()
	}
	tail, logCut, capErr := cp.Close()
	outTee.close()
	errTee.close()
	res.Tail = tail
	res.TailCut = tailCut(filepath.Join(out, "log"), len(tail), logCut)
	res.FinishAt, res.LogPath, res.Exit = finished, filepath.Join(out, "log"), code

	notRun := notStarted.Load() || (!admitted.Load() && (gateErr != nil || code == gate.ExitRefused || code == gate.ExitOrphaned))
	switch {
	case leaseLost.Load() || stale.Load():
		res.State, res.Reason, res.Exit = wire.StateFailed, "lost", -1
	case ctx.Err() != nil || cancelled.Load():
		res.State, res.Reason, res.Exit = wire.StateCancelled, "", -1
	case timedOut.Load() && !notStarted.Load(): // a command that never started cannot have run out of time
		res.State, res.Reason, res.Exit = wire.StateFailed, "timeout", ExitTimeout
	case notRun && code == gate.ExitRefused && gateErr == nil && !notStarted.Load() && waitedOut:
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
	lost := func(cause error) error {
		return fmt.Errorf("%w (the lease was lost before the result was delivered%s)", ErrStale, causeText(cause))
	}
	for {
		if leaseLost.Load() { // checked before every try: a claim that was lost is never answered for
			return lost(nil)
		}
		err := h.Finish(ctx, o)
		if err == nil || errors.Is(err, ErrStale) {
			return err
		}
		if leaseLost.Load() {
			return lost(err)
		}
		if time.Now().Add(pause).After(deadline) {
			return err
		}
		// the pause ends early when the lease is lost
		for end := time.Now().Add(pause); time.Now().Before(end) && !leaseLost.Load(); {
			time.Sleep(min(10*time.Millisecond, time.Until(end)))
		}
		if pause *= 2; pause > 10*time.Second {
			pause = 10 * time.Second
		}
	}
}

func causeText(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}

// admitFilter takes the gate's admitted mark out of the command's stderr and says when it saw it. The gate writes the mark
// in one write before the command starts, so nothing of the command's can come before it.
type admitFilter struct {
	w              io.Writer
	admitted       string
	notStartedMark string // follows the admitted mark when the command could not be started after all
	onAdmitted     func()
	onNotStarted   func()
	seenAdmitted   atomic.Bool
	seenNotStarted atomic.Bool
}

func (f *admitFilter) Write(p []byte) (int, error) {
	switch {
	case !f.seenAdmitted.Load() && string(p) == f.admitted:
		f.seenAdmitted.Store(true)
		f.onAdmitted()
		return len(p), nil
	case f.seenAdmitted.Load() && !f.seenNotStarted.Load() && string(p) == f.notStartedMark:
		f.seenNotStarted.Store(true)
		f.onNotStarted()
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
func heartbeat(ctx context.Context, h Handle, cancelled, leaseLost, stale *atomic.Bool, stop context.CancelFunc) {
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
		case errors.Is(err, ErrStale): // wwd refuses the claim: the command stops now. Delivery is not cut short: the result's own answer decides (a result committed whose reply was lost makes this heartbeat see a terminal job)
			stale.Store(true)
			stop()
			return
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

// gateClass is the slot class of a run: light unless the request names another.
func gateClass(c gate.Class) gate.Class {
	if c == "" {
		return gate.ClassLight
	}
	return c
}

// teeBuffer is how many chunks of output a live reader may fall behind before chunks are dropped for it.
const teeBuffer = 1024

// teeFlush is how long the end of a run waits for a live reader to take what is left.
const teeFlush = 2 * time.Second

// teeWriter is the writer the command's output goes to: the log first, always; and, when a live reader is given, a copy for it
// that never holds the command back. The copy goes through a buffer to a goroutine; a reader that stalls (an open pipe nobody
// reads) fills the buffer and loses chunks — the log has everything — instead of blocking the gate's copy of the output.
type teeWriter struct {
	w     io.Writer
	extra io.Writer
	ch    chan []byte
	done  chan struct{}
}

func newTee(w, extra io.Writer) *teeWriter {
	t := &teeWriter{w: w, extra: extra}
	if extra == nil {
		return t
	}
	t.ch, t.done = make(chan []byte, teeBuffer), make(chan struct{})
	go func() {
		defer close(t.done)
		for p := range t.ch {
			_, _ = t.extra.Write(p)
		}
	}()
	return t
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if t.ch != nil && n > 0 {
		select {
		case t.ch <- append([]byte(nil), p[:n]...):
		default: // the reader is behind: the log keeps it
		}
	}
	return n, err
}

// close ends the live copy: what is buffered is given to the reader for up to teeFlush, then the goroutine is left to the reader
// (a reader that never returns costs one goroutine, not the run).
func (t *teeWriter) close() {
	if t.ch == nil {
		return
	}
	close(t.ch)
	select {
	case <-t.done:
	case <-time.After(teeFlush):
	}
}

// tailCut says whether the in-memory tail of n bytes starts inside a line: the output is longer than the tail and the byte before the
// tail is not a newline. The byte is read from the log, which holds the whole output unless it hit its cap (logCut), and then the
// start of the tail cannot be told, so it counts as cut.
func tailCut(logPath string, n int, logCut bool) bool {
	st, err := os.Stat(logPath)
	if err != nil || st.Size() <= int64(n) {
		return false // the log is the tail: all of the output
	}
	if logCut {
		return true
	}
	f, err := os.Open(logPath)
	if err != nil {
		return true
	}
	defer f.Close()
	var b [1]byte
	if _, err := f.ReadAt(b[:], st.Size()-int64(n)-1); err != nil {
		return true
	}
	return b[0] != '\n'
}
