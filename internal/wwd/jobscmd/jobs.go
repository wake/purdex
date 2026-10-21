// Package jobscmd is `pdx jobs` and `pdx wwd` (wwd spec §8.1): looking at jobs, reading a log, cancelling a job, and the control
// side of the local wwd — its status, the pool, the worker switch, exceptions. The commands in cmd/pdx are thin wrappers.
package jobscmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/testcmd"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Exit codes: the same numbers `pdx test` uses for the same meaning.
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitNoWWD  = testcmd.ExitNoWWD
	ExitUsage  = testcmd.ExitUsage
	// ExitConflict is a write that --expect refused: the settings had changed since the etag was read. It is not 3, which here means
	// wwd is not running.
	ExitConflict = 7
)

// API is the part of the wwd client these commands use; *client.Client is one.
type API interface {
	Info(ctx context.Context) (wire.Info, error)
	Pool(ctx context.Context) (wire.Pool, error)
	Jobs(ctx context.Context, f client.JobsQuery) ([]wire.Job, error)
	Job(ctx context.Context, id string) (wire.Job, error)
	Log(ctx context.Context, id string) ([]byte, error)
	Cancel(ctx context.Context, id string) error
	SetPosition(ctx context.Context, id string, p wire.Position) (wire.Job, error)
	WaitJob(ctx context.Context, id string, onUpdate func(wire.Job)) (wire.Job, error)
	Worker(ctx context.Context) (wire.WorkerSettings, error)
	// WorkerOn and SetWorkerOn act on the named host through the local wwd, which forwards (spec §10); "" is this host. SetWorkerOn with
	// a non-empty expect changes nothing unless it is the etag of the settings in force (a *client.StatusError 409 otherwise).
	WorkerOn(ctx context.Context, host string) (wire.WorkerView, error)
	SetWorkerOn(ctx context.Context, host string, s wire.WorkerSettings, expect string) (wire.WorkerView, error)
	StartException(ctx context.Context, e wire.Exception) error
	// StartExceptionOn and StopExceptionOn act on the named host through the local wwd, which forwards (spec §10); "" is this host.
	StartExceptionOn(ctx context.Context, host string, e wire.Exception) error
	StopException(ctx context.Context, name string) error
	StopExceptionOn(ctx context.Context, host, name string) error
	Usage(ctx context.Context, q client.UsageQuery) (wire.UsageResponse, error)
	EarnedLabels(ctx context.Context) (list wire.EarnedLabelList, known bool, err error)
	QuarantineLabel(ctx context.Context, label string) error
	ReleaseLabel(ctx context.Context, label string) error
	Qualify(ctx context.Context, task string) (wire.QualifyStarted, error)
	QualifyStatus(ctx context.Context) (wire.QualifyStatus, error)
}

// Env is what the commands need from their surroundings.
type Env struct {
	API     API
	Session string // the caller's agent session; `pdx jobs` lists its jobs unless --all
	Now     func() time.Time
	Stdout  io.Writer
	Stderr  io.Writer
	// Log, when set, gets one line for each hold made (who asked is the host it ran on; why is the reason): `pdx wwd hold` keeps the full
	// reason there, since the exception's name holds only a slug of it.
	Log io.Writer
	// Sleep and HoldWait are for `pdx wwd hold`'s wait for the facts: Sleep nil waits on the clock (and stops with ctx), HoldWait 0 is 30 s.
	Sleep    func(time.Duration)
	HoldWait time.Duration
}

func (e Env) holdWait() time.Duration {
	if e.HoldWait > 0 {
		return e.HoldWait
	}
	return defaultHoldWait
}

func (e Env) sleep(ctx context.Context, d time.Duration) {
	if e.Sleep != nil {
		e.Sleep(d)
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

const JobsUsage = `usage: pdx jobs [ls] [--all] [--state <state>] [--limit N] [--json]
       pdx jobs show <id> [--json]
       pdx jobs log <id> [--raw]
       pdx jobs cancel <id> [--wait]
       pdx jobs move <id> front|back|hold|normal
  ls      the jobs of this session (--all: every job on this host), newest first; 20 unless --limit
  show    one job: state, where it ran, the summary
  log     the stored log (404 until the job has ended); control characters are shown as \xNN, --raw writes the bytes as they are
  cancel  ask for the job to end; --wait waits for it to
  move    put a queued job by hand: front (before every other job), back (after every other job), hold (no worker is handed it) or
          normal (the score decides again); only a queued job, on this host
`

// Jobs is `pdx jobs`; it returns the exit code.
func Jobs(ctx context.Context, args []string, env Env) int {
	sub := "ls"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "ls":
		return jobsLs(ctx, args, env)
	case "show":
		return jobsShow(ctx, args, env)
	case "log":
		return jobsLog(ctx, args, env)
	case "cancel":
		return jobsCancel(ctx, args, env)
	case "move":
		return jobsMove(ctx, args, env)
	case "help":
		io.WriteString(env.Stdout, JobsUsage)
		return ExitOK
	}
	fmt.Fprintf(env.Stderr, "pdx jobs: unknown command %q\n\n%s", sub, JobsUsage)
	return ExitUsage
}

// fail maps an error of the wwd client to a message and an exit code.
func fail(env Env, what string, err error) int {
	var se *client.StatusError
	switch {
	case errors.Is(err, client.ErrUnavailable):
		fmt.Fprintln(env.Stderr, "pdx: wwd is not running on this host")
		return ExitNoWWD
	case errors.As(err, &se) && se.Code == http.StatusNotFound:
		fmt.Fprintf(env.Stderr, "pdx %s: not found\n", what)
		return ExitUsage
	case errors.As(err, &se) && se.Code == http.StatusBadRequest:
		fmt.Fprintf(env.Stderr, "pdx %s: %s\n", what, strings.TrimSpace(se.Body))
		return ExitUsage
	}
	fmt.Fprintf(env.Stderr, "pdx %s: %v\n", what, err)
	return ExitFailed
}

// splitFlags separates `--name value` / `--name=value` / bare flags from positional words. bools lists the flags without a value.
func splitFlags(args []string, bools ...string) (flags map[string]string, pos []string, err error) {
	isBool := map[string]bool{}
	for _, b := range bools {
		isBool[b] = true
	}
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") || a == "--" {
			if a == "--" {
				pos = append(pos, args[i+1:]...)
				break
			}
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		switch {
		case isBool[name]:
			if hasVal {
				return nil, nil, fmt.Errorf("--%s takes no value", name)
			}
			flags[name] = "true"
		case hasVal:
			flags[name] = val
		case i+1 < len(args):
			i++
			flags[name] = args[i]
		default:
			return nil, nil, fmt.Errorf("--%s needs a value", name)
		}
	}
	return flags, pos, nil
}

func only(flags map[string]string, allowed ...string) error {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for f := range flags {
		if !ok[f] {
			return fmt.Errorf("unknown option --%s", f)
		}
	}
	return nil
}

func usageErr(env Env, usage string, err error) int {
	fmt.Fprintf(env.Stderr, "pdx: %v\n\n%s", err, usage)
	return ExitUsage
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// ---- ls ----

const defaultLimit = 20

func jobsLs(ctx context.Context, args []string, env Env) int {
	flags, pos, err := splitFlags(args, "all", "json")
	if err == nil {
		err = only(flags, "all", "json", "state", "limit")
	}
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	limit := defaultLimit
	if err == nil && flags["limit"] != "" {
		if _, e := fmt.Sscanf(flags["limit"], "%d", &limit); e != nil || limit < 1 {
			err = fmt.Errorf("--limit must be a positive number")
		}
	}
	q := client.JobsQuery{}
	if err == nil && flags["state"] != "" {
		q.State = wire.JobState(flags["state"])
		if !q.State.Valid() {
			err = fmt.Errorf("--state %q is not a job state", flags["state"])
		}
	}
	if err != nil {
		return usageErr(env, JobsUsage, err)
	}
	if flags["all"] == "" {
		q.Session = env.Session
	}
	jobs, err := env.API.Jobs(ctx, q)
	if err != nil {
		return fail(env, "jobs", err)
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	more := 0
	if len(jobs) > limit {
		more, jobs = len(jobs)-limit, jobs[:limit]
	}
	if flags["json"] != "" {
		writeJSON(env.Stdout, jobs)
		return ExitOK
	}
	if len(jobs) == 0 {
		fmt.Fprintln(env.Stdout, "no jobs")
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTASK\tSTATE\tCLASS\tHOST\tAGE")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", testcmd.CleanLine(j.ID), testcmd.CleanLine(j.Task), j.State, j.Class, testcmd.CleanLine(hostOf(j)), age(env.now(), j.CreatedAt))
	}
	tw.Flush()
	if more > 0 {
		fmt.Fprintf(env.Stdout, "… and %d more (--limit)\n", more)
	}
	return ExitOK
}

// hostOf is where the job ran, or is running, or "-" while nobody has it.
func hostOf(j wire.Job) string {
	switch {
	case j.Result != nil && j.Result.Host != "":
		return j.Result.Host
	case j.Claim != nil:
		return j.Claim.Worker
	}
	return "-"
}

func age(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// ---- show, log, cancel ----

func oneID(env Env, args []string, usage string, bools ...string) (id string, flags map[string]string, code int) {
	flags, pos, err := splitFlags(args, bools...)
	if err == nil && len(pos) != 1 {
		err = fmt.Errorf("a job id is needed")
	}
	if err != nil {
		return "", nil, usageErr(env, usage, err)
	}
	return pos[0], flags, 0
}

func jobsShow(ctx context.Context, args []string, env Env) int {
	id, flags, code := oneID(env, args, JobsUsage, "json")
	if code != 0 {
		return code
	}
	if err := only(flags, "json"); err != nil {
		return usageErr(env, JobsUsage, err)
	}
	j, err := env.API.Job(ctx, id)
	if err != nil {
		return fail(env, "jobs show", err)
	}
	if flags["json"] != "" {
		writeJSON(env.Stdout, j)
		return ExitOK
	}
	r := testcmd.ReportOfJob(j)
	testcmd.Print(env.Stdout, r, false)
	by := j.Submitter.Session
	if by == "" {
		by = "-"
	}
	fmt.Fprintf(env.Stdout, "class %s · from %s (session %s) · submitted %s ago\n", j.Class, testcmd.CleanLine(j.Origin), testcmd.CleanLine(by), age(env.now(), j.CreatedAt))
	if j.Claim != nil {
		fmt.Fprintf(env.Stdout, "claimed by %s (attempt %d)\n", testcmd.CleanLine(j.Claim.Worker), j.Claim.Attempt)
	}
	return ExitOK
}

func jobsLog(ctx context.Context, args []string, env Env) int {
	id, flags, code := oneID(env, args, JobsUsage, "raw")
	if code != 0 {
		return code
	}
	if err := only(flags, "raw"); err != nil {
		return usageErr(env, JobsUsage, err)
	}
	b, err := env.API.Log(ctx, id)
	if err != nil {
		var se *client.StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			fmt.Fprintf(env.Stderr, "pdx jobs log: no log stored for %s (it is stored when the job ends)\n", id)
			return ExitFailed
		}
		return fail(env, "jobs log", err)
	}
	if flags["raw"] == "" {
		b = []byte(testcmd.Clean(string(b))) // a log is output of other programs: control characters are shown, not sent to the terminal
	}
	_, _ = env.Stdout.Write(b)
	return ExitOK
}

func jobsCancel(ctx context.Context, args []string, env Env) int {
	id, flags, code := oneID(env, args, JobsUsage, "wait")
	if code != 0 {
		return code
	}
	if err := only(flags, "wait"); err != nil {
		return usageErr(env, JobsUsage, err)
	}
	if err := env.API.Cancel(ctx, id); err != nil {
		return fail(env, "jobs cancel", err)
	}
	if flags["wait"] == "" {
		fmt.Fprintf(env.Stdout, "%s: cancel requested (a running job ends at the worker's next heartbeat)\n", id)
		return ExitOK
	}
	j, err := env.API.WaitJob(ctx, id, nil)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(env.Stderr, "pdx jobs cancel: stopped waiting; the cancel is still requested for %s\n", id)
			return testcmd.ExitCancelled
		}
		return fail(env, "jobs cancel", err)
	}
	fmt.Fprintf(env.Stdout, "%s: %s\n", id, j.State)
	return ExitOK
}

func jobsMove(ctx context.Context, args []string, env Env) int {
	if len(args) != 2 {
		return usageErr(env, JobsUsage, errors.New("move needs a job id and front, back, hold or normal"))
	}
	id, p := args[0], wire.Position(args[1])
	if !p.ValidRequest() {
		return usageErr(env, JobsUsage, fmt.Errorf("%q is not front, back, hold or normal", args[1]))
	}
	j, err := env.API.SetPosition(ctx, id, p)
	if err != nil {
		return fail(env, "jobs move", err)
	}
	place := string(j.Manual)
	if place == "" {
		place = "normal"
	}
	fmt.Fprintf(env.Stdout, "%s: %s\n", id, place)
	return ExitOK
}
