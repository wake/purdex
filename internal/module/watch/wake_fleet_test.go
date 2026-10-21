package watchmod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/hostconfig"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watchwake"
)

const (
	srcHostID = "mlab:aaaaaa"
	folHostID = "air26:bbbbbb"

	hostconfigWakeKey = hostconfig.WakeRowStoreKey
)

// flakyRows is the host config's wake rows with a write of the copy's row that can be made to fail.
type flakyRows struct {
	hostconfig.WakeRowStore
	fail bool
}

func (f *flakyRows) Put(key string, raw []byte, base int64) (int64, error) {
	if f.fail && key == hostconfig.KeyWatchWakeCache {
		return 0, errors.New("disk full")
	}
	return f.WakeRowStore.Put(key, raw, base)
}

// linkCaller is the HostCaller of one host in a test: a call goes straight into the peer routes of the host it names, as the host principal of
// the caller, and comes back the way HostCaller would classify it. down makes the target unreachable.
type linkCaller struct {
	from  *wakeRig
	alias string
	id    string
	to    map[string]*wakeRig
	mu    sync.Mutex
	down  bool
	calls []callRec
}

type callRec struct {
	Target, Path string
	Body         map[string]any
}

func (c *linkCaller) Call(ctx context.Context, targetHostID, path string, body any) peersmod.CallResult {
	raw, err := json.Marshal(body)
	if err != nil {
		return peersmod.CallResult{Class: peersmod.ClassRefused, Err: err}
	}
	var rec map[string]any
	_ = json.Unmarshal(raw, &rec)
	c.mu.Lock()
	c.calls = append(c.calls, callRec{targetHostID, path, rec})
	down := c.down
	c.mu.Unlock()
	target := c.to[targetHostID]
	if down || target == nil {
		return peersmod.CallResult{Class: peersmod.ClassTransient, Err: assert.AnError}
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req = req.WithContext(middleware.WithPrincipal(ctx, middleware.Principal{Kind: middleware.PrincipalHost, Alias: c.alias, HostID: c.id}))
	rr := httptest.NewRecorder()
	target.mux.ServeHTTP(rr, req)
	res := peersmod.CallResult{Status: rr.Code}
	if rr.Code >= 200 && rr.Code < 300 {
		var ans struct {
			HostID string `json:"host_id"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &ans)
		if ans.HostID != targetHostID {
			res.Class = peersmod.ClassWrongHost
			return res
		}
		res.Class, res.Body = peersmod.ClassDone, json.RawMessage(rr.Body.Bytes())
		return res
	}
	res.Class = peersmod.ClassRefused
	return res
}

func (c *linkCaller) paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.calls {
		out = append(out, r.Path)
	}
	return out
}

func (c *linkCaller) reset() {
	c.mu.Lock()
	c.calls = nil
	c.mu.Unlock()
}

// fleet is two hosts that are paired: mlab (the source) and air26 (a follower).
type fleet struct {
	src, fol         *wakeRig
	srcCall, folCall *linkCaller
}

func newFleet(t *testing.T) *fleet {
	t.Helper()
	f := &fleet{src: newWakeRig(t, "mlab"), fol: newWakeRig(t, "air26")}
	f.src.m.core.Cfg.HostID, f.fol.m.core.Cfg.HostID = srcHostID, folHostID
	f.src.m.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "air26", HostID: folHostID, URL: "http://x", Token: "t", InboundToken: "i"}}
	f.fol.m.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "mlab", HostID: srcHostID, URL: "http://x", Token: "t", InboundToken: "i"}}
	ids := map[string]*wakeRig{srcHostID: f.src, folHostID: f.fol}
	f.srcCall = &linkCaller{from: f.src, alias: "mlab", id: srcHostID, to: ids}
	f.folCall = &linkCaller{from: f.fol, alias: "air26", id: folHostID, to: ids}
	f.src.m.caller, f.fol.m.caller = f.srcCall, f.folCall
	f.src.m.backgroundSync, f.fol.m.backgroundSync = true, true // the work that would go on a goroutine runs where it is asked
	return f
}

func (r *wakeRig) viewOf(t *testing.T) wakeSettingsView {
	t.Helper()
	rr := r.do(adminCtx, http.MethodGet, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var v wakeSettingsView
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	return v
}

func TestWakeFollow_NothingFetchedYetIsTheDefaultsStaleWithNoSourceID(t *testing.T) {
	f := newFleet(t)
	v := f.fol.viewOf(t)
	assert.False(t, v.IsSource)
	assert.True(t, v.Stale)
	assert.Nil(t, v.FetchedAt)
	assert.Equal(t, "", v.SourceID)
	assert.Equal(t, "mlab", v.Source)
	assert.Equal(t, int64(0), v.Revision)
	assert.Equal(t, watchwake.Etag(watchwake.Default("Asia/Taipei")), v.Etag)
}

func TestWakeFollow_APollTakesTheSourcesDocumentAndRevision(t *testing.T) {
	f := newFleet(t)
	want := f.src.viewOf(t) // the source migrates its watch row on this first read: revision 1
	f.fol.m.pollWake(context.Background())
	v := f.fol.viewOf(t)
	assert.False(t, v.IsSource)
	assert.False(t, v.Stale)
	assert.Equal(t, want.Revision, v.Revision)
	assert.Equal(t, want.Etag, v.Etag)
	assert.JSONEq(t, string(want.Settings), string(v.Settings))
	assert.Equal(t, srcHostID, v.SourceID)
	assert.Equal(t, "mlab", v.Source)
	require.NotNil(t, v.FetchedAt)

	evs := f.fol.events_()
	require.Len(t, evs, 1)
	assert.Equal(t, WakeSettingsChangedEvent, evs[0].Type)
	assert.JSONEq(t, `{"revision":1,"etag":"`+want.Etag+`","source":"mlab"}`, evs[0].Value)

	f.fol.m.pollWake(context.Background()) // the same document again: no new event
	assert.Len(t, f.fol.events_(), 1)
}

func TestWakeFollow_ASourceThatCannotBeReadLeavesTheCopyStale(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	require.False(t, f.fol.viewOf(t).Stale)

	f.folCall.down = true
	f.fol.m.pollWake(context.Background())
	v := f.fol.viewOf(t)
	assert.True(t, v.Stale, "the source did not answer")
	assert.Equal(t, int64(1), v.Revision, "the copy is what it was")
	assert.Equal(t, srcHostID, v.SourceID)

	f.folCall.down = false
	f.fol.m.pollWake(context.Background())
	assert.False(t, f.fol.viewOf(t).Stale)
}

// A source that answers inside the envelope with a refusal (it is not the source, or has nothing to give) is no document: the copy is stale.
func TestWakeFollow_AnAnswerThatIsNotADocumentIsNotTaken(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	f.src.m.core.Cfg.Watch = &config.WatchConfig{WakeSource: "air26"} // the source is not the source any more: it answers 503 not_source
	f.fol.m.pollWake(context.Background())
	v := f.fol.viewOf(t)
	assert.True(t, v.Stale)
	assert.Equal(t, int64(1), v.Revision)
}

func TestWakeFollow_AWriteOnTheSourceReachesTheFollowerByTheNotice(t *testing.T) {
	f := newFleet(t)
	cur := f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	f.srcCall.reset()

	s := watchwake.Default("Asia/Taipei")
	s.Rules.DiskLow.BelowGB = 55
	rr := f.src.do(adminCtx, http.MethodPut, putBody(t, s, cur.Etag))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	assert.Equal(t, []string{pathWakeChanged}, f.srcCall.paths(), "the source told the other host once")
	v := f.fol.viewOf(t)
	assert.Equal(t, int64(2), v.Revision, "the notice made the follower read")
	assert.Equal(t, watchwake.Etag(s), v.Etag)
	evs := f.fol.events_()
	require.Len(t, evs, 2)
	assert.JSONEq(t, `{"revision":2,"etag":"`+watchwake.Etag(s)+`","source":"mlab"}`, evs[1].Value)
}

// A notice that cannot be delivered is not sent again: the poll finds the change.
func TestWakeFollow_ALostNoticeIsNotRetriedAndThePollFindsTheChange(t *testing.T) {
	f := newFleet(t)
	cur := f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	f.srcCall.reset()
	f.srcCall.down = true
	s := watchwake.Default("Asia/Taipei")
	s.Rules.UserBack.AwayMin = 40
	require.Equal(t, http.StatusOK, f.src.do(adminCtx, http.MethodPut, putBody(t, s, cur.Etag)).Code)
	assert.Equal(t, []string{pathWakeChanged}, f.srcCall.paths(), "one try")
	assert.Equal(t, int64(1), f.fol.viewOf(t).Revision, "not told")

	f.fol.m.pollWake(context.Background())
	assert.Equal(t, int64(2), f.fol.viewOf(t).Revision)
}

func TestWakeFollow_APutOnAFollowerGoesToTheSourceAndShowsAtOnce(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	etag := f.fol.viewOf(t).Etag
	f.srcCall.down = true // the source's notice cannot reach the follower: what shows is the answer of the PUT itself

	s := watchwake.Default("Asia/Taipei")
	s.Rules.Patrol.Prompt = "從 air26 改的"
	rr := f.fol.do(adminCtx, http.MethodPut, putBody(t, s, etag))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var put wakeSettingsView
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &put))
	assert.True(t, put.IsSource, "the body is the source's own answer, as it came")
	assert.Equal(t, int64(2), put.Revision)

	assert.Equal(t, int64(2), f.src.viewOf(t).Revision, "the source holds it")
	v := f.fol.viewOf(t)
	assert.Equal(t, int64(2), v.Revision, "and the follower shows it without waiting for a poll")
	assert.Equal(t, put.Etag, v.Etag)
	assert.False(t, v.IsSource)
}

// What the source refuses with comes back whole: the fields of a 400 and the current document of a 409.
func TestWakeFollow_APutRefusalReachesTheCallerAsTheSourceSaidIt(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	etag := f.fol.viewOf(t).Etag

	bad := `{"settings":{"version":1,"enabled":"yes"},"expect_etag":"` + etag + `"}`
	rr := f.fol.do(adminCtx, http.MethodPut, bad)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	direct := f.src.do(adminCtx, http.MethodPut, bad)
	assert.Equal(t, direct.Code, rr.Code)
	assert.JSONEq(t, direct.Body.String(), rr.Body.String(), "the same 400 body, every field in it")
	assert.Contains(t, rr.Body.String(), `"enabled"`)

	rr = f.fol.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), "0000000000000000"))
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	var conflict struct {
		Error   string           `json:"error"`
		Current wakeSettingsView `json:"current"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &conflict))
	assert.Equal(t, "etag_mismatch", conflict.Error)
	assert.Equal(t, etag, conflict.Current.Etag, "the source's current document")
	assert.True(t, conflict.Current.IsSource)

	assert.Equal(t, int64(1), f.fol.viewOf(t).Revision, "a refusal changed no copy")
}

func TestWakeFollow_APutWhenTheSourceCannotBeReachedIs503(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	f.fol.m.pollWake(context.Background())
	etag := f.fol.viewOf(t).Etag
	f.folCall.down = true
	rr := f.fol.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), etag))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.JSONEq(t, `{"error":"source_unreachable","source":"mlab"}`, rr.Body.String())

	// no paired entry for the source at all
	f.folCall.down = false
	f.fol.m.core.Cfg.Peers.Hosts = nil
	rr = f.fol.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), etag))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// The copy only moves forward: an older answer, an equal one with another etag, and an answer for a source that is not the one set now do not
// replace it; a larger revision does; another source (or another host id) starts the copy over.
func TestWakeFollow_TheCopyOnlyMovesForwardAndAnotherSourceStartsItOver(t *testing.T) {
	f := newFleet(t)
	m := f.fol.m
	view := func(rev int64, etag, sourceID string) wakeSettingsView {
		def := watchwake.Default("Asia/Taipei")
		return wakeSettingsView{Settings: watchwake.Marshal(def), Etag: etag, Revision: rev, SourceID: sourceID, IsSource: true}
	}
	rev := func() int64 { return f.fol.viewOf(t).Revision }

	assert.True(t, m.applyWake(wakeResult{source: "mlab", view: view(5, "e5", srcHostID)}))
	assert.False(t, m.applyWake(wakeResult{source: "mlab", view: view(3, "e3", srcHostID)}), "an older answer (sent earlier, arrived later)")
	assert.False(t, m.applyWake(wakeResult{source: "mlab", view: view(5, "other", srcHostID)}), "the same revision is the same document")
	assert.Equal(t, int64(5), rev())
	assert.Equal(t, "e5", f.fol.viewOf(t).Etag)
	assert.True(t, m.applyWake(wakeResult{source: "mlab", view: view(6, "e6", srcHostID)}))
	assert.Equal(t, int64(6), rev())

	// the same alias on another host id: revisions of one source mean nothing for the other
	assert.True(t, m.applyWake(wakeResult{source: "mlab", view: view(1, "n1", "mlab:cccccc")}), "a smaller revision of a different host is the start of its copy")
	assert.Equal(t, int64(1), rev())
	assert.Equal(t, "mlab:cccccc", f.fol.viewOf(t).SourceID)

	// the source is moved to another host: a late answer of the old one is dropped, the new one starts at its own revision
	f.fol.m.core.Cfg.Watch = &config.WatchConfig{WakeSource: "air19"}
	assert.False(t, m.applyWake(wakeResult{source: "mlab", view: view(9, "late", srcHostID)}), "asked mlab, which is no longer the source")
	assert.True(t, m.applyWake(wakeResult{source: "air19", view: view(2, "a2", "air19:dddddd")}))
	v := f.fol.viewOf(t)
	assert.Equal(t, "air19", v.Source)
	assert.Equal(t, int64(2), v.Revision)

	// back to mlab: the copy was of air19, so there is nothing from mlab yet
	f.fol.m.core.Cfg.Watch = nil
	back := f.fol.viewOf(t)
	assert.Equal(t, "mlab", back.Source)
	assert.True(t, back.Stale)
	assert.Equal(t, int64(0), back.Revision)
	assert.Nil(t, back.FetchedAt)
}

// A copy that cannot be written to its row stays in memory (and is served); the next update writes it, and a restart reads it.
func TestWakeFollow_ACopyThatCannotBeStoredStaysInMemoryAndTheNextUpdateStoresIt(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	flaky := &flakyRows{WakeRowStore: f.fol.rows, fail: true}
	f.fol.m.core.Registry.Register(hostconfigWakeKey, flaky)

	f.fol.m.pollWake(context.Background())
	v := f.fol.viewOf(t)
	assert.Equal(t, int64(1), v.Revision, "served from memory")
	_, _, ok, _ := f.fol.rows.Get("watch_wake_cache")
	assert.False(t, ok, "not stored")

	flaky.fail = false
	cur := f.src.viewOf(t)
	s := watchwake.Default("Asia/Taipei")
	s.Rules.UserBack.AwayMin = 41
	require.Equal(t, http.StatusOK, f.src.do(adminCtx, http.MethodPut, putBody(t, s, cur.Etag)).Code)
	f.fol.m.pollWake(context.Background())
	raw, _, ok, _ := f.fol.rows.Get("watch_wake_cache")
	require.True(t, ok, "the next update stored it")
	var stored wakeCacheDoc
	require.NoError(t, json.Unmarshal(raw, &stored))
	assert.Equal(t, int64(2), stored.Revision)
	assert.Equal(t, srcHostID, stored.SourceID)

	// a restart: the follower starts from the stored copy, which is stale until the source has been read again
	f.fol.m.wakeFollow = wakeFollower{}
	again := f.fol.viewOf(t)
	assert.Equal(t, int64(2), again.Revision)
	assert.True(t, again.Stale)
	f.fol.m.pollWake(context.Background())
	assert.False(t, f.fol.viewOf(t).Stale)
}

// A copy that could not be stored is stored by a later read even when the source has not changed (the revision is the same): otherwise a source
// that never changes again would leave the copy in memory only, and a restart would lose it.
func TestWakeFollow_ADirtyCopyIsStoredByTheNextReadOfTheSameRevision(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	flaky := &flakyRows{WakeRowStore: f.fol.rows, fail: true}
	f.fol.m.core.Registry.Register(hostconfigWakeKey, flaky)
	f.fol.m.pollWake(context.Background())
	_, _, ok, _ := f.fol.rows.Get("watch_wake_cache")
	require.False(t, ok, "not stored: the disk was full")

	flaky.fail = false
	f.fol.m.pollWake(context.Background()) // the same revision again
	_, _, ok, _ = f.fol.rows.Get("watch_wake_cache")
	assert.True(t, ok, "stored by the next read, though nothing changed")
}

// A read that fails belongs to the source it asked: when the source has been moved meanwhile, it must not mark the copy of the new one stale.
func TestWakeFollow_AFailedReadOfAnOldSourceDoesNotMarkTheNewSourcesCopyStale(t *testing.T) {
	f := newFleet(t)
	m := f.fol.m
	def := watchwake.Default("Asia/Taipei")
	m.core.Cfg.Watch = &config.WatchConfig{WakeSource: "air19"}
	require.True(t, m.applyWake(wakeResult{source: "air19", view: wakeSettingsView{Settings: watchwake.Marshal(def), Etag: "a1", Revision: 1, SourceID: "air19:dddddd", IsSource: true}}))
	require.False(t, f.fol.viewOf(t).Stale)

	m.markWakeStale("mlab") // the read of mlab, the old source, failed after the source was moved
	assert.False(t, f.fol.viewOf(t).Stale, "air19's copy is as fresh as it was")
	m.markWakeStale("air19")
	assert.True(t, f.fol.viewOf(t).Stale, "a failed read of the source that is set does mark it")
}

// Notices that come in while a read is under way are one more read, not one each; Stop waits for it.
func TestWakeFollow_NoticesDuringAReadAreOneMoreReadAndStopWaitsForIt(t *testing.T) {
	f := newFleet(t)
	f.src.viewOf(t)
	m := f.fol.m
	m.backgroundSync = false
	ctx, cancel := context.WithCancel(context.Background())
	m.runMu.Lock()
	m.running, m.runCtx = true, ctx
	m.runMu.Unlock()

	gate := &gateCaller{inner: f.folCall, release: make(chan struct{}), entered: make(chan struct{}, 8)}
	m.caller = gate
	m.requestWakePoll()
	<-gate.entered // the first read is under way
	for i := 0; i < 5; i++ {
		m.requestWakePoll()
	}
	close(gate.release)

	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }() // what Stop does after it cancels
	<-done
	cancel()
	assert.Equal(t, 2, gate.count(), "one read, and one more for the five notices that came in during it")
}

type gateCaller struct {
	inner   hostCaller
	release chan struct{}
	entered chan struct{}
	mu      sync.Mutex
	n       int
}

func (g *gateCaller) Call(ctx context.Context, target, path string, body any) peersmod.CallResult {
	g.mu.Lock()
	g.n++
	n := g.n
	g.mu.Unlock()
	if n == 1 {
		g.entered <- struct{}{}
		<-g.release
	}
	return g.inner.Call(ctx, target, path, body)
}

func (g *gateCaller) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n
}
