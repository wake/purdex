package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/wake/purdex/internal/execstat"
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

// gitEnv is the environment of every git command here: the repository is the one in repoDir, whatever repository variables the
// process carries (the same reasoning as the gate's snapshot), and no prompt can stop it.
func gitEnv() []string {
	drop := []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_NAMESPACE", "GIT_PREFIX", "GIT_INTERNAL_SUPER_PREFIX", "GIT_QUARANTINE_PATH"}
	var out []string
next:
	for _, kv := range os.Environ() {
		for _, k := range drop {
			if strings.HasPrefix(kv, k+"=") {
				continue next
			}
		}
		out = append(out, kv)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0")
}

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
	cmd := execstat.CommandContext(ctx, tag, "git", append([]string{"-C", repoDir}, args...)...)
	cmd.Env = gitEnv()
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
	_, err := git(ctx, "wwd.snapshot-pin", repoDir, "update-ref", PinRef(jobID), commit, strings.Repeat("0", len(commit)))
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
	_, err := git(ctx, "wwd.snapshot-unpin", repoDir, "update-ref", "-d", PinRef(jobID))
	return err
}
