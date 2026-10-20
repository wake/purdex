package snapshot

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ---- the worker's side: Client and Materialize ----

// recorder is the origin's snapshot route with a note of every request: its format, and what to do first (a test's way to make the
// origin fail).
type recorder struct {
	mu      sync.Mutex
	formats []string
	conns   int
	before  func(n int, w http.ResponseWriter) bool // true = the request was answered here
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.formats...)
}

func serveRecorded(t *testing.T, src Source, rec *recorder) *Client {
	t.Helper()
	h := NewHandler(src)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}/snapshot", func(w http.ResponseWriter, req *http.Request) {
		rec.mu.Lock()
		rec.formats = append(rec.formats, req.URL.Query().Get("format"))
		n := len(rec.formats)
		rec.mu.Unlock()
		if rec.before != nil && rec.before(n, w) {
			return
		}
		h.ServeHTTP(w, req)
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			rec.mu.Lock()
			rec.conns++
			rec.mu.Unlock()
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "pool-token")
	c.sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(c.Close)
	return c
}

// materializeCase is an origin with a pinned snapshot, a worker's clone made before the snapshot, and a claim.
type materializeCase struct {
	repo, base, snap, clone, claim string
	src                            *fakeSource
}

func newMaterializeCase(t *testing.T, jobID string) materializeCase {
	t.Helper()
	repo, base, snap := origin(t)
	require.NoError(t, Pin(context.Background(), repo, jobID, snap))
	clone := filepath.Join(t.TempDir(), "clone")
	gitOut(t, repo, "clone", "-q", "--no-local", ".", clone)
	claim := wire.NewClaimID()
	return materializeCase{repo, base, snap, clone, claim, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}}}
}

func (m materializeCase) in(c *Client, jobID, dir string) MaterializeIn {
	return MaterializeIn{Client: c, JobID: jobID, ClaimID: m.claim, Commit: m.snap, Base: m.base, Clone: m.clone, Dir: dir}
}

func checkTree(t *testing.T, dir string) {
	t.Helper()
	b, err := os.ReadFile(dir + "/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "a2 changed\n", string(b))
	b, err = os.ReadFile(dir + "/keep.log")
	require.NoError(t, err)
	assert.Equal(t, "log2 changed\n", string(b), "the tracked file the ignore rules match is in it")
	_, err = os.Stat(dir + "/new.txt")
	assert.NoError(t, err)
}

// A host with a clone takes a bundle into it and works in a worktree; Cleanup takes the worktree and the ref away.
func TestMaterialize_ABundleIntoTheCloneAndAWorktree(t *testing.T) {
	m := newMaterializeCase(t, "j_1")
	rec := &recorder{}
	c := serveRecorded(t, m.src, rec)
	dir := filepath.Join(t.TempDir(), "ws")
	ws, err := Materialize(context.Background(), m.in(c, "j_1", dir))
	require.NoError(t, err)
	assert.Equal(t, []string{"bundle"}, rec.seen())
	checkTree(t, ws.Dir)
	assert.Equal(t, m.snap, gitOut(t, ws.Dir, "rev-parse", "HEAD"))
	assert.Contains(t, gitOut(t, m.clone, "worktree", "list"), ws.Dir)

	require.NoError(t, ws.Cleanup())
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err))
	assert.NotContains(t, gitOut(t, m.clone, "worktree", "list"), "ws")
	assert.Empty(t, gitOut(t, m.clone, "for-each-ref", "refs/wwd/"), "the ref is gone")
}

// A cold host (no clone) takes the tar, and the tree is the same.
func TestMaterialize_ATarForAHostWithoutAClone(t *testing.T) {
	m := newMaterializeCase(t, "j_2")
	rec := &recorder{}
	c := serveRecorded(t, m.src, rec)
	in := m.in(c, "j_2", filepath.Join(t.TempDir(), "ws"))
	in.Clone = ""
	ws, err := Materialize(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, []string{"tar"}, rec.seen())
	checkTree(t, ws.Dir)
	require.NoError(t, ws.Cleanup())
	_, err = os.Stat(ws.Dir)
	assert.True(t, os.IsNotExist(err))
}

// A clone that does not hold the base is no help to a bundle: a tar.
func TestMaterialize_ACloneWithoutTheBaseTakesATar(t *testing.T) {
	m := newMaterializeCase(t, "j_3")
	other := t.TempDir()
	gitOut(t, other, "init", "-q")
	m.clone = other
	rec := &recorder{}
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_3", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"tar"}, rec.seen())
	checkTree(t, ws.Dir)
}

// A bundle over the origin's limit is not the end: the tar is asked for.
func TestMaterialize_ABundleOverTheLimitFallsBackToATar(t *testing.T) {
	old := maxBundle
	maxBundle = 100
	t.Cleanup(func() { maxBundle = old })
	m := newMaterializeCase(t, "j_4")
	rec := &recorder{}
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_4", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"bundle", "tar"}, rec.seen())
	checkTree(t, ws.Dir)
}

// The tip of the clone's default branch is offered first; a tip the origin does not know is answered have_unknown and the base is next.
func TestMaterialize_AnUnknownHaveIsFollowedByTheBase(t *testing.T) {
	m := newMaterializeCase(t, "j_5")
	writeFile(t, m.clone+"/only-here.txt", "x\n")
	gitOut(t, m.clone, "add", "only-here.txt")
	gitOut(t, m.clone, "commit", "-q", "-m", "only in the clone")
	tip := gitOut(t, m.clone, "rev-parse", "HEAD")
	gitOut(t, m.clone, "update-ref", "refs/remotes/origin/HEAD", tip) // writes through the symbolic ref: origin/HEAD is now a commit the origin lacks
	require.Equal(t, tip, gitOut(t, m.clone, "rev-parse", "refs/remotes/origin/HEAD"))
	rec := &recorder{}
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, rec), "j_5", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	assert.Equal(t, []string{"bundle", "bundle"}, rec.seen(), "the tip, then the base")
	checkTree(t, ws.Dir)
}

// A claim that is not live is ErrStaleClaim on both ways, and nothing is left behind.
func TestMaterialize_AStaleClaimIsErrStaleClaim(t *testing.T) {
	m := newMaterializeCase(t, "j_6")
	rec := &recorder{}
	c := serveRecorded(t, m.src, rec)
	for _, clone := range []string{m.clone, ""} {
		in := m.in(c, "j_6", filepath.Join(t.TempDir(), "ws"))
		in.Clone = clone
		in.ClaimID = wire.NewClaimID()
		_, err := Materialize(context.Background(), in)
		assert.ErrorIs(t, err, ErrStaleClaim, "clone=%q", clone)
		want := "bundle"
		if clone == "" {
			want = "tar"
		}
		got := rec.seen()
		assert.Equal(t, want, got[len(got)-1], "clone=%q: the answer ended the try, no other way was asked", clone)
		_, serr := os.Stat(in.Dir)
		assert.True(t, os.IsNotExist(serr), "no directory is left")
	}
	assert.Equal(t, []string{"bundle", "tar"}, rec.seen(), "one request each: a stale claim is not retried as a tar")
	assert.Empty(t, gitOut(t, m.clone, "for-each-ref", "refs/wwd/"))
}

// A tar of another commit than the one asked for is an error, and the directory is removed.
func TestMaterialize_ATarOfAnotherCommitIsAnError(t *testing.T) {
	m := newMaterializeCase(t, "j_7")
	c := serveRecorded(t, m.src, &recorder{})
	in := m.in(c, "j_7", filepath.Join(t.TempDir(), "ws"))
	in.Clone = ""
	in.Commit = m.base // the origin serves the snapshot commit
	_, err := Materialize(context.Background(), in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not of the snapshot commit")
	_, serr := os.Stat(in.Dir)
	assert.True(t, os.IsNotExist(serr))
}

func TestMaterialize_ADirectoryThatIsThereIsRefused(t *testing.T) {
	m := newMaterializeCase(t, "j_8")
	c := serveRecorded(t, m.src, &recorder{})
	_, err := Materialize(context.Background(), m.in(c, "j_8", t.TempDir()))
	assert.Error(t, err)
}

// The Client keeps one connection to the origin for the calls it makes one after another (a refusal in between included: its body is
// drained, so the connection is not dropped).
func TestClient_ReusesItsConnection(t *testing.T) {
	m := newMaterializeCase(t, "j_9")
	rec := &recorder{}
	c := serveRecorded(t, m.src, rec)
	for i := 0; i < 3; i++ {
		body, err := c.Tar(context.Background(), "j_9", m.claim)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, body)
		require.NoError(t, err)
		body.Close()
		_, err = c.Tar(context.Background(), "j_9", wire.NewClaimID()) // a 409 in between
		assert.ErrorIs(t, err, ErrStaleClaim)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Equal(t, 1, rec.conns, "six calls, one connection")
}

// A 503 (the origin has no free slot) is waited out and asked again; past the retries it is ErrBusy.
func TestClient_A503IsRetriedThenErrBusy(t *testing.T) {
	m := newMaterializeCase(t, "j_10")
	busy := func(n int, w http.ResponseWriter) bool {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return true
	}
	rec := &recorder{before: func(n int, w http.ResponseWriter) bool { return n <= 2 && busy(n, w) }}
	c := serveRecorded(t, m.src, rec)
	body, err := c.Tar(context.Background(), "j_10", m.claim)
	require.NoError(t, err)
	body.Close()
	assert.Equal(t, 3, len(rec.seen()), "two refusals, then the tar")

	always := &recorder{before: busy}
	c2 := serveRecorded(t, m.src, always)
	_, err = c2.Tar(context.Background(), "j_10", m.claim)
	assert.ErrorIs(t, err, ErrBusy)
	assert.Equal(t, 4, len(always.seen()), "the first try and three retries")
}

// A clone that already holds the commit (an earlier try fetched it) still has its claim checked by the origin: a stale claim does not
// get a workspace, a live one needs no download.
func TestMaterialize_ACommitTheCloneHoldsStillNeedsALiveClaim(t *testing.T) {
	m := newMaterializeCase(t, "j_11")
	gitOut(t, m.clone, "fetch", "-q", m.repo, PinRef("j_11")+":refs/held/j_11") // the commit is in the clone now
	require.True(t, hasCommit(context.Background(), m.clone, m.snap))
	rec := &recorder{}
	c := serveRecorded(t, m.src, rec)

	stale := m.in(c, "j_11", filepath.Join(t.TempDir(), "ws"))
	stale.ClaimID = wire.NewClaimID()
	_, err := Materialize(context.Background(), stale)
	assert.ErrorIs(t, err, ErrStaleClaim)
	_, serr := os.Stat(stale.Dir)
	assert.True(t, os.IsNotExist(serr))

	ws, err := Materialize(context.Background(), m.in(c, "j_11", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	checkTree(t, ws.Dir)
	assert.Equal(t, []string{"bundle", "bundle"}, rec.seen(), "one question each, no download")
}

// The clone's post-checkout hook is not run by the checkout of a snapshot: it would run outside the job's environment, before `start`.
func TestMaterialize_TheClonesCheckoutHookDoesNotRun(t *testing.T) {
	m := newMaterializeCase(t, "j_12")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := m.clone + "/.git/hooks/post-checkout"
	writeFile(t, hook, "#!/bin/sh\ntouch "+marker+"\n")
	require.NoError(t, os.Chmod(hook, 0o755))
	ws, err := Materialize(context.Background(), m.in(serveRecorded(t, m.src, &recorder{}), "j_12", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	checkTree(t, ws.Dir)
	_, serr := os.Stat(marker)
	assert.True(t, os.IsNotExist(serr), "the hook ran")
}

// A checkout that fails after git made the worktree (here a required filter that fails) leaves nothing: no directory, no entry in the
// clone, no ref.
func TestMaterialize_AFailedCheckoutLeavesNothing(t *testing.T) {
	repo, base, _ := origin(t)
	writeFile(t, repo+"/.gitattributes", "a.txt filter=wwdfail\n")
	info, err := SnapshotTree(context.Background(), "", repo)
	require.NoError(t, err)
	snap := info.SHA
	require.NoError(t, Pin(context.Background(), repo, "j_13", snap))
	clone := filepath.Join(t.TempDir(), "clone")
	gitOut(t, repo, "clone", "-q", "--no-local", ".", clone)
	gitOut(t, clone, "config", "filter.wwdfail.smudge", "false")
	gitOut(t, clone, "config", "filter.wwdfail.required", "true")
	claim := wire.NewClaimID()
	src := &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}}
	c := serveRecorded(t, src, &recorder{})
	dir := filepath.Join(t.TempDir(), "ws")
	_, err = Materialize(context.Background(), MaterializeIn{Client: c, JobID: "j_13", ClaimID: claim, Commit: snap, Base: base, Clone: clone, Dir: dir})
	require.Error(t, err)
	_, serr := os.Stat(dir)
	assert.True(t, os.IsNotExist(serr), "no directory")
	assert.NotContains(t, gitOut(t, clone, "worktree", "list"), "ws", "no worktree entry")
	assert.Empty(t, gitOut(t, clone, "for-each-ref", "refs/wwd/"), "no ref")
}

// A bundle that never ends is cut off at the worker's own limit and the tar is asked for.
func TestMaterialize_ABundleThatNeverEndsIsCutOff(t *testing.T) {
	old := maxBundleDownload
	maxBundleDownload = 64 << 10
	t.Cleanup(func() { maxBundleDownload = old })
	m := newMaterializeCase(t, "j_14")
	var mu sync.Mutex
	var formats []string
	h := NewHandler(m.src)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		formats = append(formats, r.URL.Query().Get("format"))
		mu.Unlock()
		if r.URL.Query().Get("format") == "bundle" {
			w.Header().Set("Content-Type", "application/x-git-bundle")
			chunk := make([]byte, 32<<10)
			for r.Context().Err() == nil {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			return
		}
		h.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "pool-token")
	t.Cleanup(c.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := Materialize(ctx, m.in(c, "j_14", filepath.Join(t.TempDir(), "ws")))
	require.NoError(t, err)
	checkTree(t, ws.Dir)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"bundle", "tar"}, formats)
}

// The origin runs a job of its own from a commit it already has: a worktree, no download.
func TestCheckoutLocal_AWorktreeOfACommitInThisRepository(t *testing.T) {
	repo, _, snap := origin(t)
	dir := filepath.Join(t.TempDir(), "ws")
	ws, err := CheckoutLocal(context.Background(), repo, snap, dir)
	require.NoError(t, err)
	checkTree(t, ws.Dir)
	assert.Equal(t, snap, gitOut(t, ws.Dir, "rev-parse", "HEAD"))
	require.NoError(t, ws.Cleanup())
	_, serr := os.Stat(dir)
	assert.True(t, os.IsNotExist(serr))
	assert.NotContains(t, gitOut(t, repo, "worktree", "list"), "ws")

	// the git directory itself works as well
	ws, err = CheckoutLocal(context.Background(), repo+"/.git", snap, filepath.Join(t.TempDir(), "ws2"))
	require.NoError(t, err)
	checkTree(t, ws.Dir)
	require.NoError(t, ws.Cleanup())
}

func TestCheckoutLocal_RefusesWhatIsNotARepositoryACommitOrAFreeDirectory(t *testing.T) {
	repo, _, snap := origin(t)
	ctx := context.Background()
	free := func() string { return filepath.Join(t.TempDir(), "ws") }
	sub := filepath.Join(repo, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	_, err := CheckoutLocal(ctx, sub, snap, free())
	assert.Error(t, err, "a directory inside the repository is not the repository")
	_, err = CheckoutLocal(ctx, repo, strings.Repeat("a", 40), free())
	assert.Error(t, err, "a commit the repository does not have")
	_, err = CheckoutLocal(ctx, repo, "HEAD", free())
	assert.Error(t, err, "only a full commit id")
	_, err = CheckoutLocal(ctx, repo, snap, t.TempDir())
	assert.Error(t, err, "a directory that is there")
}
