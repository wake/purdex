package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// errUseTar says that the bundle way did not work out for a reason a tar fixes.
var errUseTar = errors.New("snapshot: use a tar")

func fromClone(ctx context.Context, in MaterializeIn) (Workspace, error) {
	if ownRepo(ctx, in.Clone) != nil || !hasCommit(ctx, in.Clone, in.Base) {
		return Workspace{}, errUseTar
	}
	ref := PinRef(in.JobID)
	if !hasCommit(ctx, in.Clone, in.Commit) {
		fetched := false
		for _, have := range haveCandidates(ctx, in.Clone, in.Base) {
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
			_, _ = git(ctx, "wwd.snapshot-unref", in.Clone, "update-ref", "-d", ref)
			return Workspace{}, fmt.Errorf("snapshot: the bundle holds %s, not the snapshot commit %s", got, in.Commit)
		}
	}
	if _, err := git(ctx, "wwd.snapshot-worktree", in.Clone, "worktree", "add", "--detach", "--force", in.Dir, in.Commit); err != nil {
		_, _ = git(ctx, "wwd.snapshot-unref", in.Clone, "update-ref", "-d", ref)
		return Workspace{}, err
	}
	clone, dir := in.Clone, in.Dir
	return Workspace{Dir: dir, Cleanup: func() error {
		// the worktree first, then the ref (its commit is then free for the clone's gc), then whatever is left of the directory
		_, err1 := git(context.Background(), "wwd.snapshot-wtrm", clone, "worktree", "remove", "--force", dir)
		_, err2 := git(context.Background(), "wwd.snapshot-unref", clone, "update-ref", "-d", ref)
		err3 := os.RemoveAll(dir)
		return errors.Join(err1, err2, err3)
	}}, nil
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
	_, err = io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("snapshot: reading the bundle: %w", err)
	}
	// the ref is forced: an earlier try of this job on this host may have left it
	_, err = git(ctx, "wwd.snapshot-fetch", clone, "fetch", "-q", "--no-tags", f.Name(), "+"+ref+":"+ref)
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
