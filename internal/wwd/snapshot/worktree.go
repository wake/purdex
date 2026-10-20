package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gitenv"
)

// Snapshot of the working tree for an assist host (assist-dispatch plan X2a.2, spec §4.1): a commit with the tree as it is now,
// uncommitted and untracked files included, ignored ones (node_modules) left out. It is built in a temporary index, so the caller's
// own index, tree and branch are never touched.

// SnapshotInfo is where the snapshot was taken and what it is.
type SnapshotInfo struct {
	SHA  string // the commit
	Root string // the repo's top level
	Rel  string // the directory the command was run in, relative to Root ("." for the root)
	// Skipped are the untracked symlinks left out because they point out of the repository (paths relative to Root).
	Skipped []string
}

type gitRunFn func(env []string, args ...string) (string, error)

// builtTree is the tree of the working directory taken in a temporary index, and the repo it belongs to.
type builtTree struct {
	tree, root string
	run        gitRunFn
	skipped    []string // untracked symlinks left out: they lead out of the repository
}

// buildTree takes the working tree of the repo that contains dir into a temporary index and writes its tree. With quarantine the
// objects it writes (the blobs of changed and new files, the trees) go to a temporary object directory, with the repository's own as
// an alternate to read from: the repository is not written to, and the tree id is the same.
func buildTree(ctx context.Context, git, dir string, quarantine bool) (builtTree, error) {
	if git == "" {
		git = "git"
	}
	runIn := func(env []string, stdin string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(gitenv.Clean(os.Environ()), env...)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	run := func(env []string, args ...string) (string, error) { return runIn(env, "", args...) }
	// one git call for the three answers (each process start is a few ms and the whole of this is the fast lane's cost)
	where, err := run(nil, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-path", "objects", "--git-path", "index")
	if err != nil {
		return builtTree{}, err
	}
	lines := strings.Split(where, "\n")
	if len(lines) != 3 { // a path with a newline in it: git has no -z here, so ask one question at a time
		lines = lines[:0]
		for _, q := range [][]string{{"--show-toplevel"}, {"--path-format=absolute", "--git-path", "objects"}, {"--path-format=absolute", "--git-path", "index"}} {
			one, err := run(nil, append([]string{"rev-parse"}, q...)...)
			if err != nil {
				return builtTree{}, err
			}
			lines = append(lines, one)
		}
	}
	root, objects, srcIndex := lines[0], lines[1], lines[2]
	tmp, err := os.MkdirTemp("", "pdx-assist-index-")
	if err != nil {
		return builtTree{}, err
	}
	defer os.RemoveAll(tmp)
	idxPath := filepath.Join(tmp, "index")
	idx := []string{"GIT_INDEX_FILE=" + idxPath}
	if quarantine {
		qdir := filepath.Join(tmp, "objects")
		if err := os.Mkdir(qdir, 0o700); err != nil {
			return builtTree{}, err
		}
		idx = append(idx, "GIT_OBJECT_DIRECTORY="+qdir, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+objects)
	}
	// Two copies of the index: `add -A` works on one while the other is read for flagged entries (the common case has none, and the
	// wait for the answer was a fifth of the time). A flagged entry makes the first copy's result wrong, so it is made again.
	probePath := filepath.Join(tmp, "index.probe")
	snap, err := readIndex(srcIndex) // read once: both copies, and the one made again for flags, are of these bytes
	if err != nil {
		return builtTree{}, err
	}
	if snap != nil {
		if err := snap.writeTo(idxPath); err != nil {
			return builtTree{}, err
		}
		if err := snap.writeTo(probePath); err != nil {
			return builtTree{}, err
		}
	}
	probe := append([]string{"GIT_INDEX_FILE=" + probePath}, idx[1:]...)
	type probed struct {
		flagged []string
		err     error
	}
	probeDone := make(chan probed, 1)
	go func() {
		f, err := flaggedEntries(run, probe)
		probeDone <- probed{f, err}
	}()
	_, addErr := run(idx, append(tempIndexConfig(), "add", "-A", "--", root)...)
	p := <-probeDone
	if p.err != nil {
		return builtTree{}, p.err
	}
	if len(p.flagged) > 0 {
		if snap != nil {
			if err := snap.writeTo(idxPath); err != nil {
				return builtTree{}, err
			}
		}
		if err := clearFlags(runIn, idx, p.flagged); err != nil {
			return builtTree{}, err
		}
		addErr = nil
		if _, err := run(idx, append(tempIndexConfig(), "add", "-A", "--", root)...); err != nil {
			return builtTree{}, err
		}
	}
	if addErr != nil {
		return builtTree{}, addErr
	}
	skipped, err := dropOutsideLinks(run, runIn, idx, root)
	if err != nil {
		return builtTree{}, err
	}
	tree, err := run(idx, append(tempIndexConfig(), "write-tree")...) // it writes the tree cache back into the index
	if err != nil {
		return builtTree{}, err
	}
	return builtTree{tree: tree, root: root, run: run, skipped: skipped}, nil
}

// WorkingTree is the tree of the working directory of the repo that contains dir, as it is now (uncommitted and untracked files
// included, ignored ones left out): HEAD's tree when nothing differs. It writes nothing into the repository and leaves the caller's
// index alone. git is the git program ("" = git).
func WorkingTree(ctx context.Context, git, dir string) (string, error) {
	tree, _, err := WorkingTreeSkipped(ctx, git, dir)
	return tree, err
}

// WorkingTreeSkipped is WorkingTree and also the untracked symlinks it left out because they point out of the repository (an absolute
// target, or one that climbs out, directly or through another link): on another host they point at nothing.
func WorkingTreeSkipped(ctx context.Context, git, dir string) (string, []string, error) {
	b, err := buildTree(ctx, git, dir, true)
	return b.tree, b.skipped, err
}

// SnapshotTree commits the working tree of the repo that contains dir. git is the git program ("" = git).
func SnapshotTree(ctx context.Context, git, dir string) (SnapshotInfo, error) {
	b, err := buildTree(ctx, git, dir, false)
	if err != nil {
		return SnapshotInfo{}, err
	}
	tree, root, run := b.tree, b.root, b.run
	args := []string{"commit-tree", tree, "-m", "assist snapshot"}
	if head, err := run(nil, "rev-parse", "-q", "--verify", "HEAD"); err == nil && head != "" {
		args = append(args, "-p", head)
	}
	sha, err := run([]string{"GIT_AUTHOR_NAME=pdx assist", "GIT_AUTHOR_EMAIL=assist@localhost", "GIT_COMMITTER_NAME=pdx assist", "GIT_COMMITTER_EMAIL=assist@localhost"}, args...)
	if err != nil {
		return SnapshotInfo{}, err
	}
	rel := "."
	if abs, aerr := filepath.EvalSymlinks(dir); aerr == nil {
		if r, rerr := filepath.EvalSymlinks(root); rerr == nil {
			if x, xerr := filepath.Rel(r, abs); xerr == nil && !strings.HasPrefix(x, "..") {
				rel = x
			}
		}
	}
	return SnapshotInfo{SHA: sha, Root: root, Rel: rel, Skipped: b.skipped}, nil
}

// tempIndexConfig is what every git command on the temporary index runs with: the index is written whole (a split index would leave a
// shared index in the caller's git directory) and no file system monitor is asked about a repository the caller is working in.
func tempIndexConfig() []string {
	return []string{"-c", "core.splitIndex=false", "-c", "core.fsmonitor=false"}
}

// copyIndex makes dst a copy of the caller's index file src, so that what git tracks stays in the snapshot (a tracked file that the
// ignore rules match is not lost, as it was from an empty index) and git need not read every file again (the stat information of the
// entries comes along: M-S1 measured 1.5 s down to 0.25 s for 5,457 files). The copy is of the index file alone: a split index keeps its
// shared part in the git directory, which git reads from there. A repository with no index yet starts empty (no file is made).
// indexSnapshot is the bytes of the caller's index file at one moment and its file time. A copy of it keeps what git tracks in the snapshot (a
// tracked file that the ignore rules match is not lost, as it was from an empty index) and git need not read every file again (the stat
// information of the entries comes along: M-S1 measured 1.5 s down to 0.25 s for 5,457 files). The copy is of the index file alone: a split
// index keeps its shared part in the git directory, which git reads from there. Every copy of a run is made from one, so that
// the flags read from one copy are the flags of the copy `add -A` works on, whatever other git processes do to the real index.
type indexSnapshot struct {
	b []byte
	t time.Time
}

// indexReadHook, when set, runs right after the index file was read (tests change the real index there).
var indexReadHook func()

// readIndex reads the index file src; nil, nil when there is none (a repository with no index yet starts empty).
func readIndex(src string) (*indexSnapshot, error) {
	// One open file gives both the bytes and the file time: git replaces the index by renaming a new file over the path, and a stat
	// of the path after the read could report the time of a newer index than the bytes that were copied.
	f, err := os.Open(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the index of the repository (%s): %w", src, err)
	}
	b, rerr := io.ReadAll(f)
	st, serr := f.Stat()
	f.Close()
	if rerr != nil {
		return nil, fmt.Errorf("the index of the repository (%s): %w", src, rerr)
	}
	if serr != nil {
		return nil, fmt.Errorf("the index of the repository (%s): %w", src, serr)
	}
	if indexReadHook != nil {
		indexReadHook()
	}
	return &indexSnapshot{b: b, t: st.ModTime()}, nil
}

func (x *indexSnapshot) writeTo(dst string) error {
	if err := os.WriteFile(dst, x.b, 0o600); err != nil {
		return err
	}
	// The copy keeps the original's file time. git calls an entry racily clean when its file time is not older than the index
	// file's, and then reads the content instead of trusting size and time; a copy made now would be newer than every entry, and an
	// edit of the same size in the second of the last index write would read as unchanged and be missing from the snapshot.
	return os.Chtimes(dst, x.t, x.t)
}

// flaggedEntries are the paths of the index at idx that carry skip-worktree or assume-unchanged: both say "do not look at the file", and
// `add -A` passes such an entry by, while the snapshot is the tree as it is now. (A file a sparse checkout left out is then absent from
// the snapshot, as it is from the tree.)
func flaggedEntries(run gitRunFn, idx []string) ([]string, error) {
	list, err := run(idx, append(tempIndexConfig(), "ls-files", "-v", "-z")...)
	if err != nil {
		return nil, fmt.Errorf("reading the copy of the index: %w", err)
	}
	var flagged []string
	for _, e := range strings.Split(list, "\x00") {
		if len(e) > 2 && (e[0] == 'S' || (e[0] >= 'a' && e[0] <= 'z')) {
			flagged = append(flagged, e[2:]) // "<tag> <path>"
		}
	}
	return flagged, nil
}

// clearFlags clears the two flags on those entries of the index copy; the caller's own index keeps its flags. git update-index takes one
// of the two options per call.
func clearFlags(runIn func(env []string, stdin string, args ...string) (string, error), idx, flagged []string) error {
	paths := strings.Join(flagged, "\x00") + "\x00"
	for _, opt := range []string{"--no-skip-worktree", "--no-assume-unchanged"} {
		if _, err := runIn(idx, paths, append(tempIndexConfig(), "update-index", opt, "-z", "--stdin")...); err != nil {
			return err
		}
	}
	return nil
}

// dropOutsideLinks removes from the temporary index the untracked symlinks that lead out of the repository (the caller's index decides
// what is tracked: a link the repository holds stays whatever it points at) and returns their paths.
func dropOutsideLinks(run gitRunFn, runIn func(env []string, stdin string, args ...string) (string, error), idx []string, root string) ([]string, error) {
	list, err := run(nil, "-C", root, "ls-files", "-o", "--exclude-standard", "-z", "--full-name")
	if err != nil {
		return nil, fmt.Errorf("listing untracked files: %w", err)
	}
	if list == "" {
		return nil, nil
	}
	realRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		realRoot = root
	}
	var out []string
	for _, rel := range strings.Split(list, "\x00") {
		if rel == "" {
			continue
		}
		full := filepath.Join(root, rel)
		if fi, err := os.Lstat(full); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if linkLeavesRepo(realRoot, full, rel) {
			out = append(out, rel)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	paths := strings.Join(out, "\x00") + "\x00"
	if _, err := runIn(idx, paths, append(tempIndexConfig(), "update-index", "--force-remove", "-z", "--stdin")...); err != nil {
		return nil, err
	}
	return out, nil
}

// linkLeavesRepo says whether the symlink full (rel to the repository) points out of realRoot: an absolute target, a target that
// climbs out of the repository by its text, or a path that resolves outside through other links. A link that points at nothing but
// stays inside is not out.
func linkLeavesRepo(realRoot, full, rel string) bool {
	target, err := os.Readlink(full)
	if err != nil {
		return false
	}
	if filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target)) {
		return true
	}
	// resolve the longest part of the target that exists, so that a link through another link is followed to where it really goes
	p := filepath.Join(filepath.Dir(full), target)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = filepath.Join(r, rest)
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
	r, err := filepath.Rel(realRoot, p)
	return err != nil || !(r == "." || filepath.IsLocal(r))
}
