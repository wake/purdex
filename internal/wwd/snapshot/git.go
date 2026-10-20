package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/gitenv"
)

// Pin and Unpin keep a job's snapshot commit reachable in the origin's repository (WWD spec §4.1, §4.3). The commit SnapshotTree makes
// has no ref, so a `git gc` would drop it once it is old enough; a bundle also needs a ref name to be made. Pin points
// refs/wwd/<job id> at it when the job is submitted, Unpin removes the ref when the job is over. The queue (WWD-3b) calls them
// through its Pinner interface {Pin(ctx, repoDir, jobID, commit) error; Unpin(ctx, repoDir, jobID) error}: a Pin that fails fails the
// submission, because a job whose snapshot cannot be fetched must not be queued. They run git in repoDir on this host.

var (
	jobIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	commitRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`) // sha-1, or sha-256
)

// PinRef is the ref that holds a job's snapshot.
func PinRef(jobID string) string { return "refs/wwd/" + jobID }

func validJobID(id string) error {
	if !jobIDRe.MatchString(id) || strings.Contains(id, "..") || strings.HasSuffix(id, ".lock") {
		return fmt.Errorf("snapshot: %q is not a job id", id)
	}
	return nil
}

// gitEnv is the environment of every git command here: the repository is the one in repoDir, whatever repository or configuration
// variables the process carries, and no prompt can stop it.
func gitEnv() []string { return gitenv.Clean(os.Environ()) }

// refWriteEnv is gitEnv with the C locale, for the commands that write a ref (gitRefWrite): isRefLockError reads git's English error text,
// and LC_ALL wins over LANG and LC_MESSAGES (and makes git ignore LANGUAGE), so a service started in another locale still gets the words
// it looks for. Those commands run no filter that reads the locale; a checkout (worktree add) keeps the process's own. The later value of
// a name is the one the child gets.
func refWriteEnv() []string { return append(gitEnv(), "LC_ALL=C") }

// errTooLarge is what a command whose output went over its limit is: it was stopped, not waited for.
var errTooLarge = errors.New("snapshot: output over the limit")

// limitedBuffer holds what is written to it up to max bytes; the write that goes over fails, which closes the pipe the command writes
// to, so the command is stopped by its next write instead of running to its end.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		l.exceeded = true
		return 0, errTooLarge
	}
	return l.buf.Write(p)
}

// smallOutput bounds the output of the git commands whose answer is a line or two.
const smallOutput = 1 << 20

// git runs git in repoDir and returns its standard output (at most smallOutput bytes); tag names the fork for the counters.
func git(ctx context.Context, tag, repoDir string, args ...string) ([]byte, error) {
	return gitLimited(ctx, tag, repoDir, smallOutput, args...)
}

// gitLimited is git with its own limit on the output: over it, git is stopped and the error is errTooLarge.
func gitLimited(ctx context.Context, tag, repoDir string, max int, args ...string) ([]byte, error) {
	return gitRun(ctx, gitEnv(), tag, repoDir, max, args...)
}

// gitRun is gitLimited with the environment of the command given.
func gitRun(ctx context.Context, env []string, tag, repoDir string, max int, args ...string) ([]byte, error) {
	cmd := execstat.CommandContext(ctx, tag, "git", append([]string{"-C", repoDir}, args...)...)
	cmd.Env = env
	out := &limitedBuffer{max: max}
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errb
	if err := cmd.Run(); err != nil {
		if out.exceeded {
			return nil, errTooLarge
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.buf.Bytes(), nil
}

// refLockBackoff is the wait between the tries of gitRefWrite (a var so a test can shorten it).
var refLockBackoff = 100 * time.Millisecond

// refLockTries is how many times gitRefWrite tries again after a lock error: the first try and then this many.
const refLockTries = 3

// isRefLockError says whether a git error is another process holding the lock of a ref (or of packed-refs): git says "cannot lock ref" for a
// ref and "Unable to create '...lock': File exists" for the lock file itself. Nothing else is worth a second try.
func isRefLockError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "cannot lock ref") || strings.Contains(msg, "Unable to create") && strings.Contains(msg, ".lock")
}

// waitRefLock waits refLockBackoff and returns the context's error when it ended, also when the timer and the end of the context were ready
// together (select picks either one then), so a cancelled caller never starts another git.
func waitRefLock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-time.After(refLockBackoff):
	}
	return ctx.Err()
}

// gitRefWrite is git for the commands that write or delete a ref (update-ref, fetch into a ref). The clone may be shared with another git
// user, and two writers of the same ref directory or of packed-refs fail one of them with "cannot lock ref" for the moment the other holds
// the lock; that one is tried again (refLockTries times, refLockBackoff apart), any other error at once.
func gitRefWrite(ctx context.Context, tag, repoDir string, args ...string) ([]byte, error) {
	out, err := gitRun(ctx, refWriteEnv(), tag, repoDir, smallOutput, args...)
	for try := 0; try < refLockTries && isRefLockError(err); try++ {
		if werr := waitRefLock(ctx); werr != nil {
			return nil, fmt.Errorf("%w (after %v)", werr, err)
		}
		out, err = gitRun(ctx, refWriteEnv(), tag, repoDir, smallOutput, args...)
	}
	return out, err
}

// hasCommit says whether repoDir holds the commit.
func hasCommit(ctx context.Context, repoDir, commit string) bool {
	if !commitRe.MatchString(commit) {
		return false
	}
	_, err := git(ctx, "wwd.snapshot-cat", repoDir, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

// pinned is the commit refs/wwd/<jobID> points at, or "" when there is no such ref.
func pinned(ctx context.Context, repoDir, jobID string) string {
	out, err := git(ctx, "wwd.snapshot-ref", repoDir, "rev-parse", "-q", "--verify", PinRef(jobID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Pin points refs/wwd/<jobID> at commit. It is idempotent for the same commit (also when two calls race) and an error for another one
// (a job id names one snapshot for good).
func Pin(ctx context.Context, repoDir, jobID, commit string) error {
	if err := validJobID(jobID); err != nil {
		return err
	}
	if !commitRe.MatchString(commit) {
		return fmt.Errorf("snapshot: %q is not a commit id", commit)
	}
	if err := ownRepo(ctx, repoDir); err != nil {
		return err
	}
	if !hasCommit(ctx, repoDir, commit) {
		return fmt.Errorf("snapshot: %s does not have the commit %s", repoDir, commit)
	}
	if cur := pinned(ctx, repoDir, jobID); cur != "" {
		if cur != commit {
			return fmt.Errorf("snapshot: job %s is pinned to %s, not %s", jobID, cur, commit)
		}
		return nil
	}
	// the last argument is the value the ref must have now: none, so a second Pin cannot move it
	_, err := gitRefWrite(ctx, "wwd.snapshot-pin", repoDir, "update-ref", PinRef(jobID), commit, strings.Repeat("0", len(commit)))
	if err != nil && pinned(ctx, repoDir, jobID) == commit {
		return nil // another call made it first, to the same commit
	}
	return err
}

// Unpin removes the ref. A ref that is not there is not an error (an Unpin is repeated until it is sure); a repository that cannot be
// read is, so that the caller tries again.
func Unpin(ctx context.Context, repoDir, jobID string) error {
	if err := validJobID(jobID); err != nil {
		return err
	}
	if err := ownRepo(ctx, repoDir); err != nil {
		return err
	}
	_, err := gitRefWrite(ctx, "wwd.snapshot-unpin", repoDir, "update-ref", "-d", PinRef(jobID))
	return err
}

// maxPinList bounds the list of pins: a job id is 128 characters at the most, so this is tens of thousands of pins, far more than the
// retention keeps; over it ListPins fails rather than hold an unbounded answer.
const maxPinList = 8 << 20

// ListPins is the job ids that have a ref under refs/wwd/ in repoDir: what the queue's sweep compares with its jobs to find a pin whose
// job is over or unknown. Only what Pin could have made is listed, so that the sweep can never be led to remove another ref: a direct ref
// to a commit whose name is a job id (not a nested one, not one that starts with '-', not a symbolic ref, not one to a blob or a tag).
// No refs/wwd/ is an empty list; a repoDir that is not a repository (see ownRepo) is an error.
func ListPins(ctx context.Context, repoDir string) ([]string, error) {
	if err := ownRepo(ctx, repoDir); err != nil {
		return nil, err
	}
	out, err := gitLimited(ctx, "wwd.snapshot-list", repoDir, maxPinList, "for-each-ref", "--format=%(objecttype) %(symref) %(refname)", "refs/wwd/")
	if errors.Is(err, errTooLarge) {
		return nil, fmt.Errorf("snapshot: ListPins: the pins in %s are over %d bytes of names: %w", repoDir, maxPinList, err)
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line) // objecttype, the symref (empty for a direct ref: then there are two fields), refname
		if len(f) != 2 || f[0] != "commit" {
			continue
		}
		if id := strings.TrimPrefix(f[1], "refs/wwd/"); id != f[1] && validJobID(id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ownRepo says that repoDir is itself a repository: its work tree root, or a git directory (bare, or a .git). `git -C dir` walks up to
// the first repository it finds, so a path that only lies inside one (a stale or wrong record) would make Pin, Unpin and ListPins act on
// the parent, and the queue would remove a ref in a repository it did not mean.
func ownRepo(ctx context.Context, repoDir string) error {
	abs, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		return fmt.Errorf("snapshot: %s: %w", repoDir, err)
	}
	gd, err := git(ctx, "wwd.snapshot-repo", repoDir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	if g, err := filepath.EvalSymlinks(strings.TrimSpace(string(gd))); err == nil && g == abs {
		return nil // a git directory
	}
	top, err := git(ctx, "wwd.snapshot-repo", repoDir, "rev-parse", "--show-toplevel")
	if err == nil {
		if t, err := filepath.EvalSymlinks(strings.TrimSpace(string(top))); err == nil && t == abs {
			return nil // the root of a work tree
		}
	}
	return fmt.Errorf("snapshot: %s is not a repository (it lies inside one, or is not one)", repoDir)
}
