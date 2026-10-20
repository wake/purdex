package origin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/jobsapi"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/pick"
	"github.com/wake/purdex/internal/wwd/worker/report"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const token = "pool-token"

type rig struct {
	q    *queue.Queue
	peer *Peer
	loc  Local
	srv  *httptest.Server
}

// newRig is a real queue behind the real worker routes (the TCP view, behind a token check) and the same queue in process.
func newRig(t *testing.T) *rig {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wwd.db") + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, queue.Migrate(context.Background(), db))
	q, err := queue.New(db, queue.Options{Origin: "mlab", LogDir: t.TempDir(), Now: time.Now})
	require.NoError(t, err)
	mux := http.NewServeMux()
	a := jobsapi.New(q)
	a.WorkerRoutes(mux, true)
	mux.HandleFunc("GET /v1/jobs", a.List(true))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &rig{q: q, peer: NewPeer(srv.URL+"/", token, srv.Client()), loc: Local{Q: q}, srv: srv}
}

func (r *rig) submit(t *testing.T, edit ...func(*wire.JobSubmission)) string {
	t.Helper()
	s := wire.JobSubmission{
		Repo: wire.Repo{Name: "purdex", Root: "abc", Remote: "git@github.com:wake/purdex.git"}, Snapshot: wire.Snapshot{Commit: "c0ffee", Tree: "7ee", Base: "ba5e"},
		Task: "t", Class: wire.ClassDev, Labels: []string{}, Locks: []string{}, TimeoutS: 60, GitDir: "/repos/purdex/.git",
		Submitter: wire.Submitter{Session: "s1", Host: "mlab"},
	}
	for _, e := range edit {
		e(&s)
	}
	resp, err := r.q.Submit(context.Background(), s)
	require.NoError(t, err)
	return resp.ID
}

// life runs a job's whole life through one origin and checks that the log and the result reach the queue.
func life(t *testing.T, r *rig, o interface {
	runner.Origin
	report.Sink
}) {
	t.Helper()
	ctx := context.Background()
	id := r.submit(t)
	c, err := o.Claim(ctx, id, wire.ClaimRequest{Worker: "air26", Labels: []string{}})
	require.NoError(t, err)
	assert.Equal(t, id, c.Job.ID)
	lease, err := o.Start(ctx, id, c.ClaimID)
	require.NoError(t, err)
	assert.Positive(t, lease.LeaseS)
	hb, err := o.Heartbeat(ctx, id, c.ClaimID)
	require.NoError(t, err)
	assert.False(t, hb.Cancel)
	_, err = r.q.Cancel(ctx, id)
	require.NoError(t, err)
	hb, err = o.Heartbeat(ctx, id, c.ClaimID)
	require.NoError(t, err)
	assert.True(t, hb.Cancel)
	require.NoError(t, o.PutLog(ctx, id, c.ClaimID, strings.NewReader("hello\n")))
	require.NoError(t, o.Result(ctx, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateCancelled}))
	j, err := r.q.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, wire.StateCancelled, j.State)
	body, _, err := r.q.OpenLog(ctx, id, false)
	require.NoError(t, err)
	defer body.Close()
	buf := make([]byte, 16)
	n, _ := body.Read(buf)
	assert.Equal(t, "hello\n", string(buf[:n]))
}

func TestAJobsLifeThroughPeerAndThroughLocal(t *testing.T) {
	r := newRig(t)
	t.Run("peer", func(t *testing.T) { life(t, r, r.peer) })
	t.Run("local", func(t *testing.T) { life(t, r, r.loc) })
}

func TestRefusalsAreTheSameKindOfErrorEitherWay(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for name, o := range map[string]interface {
		runner.Origin
		report.Sink
	}{"peer": r.peer, "local": r.loc} {
		id := r.submit(t)
		c, err := o.Claim(ctx, id, wire.ClaimRequest{Worker: "w"})
		require.NoError(t, err, name)

		var conf *runner.Conflict
		_, err = o.Claim(ctx, id, wire.ClaimRequest{Worker: "w2"})
		require.ErrorAs(t, err, &conf, name+": a second claim")
		assert.Equal(t, wire.StateClaimed, conf.State, name)

		stale := strings.Repeat("0", 32)
		_, err = o.Heartbeat(ctx, id, stale)
		require.ErrorAs(t, err, &conf, name+": a stale claim id")

		_, err = o.Heartbeat(ctx, "j_nothere", c.ClaimID)
		assert.ErrorIs(t, err, runner.ErrNotFound, name+": an unknown job")

		require.NoError(t, o.Release(ctx, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}), name)
		_, err = o.Start(ctx, id, c.ClaimID)
		require.ErrorAs(t, err, &conf, name+": a released claim")
		assert.Equal(t, wire.StateQueued, conf.State, name)
	}
}

func TestPeerClaimOfAdHocIsRefusedLocalClaimIsNot(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id := r.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true })

	var conf *runner.Conflict
	_, err := r.peer.Claim(ctx, id, wire.ClaimRequest{Worker: "air26"})
	require.ErrorAs(t, err, &conf, "spec 4.4: a peer never takes an ad-hoc job")

	_, err = r.loc.Claim(ctx, id, wire.ClaimRequest{Worker: "mlab"})
	assert.NoError(t, err, "the host's own worker may")
}

func TestLocalQueuedHasTwoViews(t *testing.T) {
	r := newRig(t)
	plain := r.submit(t)
	adhoc := r.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true })
	ids := func(forPeers bool) []string {
		jobs, err := r.loc.Queued(context.Background(), forPeers)
		require.NoError(t, err)
		var out []string
		for _, j := range jobs {
			out = append(out, j.ID)
		}
		return out
	}
	taken := r.submit(t)
	_, err := r.loc.Claim(context.Background(), taken, wire.ClaimRequest{Worker: "w"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{plain, adhoc}, ids(false), "only queued jobs are listed")
	assert.Equal(t, []string{plain}, ids(true))
	var _ pick.QueueReader = r.loc
}

func TestPeerSendsTheTokenAndANonAnswerIsNotAConflict(t *testing.T) {
	r := newRig(t)
	bad := NewPeer(r.srv.URL, "wrong", r.srv.Client())
	_, err := bad.Heartbeat(context.Background(), "j_x", strings.Repeat("0", 32))
	require.Error(t, err)
	var conf *runner.Conflict
	assert.False(t, errors.As(err, &conf) || errors.Is(err, runner.ErrNotFound), "a 401 is a failure to reach the origin, retryable by the caller, not a decision: %v", err)
	assert.NotContains(t, err.Error(), "wrong", "the token is never in an error")
}

func TestPeerDoesNotFollowARedirectWithTheToken(t *testing.T) {
	var seen bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = true }))
	defer other.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	_, err := NewPeer(redir.URL, token, nil).Heartbeat(context.Background(), "j_x", strings.Repeat("0", 32))
	assert.Error(t, err)
	assert.False(t, seen, "the pool token must not be sent to wherever a peer points it")
}

func TestPeerUnreadableConflictBodyIsAnErrorNotAConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("<html>proxy</html>"))
	}))
	defer srv.Close()
	_, err := NewPeer(srv.URL, token, nil).Start(context.Background(), "j_x", strings.Repeat("0", 32))
	var conf *runner.Conflict
	require.Error(t, err)
	assert.False(t, errors.As(err, &conf), "a 409 from something that is not wwd must not end a job")
}

// A peer's answer is read for what the contract says — framing, types, values — but a field of a later build is skipped (#3710).
func TestPeerAnswerToleratesAFieldOfALaterBuildButNotAWrongValue(t *testing.T) {
	for body, wantErr := range map[string]bool{
		`{"lease_s":60,"renew_s":15,"cancel":false,"surprise":1}`:      false,
		`{"lease_s":"sixty","renew_s":15,"cancel":false,"surprise":1}`: true,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := NewPeer(srv.URL, token, nil).Heartbeat(context.Background(), "j_x", strings.Repeat("0", 32))
		assert.Equal(t, wantErr, err != nil, body)
		srv.Close()
	}
}

func TestPeerRefusesAnAnswerOverTheLimitEvenWhenItsPrefixIsValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"lease_s":60,"renew_s":15,"cancel":false}`
		_, _ = w.Write([]byte(body))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxAnswerSize))) // trailing whitespace is valid JSON; only the size can refuse it
	}))
	defer srv.Close()
	_, err := NewPeer(srv.URL, token, nil).Heartbeat(context.Background(), "j_x", strings.Repeat("0", 32))
	assert.Error(t, err)
}

func TestPeerClaimMustAnswerWithTheJobAskedFor(t *testing.T) {
	r := newRig(t)
	other := r.submit(t)
	got, err := r.q.Claim(context.Background(), other, wire.ClaimRequest{Worker: "w"})
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(got) // a peer that hands back another job whatever it was asked
	}))
	defer srv.Close()
	_, err = NewPeer(srv.URL, token, nil).Claim(context.Background(), "j_asked", wire.ClaimRequest{Worker: "w"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "answered with job")
}

func TestOnlyWwdsOwnNotFoundIsADecision(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "html": "<html>404</html>", "other json": `{"error":"route"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(body))
		}))
		_, err := NewPeer(srv.URL, token, nil).Heartbeat(context.Background(), "j_x", strings.Repeat("0", 32))
		assert.Error(t, err, name)
		assert.False(t, errors.Is(err, runner.ErrNotFound), "%s: a 404 that is not wwd's must stay retryable", name)
		srv.Close()
	}
}

func TestPeerClaimNotFoundIsNeverADecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such job"}`))
	}))
	defer srv.Close()
	_, err := NewPeer(srv.URL, token, nil).Claim(context.Background(), "j_x", wire.ClaimRequest{Worker: "w"})
	require.Error(t, err)
	assert.False(t, errors.Is(err, runner.ErrNotFound))
}
