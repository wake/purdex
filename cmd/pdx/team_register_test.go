package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
)

// TestRegisterServeModules_MountsTeam: the team module is mounted, its Init
// succeeds under the real wiring (peers registers the origin resolver
// before it, by dependency order), team.db is created in the data dir and
// its routes are live through the real outer chain (TokenAuth: 401 without
// the token).
func TestRegisterServeModules_MountsTeam(t *testing.T) {
	dataDir := t.TempDir()
	c := newTestCore(&config.Config{DataDir: dataDir, Token: "t"})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("team"))
	require.NoError(t, c.InitModules())

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, nil)

	res := doRequest(t, outer, http.MethodGet, "/api/team/approvals?state=open", "t")
	assert.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.JSONEq(t, `{"approvals":[]}`, res.Body.String())
	res = doRequest(t, outer, http.MethodGet, "/api/team/inflight", "t")
	assert.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.JSONEq(t, `{"approvals_open":0,"relays_active":0}`, res.Body.String())
	assert.Equal(t, http.StatusNotFound, doRequest(t, outer, http.MethodGet, "/api/team/approvals/00000000-0000-4000-8000-000000000001", "t").Code)
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodGet, "/api/team/approvals", "").Code)
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodGet, "/api/team/inflight", "").Code)
	res = doRequest(t, outer, http.MethodPost, "/api/relay/hello", "t")
	assert.Equal(t, http.StatusBadRequest, res.Code, "the relay routes are mounted (400 for an empty body, not 404)")
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodPost, "/api/relay/hello", "").Code)
	// P2c: the hook decision route is live and behind TokenAuth too.
	res = doRequestBody(t, outer, http.MethodPost, "/api/hooks/decide", "t", `{"agent":"cc","event":"PreToolUse","session_id":"sid-x"}`)
	assert.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.JSONEq(t, `{}`, res.Body.String())
	assert.Equal(t, http.StatusUnauthorized, doRequestBody(t, outer, http.MethodPost, "/api/hooks/decide", "", `{"agent":"cc","event":"PreToolUse","session_id":"sid-x"}`).Code)
	_, err := os.Stat(filepath.Join(dataDir, "team.db"))
	assert.NoError(t, err, "team.db must be created in the data dir")
	require.NoError(t, c.CloseModules())
}

// doRequestBody is doRequest with a JSON body.
func doRequestBody(t *testing.T, h http.Handler, method, target, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
