package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaterializeIn says what to make a workspace of.
type MaterializeIn struct {
	Client  *Client
	JobID   string
	ClaimID string
	Commit  string // the snapshot commit
	Base    string // the job's snapshot.base: the merge-base the snapshot was made against ("" = none)
	Clone   string // the worker's own clone of the repository ("" = none: a cold host)
	Dir     string // the workspace to make: a path that is not there yet
}

// Workspace is a directory holding the snapshot's tree. Cleanup removes it, and the ref and worktree entry it left in the clone.
type Workspace struct {
	Dir     string
	Cleanup func() error
}

// Materialize makes the workspace of a job (WWD spec §5 "Workspace"). With a clone that holds the job's base it asks the origin for a
// bundle of have..commit (a few KB: `have` is the clone's newest commit on the base's line, then the base itself), fetches it into the
// clone and adds a detached worktree at Dir; a clone without the base, a `have` the origin does not know, or a bundle over the limit
// falls back to a tar of the commit, unpacked into Dir. Either way the tree is checked to be the commit that was asked for. A claim
// that is not live is ErrStaleClaim, whatever was done before.
func Materialize(ctx context.Context, in MaterializeIn) (Workspace, error) {
	if in.Client == nil || validJobID(in.JobID) != nil || !commitRe.MatchString(in.Commit) || in.Dir == "" {
		return Workspace{}, errors.New("snapshot: Materialize needs a client, a job id, a commit and a directory")
	}
	if _, err := os.Lstat(in.Dir); err == nil {
		return Workspace{}, fmt.Errorf("snapshot: %s is already there", in.Dir)
	}
	if in.Clone != "" && in.Base != "" && commitRe.MatchString(in.Base) {
		if ws, err := fromClone(ctx, in); err == nil {
			return ws, nil
		} else if !errors.Is(err, errUseTar) {
			return Workspace{}, err
		}
	}
	return fromTar(ctx, in)
}

// maxBundleDownload is the most of a bundle the worker writes to disk, whatever the origin sends: a little over the origin's own limit.
// (A variable so that a test can lower it.)
var maxBundleDownload = 288 << 20

// errUseTar says that the bundle way did not work out for a reason a tar fixes.
var errUseTar = errors.New("snapshot: use a tar")

// fetchBaseTimeout bounds the fetch that a missing base asks for: a remote that does not answer holds a job's start for this long and no
// longer, and the tar follows. (A variable so that a test can lower it.)
var fetchBaseTimeout = 30 * time.Second

// fetchClone is fetchCloneFromOrigin (a variable so that a test can count the calls).
var fetchClone = fetchCloneFromOrigin

// fetchCloneFromOrigin brings the clone up to date with its origin, for the one reason fromClone has: the job's base is a commit pushed after the
// clone was last fetched (manifest_ref fetches it every 10 minutes, and a release gate runs seconds after its push). Without it the
// workspace of the same task would be a worktree or a tar, with or without a .git, by the minute it started in (#3729, #3730). It fetches
// the way manifest_ref does (`fetch --quiet --no-tags -- origin`). When another fetch of the same clone (manifest_ref's, every 10 minutes)
// holds a ref lock, this one does not give up after gitRefWrite's few short tries: that other fetch is bringing in the same commits, and
// giving up on the first long one would be the tar this exists to avoid. It waits refLockBackoff between tries for as long as the deadline
// (fetchBaseTimeout) allows, and any other error ends it at once. The error is only the reason the tar follows.
func fetchCloneFromOrigin(ctx context.Context, clone string) error {
	ctx, cancel := context.WithTimeout(ctx, fetchBaseTimeout)
	defer cancel()
	for {
		_, err := gitRun(ctx, refWriteEnv(), "wwd.snapshot-fetch-base", clone, smallOutput, "fetch", "--quiet", "--no-tags", "--", "origin")
		if err == nil || !isRefLockError(err) {
			return err
		}
		if werr := waitRefLock(ctx); werr != nil {
			return fmt.Errorf("%w (after %v)", werr, err)
		}
	}
}

func fromClone(ctx context.Context, in MaterializeIn) (Workspace, error) {
	if ownRepo(ctx, in.Clone) != nil {
		return Workspace{}, errUseTar
	}
	if !hasCommit(ctx, in.Clone, in.Base) {
		// the clone may only be behind: ask its origin once. Failing, or still not having it (a base that was never pushed), is the tar.
		if fetchClone(ctx, in.Clone) != nil || !hasCommit(ctx, in.Clone, in.Base) {
			return Workspace{}, errUseTar
		}
	}
	ref := PinRef(in.JobID)
	{
		// The origin is asked even when the clone already holds the commit (an earlier try fetched it): the question is also the check
		// that the claim is live, and with the commit itself as `have` the answer is nothing_to_send, no download.
		haves := haveCandidates(ctx, in.Clone, in.Base)
		if hasCommit(ctx, in.Clone, in.Commit) {
			haves = []string{in.Commit}
		}
		fetched := false
		for _, have := range haves {
			body, err := in.Client.Bundle(ctx, in.JobID, in.ClaimID, have)
			switch {
			case errors.Is(err, ErrHaveUnknown):
				continue // an older one
			case errors.Is(err, ErrBundleTooLarge):
				return Workspace{}, errUseTar
			case errors.Is(err, ErrNothingToSend):
				fetched = hasCommit(ctx, in.Clone, in.Commit)
			case err != nil:
				return Workspace{}, err
			default:
				err = fetchBundle(ctx, in.Clone, ref, body)
				body.Close()
				if errors.Is(err, ErrBundleTooLarge) {
					return Workspace{}, errUseTar
				}
				if err != nil {
					return Workspace{}, err
				}
				fetched = true
			}
			break
		}
		if !fetched {
			return Workspace{}, errUseTar
		}
		if got := pinned(ctx, in.Clone, in.JobID); got != "" && got != in.Commit {
			_, _ = gitRefWrite(ctx, "wwd.snapshot-unref", in.Clone, "update-ref", "-d", ref)
			return Workspace{}, fmt.Errorf("snapshot: the bundle holds %s, not the snapshot commit %s", got, in.Commit)
		}
	}
	return addWorktree(ctx, in.Clone, in.Dir, in.Commit, ref)
}

// CheckoutLocal makes a workspace of a commit that is in a repository on this host (the origin's own, for a job it runs itself): a
// detached worktree at dir, with no download. The repository is gitDir (its work tree root, or a git directory); the checkout runs no
// hooks. Cleanup removes the worktree and the directory.
func CheckoutLocal(ctx context.Context, gitDir, commit, dir string) (Workspace, error) {
	if !commitRe.MatchString(commit) || dir == "" {
		return Workspace{}, errors.New("snapshot: CheckoutLocal needs a commit and a directory")
	}
	if _, err := os.Lstat(dir); err == nil {
		return Workspace{}, fmt.Errorf("snapshot: %s is already there", dir)
	}
	if err := ownRepo(ctx, gitDir); err != nil {
		return Workspace{}, err
	}
	if !hasCommit(ctx, gitDir, commit) {
		return Workspace{}, fmt.Errorf("snapshot: %s does not have the commit %s", gitDir, commit)
	}
	return addWorktree(ctx, gitDir, dir, commit, "")
}

// addWorktree adds a detached worktree of commit at dir in repo. ref, when it is not "", is a ref of the job that Cleanup removes too.
func addWorktree(ctx context.Context, repo, dir, commit, ref string) (Workspace, error) {
	cleanup := func() error {
		// the worktree first, then the ref (its commit is then free for the repository's gc), then whatever is left of the directory
		_, err1 := git(context.Background(), "wwd.snapshot-wtrm", repo, "worktree", "remove", "--force", dir)
		var err2 error
		if ref != "" {
			_, err2 = gitRefWrite(context.Background(), "wwd.snapshot-unref", repo, "update-ref", "-d", ref)
		}
		_, err3 := git(context.Background(), "wwd.snapshot-prune", repo, "worktree", "prune")
		err4 := os.RemoveAll(dir)
		return errors.Join(err1, err2, err3, err4)
	}
	// core.hooksPath: the repository's post-checkout hook is the host's, not the job's, and would run before `start` and outside the job's
	// environment. (A filter driver the host configured is the host's too: it runs only for a path the snapshot's .gitattributes names.)
	if _, err := git(ctx, "wwd.snapshot-worktree", repo, "-c", "core.hooksPath="+os.DevNull, "worktree", "add", "--detach", "--force", dir, commit); err != nil {
		_ = cleanup() // git removes what it made when the checkout fails; this makes sure, and drops the ref
		return Workspace{}, err
	}
	return Workspace{Dir: dir, Cleanup: cleanup}, nil
}

// haveCandidates are the commits to offer as `have`, newest first: the tip of the clone's default branch when it contains the base (the
// clone has been fetched since), then the base itself.
func haveCandidates(ctx context.Context, clone, base string) []string {
	var out []string
	if tip, err := git(ctx, "wwd.snapshot-tip", clone, "rev-parse", "-q", "--verify", "refs/remotes/origin/HEAD^{commit}"); err == nil {
		t := strings.TrimSpace(string(tip))
		if commitRe.MatchString(t) && t != base {
			if _, err := git(ctx, "wwd.snapshot-anc", clone, "merge-base", "--is-ancestor", base, t); err == nil {
				out = append(out, t)
			}
		}
	}
	return append(out, base)
}

// fetchBundle writes the bundle to a temporary file and fetches the job's ref from it into the clone.
func fetchBundle(ctx context.Context, clone, ref string, body io.Reader) error {
	f, err := os.CreateTemp("", "wwd-bundle-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	n, err := io.Copy(f, io.LimitReader(body, int64(maxBundleDownload)+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("snapshot: reading the bundle: %w", err)
	}
	if n > int64(maxBundleDownload) {
		return ErrBundleTooLarge // the temporary file goes with the defer
	}
	// the ref is forced: an earlier try of this job on this host may have left it
	_, err = gitRefWrite(ctx, "wwd.snapshot-fetch", clone, "fetch", "-q", "--no-tags", f.Name(), "+"+ref+":"+ref)
	return err
}

func fromTar(ctx context.Context, in MaterializeIn) (Workspace, error) {
	body, err := in.Client.Tar(ctx, in.JobID, in.ClaimID)
	if err != nil {
		return Workspace{}, err
	}
	defer body.Close()
	dir := in.Dir
	commit, err := Untar(body, dir)
	if err == nil && commit != in.Commit {
		err = fmt.Errorf("snapshot: the tar is of %q, not of the snapshot commit %s", commit, in.Commit)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	return Workspace{Dir: dir, Cleanup: func() error { return os.RemoveAll(filepath.Clean(dir)) }}, nil
}
