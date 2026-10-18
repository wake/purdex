// Package jobexec is the worker's real runner.Exec: it makes a job's workspace from the snapshot, works out what to run from the
// snapshot's own manifest, and runs it (spec §5 "Workspace", "Environment", "Platform limits"). Each job gets a directory of its own under
// the work root; the repository tree is <dir>/ws, the run's files (log, stdout, report, junit) are <dir>/out, and HOME, TMPDIR and the
// XDG directories point into <dir>.
package jobexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/manifest"
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

// PerJobDB makes and removes the database of a job whose task asks for one (db = "per-job"). Create returns the connection settings under
// every name a task may read (DB_* and WP_TESTS_DB_*); the task's db_env says which set it gets. Drop is idempotent. Sweep (the loop's
// Sweeper) removes the databases of jobs this worker no longer holds.
type PerJobDB interface {
	Create(ctx context.Context, jobID string) (map[string]string, error)
	Drop(ctx context.Context, jobID string) error
}

// dropTimeout bounds the removal of a job's database when its workspace is cleaned up.
const dropTimeout = 30 * time.Second

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
	// DB is the per-job database; nil: this host has none, and a task that asks for one is rejected.
	DB PerJobDB
	// ParentEnv is what allowed environment names are read from (os.Environ()).
	ParentEnv []string
	// Procs and KillAfter are passed to run.Run (the marker sweep's process source and the TERM→KILL grace); zero values are its defaults.
	Procs     run.ProcSource
	KillAfter time.Duration
}

// Exec implements runner.Exec.
type Exec struct {
	cfg Config
	// dbJobs: the job directories whose job has a database (made in Prepare, dropped with the workspace)
	dbJobs sync.Map
}

func New(cfg Config) *Exec { return &Exec{cfg: cfg} }

// JobDir is the directory of one attempt of a job: a child of WorkRoot named by a hash of the origin, the job id and the whole claim.
// Nothing of a job's identity is in the name as it is, so an id from a hostile origin ("../x") cannot lead out of WorkRoot, and two
// origins with the same job id, or two claims with the same beginning, never share a directory (a quarantined workspace may still be
// written to).
func (e *Exec) JobDir(job wire.Job, claimID string) string {
	sum := sha256.Sum256([]byte(job.Origin + "\x00" + job.ID + "\x00" + claimID))
	return filepath.Join(e.cfg.WorkRoot, hex.EncodeToString(sum[:16]))
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
	if !wire.ValidClaimID(claimID) {
		return snapshot.Workspace{}, fmt.Errorf("jobexec: invalid claim id")
	}
	base := e.JobDir(job, claimID)
	// the job directory is made new: one that is already there is another attempt's (or a quarantined one) and is left alone
	if err := os.MkdirAll(e.cfg.WorkRoot, 0o700); err != nil {
		return snapshot.Workspace{}, err
	}
	if err := os.Mkdir(base, 0o700); err != nil {
		return snapshot.Workspace{}, fmt.Errorf("jobexec: the job directory cannot be made new: %w", err)
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
	jobID := job.ID
	if filepath.Clean(ws.Dir) != wsDir(base) {
		// Prepare and Run take the directory above the tree as the job's own (HOME, the run's files, a sandbox's writable path): a
		// workspace anywhere else would widen that to whatever contains it
		var cerr error
		if inner != nil {
			cerr = inner()
		}
		_ = os.RemoveAll(base)
		return snapshot.Workspace{}, errors.Join(fmt.Errorf("jobexec: the fetcher made %q, not %q", ws.Dir, wsDir(base)), cerr)
	}
	return snapshot.Workspace{Dir: ws.Dir, Cleanup: func() error {
		var err error
		if inner != nil {
			err = inner() // the worktree entry and the ref in the clone first
		}
		var dberr error
		if _, had := e.dbJobs.LoadAndDelete(base); had && e.cfg.DB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
			dberr = e.cfg.DB.Drop(ctx, jobID)
			cancel()
		}
		return errors.Join(err, dberr, os.RemoveAll(base))
	}}, nil
}

// Prepare checks the job against the snapshot's manifest and works out the command (package remotejob). A job that does not fit is a
// *runner.Rejected; anything else is the worker's own trouble.
func (e *Exec) Prepare(ctx context.Context, job wire.Job, ws snapshot.Workspace) (runner.Plan, error) {
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
	env := p.Env
	if p.Task.DB == "per-job" {
		dbenv, err := e.makeDB(ctx, job, filepath.Dir(ws.Dir), p.Task.DBEnv)
		if err != nil {
			return runner.Plan{}, err
		}
		env = append(append([]string(nil), env...), dbenv...)
	}
	return runner.Plan{Argv: p.Argv, Dir: p.Dir, Timeout: p.Timeout, TaskEnv: env, Summary: p.Task.Summary, OutDir: out}, nil
}

// makeDB creates the job's database and returns the entries ("NAME=value", fixed values the task's env list cannot override) of the set
// the task reads. Nothing is made for a job that did not pass the manifest's checks (this runs after them). A host with no per-job
// database rejects the job: it is not something that would work on retry here.
func (e *Exec) makeDB(ctx context.Context, job wire.Job, base, dbEnv string) ([]string, error) {
	if e.cfg.DB == nil {
		return nil, &runner.Rejected{Reason: "db_unavailable: this host has no per-job database"}
	}
	all, err := e.cfg.DB.Create(ctx, job.ID)
	if err != nil {
		return nil, fmt.Errorf("per-job database: %w", err)
	}
	e.dbJobs.Store(base, true)
	prefix := "DB_"
	if dbEnv == manifest.DBEnvWPTests {
		prefix = "WP_"
	}
	var out []string
	for k, v := range all {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Run runs the plan: dev jobs under background QoS where the host asks for it, as a systemd service where there is a Sandbox.
func (e *Exec) Run(ctx context.Context, job wire.Job, ws snapshot.Workspace, plan runner.Plan) run.Result {
	argv, _ := qos.For(job.Class, e.cfg.BackgroundDev, plan.Argv)
	return run.Run(ctx, run.Spec{
		JobID: job.ID, Argv: argv, Dir: plan.Dir, Workspace: filepath.Dir(ws.Dir), TaskEnv: plan.TaskEnv, ParentEnv: e.cfg.ParentEnv,
		Timeout: plan.Timeout, KillAfter: e.cfg.KillAfter, OutDir: plan.OutDir, Procs: e.cfg.Procs, Sandbox: e.cfg.Sandbox,
	})
}
