package main

import (
	"net/http"
	"os"
	"path/filepath"
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
	_, err := os.Stat(filepath.Join(dataDir, "team.db"))
	assert.NoError(t, err, "team.db must be created in the data dir")
	require.NoError(t, c.CloseModules())
}
