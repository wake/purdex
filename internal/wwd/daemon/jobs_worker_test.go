package daemon

import (
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/jobsapi"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// laneHost is a daemon wired as cmd/wwd wires it: one queue whose events feed the fast lane, the fast lane over the host's lock table, and
// the worker routes on the socket — on a real TCP port and a real unix socket.
type laneHost struct {
	*jobHost
	lane *localruns.Service
}

func newLaneHost(t *testing.T, withClaimRoutes bool, edit ...func(*Options)) *laneHost {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wwd.db") + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, queue.Migrate(context.Background(), db))
	var lane *localruns.Service
	q, err := queue.New(db, queue.Options{Origin: "mlab", LogDir: t.TempDir(), Publish: func(ev wire.JobEvent) { lane.OnJobEvent(ev) }})
	require.NoError(t, err)
	lane = localruns.New(locks.New(), localruns.FromQueue(q), "mlab")
	require.NoError(t, lane.Recover(context.Background()))

	o := Options{HostID: "mlab-id", Alias: "mlab", Version: "v0", Token: testToken, Queue: q, LocalRuns: lane}
	if withClaimRoutes {
		o.ClaimRoutes = func(mux *http.ServeMux) { jobsapi.New(q).WorkerRoutes(mux, false) }
	}
	for _, e := range edit {
		e(&o)
	}
	d := New(o)
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "w.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	run, err := d.Listen(ctx, ListenSpec{Addrs: []string{"127.0.0.1"}, Port: 0, SocketPath: sock})
	require.NoError(t, err)
	t.Cleanup(func() { run.Close() })
	jh := &jobHost{q: q, run: run, sock: sock, tcp: run.TCPAddrs()[0], local: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}}
	return &laneHost{jobHost: jh, lane: lane}
}

func localRun(keys ...string) wire.LocalRunRequest {
	s := submissionBody(func(s *wire.JobSubmission) { s.Locks = keys; s.SupersedeKey = "" })
	return wire.LocalRunRequest{JobSubmission: s}
}

// The fast lane end to end through the real socket: a run is registered (its lock keys held), heartbeats, uploads its log and reports its
// result with the claim it was given; a second run with the same key is refused while the first is going and accepted once it has ended.
func TestFastLaneEndToEndOverTheRealSocket(t *testing.T) {
	h := newLaneHost(t, true)

	code, raw, _ := h.socketDo(t, "POST", "/v1/local-runs", localRun("db"))
	require.Equal(t, 200, code, string(raw))
	run, err := wire.Decode[wire.LocalRunResponse](raw)
	require.NoError(t, err)

	code, raw, _ = h.socketDo(t, "POST", "/v1/local-runs", localRun("db"))
	require.Equal(t, 409, code, "the key is held by the first run")
	busy, err := wire.Decode[wire.LocksBusy](raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"db"}, busy.Keys)

	code, raw, _ = h.socketDo(t, "POST", "/v1/jobs/"+run.ID+"/heartbeat", wire.HeartbeatRequest{ClaimID: run.ClaimID})
	require.Equal(t, 200, code, string(raw))
	hb, err := wire.Decode[wire.HeartbeatResponse](raw)
	require.NoError(t, err)
	assert.False(t, hb.Cancel)

	code, raw = putLog(t, h, run.ID, run.ClaimID, "ran fine\n")
	require.Equal(t, 200, code, string(raw))

	code, raw, _ = h.socketDo(t, "POST", "/v1/jobs/"+run.ID+"/result", wire.ResultRequest{ClaimID: run.ClaimID, State: wire.StateSucceeded, Exit: new(int)})
	require.Equal(t, 200, code, string(raw))
	job := mustJob(t, raw)
	assert.Equal(t, wire.StateSucceeded, job.State)
	assert.True(t, job.Local)
	assert.Equal(t, run.ID+"/log", job.Result.Log)

	code, raw, _ = h.socketDo(t, "POST", "/v1/local-runs", localRun("db"))
	require.Equal(t, 200, code, "the first run ended, so its key is free: %s", raw)
	second, err := wire.Decode[wire.LocalRunResponse](raw)
	require.NoError(t, err)
	assert.Equal(t, []string{second.ID}, h.lane.Held(), "only the new run holds a key now")

	// a fast-lane run is the host's own: a peer sees nothing of it
	code, raw, _ = h.tcpDo(t, "GET", "/v1/jobs", "Bearer "+testToken, nil)
	assert.Empty(t, h.listIDs(t, code, raw))
	code, _, _ = h.tcpDo(t, "GET", "/v1/jobs/"+run.ID, "Bearer "+testToken, nil)
	assert.Equal(t, 404, code)
}

func putLog(t *testing.T, h *laneHost, id, claimID, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("PUT", "http://wwd/v1/jobs/"+id+"/log?claim_id="+claimID, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := h.local.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, b
}

// A worker on another host does a job's whole life over the real TCP port with the pool token — claim, start, heartbeat, log, result — and
// the same calls without the token are refused. The snapshot route is there too and answers a claim that is not live with the 409 and the
// job's state.
func TestWorkerLifeOverTheRealTCPPort(t *testing.T) {
	h := newLaneHost(t, true)
	_, raw, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody())
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	auth := "Bearer " + testToken

	code, _, _ := h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/claim", "", wire.ClaimRequest{Worker: "air26"})
	assert.Equal(t, 401, code, "no token")
	code, raw, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/claim", auth, wire.ClaimRequest{Worker: "air26"})
	require.Equal(t, 200, code, string(raw))
	claim, err := wire.Decode[wire.ClaimResponse](raw)
	require.NoError(t, err)

	code, raw, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/claim", auth, wire.ClaimRequest{Worker: "mserv"})
	require.Equal(t, 409, code, "first wins")
	assert.Equal(t, wire.StateClaimed, mustConflict(t, raw).State)

	for _, step := range []struct {
		path string
		body any
	}{
		{"/start", wire.StartRequest{ClaimID: claim.ClaimID}},
		{"/heartbeat", wire.HeartbeatRequest{ClaimID: claim.ClaimID}},
	} {
		code, raw, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+step.path, auth, step.body)
		require.Equal(t, 200, code, step.path+": "+string(raw))
		code, _, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+step.path, "", step.body)
		assert.Equal(t, 401, code, step.path+" without the token")
	}
	code, raw, _ = h.tcpDo(t, "PUT", "/v1/jobs/"+sub.ID+"/log?claim_id="+claim.ClaimID, auth, nil)
	require.Equal(t, 200, code, string(raw))
	code, raw, _ = h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/result", auth, wire.ResultRequest{ClaimID: claim.ClaimID, State: wire.StateFailed, Exit: ptrInt(1), Reason: "timeout"})
	require.Equal(t, 200, code, string(raw))
	job := mustJob(t, raw)
	assert.Equal(t, wire.StateFailed, job.State)
	assert.Equal(t, "timeout", job.Result.Reason)

	// the snapshot of a job whose claim has ended is refused with the job's state, not served
	code, raw, _ = h.tcpDo(t, "GET", "/v1/jobs/"+sub.ID+"/snapshot?claim_id="+claim.ClaimID+"&format=tar", auth, nil)
	require.Equal(t, 409, code, string(raw))
	assert.Equal(t, wire.StateFailed, mustConflict(t, raw).State)
	code, _, _ = h.tcpDo(t, "GET", "/v1/jobs/"+sub.ID+"/snapshot?claim_id="+claim.ClaimID+"&format=tar", "", nil)
	assert.Equal(t, 401, code)
}

func mustConflict(t *testing.T, raw []byte) wire.Conflict409 {
	t.Helper()
	c, err := wire.Decode[wire.Conflict409](raw)
	require.NoError(t, err, string(raw))
	return c
}

func ptrInt(n int) *int { return &n }

// A peer cannot claim the host's ad-hoc job over TCP (spec 4.4, 11); the host's own worker can, on the socket.
func TestPeerCannotClaimAnAdHocJobOverTCP(t *testing.T) {
	h := newLaneHost(t, true)
	_, raw, _ := h.socketDo(t, "POST", "/v1/jobs", submissionBody(func(s *wire.JobSubmission) { s.AdHoc = true }))
	sub, err := wire.Decode[wire.JobSubmissionResponse](raw)
	require.NoError(t, err)
	code, _, _ := h.tcpDo(t, "POST", "/v1/jobs/"+sub.ID+"/claim", "Bearer "+testToken, wire.ClaimRequest{Worker: "air26"})
	assert.Equal(t, 409, code)
	j, err := h.q.Get(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, j.State, "the refused claim changed nothing")
	code, _, _ = h.socketDo(t, "POST", "/v1/jobs/"+sub.ID+"/claim", wire.ClaimRequest{Worker: "mlab"})
	assert.Equal(t, 200, code)
}

// Without the claim routes the socket has neither the worker routes nor the fast lane (a run that cannot heartbeat or report must not
// be handed a claim); TCP keeps its worker routes either way.
func TestSocketWorkerRoutesAndFastLaneOpenTogether(t *testing.T) {
	h := newLaneHost(t, false)
	code, _, _ := h.socketDo(t, "POST", "/v1/local-runs", localRun())
	assert.Equal(t, 404, code)
	code, _, _ = h.socketDo(t, "POST", "/v1/jobs/j_x/heartbeat", wire.HeartbeatRequest{ClaimID: wire.NewClaimID()})
	assert.Equal(t, 404, code)
	code, _, _ = h.tcpDo(t, "POST", "/v1/jobs/j_x/heartbeat", "Bearer "+testToken, wire.HeartbeatRequest{ClaimID: wire.NewClaimID()})
	assert.Equal(t, 404, code, "TCP has the route; the job is simply not there")
}
