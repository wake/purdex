package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

// jobHost is a daemon over a real queue, listening on a real TCP port and a real unix socket.
type jobHost struct {
	q     *queue.Queue
	run   *Running
	sock  string
	tcp   string
	local *http.Client
}

func newJobHost(t *testing.T, alias string, peers ...PeerAddr) *jobHost {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wwd.db") + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, queue.Migrate(context.Background(), db))
	q, err := queue.New(db, queue.Options{Origin: alias, LogDir: t.TempDir()})
	require.NoError(t, err)

	d := New(Options{HostID: alias + "-id", Alias: alias, Version: "v0", Token: testToken, Queue: q, Peers: peers})
	dir, err := os.MkdirTemp("", "w") // short path: unix socket names are limited to ~104 bytes
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	run, err := d.Listen(ctx, ListenSpec{Addrs: []string{"127.0.0.1"}, Port: 0, SocketPath: sock})
	require.NoError(t, err)
	t.Cleanup(func() { run.Close() })
	return &jobHost{q: q, run: run, sock: sock, tcp: run.TCPAddrs()[0], local: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}}
}

func submissionBody(edit ...func(*wire.JobSubmission)) wire.JobSubmission {
	s := wire.JobSubmission{
		Repo:      wire.Repo{Name: "purdex", Root: "abc123", Remote: "git@github.com:wake/purdex.git"},
		Snapshot:  wire.Snapshot{Commit: "c0ffee", Tree: "7ee", Base: "ba5e", RelCwd: "spa"},
		Task:      "spa-related",
		Args:      []string{},
		Class:     wire.ClassMerge,
		Labels:    []string{"os:darwin"},
		Locks:     []string{},
		TimeoutS:  1800,
		Submitter: wire.Submitter{Session: "s1", Ref: "_abc123", Team: "wwd", Host: "mlab"},
		GitDir:    "/repos/purdex/.git",
	}
	for _, e := range edit {
		e(&s)
	}
	return s
}

func (h *jobHost) socketDo(t *testing.T, method, path string, body any) (int, []byte, http.Header) {
	t.Helper()
	return doReq(t, h.local, method, "http://wwd"+path, "", body)
}

func (h *jobHost) tcpDo(t *testing.T, method, path, auth string, body any) (int, []byte, http.Header) {
	t.Helper()
	return doReq(t, http.DefaultClient, method, "http://"+h.tcp+path, auth, body)
}

func doReq(t *testing.T, c *http.Client, method, url, auth string, body any) (int, []byte, http.Header) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, rd)
	require.NoError(t, err)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, b, resp.Header
}

func (h *jobHost) listIDs(t *testing.T, code int, raw []byte) []string {
	t.Helper()
	require.Equal(t, 200, code, string(raw))
	r, err := wire.Decode[wire.JobsListResponse](raw)
	require.NoError(t, err)
	out := []string{}
	for _, j := range r.Jobs {
		out = append(out, j.ID)
	}
	return out
}

// Through the real daemon: the socket takes a submission and lists the job with a cursor; TCP with the token reads the same queue.
func TestJobRoutesOnTheRealSocketAndTCP(t *testing.T) {
	h := newJobHost(t, "mlab")
	code, raw, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody())
	require.Equal(t, 200, code, string(raw))
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)

	code, raw, _ = h.socketDo(t, "GET", "/v1/jobs", nil)
	assert.Equal(t, []string{sub.ID}, h.listIDs(t, code, raw))
	code, raw, _ = h.tcpDo(t, "GET", "/v1/jobs?state=queued", "Bearer "+testToken, nil)
	assert.Equal(t, []string{sub.ID}, h.listIDs(t, code, raw))

	list, err := wire.Decode[wire.JobsListResponse](raw)
	require.NoError(t, err)
	code, raw, _ = h.tcpDo(t, "GET", "/v1/jobs?after="+list.Cursor, "Bearer "+testToken, nil)
	assert.Empty(t, h.listIDs(t, code, raw), "nothing changed since the cursor")

	code, _, _ = h.tcpDo(t, "GET", "/v1/jobs/"+sub.ID, "", nil)
	assert.Equal(t, 401, code, "TCP needs the token")
	code, _, _ = h.tcpDo(t, "GET", "/v1/jobs/"+sub.ID, "Bearer "+testToken, nil)
	assert.Equal(t, 200, code)
}

// A job is submitted only on its own host: through the real TCP server a POST /v1/jobs is refused, with the token and without it, and
// nothing is stored (spec 4.4). On the socket the same request is taken.
func TestSubmitIsRefusedOnTCP(t *testing.T) {
	h := newJobHost(t, "mlab")
	for _, auth := range []string{"", "Bearer wrong", "Bearer " + testToken} {
		code, _, _ := h.tcpDo(t, "POST", "/v1/jobs", auth, submissionBody())
		if auth == "Bearer "+testToken {
			assert.Contains(t, []int{404, 405}, code, "with the token the route still does not exist")
		} else {
			assert.Equal(t, 401, code, auth)
		}
	}
	js, err := h.q.List(context.Background(), queue.Filter{})
	require.NoError(t, err)
	assert.Empty(t, js, "no TCP request stored a job")

	code, _, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody())
	assert.Equal(t, 200, code)
}

// A peer's view through the real TCP server leaves out the host's ad-hoc jobs, in the list, by id and for cancel; the socket sees them.
func TestAdHocJobsAreNotServedToPeers(t *testing.T) {
	h := newJobHost(t, "mlab")
	_, raw, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody(func(s *wire.JobSubmission) { s.AdHoc = true }))
	adhoc, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	_, raw, _ = h.socketDo(t, "POST", "/v1/jobs", submissionBody())
	plain, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)

	code, raw, _ := h.tcpDo(t, "GET", "/v1/jobs", "Bearer "+testToken, nil)
	assert.Equal(t, []string{plain.ID}, h.listIDs(t, code, raw))
	code, raw, _ = h.socketDo(t, "GET", "/v1/jobs", nil)
	assert.ElementsMatch(t, []string{plain.ID, adhoc.ID}, h.listIDs(t, code, raw))

	code, _, _ = h.tcpDo(t, "GET", "/v1/jobs/"+adhoc.ID, "Bearer "+testToken, nil)
	assert.Equal(t, 404, code)
	code, _, _ = h.tcpDo(t, "POST", "/v1/jobs/"+adhoc.ID+"/cancel", "Bearer "+testToken, nil)
	assert.Equal(t, 404, code)
	code, _, _ = h.socketDo(t, "POST", "/v1/jobs/"+adhoc.ID+"/cancel", nil)
	assert.Equal(t, 200, code)
}

// On the socket ?host=<alias> forwards a read to that host over TCP with the pool token, so the App reads another host's queue through its
// own daemon; a log comes back as text that stays text.
func TestJobReadsForwardToAPeer(t *testing.T) {
	remote := newJobHost(t, "air26")
	_, raw, _ := remote.socketDo(t, "POST", "/v1/jobs", submissionBody())
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	c, err := remote.q.Claim(context.Background(), sub.ID, wire.ClaimRequest{Worker: "air26"})
	require.NoError(t, err)
	require.NoError(t, remote.q.PutLog(context.Background(), sub.ID, c.ClaimID, strings.NewReader("remote log\n")))

	local := newJobHost(t, "mlab", PeerAddr{Alias: "air26", HostPort: remote.tcp})
	code, raw, _ := local.socketDo(t, "GET", "/v1/jobs?host=air26", nil)
	assert.Equal(t, []string{sub.ID}, local.listIDs(t, code, raw), "the remote host's job, not the local queue's")
	code, raw, _ = local.socketDo(t, "GET", "/v1/jobs", nil)
	assert.Empty(t, local.listIDs(t, code, raw), "and without host= it is the local queue")

	code, raw, hdr := local.socketDo(t, "GET", "/v1/jobs/"+sub.ID+"/log?host=air26", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "remote log\n", string(raw))
	assert.True(t, strings.HasPrefix(hdr.Get("Content-Type"), "text/plain"))
	assert.Equal(t, "nosniff", hdr.Get("X-Content-Type-Options"))

	code, raw, _ = local.socketDo(t, "GET", "/v1/jobs/"+sub.ID+"?host=air26", nil)
	require.Equal(t, 200, code, "one job by id, from the remote queue")
	got, err := wire.Decode[wire.Job](raw)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, got.ID)
	code, _, _ = local.socketDo(t, "GET", "/v1/jobs/"+sub.ID, nil)
	assert.Equal(t, 404, code, "and the local queue has no such job")

	code, _, _ = local.socketDo(t, "POST", "/v1/jobs/"+sub.ID+"/cancel?host=air26", nil)
	assert.Equal(t, 200, code)
	held, err := remote.q.Get(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, wire.StateClaimed, held.State, "the remote job is held, so it was only asked to stop")
	code, raw, _ = local.socketDo(t, "GET", "/v1/jobs/"+sub.ID+"?host=air26", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, wire.StateClaimed, mustJob(t, raw).State)

	code, _, _ = local.socketDo(t, "GET", "/v1/jobs?host=nowhere", nil)
	assert.Equal(t, 404, code)
}

func mustJob(t *testing.T, raw []byte) wire.Job {
	t.Helper()
	j, err := wire.Decode[wire.Job](raw)
	require.NoError(t, err)
	return j
}

// A daemon built without a queue has no job routes: the TCP surface answers 404 behind the token, the socket keeps its 501 for a
// submission.
func TestNoQueueNoJobRoutes(t *testing.T) {
	d := newTestDaemon(t)
	assert.Equal(t, 404, do(d.TCPHandler(), "GET", "/v1/jobs", "Bearer "+testToken).Code)
	assert.Equal(t, 501, do(d.SocketHandler(), "POST", "/v1/jobs", "").Code)
}
