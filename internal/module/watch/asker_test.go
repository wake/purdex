package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/watch"
)

// fakeCaller answers HostCaller's Call by path.
type fakeCaller struct {
	reply map[string]peersmod.CallResult
	calls []string
}

func (f *fakeCaller) Call(_ context.Context, host, path string, body any) peersmod.CallResult {
	f.calls = append(f.calls, host+" "+path)
	b, _ := json.Marshal(body)
	var addr struct {
		To string `json:"to_host_id"`
	}
	_ = json.Unmarshal(b, &addr)
	if addr.To != host {
		return peersmod.CallResult{Class: peersmod.ClassRefused, Code: "bad_request_local"}
	}
	if r, ok := f.reply[path]; ok {
		return r
	}
	return peersmod.CallResult{Class: peersmod.ClassUnsupported, Status: 404}
}

func done(v HolderView) peersmod.CallResult {
	b, _ := json.Marshal(v)
	return peersmod.CallResult{Class: peersmod.ClassDone, Status: 200, Body: b}
}

func askerRig(t *testing.T) (*testRig, *fakeCaller) {
	r := newRig(t)
	r.m.core.Cfg.Peers.Alias = "mlab"
	r.m.core.Cfg.Peers.Hosts[0].Alias = "air26"
	fc := &fakeCaller{reply: map[string]peersmod.CallResult{}}
	r.m.caller = fc
	return r, fc
}

func TestAsker_AnotherHostIsAskedThroughTheCallerWithItsHostID(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Targets: []string{"air26", "mlab"}})
	a, err := asker{r.m}.Holder(context.Background(), "air26")
	require.NoError(t, err)
	require.Equal(t, watch.HolderAwake, a.State)
	require.Equal(t, "s-26", a.SessionID)
	require.Equal(t, []string{"air26", "mlab"}, a.Targets)
	require.Equal(t, []string{peerID + " " + pathHolder}, fc.calls, "addressed by host id, never by alias")
}

func TestAsker_ThisHostAnswersFromItsOwnStateAndNeverCallsItself(t *testing.T) {
	r, fc := askerRig(t)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.State, h.SessionID = watch.HolderResting, "mine" }))
	for _, alias := range []string{"mlab", "MLAB"} {
		a, err := asker{r.m}.Holder(context.Background(), alias)
		require.NoError(t, err)
		require.Equal(t, watch.HolderResting, a.State)
		require.Equal(t, "mine", a.SessionID)
		require.Equal(t, ourID, a.HostID)
	}
	require.Empty(t, fc.calls)
}

func TestAsker_FailuresAreTheLocatorsVocabulary(t *testing.T) {
	r, fc := askerRig(t)
	// no watch routes on that host: an old daemon
	_, err := asker{r.m}.Holder(context.Background(), "air26")
	require.ErrorIs(t, err, watch.ErrNoWatchSupport)
	// a transport failure is just an error
	fc.reply[pathHolder] = peersmod.CallResult{Class: peersmod.ClassTransient}
	_, err = asker{r.m}.Holder(context.Background(), "air26")
	require.Error(t, err)
	require.NotErrorIs(t, err, watch.ErrNoWatchSupport)
	// an alias that is not paired
	_, err = asker{r.m}.Holder(context.Background(), "nowhere")
	require.Error(t, err)
	// an answer that is not a holder view
	fc.reply[pathHolder] = peersmod.CallResult{Class: peersmod.ClassDone, Body: []byte(`[1]`)}
	_, err = asker{r.m}.Holder(context.Background(), "air26")
	require.Error(t, err)
	// without a caller only this host can be asked
	r.m.caller = nil
	_, err = asker{r.m}.Holder(context.Background(), "air26")
	require.Error(t, err)
}

func TestAsker_WakeAndStartGoToThePeerRoutesOrTheLocalSession(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathWake] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26"})
	a, err := asker{r.m}.Wake(context.Background(), "air26")
	require.NoError(t, err)
	require.Equal(t, watch.HolderAwake, a.State)
	// that host has no start route (older): not "unsupported" but "cannot start there"
	_, err = asker{r.m}.Start(context.Background(), "air26")
	require.ErrorIs(t, err, watch.ErrNoSession)
	// this host: no session part yet → cannot; with one → it is used
	_, err = asker{r.m}.Start(context.Background(), "mlab")
	require.ErrorIs(t, err, watch.ErrNoSession)
	_, err = asker{r.m}.Wake(context.Background(), "mlab")
	require.ErrorIs(t, err, watch.ErrNoSession)
	r.m.session = fakeSession{}
	a, err = asker{r.m}.Start(context.Background(), "mlab")
	require.NoError(t, err)
	require.Equal(t, "local-new", a.SessionID)
}

type fakeSession struct{}

func (fakeSession) Wake(context.Context, string) (watch.HolderAnswer, error) {
	return watch.HolderAnswer{HostID: ourID, State: watch.HolderAwake, SessionID: "local-woken"}, nil
}
func (fakeSession) Start(context.Context) (watch.HolderAnswer, error) {
	return watch.HolderAnswer{HostID: ourID, State: watch.HolderAwake, SessionID: "local-new"}, nil
}

func TestStatusRoute_ShowsTheHolderTheRememberedWatcherAndTheSettings(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "awake", SessionID: "s-26", Targets: []string{"air26", "mlab"}})
	_, err := r.m.loc.Current(context.Background(), []string{"air26", "mlab"}, true)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/status", nil))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var st Status
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &st))
	require.Equal(t, "none", st.Self.State)
	require.NotNil(t, st.Current)
	require.Equal(t, "air26", st.Current.Alias)
	require.Equal(t, "s-26", st.Current.SessionID)
	require.True(t, st.Enabled)
	require.True(t, st.AutoWake)
	require.Equal(t, "patrol", st.Mode)
	require.Equal(t, []string{"air26", "mlab"}, st.Targets)
}

// targets are validated without regard to case, so a target written AIR26 is the paired host air26.
func TestAsker_AnAliasIsFoundWithoutRegardToCase(t *testing.T) {
	r, fc := askerRig(t)
	fc.reply[pathHolder] = done(HolderView{HostID: peerID, State: "none"})
	a, err := asker{r.m}.Holder(context.Background(), "AIR26")
	require.NoError(t, err)
	require.Equal(t, peerID, a.HostID)
	require.Equal(t, []string{peerID + " " + pathHolder}, fc.calls)
}

// A host without a configured alias goes by the name of its host id; a target of that name is this host.
func TestAsker_ThisHostWithoutAnAliasIsKnownByItsHostIDName(t *testing.T) {
	r, fc := askerRig(t)
	r.m.core.Cfg.Peers.Alias = ""
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) { h.State, h.SessionID = watch.HolderAwake, "mine" }))
	a, err := asker{r.m}.Holder(context.Background(), "mlab") // HostID is "mlab:aaaaaa"
	require.NoError(t, err)
	require.Equal(t, "mine", a.SessionID)
	require.Empty(t, fc.calls)
}

func TestAsker_AHostsCooldownIsRememberedAndTheStartRouteAnswers409WithItsTime(t *testing.T) {
	r, fc := askerRig(t)
	until := time.Date(2026, 10, 17, 13, 0, 0, 0, time.UTC)
	r.m.now = func() time.Time { return until.Add(-time.Hour) }
	fc.reply[pathStart] = peersmod.CallResult{Class: peersmod.ClassRefused, Status: 409, Code: "cooldown", Detail: until.Format(time.RFC3339)}
	_, err := asker{r.m}.Start(context.Background(), "air26")
	require.ErrorIs(t, err, watch.ErrNoSession, "the locator treats it as a refusal")
	got, ok := r.m.earliestCooldown()
	require.True(t, ok)
	require.True(t, got.Equal(until))

	fc.reply[pathStart] = peersmod.CallResult{Class: peersmod.ClassRefused, Status: 409, Code: "cooldown", Detail: "not a time"}
	_, _ = asker{r.m}.Start(context.Background(), "air26")
	got, _ = r.m.earliestCooldown()
	require.True(t, got.Equal(until), "a detail that is not a time changes nothing")
}
