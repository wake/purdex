package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ---- the origin's side: Pin / Unpin, and the snapshot route ----

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// origin is a repository with two commits (base is the second), and a snapshot of a working tree that has a changed file, a new one and a
// tracked file the ignore rules match. It returns the repository, the base and the snapshot commit.
func origin(t *testing.T) (dir, base, snap string) {
	t.Helper()
	dir = t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	gitOut(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir+"/.gitignore", "*.log\n")
	writeFile(t, dir+"/a.txt", "a1\n")
	writeFile(t, dir+"/keep.log", "log1\n")
	gitOut(t, dir, "add", ".gitignore", "a.txt")
	gitOut(t, dir, "add", "-f", "keep.log")
	gitOut(t, dir, "commit", "-q", "-m", "one")
	writeFile(t, dir+"/b.txt", "b1\n")
	gitOut(t, dir, "add", "b.txt")
	gitOut(t, dir, "commit", "-q", "-m", "two")
	base = gitOut(t, dir, "rev-parse", "HEAD")
	writeFile(t, dir+"/a.txt", "a2 changed\n")
	writeFile(t, dir+"/new.txt", "new\n")
	writeFile(t, dir+"/keep.log", "log2 changed\n")
	info, err := gate.SnapshotTree(context.Background(), "", dir)
	require.NoError(t, err)
	return dir, base, info.SHA
}

// fakeSource knows one claim.
type fakeSource struct {
	ref     Ref
	claimID string
	asked   int
}

func (f *fakeSource) Claimed(_ context.Context, jobID, claimID string) (Ref, error) {
	f.asked++
	if claimID != f.claimID {
		return Ref{State: wire.StateClaimed}, ErrStaleClaim
	}
	return f.ref, nil
}

func serve(t *testing.T, src Source) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/jobs/{id}/snapshot", NewHandler(src))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, id, query string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/jobs/" + id + "/snapshot?" + query)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, b
}

// A live claim gets a bundle of have..commit: it carries the snapshot and needs only `have` on the other side.
func TestServe_ABundleOfTheSnapshotForALiveClaim(t *testing.T) {
	repo, base, snap := origin(t)
	claim := wire.NewClaimID()
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}})
	code, body := get(t, srv, "j_1", "claim_id="+claim+"&format=bundle&have="+base)
	require.Equal(t, 200, code, string(body))

	// a worker that holds `base` (a clone made before the snapshot) takes it in
	worker := filepath.Join(t.TempDir(), "clone")
	gitOut(t, repo, "clone", "-q", "--no-local", ".", worker) // a clone has base and not the snapshot
	require.Error(t, exec.Command("git", "-C", worker, "cat-file", "-e", snap+"^{commit}").Run(), "the clone does not have the snapshot yet")
	bf := filepath.Join(t.TempDir(), "s.bundle")
	require.NoError(t, os.WriteFile(bf, body, 0o644))
	gitOut(t, worker, "fetch", "-q", bf, PinRef("j_1")+":"+PinRef("j_1"))
	assert.Equal(t, snap, gitOut(t, worker, "rev-parse", PinRef("j_1")))
	assert.Equal(t, "a2 changed", gitOut(t, worker, "show", snap+":a.txt"))
	assert.Equal(t, "log2 changed", gitOut(t, worker, "show", snap+":keep.log"), "the tracked file the ignore rules match is in it")
	assert.Less(t, len(body), 20_000, "a few KB, not the history")
	assert.Equal(t, snap, gitOut(t, repo, "rev-parse", PinRef("j_1")), "and the commit is pinned in the origin")
}

// The same snapshot as a tar of the commit (git archive), with the commit id in the pax header.
func TestServe_ATarOfTheSnapshot(t *testing.T) {
	repo, _, snap := origin(t)
	claim := wire.NewClaimID()
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap}})
	code, body := get(t, srv, "j_2", "claim_id="+claim+"&format=tar")
	require.Equal(t, 200, code)
	names := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(body))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if h.Typeflag == tar.TypeXGlobalHeader {
			assert.Equal(t, snap, h.PAXRecords["comment"])
			continue
		}
		b, _ := io.ReadAll(tr)
		names[h.Name] = string(b)
	}
	assert.Equal(t, "a2 changed\n", names["a.txt"])
	assert.Equal(t, "new\n", names["new.txt"])
	assert.Equal(t, "log2 changed\n", names["keep.log"])
}

// A claim that is not the live one — unknown, expired, given away — is a 409 with the job's state, and nothing is read from the repo.
func TestServe_AStaleOrUnknownClaimIs409(t *testing.T) {
	repo, base, snap := origin(t)
	src := &fakeSource{claimID: wire.NewClaimID(), ref: Ref{RepoDir: repo, Commit: snap, Base: base}}
	srv := serve(t, src)
	for _, format := range []string{"bundle&have=" + base, "tar"} {
		code, body := get(t, srv, "j_3", "claim_id="+wire.NewClaimID()+"&format="+format)
		assert.Equal(t, 409, code, format)
		var c wire.Conflict409
		require.NoError(t, json.Unmarshal(body, &c))
		assert.NoError(t, c.Validate())
		assert.Equal(t, wire.StateClaimed, c.State)
	}
	assert.Empty(t, gitOut(t, repo, "for-each-ref", "refs/wwd"), "a refused request pins nothing")
}

// A request that is malformed is a 400 before the queue is asked.
func TestServe_MalformedRequestsAre400(t *testing.T) {
	repo, base, snap := origin(t)
	claim := wire.NewClaimID()
	src := &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}}
	srv := serve(t, src)
	for name, q := range map[string]string{
		"no claim":       "format=tar",
		"short claim":    "claim_id=abc&format=tar",
		"no format":      "claim_id=" + claim,
		"other format":   "claim_id=" + claim + "&format=zip",
		"bundle no have": "claim_id=" + claim + "&format=bundle",
		"bad have":       "claim_id=" + claim + "&format=bundle&have=--output=/tmp/x",
	} {
		code, _ := get(t, srv, "j_4", q)
		assert.Equal(t, 400, code, name)
	}
	code, _ := get(t, srv, "..%2fevil", "claim_id="+claim+"&format=tar")
	assert.Equal(t, 400, code, "a job id that is not one")
	assert.Zero(t, src.asked, "the queue was not asked")
}

// A `have` the origin does not hold is 422 have_unknown (the worker asks again with an older base, or for a tar); a `have` that already
// contains the commit leaves nothing to send.
func TestServe_AHaveTheOriginDoesNotKnowIs422(t *testing.T) {
	repo, base, snap := origin(t)
	claim := wire.NewClaimID()
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}})
	code, body := get(t, srv, "j_5", "claim_id="+claim+"&format=bundle&have="+strings.Repeat("ab", 20))
	assert.Equal(t, 422, code)
	assert.Contains(t, string(body), "have_unknown")
	code, body = get(t, srv, "j_5", "claim_id="+claim+"&format=bundle&have="+snap)
	assert.Equal(t, 422, code)
	assert.Contains(t, string(body), "nothing_to_send")
}

func TestPin_PinsKeepsAndUnpins(t *testing.T) {
	repo, _, snap := origin(t)
	ctx := context.Background()
	require.NoError(t, Pin(ctx, repo, "j_pin", snap))
	require.NoError(t, Pin(ctx, repo, "j_pin", snap), "the same again is fine")
	assert.Equal(t, snap, gitOut(t, repo, "rev-parse", PinRef("j_pin")))
	other := gitOut(t, repo, "rev-parse", "HEAD")
	assert.Error(t, Pin(ctx, repo, "j_pin", other), "a job id names one snapshot")
	assert.ErrorContains(t, Pin(ctx, repo, "j_none", strings.Repeat("c", 40)), "does not have the commit", "a commit the repository does not have")
	for _, bad := range []string{"", "../x", "a/b", "-x", "a..b", "x.lock", strings.Repeat("a", 200)} {
		assert.ErrorContains(t, Pin(ctx, repo, bad, snap), "is not a job id", "job id %q is refused by us, before git is asked", bad)
		assert.ErrorContains(t, Unpin(ctx, repo, bad), "is not a job id", "and so for Unpin: %q", bad)
	}
	assert.ErrorContains(t, Pin(ctx, repo, "j_x", "HEAD"), "is not a commit id", "a commit id, not a name")

	// the commit survives a gc while it is pinned and goes with the pin
	gitOut(t, repo, "reflog", "expire", "--expire=now", "--all")
	gitOut(t, repo, "gc", "-q", "--prune=now")
	assert.NoError(t, exec.Command("git", "-C", repo, "cat-file", "-e", snap+"^{commit}").Run(), "pinned: kept")
	require.NoError(t, Unpin(ctx, repo, "j_pin"))
	require.NoError(t, Unpin(ctx, repo, "j_pin"), "a ref that is not there is not an error")
	gitOut(t, repo, "gc", "-q", "--prune=now")
	assert.Error(t, exec.Command("git", "-C", repo, "cat-file", "-e", snap+"^{commit}").Run(), "unpinned: the unreferenced snapshot is collected")
	assert.Error(t, Unpin(ctx, filepath.Join(t.TempDir(), "no-repo"), "j_pin"), "a repository that cannot be read is an error, so the caller tries again")
}
