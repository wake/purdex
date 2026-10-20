package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// fakeDial answers the probe of the SSH port as the test says; it notes every address it was asked for.
type fakeDial struct {
	mu    sync.Mutex
	asked []string
	err   error
}

func (f *fakeDial) dial(_ context.Context, _, address string) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, address)
	if f.err != nil {
		return nil, f.err
	}
	c, s := net.Pipe()
	_ = s.Close()
	return c, nil
}

func (f *fakeDial) set(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *fakeDial) addrs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func livenessRig(t *testing.T) (*PoolPoller, *lastPeer, *fakeDial) {
	t.Helper()
	air19 := newLastPeer(t, "air19", wire.Proto)
	p := NewPoolPoller([]PeerAddr{{"air19", addr(air19.srv)}}, testToken, nil)
	d := &fakeDial{}
	p.dial = d.dial
	return p, air19, d
}

// A peer that answers is ok and its SSH port is not looked at; one never looked at is unknown.
func TestLivenessIsUnknownUntilALookAndOkWhileTheWwdAnswers(t *testing.T) {
	p, _, d := livenessRig(t)
	assert.Equal(t, wire.LivenessUnknown, p.Peers()[0].Liveness)
	p.PollOnce(context.Background())
	got := p.Peers()[0]
	assert.Equal(t, wire.LivenessOK, got.Liveness)
	require.NotNil(t, got.LivenessAt)
	assert.Empty(t, d.addrs(), "a wwd that answers needs no look at the host")
}

// The wwd does not answer: a connect to the SSH port of its address says whether the host does. Connected, or refused (something answered
// with a refusal): the host is there. Any other failure (time out, no route): nothing answers.
func TestLivenessSaysWhetherTheHostAnswersWhenTheWwdDoesNot(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want wire.Liveness
	}{
		{"connects", nil, wire.LivenessWwdDown},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, wire.LivenessWwdDown},
		{"time out", context.DeadlineExceeded, wire.LivenessHostDown},
		{"no route", &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}, wire.LivenessHostDown},
		{"network down", &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}, wire.LivenessHostDown},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, air19, d := livenessRig(t)
			air19.down.Store(true)
			d.set(c.err)
			p.PollOnce(context.Background())
			got := p.Peers()[0]
			assert.Equal(t, c.want, got.Liveness)
			require.NotNil(t, got.LivenessAt)
			host, _, err := net.SplitHostPort(addr(air19.srv))
			require.NoError(t, err)
			assert.Equal(t, []string{net.JoinHostPort(host, "22")}, d.addrs(), "the SSH port of the peer's address, once")
			assert.Equal(t, c.want, p.Downs()[0].Liveness, "the silence rules are told the same")
		})
	}
}

// A wwd that answers with something wrong (an error status, a body that is not info, another host's name or facts) is proof that its host is
// up: the SSH port is not asked, so a host that keeps 22 closed is never reported as off.
func TestAnAnswerThatIsWrongProvesTheHostIsUp(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"error status": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusInternalServerError) },
		"not json":     func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"other alias": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, wire.Info{HostID: "id-x", Alias: "someone-else", Version: "v", Proto: wire.Proto, Platform: "darwin", Arch: "amd64", Labels: []string{}})
		},
		"other host's facts": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/info":
				writeJSON(w, 200, wire.Info{HostID: "id-air19", Alias: "air19", Version: "v", Proto: wire.Proto, Platform: "darwin", Arch: "amd64", Labels: []string{}})
			case "/v1/facts":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(factsFor(wireFixture(t, "facts_darwin.json"), "someone-else")))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)
			p := NewPoolPoller([]PeerAddr{{"air19", addr(srv)}}, testToken, nil)
			d := &fakeDial{}
			d.set(context.DeadlineExceeded) // if it were asked, the host would be called off
			p.dial = d.dial
			p.PollOnce(context.Background())
			got := p.Peers()[0]
			assert.False(t, got.Reachable)
			assert.Equal(t, wire.LivenessWwdDown, got.Liveness)
			assert.Empty(t, d.addrs(), "the host answered over HTTP: no look at the SSH port")
		})
	}
}

// A connection that goes away after the headers (or in the middle of the body) is not an answer: the wwd did not answer, and the host is
// looked at.
func TestAConnectionCutInTheMiddleOfAnAnswerIsLookedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"host_id":"id-air19",`))
		panic(http.ErrAbortHandler)
	}))
	t.Cleanup(srv.Close)
	p := NewPoolPoller([]PeerAddr{{"air19", addr(srv)}}, testToken, nil)
	d := &fakeDial{}
	d.set(context.DeadlineExceeded)
	p.dial = d.dial
	p.PollOnce(context.Background())
	assert.Equal(t, wire.LivenessHostDown, p.Peers()[0].Liveness)
	assert.Len(t, d.addrs(), 1)
}

// Two times: when the peer was last looked at (moves at every look) and since when it has been in this liveness (moves only when the value
// does, and is set when it comes back to ok too), which is what the App shows as "offline for 12 minutes".
func TestLivenessSinceMovesOnlyWhenTheValueDoes(t *testing.T) {
	p, air19, d := livenessRig(t)
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	now := t0
	p.now = func() time.Time { return now }
	air19.down.Store(true)
	d.set(context.DeadlineExceeded)
	p.PollOnce(context.Background())
	got := p.Peers()[0]
	assert.Equal(t, t0, *got.LivenessSince)
	assert.Equal(t, t0, *got.LivenessAt)

	now = t0.Add(12 * time.Minute)
	p.PollOnce(context.Background())
	got = p.Peers()[0]
	assert.Equal(t, t0, *got.LivenessSince, "still host_down: since does not move")
	assert.Equal(t, now, *got.LivenessAt, "but it was looked at again")

	d.set(nil)
	now = now.Add(time.Minute)
	p.PollOnce(context.Background())
	got = p.Peers()[0]
	assert.Equal(t, wire.LivenessWwdDown, got.Liveness)
	assert.Equal(t, now, *got.LivenessSince, "another value: since is now")

	air19.down.Store(false)
	now = now.Add(time.Minute)
	p.PollOnce(context.Background())
	got = p.Peers()[0]
	assert.Equal(t, wire.LivenessOK, got.Liveness)
	assert.Equal(t, now, *got.LivenessSince, "back to ok is a change too")
	now = now.Add(time.Minute)
	p.PollOnce(context.Background())
	got = p.Peers()[0]
	assert.Equal(t, now.Add(-time.Minute), *got.LivenessSince, "ok stays ok: since stays")
	assert.Equal(t, now, *got.LivenessAt)
}

// Back to ok when the wwd answers again, and the look is not made again while it does.
func TestLivenessIsOkAgainWhenTheWwdAnswers(t *testing.T) {
	p, air19, d := livenessRig(t)
	air19.down.Store(true)
	d.set(context.DeadlineExceeded)
	p.PollOnce(context.Background())
	require.Equal(t, wire.LivenessHostDown, p.Peers()[0].Liveness)
	air19.down.Store(false)
	p.PollOnce(context.Background())
	assert.Equal(t, wire.LivenessOK, p.Peers()[0].Liveness)
	assert.Equal(t, wire.LivenessOK, p.Downs()[0].Liveness)
	assert.Len(t, d.addrs(), 1)
}

// A look that is cut short because the wwd is stopping says nothing about the host.
func TestACancelledLookLeavesLivenessAlone(t *testing.T) {
	p, air19, d := livenessRig(t)
	air19.down.Store(true)
	d.set(errors.New("operation was canceled"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.PollOnce(ctx)
	assert.Equal(t, wire.LivenessUnknown, p.Peers()[0].Liveness)
}

// GET /v1/pool carries the liveness only for a reader that asks for it: the Purdex daemon that reads this route is not a wwd of the pool,
// and an older one would reject a name it does not know.
func TestPoolRouteCarriesLivenessOnlyWhenAsked(t *testing.T) {
	_, srv := peerDaemon(t, "air26", testToken)
	d := New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Peers: []PeerAddr{{"air26", addr(srv)}}})
	dial := &fakeDial{}
	d.Pool().dial = dial.dial
	d.Pool().PollOnce(context.Background())

	plain := do(d.SocketHandler(), "GET", "/v1/pool", "")
	require.Equal(t, 200, plain.Code, plain.Body.String())
	assert.NotContains(t, plain.Body.String(), "liveness", "an older reader sees exactly the shape it knew")

	asked := do(d.SocketHandler(), "GET", "/v1/pool?"+wire.PoolLivenessQuery, "")
	require.Equal(t, 200, asked.Code, asked.Body.String())
	pool, err := wire.Decode[wire.Pool](asked.Body.Bytes())
	require.NoError(t, err)
	require.Len(t, pool.Peers, 2)
	assert.Empty(t, pool.Peers[0].Liveness, "this host's own entry has none")
	assert.Equal(t, wire.LivenessOK, pool.Peers[1].Liveness)
	assert.NotNil(t, pool.Peers[1].LivenessAt)
	assert.NotNil(t, pool.Peers[1].LivenessSince)

	// A query this build does not know is ignored, not refused: a newer Purdex daemon asks an older wwd with liveness=1 (the daemon is
	// upgraded first), and an older wwd's route never read the query at all.
	for _, q := range []string{"?surprise=1", "?liveness=0", "?liveness=yes&x=y"} {
		other := do(d.SocketHandler(), "GET", "/v1/pool"+q, "")
		require.Equal(t, 200, other.Code, "%s: %s", q, other.Body.String())
		assert.NotContains(t, other.Body.String(), "liveness", q)
	}
}
