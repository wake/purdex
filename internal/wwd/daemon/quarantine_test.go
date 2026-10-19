package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

const quarLabel = "task:purdex/tsc"

var quarGranted = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// recordingCache is the cache the quarantine drops from: it only remembers what it was asked.
type recordingCache struct {
	mu    sync.Mutex
	drops [][2]string
	err   error
}

func (c *recordingCache) DeleteCacheByLabelAndHost(label, host string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drops = append(c.drops, [2]string{label, host})
	return 1, c.err
}

func (c *recordingCache) dropped() [][2]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][2]string(nil), c.drops...)
}

// quarRig is a host named mserv with a real earned-label table holding quarLabel, granted at quarGranted.
type quarRig struct {
	d     *Daemon
	st    *store.Store
	cache *recordingCache
}

func newQuarRig(t *testing.T, o Options) *quarRig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	require.NoError(t, st.SaveEarnedLabel(wire.EarnedLabel{Label: quarLabel, Fingerprint: map[string]string{"node": "24"}, Snapshot: "abc", GrantedAt: quarGranted, State: wire.EarnedLabelEarned}))
	cache := &recordingCache{}
	o.HostID, o.Alias, o.Version, o.Token = "h1", "mserv", "v0", testToken
	o.Labels, o.CacheDrop, o.Earned = st, cache, st
	return &quarRig{d: New(o), st: st, cache: cache}
}

func (r *quarRig) state(t *testing.T) string {
	t.Helper()
	e, ok, err := r.st.EarnedLabel(quarLabel)
	require.NoError(t, err)
	if !ok {
		return "none"
	}
	return e.State
}

func disagreementFor(id int64, host, other string, at time.Time) wire.Disagreement {
	return wire.Disagreement{ID: id, Label: quarLabel, Host: host, OtherHost: other, Tree: "7ee", Task: "tsc",
		Jobs: [2]string{"j_old" + string(rune('a'+id)), "j_new" + string(rune('a'+id))}, At: at}
}

// peerOrigin is a peer that offers the given disagreements, and counts and remembers what it was asked.
type peerOrigin struct {
	mu      sync.Mutex
	items   []wire.Disagreement
	status  int // 0: 200
	queries []string
	subject string // what it answers for; "" is the asked subject
}

func (p *peerOrigin) serve(alias string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.URL.Path != "/v1/outcomes/disagreements" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p.queries = append(p.queries, r.URL.RawQuery)
		if p.status != 0 {
			w.WriteHeader(p.status)
			return
		}
		after := int64(0)
		if v := r.URL.Query().Get("after"); v != "" {
			for _, c := range v {
				after = after*10 + int64(c-'0')
			}
		}
		subject := r.URL.Query().Get("subject")
		if p.subject != "" {
			subject = p.subject
		}
		out := wire.DisagreementList{Host: subject, Origin: alias, Cursor: after, Items: []wire.Disagreement{}}
		for _, x := range p.items {
			if x.ID > after {
				out.Items = append(out.Items, x)
				out.Cursor = x.ID
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func (p *peerOrigin) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.queries...)
}

// A disagreement a peer origin offers about this host's earned label quarantines it, drops the reuse entries the host made under it,
// and is not read again by the next pass.
func TestAPeersDisagreementQuarantinesTheLabel(t *testing.T) {
	origin := &peerOrigin{items: []wire.Disagreement{disagreementFor(1, "mserv", "air26", quarGranted.Add(time.Hour))}}
	srv := origin.serve("air26")
	defer srv.Close()
	r := newQuarRig(t, Options{Peers: []PeerAddr{{"air26", addr(srv)}}})
	r.d.OnFacts(linuxFacts)
	advertised := func() bool { l, _ := r.d.Labels(); return slices.Contains(l, quarLabel) }
	require.True(t, advertised(), "the earned label is advertised")

	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelQuarantined, r.state(t))
	assert.False(t, advertised(), "and stops being advertised at once")
	assert.Equal(t, [][2]string{{quarLabel, "mserv"}}, r.cache.dropped())

	r.d.QuarantinePass(t.Context())
	qs := origin.asked()
	require.Len(t, qs, 2)
	assert.Contains(t, qs[0], "subject=mserv")
	assert.Contains(t, qs[0], "after=0")
	assert.Contains(t, qs[1], "after=1", "the second pass reads from the cursor")
	assert.Len(t, r.cache.dropped(), 1, "nothing is dropped twice")
}

// What does not take the label: a disagreement from before it was granted, about another host, a label that is not there, or one that
// is quarantined already.
func TestADisagreementThatDoesNotConcernTheLabelChangesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		x    wire.Disagreement
		want string
	}{
		"from before the grant": {x: disagreementFor(1, "mserv", "air26", quarGranted.Add(-time.Hour)), want: wire.EarnedLabelEarned},
		"at the grant instant":  {x: disagreementFor(1, "mserv", "air26", quarGranted), want: wire.EarnedLabelEarned},
		"about another host":    {x: disagreementFor(1, "mini", "air26", quarGranted.Add(time.Hour)), want: wire.EarnedLabelEarned},
	} {
		r := newQuarRig(t, Options{})
		changed, err := r.d.ApplyDisagreement(tc.x)
		require.NoError(t, err, name)
		assert.False(t, changed, name)
		assert.Equal(t, tc.want, r.state(t), name)
		assert.Empty(t, r.cache.dropped(), name)
	}
	r := newQuarRig(t, Options{})
	x := disagreementFor(1, "mserv", "air26", quarGranted.Add(time.Hour))
	x.Label = "task:purdex/other"
	changed, err := r.d.ApplyDisagreement(x)
	require.NoError(t, err)
	assert.False(t, changed, "a label this host does not have")
	changed, err = r.d.ApplyDisagreement(disagreementFor(2, "mserv", "air26", quarGranted.Add(time.Hour)))
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = r.d.ApplyDisagreement(disagreementFor(3, "mserv", "air26", quarGranted.Add(2*time.Hour)))
	require.NoError(t, err)
	assert.False(t, changed, "already quarantined")
	assert.Len(t, r.cache.dropped(), 1)
}

// A peer that is an older build (404), one that does not answer, and one whose answer is not for the host asked about leave the label
// as it is; the pass goes on to the next origin.
func TestAPeerThatCannotOfferLeavesTheLabelAndTheOthersAreRead(t *testing.T) {
	old := (&peerOrigin{status: http.StatusNotFound}).serve("air19")
	defer old.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	downAddr := addr(down)
	down.Close()
	wrong := (&peerOrigin{subject: "someone-else", items: []wire.Disagreement{disagreementFor(1, "mserv", "x", quarGranted.Add(time.Hour))}}).serve("air26")
	defer wrong.Close()
	good := (&peerOrigin{items: []wire.Disagreement{disagreementFor(1, "mserv", "air26", quarGranted.Add(time.Hour))}}).serve("mini")
	defer good.Close()
	r := newQuarRig(t, Options{Peers: []PeerAddr{{"air19", addr(old)}, {"gone", downAddr}, {"air26", addr(wrong)}, {"mini", addr(good)}}})

	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelQuarantined, r.state(t), "the good origin was still read")
	assert.NotContains(t, r.d.quar.failed, "air19", "an older build with nothing to offer is not a failure")
	assert.Contains(t, r.d.quar.failed, "gone", "one that does not answer is")
	assert.Contains(t, r.d.quar.failed, "air26", "and one that answers for another host")
}

// An answer that is for another host than the one asked about is refused even if its items name this host.
func TestAnAnswerForAnotherSubjectIsRefused(t *testing.T) {
	wrong := (&peerOrigin{subject: "someone-else", items: []wire.Disagreement{disagreementFor(1, "mserv", "x", quarGranted.Add(time.Hour))}}).serve("air26")
	defer wrong.Close()
	r := newQuarRig(t, Options{Peers: []PeerAddr{{"air26", addr(wrong)}}})
	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelEarned, r.state(t))
}

// A page with a disagreement that is not valid is refused whole: nothing in it is applied.
func TestAnInvalidPageIsRefusedWhole(t *testing.T) {
	bad := disagreementFor(2, "mserv", "air26", quarGranted.Add(time.Hour))
	bad.Jobs[1] = bad.Jobs[0]
	srv := (&peerOrigin{items: []wire.Disagreement{bad}}).serve("air26")
	defer srv.Close()
	r := newQuarRig(t, Options{Peers: []PeerAddr{{"air26", addr(srv)}}})
	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelEarned, r.state(t))
}

// The host's own queue is an origin too: the disagreement its own queue recorded about this host quarantines the label.
func TestTheOwnQueueIsAnOriginToo(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	require.NoError(t, err)
	defer st.Close()
	require.NoError(t, queue.Migrate(t.Context(), st.DB()))
	q, err := queue.New(st.DB(), queue.Options{Origin: "mserv", LogDir: t.TempDir()})
	require.NoError(t, err)
	at := quarGranted.Add(time.Hour)
	mk := func(id, host, standing string, ok bool) queue.Outcome {
		return queue.Outcome{JobID: id, Host: host, Label: quarLabel, Tree: "7ee", Task: "tsc", DefHash: "def1", Reference: "air26", OK: ok, Standing: standing, At: at}
	}
	_, err = q.RecordOutcome(t.Context(), mk("j_1", "air26", queue.StandingReference, true))
	require.NoError(t, err)
	_, err = q.RecordOutcome(t.Context(), mk("j_2", "mserv", queue.StandingEarned, false))
	require.NoError(t, err)

	r := newQuarRig(t, Options{Queue: q})
	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelQuarantined, r.state(t))
}

// A failure to write is met again: the cursor does not move past a disagreement that was not handled.
func TestAFailedWriteIsRetriedOnTheNextPass(t *testing.T) {
	origin := &peerOrigin{items: []wire.Disagreement{disagreementFor(1, "mserv", "air26", quarGranted.Add(time.Hour))}}
	srv := origin.serve("air26")
	defer srv.Close()
	r := newQuarRig(t, Options{Peers: []PeerAddr{{"air26", addr(srv)}}})
	r.d.opts.Labels = failingKeeper{LabelKeeper: r.st}
	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelEarned, r.state(t))
	r.d.opts.Labels = r.st
	r.d.QuarantinePass(t.Context())
	assert.Equal(t, wire.EarnedLabelQuarantined, r.state(t))
	qs := origin.asked()
	require.Len(t, qs, 2)
	assert.Contains(t, qs[1], "after=0", "read again from where it had handled up to")
}

type failingKeeper struct{ LabelKeeper }

func (failingKeeper) QuarantineEarnedLabel(string, time.Time) (bool, error) {
	return false, context.DeadlineExceeded
}

// By hand: quarantine and release go through the local socket only, and act on the label of this host.
func TestQuarantineAndReleaseByHand(t *testing.T) {
	r := newQuarRig(t, Options{})
	sock, tcp := r.d.SocketHandler(), r.d.TCPHandler()
	post := func(h http.Handler, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	good := `{"label":"` + quarLabel + `"}`
	assert.Equal(t, http.StatusNotFound, post(tcp, "/v1/earned-labels/quarantine", good).Code, "no such route over TCP")
	assert.Equal(t, http.StatusBadRequest, post(sock, "/v1/earned-labels/quarantine?host=air26", good).Code, "never forwarded")
	assert.Equal(t, http.StatusBadRequest, post(sock, "/v1/earned-labels/quarantine", `{"label":"os:linux"}`).Code)
	assert.Equal(t, http.StatusBadRequest, post(sock, "/v1/earned-labels/quarantine", `{"label":"`+quarLabel+`","x":1}`).Code)
	assert.Equal(t, http.StatusNotFound, post(sock, "/v1/earned-labels/quarantine", `{"label":"task:purdex/none"}`).Code)

	assert.Equal(t, http.StatusOK, post(sock, "/v1/earned-labels/quarantine", good).Code)
	assert.Equal(t, wire.EarnedLabelQuarantined, r.state(t))
	assert.Equal(t, [][2]string{{quarLabel, "mserv"}}, r.cache.dropped())

	assert.Equal(t, http.StatusOK, post(sock, "/v1/earned-labels/release", good).Code)
	assert.Equal(t, "none", r.state(t), "release deletes the row: the label is earned again by qualifying")
	assert.Equal(t, http.StatusNotFound, post(sock, "/v1/earned-labels/release", good).Code)
}
