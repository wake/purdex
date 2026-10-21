package jobexec

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// tarFetcher unpacks a tar the way the origin's tar route is unpacked (snapshot.Untar), whatever the tar holds.
type tarFetcher struct{ body []byte }

func (f tarFetcher) Fetch(ctx context.Context, job wire.Job, claimID, clone, dir string) (snapshot.Workspace, error) {
	if _, err := snapshot.Untar(bytes.NewReader(f.body), dir); err != nil {
		_ = os.RemoveAll(dir)
		return snapshot.Workspace{}, err
	}
	return snapshot.Workspace{Dir: dir, Cleanup: func() error { return os.RemoveAll(dir) }}, nil
}

func assertRejectedOutLink(t *testing.T, err error, path string) {
	t.Helper()
	var rej *runner.Rejected
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v (%T): a snapshot with a symlink out of the workspace must be rejected, not an infra failure", err, err)
	}
	if !strings.Contains(rej.Reason, path) || !strings.Contains(rej.Reason, "/etc") {
		t.Fatalf("reason %q must name the link %q and its target", rej.Reason, path)
	}
}

// #3841: a symlink to a path outside the workspace (a node_modules that points at the submitter's checkout) was an ordinary error, so the
// job was an infra failure: released, claimed again, forever.
func TestASnapshotTarWithASymlinkOutOfTheWorkspaceIsRejected(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "node_modules", Typeflag: tar.TypeSymlink, Linkname: "/etc", Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	e := newExec(t, tarFetcher{body: buf.Bytes()})
	_, err := e.Materialize(context.Background(), testRef, job("hello"), claimID)
	assertRejectedOutLink(t, err, "node_modules")
	if _, serr := os.Stat(e.JobDir(job("hello"), claimID)); !os.IsNotExist(serr) {
		t.Fatalf("a rejected snapshot leaves its directory behind: %v", serr)
	}
}

// The clone route checks the commit out; a tracked symlink out of the workspace is the same snapshot problem.
func TestACheckedOutSymlinkOutOfTheWorkspaceIsRejected(t *testing.T) {
	r, _ := repo(t)
	if err := os.Symlink("/etc", filepath.Join(r, "node_modules")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "link"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = r
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = r
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	e := newExec(t, &localFetcher{repo: r, commit: strings.TrimSpace(string(out))})
	_, err = e.Materialize(context.Background(), testRef, job("hello"), claimID)
	assertRejectedOutLink(t, err, "node_modules")
	if _, serr := os.Stat(e.JobDir(job("hello"), claimID)); !os.IsNotExist(serr) {
		t.Fatalf("a rejected snapshot leaves its directory behind: %v", serr)
	}
}
