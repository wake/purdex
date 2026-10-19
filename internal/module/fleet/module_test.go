package fleetmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/fleet"
	"github.com/wake/purdex/internal/middleware"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/push"
)

const ourID, peerID = "air26:aaaaaa", "mlab:bbbbbb"

type fakeCaller struct {
	ids   map[string]string
	calls []fakeCall
	reply func(path string, body map[string]any) peersmod.CallResult
}
type fakeCall struct {
	target, path string
	body         map[string]any
}

func (f *fakeCaller) HostIDOf(alias string) string { return f.ids[alias] }
func (f *fakeCaller) Call(_ context.Context, target, path string, body any) peersmod.CallResult {
	raw, _ := json.Marshal(body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	f.calls = append(f.calls, fakeCall{target, path, m})
	return f.reply(path, m)
}

type rig struct {
	m      *Module
	mux    *http.ServeMux
	caller *fakeCaller
	set    *fakeSettings
}

func newRig(t *testing.T) *rig {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), HostID: ourID}
	cfg.Peers.Alias = "air26"
	cfg.Peers.Hosts = []config.PeerHost{{Alias: "mlab", HostID: peerID, URL: "http://x", Token: "t", InboundToken: "i"}}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	m := New()
	m.logf = func(string, ...any) {}
	set := &fakeSettings{set: Settings{Order: []string{"air26", "mlab"}, Enabled: true, Self: "air26"}}
	m.settings = set
	m.statePath = t.TempDir() + "/state.json"
	fc := &fakeCaller{ids: map[string]string{"mlab": peerID}}
	m.caller = fc
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return &rig{m: m, mux: mux, caller: fc, set: set}
}

var hostP = &middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: peerID}

func (r *rig) post(path string, p *middleware.Principal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "fleet", m.Name())
	require.Equal(t, []string{"peers", "hostconfig", "resources"}, m.Dependencies())
}

func TestModule_InitRegistersRoleViewAndFakeLocalWhenMissing(t *testing.T) {
	r := newRig(t)
	svc, ok := r.m.core.Registry.Get(RoleViewKey)
	require.True(t, ok)
	_, isView := svc.(fleet.RoleView)
	require.True(t, isView)
	require.NotNil(t, r.m.local, "an empty FakeLocal stands in")
}

func TestModule_StartStopLeavesNoGoroutine(t *testing.T) {
	r := newRig(t)
	r.set.change(func(s *Settings) { s.Enabled = false }) // Startup returns at once: no peer call
	require.NoError(t, r.m.Start(context.Background()))
	require.Eventually(t, func() bool { return r.m.engine.State().Role == fleet.RoleOff }, 2*time.Second, time.Millisecond)
	done := make(chan struct{})
	go func() { _ = r.m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return: a goroutine is left")
	}
}

func TestRoleRoute_AnswersWithHostIDAndAppliesCallersTerm(t *testing.T) {
	r := newRig(t)
	rr := r.post(pathRole, hostP, `{"to_host_id":"`+ourID+`","from_alias":"mlab","role":"primary","term":4}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var a RoleAnswer
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &a))
	require.Equal(t, ourID, a.HostID, "HostCaller checks the answer names the host it asked")
	require.Equal(t, "air26", a.Alias)
	require.Equal(t, fleet.RoleStandby, a.Role)
	require.Equal(t, int64(4), a.Term, "the caller's term is applied like an announcement")
	require.Equal(t, []string{"air26", "mlab"}, a.Order)
	require.Contains(t, rr.Body.String(), `"newest_age_ms":null`)
	require.Equal(t, "mlab", r.m.engine.State().Primary)
}

func TestRoleRoute_NewestAgeWhenWired(t *testing.T) {
	r := newRig(t)
	r.m.engine.SetNewestAge(age(1234 * time.Millisecond))
	rr := r.post(pathRole, hostP, `{"to_host_id":"`+ourID+`","from_alias":"mlab","role":"standby","term":0}`)
	require.Contains(t, rr.Body.String(), `"newest_age_ms":1234`)
}

// stubNotifier is the push module's alert notifier as the registry holds it; ready is what its AlertReady answers.
type stubNotifier struct{ ready bool }

func (*stubNotifier) NotifyAlert(push.AlertPush) bool { return true }
func (s *stubNotifier) AlertReady() bool              { return s.ready }

// The role route says whether this daemon's push module can queue a push now. The registry keeps a service after its module stops, so
// the answer follows the notifier's own readiness — false before it is registered, true while it is up, false again after it stopped —
// and it is always there (never left out) in this module.
func TestRoleRoute_PushReadyFollowsThePushModule(t *testing.T) {
	r := newRig(t)
	body := `{"to_host_id":"` + ourID + `","from_alias":"mlab","role":"standby","term":0}`
	ready := func() (bool, string) {
		rr := r.post(pathRole, hostP, body)
		var a RoleAnswer
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &a))
		require.NotNil(t, a.PushReady, "never left out in this module")
		return *a.PushReady, rr.Body.String()
	}
	got, _ := ready()
	require.False(t, got, "no push module yet")
	n := &stubNotifier{ready: true}
	r.m.core.Registry.Register(push.AlertNotifyKey, n)
	got, raw := ready()
	require.True(t, got)
	require.Contains(t, raw, `"push_ready":true`)
	n.ready = false // the push module stopped; the registry still holds it
	got, _ = ready()
	require.False(t, got, "a stopped push module is not ready, whatever the registry holds")
}

func TestRoutes_WrongHost409(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{pathRole, pathAnnounce} {
		rr := r.post(p, hostP, `{"to_host_id":"someone:else","from_alias":"mlab","role":"standby","term":1}`)
		require.Equal(t, http.StatusConflict, rr.Code, p)
		require.Contains(t, rr.Body.String(), "wrong_host")
	}
	require.Equal(t, int64(0), r.m.engine.State().Term, "nothing applied")
}

func TestRoutes_Refusals(t *testing.T) {
	r := newRig(t)
	require.Equal(t, http.StatusForbidden, r.post(pathRole, nil, `{}`).Code, "no principal")
	body := func(extra string) string { return `{"to_host_id":"` + ourID + `",` + extra + `}` }
	require.Equal(t, http.StatusBadRequest, r.post(pathRole, hostP, body(`"from_alias":"mlab","role":"king","term":1`)).Code)
	require.Equal(t, http.StatusForbidden, r.post(pathRole, hostP, body(`"from_alias":"","role":"standby","term":1`)).Code, "an empty alias is not the caller's")
	require.Equal(t, http.StatusBadRequest, r.post(pathRole, hostP, `not json`).Code)
	require.Equal(t, http.StatusBadRequest, r.post(pathAnnounce, hostP, body(`"from_alias":"mlab","term":-1`)).Code)
}

func TestAnnounceRoute_ReplyHostIDAndStepsDownAPrimary(t *testing.T) {
	r := newRig(t)
	r.caller.reply = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnpaired}
	}
	r.m.engine.Startup(context.Background()) // the peer is unreachable: primary term 1
	require.Equal(t, fleet.RolePrimary, r.m.engine.State().Role)

	rr := r.post(pathAnnounce, hostP, `{"to_host_id":"`+ourID+`","from_alias":"mlab","term":2}`)
	require.Equal(t, http.StatusOK, rr.Code)
	require.JSONEq(t, `{"host_id":"`+ourID+`"}`, rr.Body.String())
	require.Equal(t, fleet.RoleStandby, r.m.engine.State().Role)
	require.Equal(t, int64(2), r.m.engine.State().Term)
}

func TestPeerOverHostCaller_RoleAndAnnounceBodies(t *testing.T) {
	r := newRig(t)
	r.caller.reply = func(path string, _ map[string]any) peersmod.CallResult {
		if path == pathRole {
			return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{"host_id":"` + peerID + `","alias":"mlab","role":"primary","term":3,"order":["air26","mlab"],"newest_age_ms":900}`)}
		}
		return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{"host_id":"` + peerID + `"}`)}
	}
	r.m.engine.Startup(context.Background())
	st := r.m.engine.State()
	require.Equal(t, fleet.RoleStandby, st.Role, "the other is primary")
	require.Equal(t, int64(3), st.Term)

	require.Len(t, r.caller.calls, 1)
	c := r.caller.calls[0]
	require.Equal(t, peerID, c.target)
	require.Equal(t, pathRole, c.path)
	require.Equal(t, peerID, c.body["to_host_id"])
	require.Equal(t, "air26", c.body["from_alias"])
	require.Equal(t, "starting", c.body["role"])
	require.EqualValues(t, 0, c.body["term"])

	require.NoError(t, peerOf{r.m}.Announce(context.Background(), 5))
	c = r.caller.calls[1]
	require.Equal(t, pathAnnounce, c.path)
	require.Equal(t, peerID, c.body["to_host_id"])
	require.Equal(t, "air26", c.body["from_alias"])
	require.EqualValues(t, 5, c.body["term"])
}

func TestPeerOverHostCaller_UnreachableClassesAreErrors(t *testing.T) {
	r := newRig(t)
	for _, cl := range []peersmod.CallClass{peersmod.ClassTransient, peersmod.ClassUnsupported, peersmod.ClassWrongHost, peersmod.ClassUnpaired} {
		r.caller.reply = func(string, map[string]any) peersmod.CallResult { return peersmod.CallResult{Class: cl, Code: "x"} }
		_, err := peerOf{r.m}.Role(context.Background(), RoleRequest{FromAlias: "air26", Role: "standby"})
		require.Error(t, err, string(cl))
	}
	r.caller.ids = map[string]string{} // no peer host id for the alias
	_, err := peerOf{r.m}.Role(context.Background(), RoleRequest{FromAlias: "air26", Role: "standby"})
	require.Error(t, err)
}
