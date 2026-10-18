// Package remotejob is what a worker checks about a job that came from another host, after its snapshot is on disk and before anything
// runs (wwd spec §11): the job is looked up as a task in the manifest *of the snapshot* (never the origin's copy, never a field the
// origin sent), its arguments are validated against the task's args kind, and the command is rendered to the argv that is exec'd
// directly. A job that does not fit is a *Rejection and nothing runs. It shares one renderer with the local fast lane
// (manifest.Task.Render), so both turn a task and its arguments into an argv the same way.
package remotejob

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Codes of a Rejection, fixed: the report to the origin carries them and tests pin them.
const (
	CodeNotRemote       = "not_remote"       // an ad-hoc or fast-lane job: those never leave the host that made them
	CodeManifestMissing = "manifest_missing" // the snapshot has no .wwd/tasks.toml (or it is not a regular file inside the tree)
	CodeManifestInvalid = "manifest_invalid" // the snapshot's manifest does not parse
	CodeTaskUnknown     = "task_unknown"     // the snapshot's manifest has no such task
	CodeLocalOnly       = "local_only"       // the task takes pass-through arguments: it is never run for another host
	CodeClassAbove      = "class_above_task" // the job asks for a class above the task's own
	CodeArgsInvalid     = "args_invalid"     // the arguments do not fit the task's args kind
	CodeCwdOutside      = "cwd_outside"      // the task's working directory is not a directory inside the snapshot
)

// Rejection is why a remote job is not run. It is the caller's reason to report; it is an error so a hook can return it as it is.
type Rejection struct {
	Code   string
	Detail string
}

func (r *Rejection) Error() string { return r.Reason() }

// Reason is the one line told to the origin: the code, then what was wrong.
func (r *Rejection) Reason() string {
	if r.Detail == "" {
		return r.Code
	}
	return r.Code + ": " + r.Detail
}

func reject(code, format string, args ...any) *Rejection {
	return &Rejection{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Plan is what a job that passed is run as.
type Plan struct {
	Task    *manifest.Task
	Argv    []string      // rendered: exec'd directly, never through a shell
	Dir     string        // the working directory: the snapshot root joined with the task's cwd
	Timeout time.Duration // the snapshot task's; the job's own can only shorten it
	Env     []string      // the task's env entries ("NAME" allowed through, "NAME=value" fixed); the worker applies them
	Class   wire.JobClass // what the job asked for, never above the task's
}

// Prepare checks job against the manifest in the snapshot at root and renders its command. out is a directory the run may write
// its report and junit files in ({report}, {junit}); it must be inside the worker's own workspace. A *Rejection says why a job does
// not fit; any other error is the worker's own trouble (it is not a fault of the job).
func Prepare(job wire.Job, root, out string) (*Plan, error) {
	if job.AdHoc {
		return nil, reject(CodeNotRemote, "job %s is ad-hoc", job.ID)
	}
	if job.Local {
		return nil, reject(CodeNotRemote, "job %s is a fast-lane run", job.ID)
	}
	// the manifest is a regular file of the snapshot whose real place is inside it (a link out of the tree is no file of it)
	if !submit.TreeFileExists(root, submit.ManifestPath) {
		return nil, reject(CodeManifestMissing, "%s is not in the snapshot", submit.ManifestPath)
	}
	m, err := manifest.Load(filepath.Join(root, filepath.FromSlash(submit.ManifestPath)))
	if err != nil {
		return nil, reject(CodeManifestInvalid, "%v", err)
	}
	task := m.Tasks[job.Task]
	if task == nil {
		return nil, reject(CodeTaskUnknown, "%q is not a task of the snapshot's manifest", job.Task)
	}
	if task.Args == manifest.ArgsPass {
		return nil, reject(CodeLocalOnly, "task %s takes pass-through arguments and runs only where it was asked for", task.Name)
	}
	if !job.Class.Valid() || classRank[job.Class] > classRank[task.Class] {
		return nil, reject(CodeClassAbove, "job asks for class %q, task %s allows %q at most", job.Class, task.Name, task.Class)
	}
	dir, err := cwdIn(root, task.Cwd)
	if err != nil {
		return nil, err
	}
	argv, err := task.Render(job.Args, manifest.RenderEnv{
		Workspace: out, Report: filepath.Join(out, "report.json"), Junit: filepath.Join(out, "junit.xml"),
		Exists: func(rel string) bool { return submit.TreeFileExists(root, rel) },
	})
	if err != nil {
		return nil, reject(CodeArgsInvalid, "%v", err)
	}
	timeout := task.Timeout
	if s := time.Duration(job.TimeoutS) * time.Second; s > 0 && s < timeout {
		timeout = s // the origin may shorten a run, never lengthen it
	}
	return &Plan{Task: task, Argv: argv, Dir: dir, Timeout: timeout, Env: task.Env, Class: job.Class}, nil
}

// classRank orders the classes by how much they may ask for.
var classRank = map[wire.JobClass]int{wire.ClassDev: 0, wire.ClassMerge: 1, wire.ClassRelease: 2}

// cwdIn is the absolute directory of the task's cwd inside the snapshot, after every link on the way is followed, or a rejection
// when it is not a directory of the tree.
func cwdIn(root, cwd string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("snapshot root: %w", err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(cwd)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", reject(CodeCwdOutside, "cwd %q is not in the snapshot", cwd)
		}
		return "", fmt.Errorf("task cwd: %w", err)
	}
	rel, err := filepath.Rel(realRoot, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", reject(CodeCwdOutside, "cwd %q leads out of the snapshot", cwd)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", reject(CodeCwdOutside, "cwd %q is not a directory", cwd)
	}
	return dir, nil
}
