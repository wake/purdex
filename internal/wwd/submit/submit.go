// Package submit turns "run task T with these arguments in this directory" into the submission the local wwd queue accepts
// (spec §4.1, §4.4): it reads the repo's manifest, checks the task and arguments, takes the snapshot, and fills in what the
// manifest fixes (class, labels, locks, cacheable, timeouts, interruptible, queue timeout) so the origin never rereads the
// manifest. Every git call goes through the exec helper with the clean git environment of #3199.
package submit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/gitenv"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ManifestPath is the manifest's place in a repo.
const ManifestPath = ".wwd/tasks.toml"

// UsageError is a refusal that is the caller's to fix (no manifest, unknown task, a bad argument, a class above the
// manifest's): `pdx test` exits 4.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usagef(format string, a ...any) error { return &UsageError{Err: fmt.Errorf(format, a...)} }

// IsUsage reports whether err is a UsageError.
func IsUsage(err error) bool {
	var u *UsageError
	return errors.As(err, &u)
}

// Body is the POST /v1/jobs body: the wire submission plus the repo's common git directory, which the origin uses to check
// and pin the snapshot (spec §4.1) and never repeats to a peer.
type Body struct {
	wire.JobSubmission
	GitDir string `json:"git_dir"`
}

// Request is one `pdx test` call.
type Request struct {
	Dir  string   // the caller's working directory
	Task string   // a task name of the repo's manifest
	Args []string // the task's arguments, validated against its args kind
	// Class lowers the priority below the manifest's; "" takes the manifest's.
	Class     wire.JobClass
	Submitter wire.Submitter
	Git       string // the git program ("" = git)
	// Local prepares a fast-lane record (spec §5.2): no snapshot, HEAD named in its place, the manifest read from the working
	// tree, local-only (pass) tasks allowed, never cacheable.
	Local bool
}

// Prepared is a submission ready to send, with what the caller needs to decide how to run it.
type Prepared struct {
	Body     Body
	Task     *manifest.Task
	Manifest *manifest.Manifest
	Root     string // the repo's top level
}

// Prepare reads the manifest, validates the request and takes the snapshot.
func Prepare(ctx context.Context, req Request) (*Prepared, error) {
	g := &gitRunner{bin: req.Git, dir: req.Dir}
	if g.bin == "" {
		g.bin = "git"
	}
	root, err := g.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, usagef("not inside a git repository: %v", err)
	}
	var snap gate.SnapshotInfo
	var src []byte
	if req.Local {
		// The fast lane runs in the working tree as it is: no snapshot is taken, the record names HEAD, and the manifest is
		// the file in the tree (the command runs against that tree).
		head, err := g.run(ctx, "rev-parse", "--verify", "-q", "HEAD^{commit}")
		if err != nil || head == "" {
			return nil, usagef("the repository has no commit to record a local run against")
		}
		snap = gate.SnapshotInfo{SHA: head, Root: root, Rel: "."}
		if src, err = os.ReadFile(filepath.Join(root, ManifestPath)); err != nil {
			return nil, usagef("%s has no %s", root, ManifestPath)
		}
	} else {
		var err error
		if snap, err = gate.SnapshotTree(ctx, req.Git, req.Dir); err != nil {
			return nil, fmt.Errorf("snapshot: %w", err)
		}
		if !sameDir(root, snap.Root) {
			return nil, fmt.Errorf("the repository changed while the snapshot was taken (%s, then %s)", root, snap.Root)
		}
		// The manifest comes from the snapshot, the same bytes the worker will read, never from the working tree on its own.
		out, err := g.run(ctx, "show", snap.SHA+":"+ManifestPath)
		if err != nil {
			return nil, usagef("%s has no %s", root, ManifestPath)
		}
		src = []byte(out)
	}
	m, err := manifest.Parse(src)
	if err != nil {
		return nil, usagef("%v", err)
	}
	task := m.Tasks[req.Task]
	if task == nil {
		return nil, usagef("task %q is not in %s (have: %s)", req.Task, ManifestPath, strings.Join(taskNames(m), ", "))
	}
	if task.Args == manifest.ArgsPass && !req.Local {
		return nil, usagef("task %q is local-only (args = \"pass\") and is never queued", req.Task)
	}
	class, err := resolveClass(task.Class, req.Class)
	if err != nil {
		return nil, err
	}
	// A stand-in workspace: the real one is the worker's; here only the arguments are checked.
	check := manifest.RenderEnv{Workspace: "/wwd-check", Report: "/wwd-check/report.json", Junit: "/wwd-check/junit.xml"}
	if task.Args == manifest.ArgsFiles {
		check.Exists = func(rel string) bool { return g.exists(ctx, snap.SHA, rel) }
		if req.Local {
			check.Exists = func(rel string) bool {
				st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
				return err == nil && st.Mode().IsRegular()
			}
		}
	}
	if _, err := task.Render(req.Args, check); err != nil {
		return nil, usagef("%v", err)
	}
	gitDir, err := g.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	repo, err := g.repo(ctx, gitDir)
	if err != nil {
		return nil, err
	}
	tree, err := g.run(ctx, "rev-parse", snap.SHA+"^{tree}")
	if err != nil {
		return nil, err
	}
	base := g.base(ctx, snap.SHA)
	// The answers above came from req.Dir after the snapshot: if the path was pointed at another repository meanwhile, refuse.
	// This is the last use of req.Dir, so the check covers every query.
	if again, err := g.run(ctx, "rev-parse", "--show-toplevel"); err != nil || !sameDir(again, root) {
		return nil, fmt.Errorf("the repository changed while the submission was prepared")
	}
	if again, err := g.run(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir"); err != nil || again != gitDir {
		return nil, fmt.Errorf("the git directory changed while the submission was prepared")
	}
	sub := wire.JobSubmission{
		Repo:          repo,
		Snapshot:      wire.Snapshot{Commit: snap.SHA, Tree: tree, Base: base, RelCwd: relCwd(snap.Rel, task.Cwd)},
		Task:          task.Name,
		Args:          nonNil(req.Args),
		Class:         class,
		Labels:        nonNil(task.Labels),
		Locks:         nonNil(task.Locks),
		Cacheable:     task.Cacheable && !req.Local, // a local run has no snapshot tree to key a result by
		TimeoutS:      int(task.Timeout.Seconds()),
		SupersedeKey:  supersedeKey(root, task.Name),
		Interruptible: task.Interruptible,
		QueueTimeoutS: int(task.QueueTimeout.Seconds()),
		Submitter:     req.Submitter,
	}
	if err := sub.Validate(); err != nil {
		return nil, err
	}
	return &Prepared{Body: Body{JobSubmission: sub, GitDir: gitDir}, Task: task, Manifest: m, Root: root}, nil
}

// relCwd is the directory the command runs in, relative to the repo root: the task's cwd (the caller's own directory does
// not decide what a task means).
func relCwd(_, taskCwd string) string {
	if taskCwd == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(taskCwd))
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func taskNames(m *manifest.Manifest) []string {
	names := make([]string, 0, len(m.Tasks))
	for n := range m.Tasks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var classRank = map[wire.JobClass]int{wire.ClassDev: 0, wire.ClassMerge: 1, wire.ClassRelease: 2}

// resolveClass gives the manifest's class, or the lower one the caller asked for; a higher one is refused.
func resolveClass(max, asked wire.JobClass) (wire.JobClass, error) {
	if asked == "" {
		return max, nil
	}
	if !asked.Valid() {
		return "", usagef("class %q is not dev, merge or release", asked)
	}
	if classRank[asked] > classRank[max] {
		return "", usagef("class %s is above the task's maximum %s", asked, max)
	}
	return asked, nil
}

// supersedeKey ties a worktree and a task: a newer job from the same worktree for the same task replaces an older one.
func supersedeKey(root, task string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:8]) + ":" + task
}

type gitRunner struct {
	bin string
	dir string
}

// beforeGit is called before every git command; tests use it to change the world between two queries.
var beforeGit func(args []string)

func (g *gitRunner) run(ctx context.Context, args ...string) (string, error) {
	if beforeGit != nil {
		beforeGit(args)
	}
	cmd := execstat.CommandContext(ctx, "wwd.submit.git", g.bin, append([]string{"-C", g.dir}, args...)...)
	cmd.Env = gitenv.Clean(os.Environ())
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// exists reports whether path (relative to the repo root) is in the snapshot commit.
func (g *gitRunner) exists(ctx context.Context, commit, path string) bool {
	typ, err := g.run(ctx, "cat-file", "-t", commit+":"+path)
	return err == nil && typ == "blob"
}

// repo names the repository: the first commit identifies it across clones and worktrees, the remote's last element names it.
func (g *gitRunner) repo(ctx context.Context, gitDir string) (wire.Repo, error) {
	if shallow, _ := g.run(ctx, "rev-parse", "--is-shallow-repository"); shallow == "true" {
		// a shallow boundary would read as the first commit and give the same repo another identity per clone depth
		return wire.Repo{}, usagef("a shallow clone cannot be identified by its first commit; run git fetch --unshallow")
	}
	roots, err := g.run(ctx, "rev-list", "--max-parents=0", "HEAD")
	if err != nil || roots == "" {
		return wire.Repo{}, usagef("the repository has no commit to identify it by")
	}
	list := strings.Fields(roots)
	sort.Strings(list)
	remote, _ := g.run(ctx, "config", "--get", "remote.origin.url")
	name := strings.TrimSuffix(filepath.Base(strings.TrimRight(remote, "/")), ".git")
	if i := strings.LastIndexAny(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	if remote == "" || name == "" || name == "." {
		name = filepath.Base(gitDir)
		if name == ".git" {
			name = filepath.Base(filepath.Dir(gitDir))
		}
	}
	return wire.Repo{Name: name, Root: list[0], Remote: remote}, nil
}

// base is the merge base of the snapshot with the default branch, or the snapshot's parent line's head when none is found.
func (g *gitRunner) base(ctx context.Context, commit string) string {
	for _, ref := range []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"} {
		if b, err := g.run(ctx, "merge-base", commit, ref); err == nil && b != "" {
			return b
		}
	}
	if head, err := g.run(ctx, "rev-parse", "--verify", "-q", "HEAD"); err == nil && head != "" {
		return head
	}
	return commit
}
