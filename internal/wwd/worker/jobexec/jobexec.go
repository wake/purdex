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
	"log"
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
	"github.com/wake/purdex/internal/wwd/worker/ident"
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
// Sweeper) removes the databases of jobs this worker no longer holds. A job is named by its key, "source/id" (worker/ident): a job id is
// only unique per origin, and the databases of two jobs with one id must be two.
type PerJobDB interface {
	Create(ctx context.Context, key string) (map[string]string, error)
	Drop(ctx context.Context, key string) error
}

// dropTimeout is how long cleaning up a workspace waits for the removal of the job's database (Config.DropTimeout overrides it). The
// runner cannot finish a job before Cleanup returns, and the database manager's Drop may first wait for the manager's own lock, which a
// Sweep holds while it works, so the wait is bounded here and not left to the manager's context.
const dropTimeout = 30 * time.Second

// Config is what Exec needs from the worker.
type Config struct {
	// Host is this worker's own name (its host alias): a half of a qualification pair is accepted only when it was worked out for it.
	Host string
	// WorkRoot holds one directory per job.
	WorkRoot string
	// Fetchers gives the Fetcher of the connection a job was claimed on (ident.Ref.Source: the worker's own name for it, never the
	// Job.Origin text the other host wrote); nil: nothing can be fetched from that source.
	Fetchers func(source string) Fetcher
	// ClonePath is the host's own clone of a repository, by name.
	ClonePath func(repo string) (string, bool)
	// BackgroundDev runs dev jobs under background QoS (mlab; spec §5 Platform limits).
	BackgroundDev bool
	// Sandbox runs commands as transient systemd services (Linux hosts); nil: a process group.
	Sandbox *run.Sandbox
	// DB is the per-job database; nil: this host has none, and a task that asks for one is rejected.
	DB PerJobDB
	// DropTimeout bounds the wait for DB.Drop at cleanup; zero is dropTimeout.
	DropTimeout time.Duration
	// ParentEnv is what allowed environment names are read from (os.Environ()).
	ParentEnv []string
	// Procs and KillAfter are passed to run.Run (the marker sweep's process source and the TERM→KILL grace); zero values are its defaults.
	Procs     run.ProcSource
	KillAfter time.Duration
	// DepsDir is where the dependency cache and the package managers' download caches live (a sibling of WorkRoot, on the same volume, so
	// that a clone of installed dependencies is cheap); DepsKeep is how many installed sets are kept (2 when zero). "": a task that declares
	// deps cannot be prepared on this worker. The directory is made when a job first needs it.
	DepsDir  string
	DepsKeep int
	// DepsStoreMaxMB bounds each package manager's download cache in DepsDir; one over it is emptied before the next install (4096 when zero).
	DepsStoreMaxMB int
	// GoCacheMaxMB bounds the shared build cache of the go tasks (one per Go version, under DepsDir/gocache): over it, and with no job using
	// it, it is emptied before the next job (10240 when zero). See gocache.go.
	GoCacheMaxMB int
	// GoCachePruneHours: an entry of the shared build cache that has not been used for this long is removed (24 when zero). See gocache.go.
	GoCachePruneHours int
}

// Exec implements runner.Exec.
type Exec struct {
	cfg  Config
	deps depsState // the dependency cache and what is remembered of installs (deps.go)
	// dbJobs: the job directories whose job has a database (made in Prepare, dropped with the workspace)
	dbJobs sync.Map
	// goJobs: the job directories whose job holds the shared build cache (taken in Prepare, let go with the workspace)
	goJobs  sync.Map
	goCache goCacheState
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
func (e *Exec) Materialize(ctx context.Context, ref ident.Ref, job wire.Job, claimID string) (snapshot.Workspace, error) {
	var f Fetcher
	if e.cfg.Fetchers != nil {
		f = e.cfg.Fetchers(ref.Source)
	}
	if f == nil {
		return snapshot.Workspace{}, fmt.Errorf("jobexec: no way to fetch a snapshot from source %q", ref.Source)
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
	key := ref.Key()
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
		e.releaseGoCache(base)
		var dberr error
		if _, had := e.dbJobs.LoadAndDelete(base); had && e.cfg.DB != nil {
			dberr = e.dropBounded(key)
			if dberr != nil {
				// the runner does not look at what Cleanup returns, so say it here. Cleanup runs once (it removes the worktree too); the
				// loop's Sweeper removes the databases of jobs this worker no longer holds
				log.Printf("jobexec: could not drop the database of %s, left for the sweep: %v", key, dberr)
			}
		}
		return errors.Join(err, dberr, os.RemoveAll(base))
	}}, nil
}

// dropBounded removes the job's database and waits for it no longer than the drop timeout. A Drop that has not returned by then is left
// to finish on its own (it has the same expired context) and the error says so; the loop's Sweeper removes the database later.
func (e *Exec) dropBounded(key string) error {
	d := e.cfg.DropTimeout
	if d <= 0 {
		d = dropTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.cfg.DB.Drop(ctx, key) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("per-job database %s: drop not finished in %s: %w", key, d, ctx.Err())
	}
}

// Prepare checks the job against the snapshot's manifest and works out the command (package remotejob). A job that does not fit is a
// *runner.Rejected; anything else is the worker's own trouble.
func (e *Exec) Prepare(ctx context.Context, ref ident.Ref, job wire.Job, ws snapshot.Workspace) (runner.Plan, error) {
	out := outDir(filepath.Dir(ws.Dir))
	if err := os.MkdirAll(out, 0o700); err != nil {
		return runner.Plan{}, err
	}
	p, err := remotejob.PrepareAt(job, ws.Dir, out, e.cfg.Host)
	if err != nil {
		var rej *remotejob.Rejection
		if errors.As(err, &rej) {
			return runner.Plan{}, &runner.Rejected{Reason: rej.Reason()}
		}
		return runner.Plan{}, err
	}
	// a task that declares lockfiles gets its installed dependencies before anything else is made for it (a database, say)
	if len(p.Task.Deps) > 0 {
		if err := e.prepareDeps(ctx, ref, job, ws, p.Task.Deps, p.Env); err != nil {
			return runner.Plan{}, err
		}
		goCache := ""
		if hasGoSum(p.Task.Deps) {
			goCache = e.prepareGoCache(ctx, filepath.Dir(ws.Dir))
		}
		p.Env = append(append([]string(nil), p.Env...), depsJobEnv(ws.Dir, p.Task.Deps, p.Env, goCache)...)
	}
	env := p.Env
	if p.Task.DB == "per-job" {
		dbenv, err := e.makeDB(ctx, ref, filepath.Dir(ws.Dir), p.Task.DBEnv)
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
func (e *Exec) makeDB(ctx context.Context, ref ident.Ref, base, dbEnv string) ([]string, error) {
	if e.cfg.DB == nil {
		return nil, &runner.Rejected{Reason: "db_unavailable: this host has no per-job database"}
	}
	all, err := e.cfg.DB.Create(ctx, ref.Key())
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
func (e *Exec) Run(ctx context.Context, ref ident.Ref, job wire.Job, ws snapshot.Workspace, plan runner.Plan) run.Result {
	argv, _ := qos.For(job.Class, e.cfg.BackgroundDev, plan.Argv)
	return run.Run(ctx, run.Spec{
		JobID: job.ID, Origin: ref.Tag(), Argv: argv, Dir: plan.Dir, Workspace: filepath.Dir(ws.Dir), TaskEnv: plan.TaskEnv, ParentEnv: e.cfg.ParentEnv,
		Timeout: plan.Timeout, KillAfter: e.cfg.KillAfter, OutDir: plan.OutDir, Procs: e.cfg.Procs, Sandbox: e.sandboxOf(filepath.Dir(ws.Dir)),
	})
}
