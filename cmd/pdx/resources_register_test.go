package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
)

// TestRegisterServeModules_MountsResources: the resources module is mounted,
// its Init finds the peers origin resolver under the real wiring, and
// GET /api/resources is live behind TokenAuth. The module is not started
// here, so the snapshot is the warming-up one.
func TestRegisterServeModules_MountsResources(t *testing.T) {
	c := newTestCore(&config.Config{DataDir: t.TempDir(), Token: "t"})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("resources"))
	require.NoError(t, c.InitModules())

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)
	outer := newOuterHandler(c, mux, nil)

	res := doRequest(t, outer, http.MethodGet, "/api/resources", "t")
	assert.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.Contains(t, res.Body.String(), `"reason":"warming_up"`)
	assert.Equal(t, http.StatusUnauthorized, doRequest(t, outer, http.MethodGet, "/api/resources", "").Code)
	require.NoError(t, c.CloseModules())
}
