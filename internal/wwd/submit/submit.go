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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/gitenv"
	"github.com/wake/purdex/internal/wwd/manifest"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ManifestPath is the manifest's place in a repo.
const ManifestPath = ".wwd/tasks.toml"

// UsageError is a refusal that is the caller's to fix (no manifest, unknown task, a bad argument, a class above the
// manifest's): `pdx test` exits 4.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// ErrNoCommit: the repository has no commit, so a local run cannot be recorded against one (the run itself needs none).
var ErrNoCommit = errors.New("the repository has no commit to record a local run against")

func usagef(format string, a ...any) error { return &UsageError{Err: fmt.Errorf(format, a...)} }

// IsUsage reports whether err is a UsageError.
func IsUsage(err error) bool {
	var u *UsageError
	return errors.As(err, &u)
}

// Body is the POST /v1/jobs body. The repo's common git directory, which the origin uses to check and pin the snapshot (spec §4.1)
// and never repeats to a peer, is part of the wire submission itself (WWD-3b-1b).
type Body = wire.JobSubmission

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
	// Commit, when set (and not Local), is the snapshot: that commit of the repository, not a commit of the working tree. The
	// qualification coordinator runs the commit a clone's manifest_ref names (#3658).
	Commit string
	// NoCache: the origin must not answer this call from a stored result (`pdx test --no-cache`).
	NoCache bool
	// AdHoc, when not nil, is an undeclared command (spec §8.3): its words are the whole argv, there is no manifest task, and the
	// record is local (Local is implied), marked adhoc, with no locks, labels, cache or supersede key.
	AdHoc []string
}

// Prepared is a submission ready to send, with what the caller needs to decide how to run it.
type Prepared struct {
	Body     Body
	Task     *manifest.Task
	Manifest *manifest.Manifest
	Root     string // the repo's top level
	// Note is something the caller should tell the user about the record ("" when there is nothing).
	Note string
}

// WorkingManifest is the manifest of the repo that holds dir, read from the working tree (what `pdx test --list` shows). A
// problem with the repo or the file is a UsageError.
func WorkingManifest(ctx context.Context, dir, gitBin string) (*manifest.Manifest, string, error) {
	g := &gitRunner{bin: gitBin, dir: dir}
	if g.bin == "" {
		g.bin = "git"
	}
	root, err := g.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", usagef("not inside a git repository: %v", err)
	}
	m, err := manifest.Load(filepath.Join(root, ManifestPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, root, usagef("%s has no %s", root, ManifestPath)
		}
		return nil, root, usagef("%v", err)
	}
	return m, root, nil
}

// Prepare reads the manifest, validates the request and takes the snapshot.
func Prepare(ctx context.Context, req Request) (*Prepared, error) {
	if req.AdHoc != nil {
		req.Local, req.Task, req.Args = true, manifest.AdHocName, req.AdHoc
	}
	g := &gitRunner{bin: req.Git, dir: req.Dir}
	if g.bin == "" {
		g.bin = "git"
	}
	root, err := g.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, usagef("not inside a git repository: %v", err)
	}
	var snap snapshot.SnapshotInfo
	var src []byte
	if req.Local {
		// The fast lane runs in the working tree as it is: no snapshot is taken, the record names HEAD, and the manifest is
		// the file in the tree (the command runs against that tree).
		head, err := g.run(ctx, "rev-parse", "--verify", "-q", "HEAD^{commit}")
		if err != nil || head == "" {
			if g.unborn(ctx) {
				return nil, &UsageError{Err: ErrNoCommit}
			}
			// a HEAD that cannot be resolved for any other reason (damaged objects, permissions) is not "no commit": the run must
			// not go on unrecorded and past the lock table
			return nil, fmt.Errorf("cannot resolve HEAD: %v", err)
		}
		snap = snapshot.SnapshotInfo{SHA: head, Root: root, Rel: "."}
		if req.AdHoc == nil { // an undeclared command needs no manifest
			if src, err = os.ReadFile(filepath.Join(root, ManifestPath)); err != nil {
				return nil, usagef("%s has no %s", root, ManifestPath)
			}
		}
	} else {
		var err error
		if req.Commit != "" {
			sha, err := g.run(ctx, "rev-parse", "--verify", "-q", "--end-of-options", req.Commit+"^{commit}")
			if err != nil || sha == "" {
				return nil, fmt.Errorf("snapshot: %q is not a commit of %s", req.Commit, root)
			}
			snap = snapshot.SnapshotInfo{SHA: sha, Root: root, Rel: "."}
		} else if snap, err = snapshot.SnapshotTree(ctx, req.Git, req.Dir); err != nil {
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
	var m *manifest.Manifest
	var task *manifest.Task
	if req.AdHoc != nil {
		task = manifest.AdHocTask()
		if task.Cwd, err = RelDir(root, req.Dir); err != nil { // it runs in the directory it was asked from
			return nil, err
		}
	} else {
		var perr error
		if m, perr = manifest.Parse(src); perr != nil {
			return nil, usagef("%v", perr)
		}
		if task = m.Tasks[req.Task]; task == nil {
			return nil, usagef("task %q is not in %s (have: %s)", req.Task, ManifestPath, strings.Join(taskNames(m), ", "))
		}
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
			check.Exists = func(rel string) bool { return TreeFileExists(root, rel) }
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
	note := ""
	if req.Local {
		// The record names HEAD, but the run is of the working tree: its tree is what the record holds, HEAD's when nothing differs.
		// Never HEAD's tree for a tree that could not be taken: that would say a run was of a state it was not. The run goes on with
		// no tree recorded, and says so.
		if wt, werr := snapshot.WorkingTree(ctx, req.Git, req.Dir); werr != nil {
			tree, note = "", fmt.Sprintf("the working tree could not be recorded (the record has no tree): %v", werr)
		} else {
			tree = wt
		}
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
		NoCache:       req.NoCache,
		DefHash:       cacheableHash(req, task),
		TimeoutS:      int(task.Timeout.Seconds()),
		SupersedeKey:  adHocFree(req.AdHoc, supersedeKey(root, task.Name)),
		AdHoc:         req.AdHoc != nil,
		Interruptible: task.Interruptible,
		QueueTimeoutS: int(task.QueueTimeout.Seconds()),
		Submitter:     req.Submitter,
		GitDir:        gitDir,
	}
	if err := sub.Validate(); err != nil {
		return nil, err
	}
	return &Prepared{Body: sub, Task: task, Manifest: m, Root: root, Note: note}, nil
}

// adHocFree drops a supersede key for an undeclared command: nothing is known about what a newer one would replace.
// cacheableHash is the definition hash a stored result is compared on; a run that is never answered from one (undeclared or local) sends
// none, so the field is left out of its body and an older wwd that does not know it still decodes the fast lane's.
func cacheableHash(req Request, task *manifest.Task) string {
	if req.AdHoc != nil || req.Local {
		return ""
	}
	return task.DefinitionHash()
}

func adHocFree(adhoc []string, key string) string {
	if adhoc != nil {
		return ""
	}
	return key
}

// relCwd is the directory the command runs in, relative to the repo root: the task's cwd (the caller's own directory does
// not decide what a task means).
func relCwd(_, taskCwd string) string {
	if taskCwd == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(taskCwd))
}

// TreeFileExists reports whether rel (a slash path relative to root) is a regular file in the working tree whose real location,
// after every symlink on the way is followed, is still inside the tree: a link out of the tree is not a file of it.
func TreeFileExists(root, rel string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	if r, err := filepath.Rel(realRoot, real); err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return false
	}
	st, err := os.Stat(real)
	return err == nil && st.Mode().IsRegular()
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

// ResolveClass is the class a call runs at: the task's, or the lower one asked for; a higher one is a UsageError. The fast lane
// checks it too, whether or not the run is recorded.
func ResolveClass(max, asked wire.JobClass) (wire.JobClass, error) { return resolveClass(max, asked) }

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

// unborn reports whether HEAD names a branch that has no commit yet: HEAD is a symbolic ref and that ref is absent (git exits 1
// for exactly that; any other failure is not an unborn branch).
func (g *gitRunner) unborn(ctx context.Context) bool {
	ref, err := g.run(ctx, "symbolic-ref", "-q", "HEAD")
	if err != nil || ref == "" {
		return false
	}
	_, err = g.run(ctx, "show-ref", "--verify", "-q", ref)
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1
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
