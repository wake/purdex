package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	dir := shortSockDir(t)
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

// The manual place of a queued job is set on the socket only: TCP (with the token or without) does not have the route, and a ?host= on
// the socket is refused instead of forwarded.
func TestPositionIsLocalOnly(t *testing.T) {
	h := newJobHost(t, "mlab")
	_, raw, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody())
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	path := "/v1/jobs/" + sub.ID + "/position"

	for _, auth := range []string{"", "Bearer " + testToken} {
		code, _, _ := h.tcpDo(t, "POST", path, auth, wire.PositionRequest{Position: wire.PositionFront})
		if auth == "" {
			assert.Equal(t, 401, code)
		} else {
			assert.Contains(t, []int{404, 405}, code, "with the token the route still does not exist")
		}
	}
	for _, q := range []string{"?host=air26", "?host=", "?host=&host=air26", "?host=air26&host=", "?host"} {
		code, _, _ := h.socketDo(t, "POST", path+q, wire.PositionRequest{Position: wire.PositionFront})
		assert.Equal(t, 400, code, "no forwarding, and no ?host= at all: "+q)
	}
	j, err := h.q.Get(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, wire.Position(""), j.Manual, "nothing moved yet")

	code, raw, _ := h.socketDo(t, "POST", path, wire.PositionRequest{Position: wire.PositionHold})
	require.Equal(t, 200, code, string(raw))
	j, err = h.q.Get(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, wire.PositionHold, j.Manual)
	// a peer reads the place with the job
	code, raw, _ = h.tcpDo(t, "GET", "/v1/jobs/"+sub.ID, "Bearer "+testToken, nil)
	require.Equal(t, 200, code)
	pj, err := wire.Decode[wire.Job](raw)
	require.NoError(t, err)
	assert.Equal(t, wire.PositionHold, pj.Manual)
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

// A log bigger than the 8 MiB the other forwarded answers are held to still comes through whole: logs are streamed (the queue keeps up to
// 64 MiB). Mutation gate: through the capped path this is a 502.
func TestAForwardedLogBeyondTheAnswerCapComesThrough(t *testing.T) {
	remote := newJobHost(t, "air26")
	_, raw, _ := remote.socketDo(t, "POST", "/v1/jobs", submissionBody())
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	c, err := remote.q.Claim(context.Background(), sub.ID, wire.ClaimRequest{Worker: "air26"})
	require.NoError(t, err)
	big := strings.Repeat("0123456789abcdef", (maxPeerBytes+(1<<20))/16) // 9 MiB
	require.Greater(t, len(big), maxPeerBytes)
	require.NoError(t, remote.q.PutLog(context.Background(), sub.ID, c.ClaimID, strings.NewReader(big)))

	local := newJobHost(t, "mlab", PeerAddr{Alias: "air26", HostPort: remote.tcp})
	code, got, hdr := local.socketDo(t, "GET", "/v1/jobs/"+sub.ID+"/log?host=air26", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, len(big), len(got))
	assert.True(t, bytes.Equal([]byte(big), got))
	assert.Equal(t, "nosniff", hdr.Get("X-Content-Type-Options"))

	// the other answers keep their cap, and a log that is not there is the peer's own 404
	code, _, _ = local.socketDo(t, "GET", "/v1/jobs/j_nope/log?host=air26", nil)
	assert.Equal(t, 404, code)
}

// socketOver serves a daemon's socket handler on a real listener, so a connection the handler aborts is a real one.
func socketOver(t *testing.T, peers ...PeerAddr) *httptest.Server {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wwd.db") + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, queue.Migrate(context.Background(), db))
	q, err := queue.New(db, queue.Options{Origin: "mlab", LogDir: t.TempDir()})
	require.NoError(t, err)
	d := New(Options{HostID: "h", Alias: "mlab", Version: "v0", Token: testToken, Queue: q, Peers: peers,
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond, CheckRedirect: noRedirect}}) // a client far shorter than a log takes
	srv := httptest.NewServer(d.SocketHandler())
	t.Cleanup(srv.Close)
	return srv
}

// readLog reads the forwarded log and says how the read ended.
func readLog(t *testing.T, srv *httptest.Server, path string) (status int, body []byte, readErr error) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, readErr = io.ReadAll(resp.Body)
	return resp.StatusCode, body, readErr
}

// A job log is a long answer from a peer, and what reaches the caller is whole or visibly broken, never a shorter text that looks
// finished: it is not held to the ten seconds of the other answers (here, a client of 100 ms still gets a log that takes 400 ms), it
// carries the length the peer stated, and when the peer stops short, stalls past the log's own time, or states no length, the read fails
// or the answer is a 502 (3b-4b R2).
func TestForwardedLogIsWholeOrVisiblyBroken(t *testing.T) {
	const size = 1 << 20
	payload := bytes.Repeat([]byte("log line\n"), size/9)
	peerFor := func(h http.HandlerFunc) PeerAddr {
		p := httptest.NewServer(h)
		t.Cleanup(p.Close)
		return PeerAddr{Alias: "air26", HostPort: p.Listener.Addr().String()}
	}
	path := "/v1/jobs/j_abc/log?host=air26"

	t.Run("slow but whole", func(t *testing.T) {
		srv := socketOver(t, peerFor(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			half := len(payload) / 2
			_, _ = w.Write(payload[:half])
			w.(http.Flusher).Flush()
			time.Sleep(400 * time.Millisecond)
			_, _ = w.Write(payload[half:])
		}))
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		assert.True(t, bytes.Equal(payload, got))
		assert.EqualValues(t, len(payload), resp.ContentLength, "the answer states the length the peer stated")
	})

	t.Run("peer stops short", func(t *testing.T) {
		srv := socketOver(t, peerFor(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:1000])
		}))
		_, got, err := readLog(t, srv, path)
		assert.Error(t, err, "a short log must not read as a complete one")
		assert.Less(t, len(got), len(payload))
	})

	t.Run("peer stalls past the log time", func(t *testing.T) {
		old := logForwardTimeout
		logForwardTimeout = 300 * time.Millisecond
		t.Cleanup(func() { logForwardTimeout = old })
		srv := socketOver(t, peerFor(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:1000])
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Second)
			_, _ = w.Write(payload[1000:])
		}))
		_, _, err := readLog(t, srv, path)
		assert.Error(t, err)
	})

	t.Run("peer states no length", func(t *testing.T) {
		srv := socketOver(t, peerFor(func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush() // chunked: no Content-Length
			_, _ = w.Write(payload[:100])
		}))
		code, _, _ := readLog(t, srv, path)
		assert.Equal(t, 502, code)
	})

	t.Run("peer states a length no log can have", func(t *testing.T) {
		srv := socketOver(t, peerFor(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(maxForwardedLog+1))
			w.(http.Flusher).Flush()
		}))
		code, _, _ := readLog(t, srv, path)
		assert.Equal(t, 502, code)
	})
}

// A daemon built without a queue has no job routes: the TCP surface answers 404 behind the token, the socket keeps its 501 for a
// submission.
func TestNoQueueNoJobRoutes(t *testing.T) {
	d := newTestDaemon(t)
	assert.Equal(t, 404, do(d.TCPHandler(), "GET", "/v1/jobs", "Bearer "+testToken).Code)
	assert.Equal(t, 501, do(d.SocketHandler(), "POST", "/v1/jobs", "").Code)
}
