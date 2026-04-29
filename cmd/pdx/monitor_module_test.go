package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func TestRegisterServeModules_IncludesMonitorRoutes(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})

	require.NoError(t, registerServeModules(c, nil, nil))
	require.NoError(t, c.InitModules())

	mux := http.NewServeMux()
	c.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/monitor/snapshot", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusNotFound, rec.Code)
}
