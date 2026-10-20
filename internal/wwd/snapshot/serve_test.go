package snapshot

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	info, err := SnapshotTree(context.Background(), "", dir)
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

// smallBuffers starts srv and returns a client, both holding little in flight. The cut test needs a tar the kernel cannot swallow
// whole: the server's send buffer and the client's receive buffer autotune to megabytes on Linux, and what sits in them has to be read,
// at the test's slow pace, before the reader sees that the connection was aborted. On a roomy host (mserv) that took longer than the
// test waits. The write side is set from the server (ConnContext), the read side from the client's dialer.
func smallBuffers(srv *httptest.Server) *http.Client {
	srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetWriteBuffer(64 << 10)
		}
		return ctx
	}
	srv.Start()
	d := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := d.DialContext(ctx, network, addr)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetReadBuffer(64 << 10)
		}
		return c, err
	}}}
}

func serve(t *testing.T, src Source) *httptest.Server {
	t.Helper()
	srv, _ := serveWith(t, src, false)
	return srv
}

// serveWith is serve, and with small=true it also returns a client whose connections hold little in flight (see smallBuffers).
func serveWith(t *testing.T, src Source, small bool) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /v1/jobs/{id}/snapshot", NewHandler(src))
	srv := httptest.NewUnstartedServer(mux)
	client := http.DefaultClient
	if small {
		client = smallBuffers(srv)
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	return srv, client
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
	require.NoError(t, Pin(context.Background(), repo, "j_1", snap)) // what the queue does when the job is submitted
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
	require.NoError(t, Pin(context.Background(), repo, "j_2", snap))
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
	require.NoError(t, Pin(context.Background(), repo, "j_5", snap))
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}})
	code, body := get(t, srv, "j_5", "claim_id="+claim+"&format=bundle&have="+strings.Repeat("ab", 20))
	assert.Equal(t, 422, code)
	assert.Contains(t, string(body), "have_unknown")
	code, body = get(t, srv, "j_5", "claim_id="+claim+"&format=bundle&have="+snap)
	assert.Equal(t, 422, code)
	assert.Contains(t, string(body), "nothing_to_send")
}

// The handler only checks the pin, it does not make one: a claim that ended between the lookup and the pin would otherwise bring back a
// ref the queue has just removed. A live claim whose commit is not pinned is the queue's fault, and says so (500), for both formats.
func TestServe_ALiveClaimWhoseCommitIsNotPinnedIs500AndNothingIsPinned(t *testing.T) {
	repo, base, snap := origin(t)
	claim := wire.NewClaimID()
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}})
	for _, q := range []string{"format=tar", "format=bundle&have=" + base} {
		code, body := get(t, srv, "j_6", "claim_id="+claim+"&"+q)
		assert.Equal(t, 500, code, q)
		assert.Contains(t, string(body), "not pinned")
	}
	assert.Empty(t, gitOut(t, repo, "for-each-ref", "refs/wwd"), "and the handler pinned nothing")
	// pinned to another commit is the same thing
	require.NoError(t, Pin(context.Background(), repo, "j_6", gitOut(t, repo, "rev-parse", "HEAD")))
	code, _ := get(t, srv, "j_6", "claim_id="+claim+"&format=tar")
	assert.Equal(t, 500, code)
}

// A bundle is cut off at a hard size, not after it is all in memory: a `have` that is old or unrelated would make a huge one.
func TestServe_ABundleOverTheLimitIs413AndGitIsStopped(t *testing.T) {
	old := maxBundle
	maxBundle = 100 // less than the header of any bundle
	t.Cleanup(func() { maxBundle = old })
	repo, base, snap := origin(t)
	claim := wire.NewClaimID()
	require.NoError(t, Pin(context.Background(), repo, "j_7", snap))
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}})
	// the oldest commit as `have`: the bundle carries everything after it
	first := gitOut(t, repo, "rev-list", "--max-parents=0", "HEAD")
	code, body := get(t, srv, "j_7", "claim_id="+claim+"&format=bundle&have="+first)
	assert.Equal(t, 413, code, string(body))
	assert.Contains(t, string(body), "ask for a tar")
}

// The bundle is far bigger than a pipe holds (64 KB) and the limit is far under it: git must be stopped by the cut, not left blocked on a
// full pipe until the request ends.
func TestServe_ABundleMuchBiggerThanAPipeIs413AtOnce(t *testing.T) {
	old := maxBundle
	maxBundle = 1000
	t.Cleanup(func() { maxBundle = old })
	repo, _, _ := origin(t)
	first := gitOut(t, repo, "rev-list", "--max-parents=0", "HEAD")
	big := make([]byte, 3<<20)
	_, _ = rand.Read(big) // does not compress
	require.NoError(t, os.WriteFile(repo+"/big.bin", big, 0o644))
	gitOut(t, repo, "add", "big.bin")
	gitOut(t, repo, "commit", "-q", "-m", "big")
	head := gitOut(t, repo, "rev-parse", "HEAD")
	claim := wire.NewClaimID()
	require.NoError(t, Pin(context.Background(), repo, "j_8", head))
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: head}})
	client := &http.Client{Timeout: 20 * time.Second}
	start := time.Now()
	resp, err := client.Get(srv.URL + "/v1/jobs/j_8/snapshot?claim_id=" + claim + "&format=bundle&have=" + first)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 413, resp.StatusCode)
	assert.Less(t, time.Since(start), 10*time.Second)
}

// blockingSource holds a request inside Claimed until released, so that a second one arrives while the first holds the only slot.
type blockingSource struct {
	in, release chan struct{}
	fakeSource
}

func (b *blockingSource) Claimed(ctx context.Context, jobID, claimID string) (Ref, error) {
	b.in <- struct{}{}
	<-b.release
	return b.fakeSource.Claimed(ctx, jobID, claimID)
}

// More snapshots at once than there are slots: the extra one is turned away at once with 503 and Retry-After, and the slot comes back.
func TestServe_PastTheSlotsIs503AndTheSlotComesBack(t *testing.T) {
	old := maxConcurrent
	maxConcurrent = 1
	t.Cleanup(func() { maxConcurrent = old })
	repo, base, snap := origin(t)
	claim := wire.NewClaimID()
	require.NoError(t, Pin(context.Background(), repo, "j_9", snap))
	src := &blockingSource{in: make(chan struct{}, 2), release: make(chan struct{}),
		fakeSource: fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: snap, Base: base}}}
	srv := serve(t, src)
	q := "claim_id=" + claim + "&format=tar"
	first := make(chan int, 1)
	go func() { code, _ := get(t, srv, "j_9", q); first <- code }()
	<-src.in // the first holds the slot
	resp, err := http.Get(srv.URL + "/v1/jobs/j_9/snapshot?" + q)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 503, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("Retry-After"))
	close(src.release)
	assert.Equal(t, 200, <-first)
	code, _ := get(t, srv, "j_9", q)
	assert.Equal(t, 200, code, "the slot is back")
}

// A reader that takes nothing is dropped, and git archive with it.
func TestServe_ATarReaderThatStallsIsDropped(t *testing.T) {
	old := writeStall
	writeStall = 300 * time.Millisecond
	t.Cleanup(func() { writeStall = old })
	repo, _, _ := origin(t)
	big := make([]byte, 32<<20)
	_, _ = rand.Read(big)
	require.NoError(t, os.WriteFile(repo+"/big.bin", big, 0o644))
	gitOut(t, repo, "add", "big.bin")
	gitOut(t, repo, "commit", "-q", "-m", "big")
	head := gitOut(t, repo, "rev-parse", "HEAD")
	claim := wire.NewClaimID()
	require.NoError(t, Pin(context.Background(), repo, "j_10", head))
	srv := serve(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: head}})
	resp, err := http.Get(srv.URL + "/v1/jobs/j_10/snapshot?claim_id=" + claim + "&format=tar")
	require.NoError(t, err)
	defer resp.Body.Close()
	time.Sleep(2 * time.Second) // read nothing
	n, err := io.Copy(io.Discard, resp.Body)
	assert.Error(t, err, "the answer was cut off (read %d bytes)", n)
	assert.Less(t, n, int64(len(big)))
}

// A reader that never stalls but never hurries either is dropped when the tar has taken too long.
func TestServe_ATarThatTakesTooLongIsCut(t *testing.T) {
	old := maxTar
	maxTar = 500 * time.Millisecond
	t.Cleanup(func() { maxTar = old })
	repo, _, _ := origin(t)
	big := make([]byte, 32<<20)
	_, _ = rand.Read(big)
	require.NoError(t, os.WriteFile(repo+"/big.bin", big, 0o644))
	gitOut(t, repo, "add", "big.bin")
	gitOut(t, repo, "commit", "-q", "-m", "big")
	head := gitOut(t, repo, "rev-parse", "HEAD")
	claim := wire.NewClaimID()
	require.NoError(t, Pin(context.Background(), repo, "j_11", head))
	srv, client := serveWith(t, &fakeSource{claimID: claim, ref: Ref{RepoDir: repo, Commit: head}}, true)
	resp, err := client.Get(srv.URL + "/v1/jobs/j_11/snapshot?claim_id=" + claim + "&format=tar")
	require.NoError(t, err)
	defer resp.Body.Close()
	var n int64
	buf := make([]byte, 4096)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		k, rerr := resp.Body.Read(buf)
		n += int64(k)
		if rerr != nil {
			assert.NotEqual(t, io.EOF, rerr, "cut off, not finished")
			assert.Less(t, n, int64(len(big)))
			return
		}
		time.Sleep(20 * time.Millisecond) // steady, never stalled
	}
	t.Fatal("the tar was never cut off")
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

// The git commands here run in the repository they are given, with no configuration from the process's environment.
func TestGitEnv_HasNoInheritedGitVariables(t *testing.T) {
	t.Setenv("GIT_DIR", "/other/.git")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	for _, kv := range gitEnv() {
		for _, bad := range []string{"GIT_DIR=", "GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_0="} {
			assert.False(t, strings.HasPrefix(kv, bad), kv)
		}
	}
}

func TestListPins_ListsTheJobsThatArePinnedAndOnlyThose(t *testing.T) {
	repo, _, snap := origin(t)
	ctx := context.Background()
	ids, err := ListPins(ctx, repo)
	require.NoError(t, err)
	assert.Equal(t, []string{}, ids, "no refs/wwd/ is an empty list, not an error")

	require.NoError(t, Pin(ctx, repo, "j_2", snap))
	require.NoError(t, Pin(ctx, repo, "j_1", snap))
	gitOut(t, repo, "update-ref", "refs/wwd/nested/x", snap) // not a job id: the sweep must not see it
	gitOut(t, repo, "update-ref", "refs/wwd/-dash", snap)
	gitOut(t, repo, "update-ref", "refs/heads/j_3", snap) // not under refs/wwd/
	ids, err = ListPins(ctx, repo)
	require.NoError(t, err)
	assert.Equal(t, []string{"j_1", "j_2"}, ids)

	require.NoError(t, Unpin(ctx, repo, "j_1"))
	ids, err = ListPins(ctx, repo)
	require.NoError(t, err)
	assert.Equal(t, []string{"j_2"}, ids)
}

func TestListPins_ARepositoryGitCannotReadIsAnError(t *testing.T) {
	_, err := ListPins(context.Background(), filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
	_, err = ListPins(context.Background(), t.TempDir()) // exists, is not a repository
	assert.Error(t, err)
}

// Thousands of pins with long names are more than a short answer: the list is not cut at the limit of the small git commands.
func TestListPins_ManyPinsWithLongNamesAreAllListed(t *testing.T) {
	repo, _, snap := origin(t)
	const n = 7500 // x ~145 bytes is over 1 MiB
	var in strings.Builder
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("j%0127d", i) // 128 characters, the longest a job id may be
		want = append(want, id)
		in.WriteString("create refs/wwd/" + id + " " + snap + "\n")
	}
	cmd := exec.Command("git", "-C", repo, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	ids, err := ListPins(context.Background(), repo)
	require.NoError(t, err)
	assert.Len(t, ids, n)
	assert.Equal(t, want[0], ids[0])
}

// Only a ref that Pin could have made is listed: a direct ref to a commit. A symbolic ref or one to a blob or a tag is somebody else's.
func TestListPins_OnlyDirectRefsToCommitsAreListed(t *testing.T) {
	repo, _, snap := origin(t)
	ctx := context.Background()
	require.NoError(t, Pin(ctx, repo, "j_1", snap))
	blob := gitOut(t, repo, "rev-parse", snap+":a.txt")
	gitOut(t, repo, "update-ref", "refs/wwd/j_blob", blob)
	gitOut(t, repo, "symbolic-ref", "refs/wwd/j_sym", "refs/wwd/j_1")
	gitOut(t, repo, "pack-refs", "--all") // a packed ref is still a ref
	ids, err := ListPins(ctx, repo)
	require.NoError(t, err)
	assert.Equal(t, []string{"j_1"}, ids)
}

// A path that is not a repository but lies inside one is an error, for the three calls: git would walk up and use the parent, and the
// queue would then remove a ref in a repository it did not mean.
func TestRepoDir_ASubdirectoryOfARepositoryIsNotTheRepository(t *testing.T) {
	repo, _, snap := origin(t)
	ctx := context.Background()
	require.NoError(t, Pin(ctx, repo, "j_1", snap))
	sub := filepath.Join(repo, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	_, err := ListPins(ctx, sub)
	assert.Error(t, err)
	assert.Error(t, Unpin(ctx, sub, "j_1"))
	assert.Error(t, Pin(ctx, sub, "j_2", snap))
	assert.Equal(t, snap, gitOut(t, repo, "rev-parse", PinRef("j_1")), "the parent's pin is still there")
	// the work tree root and the git directory itself are the repository
	ids, err := ListPins(ctx, repo)
	require.NoError(t, err)
	assert.Equal(t, []string{"j_1"}, ids)
	ids, err = ListPins(ctx, repo+"/.git")
	require.NoError(t, err)
	assert.Equal(t, []string{"j_1"}, ids)
}

// A bare repository is a repository too.
func TestRepoDir_ABareRepositoryWorks(t *testing.T) {
	repo, _, snap := origin(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitOut(t, repo, "clone", "-q", "--bare", ".", bare)
	gitOut(t, repo, "push", "-q", bare, snap+":refs/heads/snap")
	ctx := context.Background()
	require.NoError(t, Pin(ctx, bare, "j_1", snap))
	ids, err := ListPins(ctx, bare)
	require.NoError(t, err)
	assert.Equal(t, []string{"j_1"}, ids)
	require.NoError(t, Unpin(ctx, bare, "j_1"))
}
