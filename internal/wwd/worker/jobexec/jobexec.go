// Package jobexec is the worker's real runner.Exec: it makes a job's workspace from the snapshot, works out what to run from the
// snapshot's own manifest, and runs it (spec §5 "Workspace", "Environment", "Platform limits"). Each job gets a directory of its own under
// the work root; the repository tree is <dir>/ws, the run's files (log, stdout, report, junit) are <dir>/out, and HOME, TMPDIR and the
// XDG directories point into <dir>.
package jobexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/qos"
	"github.com/wake/purdex/internal/wwd/worker/remotejob"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// Fetcher makes the workspace of a claimed job at dir (a path that is not there yet): from another host's snapshot over TCP, or, for
// this host's own queue, a checkout of its repository. clone is the host's own clone of the job's repository ("" = none).
type Fetcher interface {
	Fetch(ctx context.Context, job wire.Job, claimID, clone, dir string) (snapshot.Workspace, error)
}

// Config is what Exec needs from the worker.
type Config struct {
	// WorkRoot holds one directory per job.
	WorkRoot string
	// Fetchers gives the Fetcher of a job's origin (Job.Origin); nil: that origin cannot be fetched from.
	Fetchers func(origin string) Fetcher
	// ClonePath is the host's own clone of a repository, by name.
	ClonePath func(repo string) (string, bool)
	// BackgroundDev runs dev jobs under background QoS (mlab; spec §5 Platform limits).
	BackgroundDev bool
	// Sandbox runs commands as transient systemd services (Linux hosts); nil: a process group.
	Sandbox *run.Sandbox
	// ParentEnv is what allowed environment names are read from (os.Environ()).
	ParentEnv []string
	// Procs and KillAfter are passed to run.Run (the marker sweep's process source and the TERM→KILL grace); zero values are its defaults.
	Procs     run.ProcSource
	KillAfter time.Duration
}

// Exec implements runner.Exec.
type Exec struct{ cfg Config }

func New(cfg Config) *Exec { return &Exec{cfg: cfg} }

// JobDir is the directory of one attempt of a job. The claim is part of the name: a later attempt of the same job never meets what an
// earlier one left (a quarantined workspace may still be written to).
func (e *Exec) JobDir(job wire.Job, claimID string) string {
	short := claimID
	if len(short) > 8 {
		short = short[:8]
	}
	return filepath.Join(e.cfg.WorkRoot, job.ID+"-"+short)
}

func wsDir(base string) string  { return filepath.Join(base, "ws") }
func outDir(base string) string { return filepath.Join(base, "out") }

// Materialize makes <JobDir>/ws. The returned Cleanup removes the whole job directory (the tree and the run's files), so that nothing of
// a job stays behind once its result is delivered.
func (e *Exec) Materialize(ctx context.Context, job wire.Job, claimID string) (snapshot.Workspace, error) {
	var f Fetcher
	if e.cfg.Fetchers != nil {
		f = e.cfg.Fetchers(job.Origin)
	}
	if f == nil {
		return snapshot.Workspace{}, fmt.Errorf("jobexec: no way to fetch a snapshot from origin %q", job.Origin)
	}
	base := e.JobDir(job, claimID)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return snapshot.Workspace{}, err
	}
	clone := ""
	if e.cfg.ClonePath != nil {
		clone, _ = e.cfg.ClonePath(job.Repo.Name)
	}
	ws, err := f.Fetch(ctx, job, claimID, clone, wsDir(base))
	if err != nil {
		_ = os.RemoveAll(base)
		return snapshot.Workspace{}, err
	}
	inner := ws.Cleanup
	return snapshot.Workspace{Dir: ws.Dir, Cleanup: func() error {
		var err error
		if inner != nil {
			err = inner() // the worktree entry and the ref in the clone first
		}
		return errors.Join(err, os.RemoveAll(base))
	}}, nil
}

// Prepare checks the job against the snapshot's manifest and works out the command (package remotejob). A job that does not fit is a
// *runner.Rejected; anything else is the worker's own trouble.
func (e *Exec) Prepare(_ context.Context, job wire.Job, ws snapshot.Workspace) (runner.Plan, error) {
	out := outDir(filepath.Dir(ws.Dir))
	if err := os.MkdirAll(out, 0o700); err != nil {
		return runner.Plan{}, err
	}
	p, err := remotejob.Prepare(job, ws.Dir, out)
	if err != nil {
		var rej *remotejob.Rejection
		if errors.As(err, &rej) {
			return runner.Plan{}, &runner.Rejected{Reason: rej.Reason()}
		}
		return runner.Plan{}, err
	}
	return runner.Plan{Argv: p.Argv, Dir: p.Dir, Timeout: p.Timeout, TaskEnv: p.Env, Summary: p.Task.Summary, OutDir: out}, nil
}

// Run runs the plan: dev jobs under background QoS where the host asks for it, as a systemd service where there is a Sandbox.
func (e *Exec) Run(ctx context.Context, job wire.Job, ws snapshot.Workspace, plan runner.Plan) run.Result {
	argv, _ := qos.For(job.Class, e.cfg.BackgroundDev, plan.Argv)
	return run.Run(ctx, run.Spec{
		JobID: job.ID, Argv: argv, Dir: plan.Dir, Workspace: filepath.Dir(ws.Dir), TaskEnv: plan.TaskEnv, ParentEnv: e.cfg.ParentEnv,
		Timeout: plan.Timeout, KillAfter: e.cfg.KillAfter, OutDir: plan.OutDir, Procs: e.cfg.Procs, Sandbox: e.cfg.Sandbox,
	})
}
