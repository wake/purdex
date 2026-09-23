package nex

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/session"
)

// #1293: the handoff and take-back preflights read the session under the
// request's own context, so a client that gives up ends a stuck tmux read.

type reqCtxKey struct{}

// ctxSessions is the env's provider plus GetSessionContext, recording the
// context each call got. It answers "not found" so the handler stops right
// after the read (404 session_missing) — no step past it runs.
type ctxSessions struct {
	session.SessionProvider
	got []context.Context
}

func (c *ctxSessions) GetSessionContext(ctx context.Context, _ string) (*session.SessionInfo, error) {
	c.got = append(c.got, ctx)
	return nil, nil
}

func serveWithValue(t *testing.T, m *Module, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), reqCtxKey{}, "the request")
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestHandoff_SessionReadUsesRequestContext(t *testing.T) {
	env := newHandoffEnv(t)
	cs := &ctxSessions{SessionProvider: env.sessions}
	env.m.sessions = cs

	w := serveWithValue(t, env.m, "/api/sessions/"+hoCode+"/nex-handoff", goodBody())
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	require.Len(t, cs.got, 1, "the handoff did not read the session through GetSessionContext")
	assert.Equal(t, "the request", cs.got[0].Value(reqCtxKey{}), "the session read ran on a context other than the request's")
}

func TestTakeback_SessionReadUsesRequestContext(t *testing.T) {
	env := newTakebackEnv(t)
	cs := &ctxSessions{SessionProvider: env.sessions}
	env.m.sessions = cs

	w := serveWithValue(t, env.m, "/api/sessions/"+hoCode+"/nex-takeback", takebackBody())
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	require.Len(t, cs.got, 1, "the take-back did not read the session through GetSessionContext")
	assert.Equal(t, "the request", cs.got[0].Value(reqCtxKey{}), "the session read ran on a context other than the request's")
	env.assertUntouched(t)
}
