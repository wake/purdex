package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/watch"
)

func (r *sessionRig) serve(ctx context.Context, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

func hostCtx() context.Context {
	return middleware.WithPrincipal(context.Background(), *host)
}

func TestSessionRoutes_OnlyTheAdminTokenStartsOrWakes(t *testing.T) {
	r := newSessionRig(t, "f1")
	for _, p := range []string{"/api/watch/start", "/api/watch/wake"} {
		require.Equal(t, http.StatusForbidden, r.serve(context.Background(), p, `{}`).Code, p+" without a principal")
		dev := devices.WithPrincipal(context.Background(), devices.Principal{ID: "d_1"})
		require.Equal(t, http.StatusForbidden, r.serve(dev, p, `{}`).Code, p+" with a device token")
	}
	require.Empty(t, r.typer.typed())
}

func TestWakeRoute_TheThreeFormsOfPromptAreValidAndABadOneIs400(t *testing.T) {
	r := newSessionRig(t, "f1")
	require.Equal(t, http.StatusOK, r.serve(adminCtx, "/api/watch/wake", ``).Code, "no body")
	require.Equal(t, http.StatusOK, r.serve(adminCtx, "/api/watch/wake", `{"prompt":""}`).Code)
	require.Equal(t, http.StatusOK, r.serve(adminCtx, "/api/watch/wake", `{"prompt":"check the disk"}`).Code)
	require.Equal(t, http.StatusBadRequest, r.serve(adminCtx, "/api/watch/wake", `{"prompt":"a\u0007b"}`).Code, "a control character")
	require.Equal(t, http.StatusBadRequest, r.serve(adminCtx, "/api/watch/wake", `{"prompt":"`+strings.Repeat("x", 5000)+`"}`).Code, "over 4 KB")
	require.Equal(t, http.StatusBadRequest, r.serve(adminCtx, "/api/watch/wake", `{`).Code, "not JSON")
}

func TestWakeRoute_ACooldownIs409WithTheTimeAndNothingIsTyped(t *testing.T) {
	r := newSessionRig(t, "f1")
	until := r.m.now().Add(20 * time.Minute)
	require.NoError(t, r.m.sess.Update(func(x *watch.SessionState) { x.CooldownUntil = until }))
	rr := r.serve(adminCtx, "/api/watch/wake", `{}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	var e struct{ Error, Detail string }
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &e))
	require.Equal(t, "cooldown", e.Error)
	require.Equal(t, until.Format(time.RFC3339), e.Detail)
	require.Empty(t, r.typer.typed())
}

func TestPeerSessionRoutes_AHostStartsAndWakesAndIsHeldToTheHolderRoutesRules(t *testing.T) {
	r := newSessionRig(t, "f1")
	body := `{"to_host_id":"` + ourID + `"}`
	rr := r.serve(hostCtx(), "/api/peers/watch/start", body)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var v HolderView
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	require.Equal(t, ourID, v.HostID)
	require.Equal(t, "awake", v.State)

	require.Equal(t, http.StatusOK, r.serve(hostCtx(), "/api/peers/watch/wake", body[:len(body)-1]+`,"prompt":"hi"}`).Code)
	require.Equal(t, http.StatusConflict, r.serve(hostCtx(), "/api/peers/watch/start", `{"to_host_id":"other"}`).Code, "addressed to another host")
	require.Equal(t, http.StatusForbidden, r.serve(adminCtx, "/api/peers/watch/start", body).Code, "not a host principal")
	require.Equal(t, http.StatusBadRequest, r.serve(hostCtx(), "/api/peers/watch/wake", body[:len(body)-1]+`,"prompt":"a\u0001"}`).Code)
}

func TestPeerSessionRoutes_ACooldownIsToldAsCooldownWithTheTime(t *testing.T) {
	r := newSessionRig(t, "f1")
	until := r.m.now().Add(time.Hour)
	require.NoError(t, r.m.sess.Update(func(x *watch.SessionState) { x.CooldownUntil = until }))
	rr := r.serve(hostCtx(), "/api/peers/watch/start", `{"to_host_id":"`+ourID+`"}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	require.Contains(t, rr.Body.String(), `"cooldown"`)
	require.Contains(t, rr.Body.String(), until.Format(time.RFC3339))
}

func TestStartRoute_WithAWatcherAwakeItAnswers200AndTypesNothing(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("f1"), true, nil)
	_, _, _ = r.m.Reconcile(context.Background())
	rr := r.serve(adminCtx, "/api/watch/start", ``)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var v startReply
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	require.Equal(t, "sess-f1", v.SessionID)
	require.Empty(t, r.typer.typed())
}
