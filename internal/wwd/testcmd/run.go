package testcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// API is the part of the wwd client `pdx test` uses; *client.Client is one.
type API interface {
	Info(ctx context.Context) (wire.Info, error)
	Submit(ctx context.Context, body any) (wire.JobSubmissionResponse, error)
	WaitJob(ctx context.Context, id string, onUpdate func(wire.Job)) (wire.Job, error)
}

// Options is everything `pdx test` needs from its surroundings.
type Options struct {
	Args   Args
	Dir    string // the working directory
	API    API
	Gate   fastlane.Gater // runs a fast-lane command under a gate slot; nil disables the fast lane
	Env    []string       // the caller's environment (the fast lane's command keeps it)
	GitBin string
	// Session names the caller's agent session for the job record (cmd/pdx reads it with gate.SessionFromEnv); may be empty.
	Session string
	Ref     string
	Team    string
	Stdout  io.Writer
	Stderr  io.Writer
	// LogDir is where a fast-lane run keeps its log and report files (one directory per run); empty keeps nothing.
	LogDir string
	// Recorder registers fast-lane runs with wwd; nil runs them unrecorded (until wwd's fast-lane entry exists).
	Recorder fastlane.Recorder
	// LockWait and LockPoll replace the task's queue_timeout and the two seconds between asks while a lock keeps the fast lane
	// back (tests).
	LockWait, LockPoll time.Duration
}

// Run is `pdx test`: it returns the process exit code.
func Run(ctx context.Context, o Options) int {
	if o.Args.List {
		return list(ctx, o)
	}
	if o.Args.AdHoc {
		return adHoc(ctx, o)
	}
	info, err := o.API.Info(ctx)
	if err != nil {
		// A wwd that is not there cannot record a run, but a task that may take the fast lane still runs here: that does not leave
		// the host (spec D12).
		if errors.Is(err, client.ErrUnavailable) && o.Gate != nil {
			o.Recorder = nil
			if code, took := fastLane(ctx, o, wire.Submitter{Session: o.Session, Ref: o.Ref, Team: o.Team}); took {
				return code
			}
		}
		return failWWD(o, err)
	}
	sub := wire.Submitter{Session: o.Session, Ref: o.Ref, Team: o.Team, Host: info.Alias}

	// The fast lane: a task that allows it, for this many arguments, runs here and now.
	if o.Gate != nil {
		if code, took := fastLane(ctx, o, sub); took {
			return code
		}
	}

	p, err := submit.Prepare(ctx, submit.Request{Dir: o.Dir, Task: o.Args.Task, Args: o.Args.TaskArgs, Class: o.Args.Class, Submitter: sub, Git: o.GitBin, NoCache: o.Args.NoCache})
	if err != nil {
		return failPrepare(o, err)
	}
	if code, stop := checkPool(ctx, o, o.Args.Task, p.Body.Labels); stop {
		return code
	}
	resp, err := o.API.Submit(ctx, p.Body)
	if err != nil {
		return failSubmit(o, err)
	}
	if resp.Result != nil { // a stored result was reused: nothing ran
		r := Report{ID: resp.ID, Task: o.Args.Task, State: stateOf(*resp.Result), Exit: resp.Result.Exit, Reason: resp.Result.Reason,
			Host: resp.Result.Host, CacheHit: true, Summary: resp.Result.Summary}
		Print(o.Stdout, r, o.Args.JSON)
		return ExitCode(r)
	}
	if o.Args.Detach {
		if o.Args.JSON {
			Print(o.Stdout, Report{ID: resp.ID, Task: o.Args.Task, State: wire.StateQueued}, true)
		} else {
			fmt.Fprintln(o.Stdout, resp.ID)
		}
		return ExitOK
	}
	last := wire.JobState("")
	job, err := o.API.WaitJob(ctx, resp.ID, func(j wire.Job) {
		if j.State != last && !o.Args.JSON {
			last = j.State
			fmt.Fprintf(o.Stderr, "%s %s\n", resp.ID, j.State)
		}
	})
	if err != nil {
		if ctx.Err() != nil { // the wait was interrupted: the job goes on without its caller (D5)
			fmt.Fprintf(o.Stderr, "pdx test: stopped waiting; job %s keeps running (pdx jobs show %s, pdx jobs cancel %s)\n", resp.ID, resp.ID, resp.ID)
			return ExitCancelled
		}
		return failWWD(o, err)
	}
	r := ReportOfJob(job)
	Print(o.Stdout, r, o.Args.JSON)
	return ExitCode(r)
}

// stateOf is the state of a stored result that was reused: a result is only ever reused from a success.
func stateOf(res wire.Result) wire.JobState {
	if res.Exit != nil && *res.Exit == 0 {
		return wire.StateSucceeded
	}
	return wire.StateFailed
}

// fastLane runs the task on the local fast lane when it may. took is false when the caller should go on through the queue
// (the task or its arguments do not allow it, or a lock is busy). A usage problem is reported here and counts as taken.
func fastLane(ctx context.Context, o Options, sub wire.Submitter) (code int, took bool) {
	m, root, err := submit.WorkingManifest(ctx, o.Dir, o.GitBin)
	if err != nil {
		return failPrepare(o, err), true
	}
	task := m.Tasks[o.Args.Task]
	if task == nil || !task.Local.Permits(len(o.Args.TaskArgs)) {
		return 0, false // not a local task: the queue path says what is wrong with an unknown one
	}
	if o.Args.Detach {
		// A task that takes the fast lane never goes to the queue (spec D12), and a detached run is a job of the queue.
		fmt.Fprintln(o.Stderr, "pdx test: 單檔任務只在前景跑；要排進佇列請用 go-changed／spa-related")
		return ExitUsage, true
	}
	// the same class rule as the queue's, whether or not this run is recorded
	if _, err := submit.ResolveClass(task.Class, o.Args.Class); err != nil {
		return failPrepare(o, err), true
	}
	var body submit.Body
	note := ""
	if o.Recorder != nil {
		p, err := submit.Prepare(ctx, submit.Request{Dir: o.Dir, Task: o.Args.Task, Args: o.Args.TaskArgs, Class: o.Args.Class, Submitter: sub, Git: o.GitBin, Local: true})
		switch {
		case errors.Is(err, submit.ErrNoCommit): // nothing to record against: the run goes on unrecorded
			o.Recorder = nil
		case err != nil:
			return failPrepare(o, err), true
		default:
			body, note = p.Body, p.Note
		}
	}
	req := fastlane.Request{Root: root, Task: task, Args: o.Args.TaskArgs, Body: body, Gate: o.Gate, Recorder: o.Recorder, Env: o.Env}
	return runFast(ctx, o, task, req, o.Args.Task, note, false)
}

func list(ctx context.Context, o Options) int {
	m, root, err := submit.WorkingManifest(ctx, o.Dir, o.GitBin)
	if err != nil {
		return failPrepare(o, err)
	}
	names := make([]string, 0, len(m.Tasks))
	for n := range m.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	type row struct {
		Name  string `json:"name"`
		Class string `json:"class"`
		Args  string `json:"args"`
		Local bool   `json:"local"`
		Run   string `json:"run"`
	}
	rows := make([]row, len(names))
	for i, n := range names {
		t := m.Tasks[n]
		rows[i] = row{n, string(t.Class), string(t.Args), t.Local.Allowed, t.Run}
	}
	if o.Args.JSON {
		enc := json.NewEncoder(o.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"repo": root, "tasks": rows})
		return ExitOK
	}
	fmt.Fprintf(o.Stdout, "%s (%s)\n", root, ".wwd/tasks.toml")
	for _, r := range rows {
		lane := ""
		if r.Local {
			lane = "  local"
		}
		fmt.Fprintf(o.Stdout, "  %-16s %-8s args=%-9s%s  %s\n", r.Name, r.Class, r.Args, lane, r.Run)
	}
	return ExitOK
}

// ---- failures ----

func failWWD(o Options, err error) int {
	if errors.Is(err, client.ErrUnavailable) {
		fmt.Fprintln(o.Stderr, "pdx test: wwd is not running on this host (start it, or see pdx wwd status)")
		return ExitNoWWD
	}
	fmt.Fprintf(o.Stderr, "pdx test: %v\n", err)
	return ExitFailed
}

func failPrepare(o Options, err error) int {
	fmt.Fprintf(o.Stderr, "pdx test: %v\n", err)
	if submit.IsUsage(err) {
		return ExitUsage
	}
	return ExitFailed
}

// failSubmit maps a refusal by wwd: 400 is the caller's (a bad submission, a snapshot the git directory does not hold), 422 is
// a job no host can ever take, 503 means the queue is not available.
func failSubmit(o Options, err error) int {
	var se *client.StatusError
	if errors.As(err, &se) {
		fmt.Fprintf(o.Stderr, "pdx test: wwd refused the job (%d): %s\n", se.Code, strings.TrimSpace(se.Body))
		switch se.Code {
		case http.StatusBadRequest:
			return ExitUsage
		case http.StatusUnprocessableEntity:
			return ExitNoHost
		}
		return ExitFailed
	}
	return failWWD(o, err)
}
