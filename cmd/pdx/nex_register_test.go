// cmd/pdx/nex_register_test.go
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// TestRegisterServeModules_NexDisabled is the I1 end-to-end case: with
// [nex] disabled (the default), registerServeModules must not mount the
// nex module at all, so its API is a plain 404 through the full outer
// handler chain, its data directory is never created, and the process
// PATH is left untouched (only a mounted module's Init would touch it).
func TestRegisterServeModules_NexDisabled(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", origPath)

	dataDir := t.TempDir()
	cfg := &config.Config{
		DataDir: dataDir,
		Token:   "t",
		// Nex left zero-value: Enabled=false.
	}
	c := core.New(core.CoreDeps{Config: cfg})

	require.NoError(t, registerServeModules(c, nil, nil))
	assert.False(t, c.Mounted("nex"))

	require.NoError(t, c.InitModules())

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)

	outer := newOuterHandler(c, mux, []string{"127.0.0.1"})

	req := httptest.NewRequest(http.MethodGet, "/api/nex/v1/capabilities", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	outer.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	if _, err := os.Stat(filepath.Join(dataDir, "nex")); !os.IsNotExist(err) {
		t.Errorf("expected %s/nex to not exist, stat err = %v", dataDir, err)
	}

	assert.Equal(t, origPath, os.Getenv("PATH"), "PATH must be unchanged when nex is disabled")
}

// TestRegisterServeModules_NexEnabled is the mount half of I1: with [nex]
// enabled, registerServeModules must add the module so Core.Mounted("nex")
// reports true immediately, before InitModules (which would run the real
// Nexen Assemble) is ever called.
func TestRegisterServeModules_NexEnabled(t *testing.T) {
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Nex:     config.NexConfig{Enabled: true},
	}
	c := core.New(core.CoreDeps{Config: cfg})

	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("nex"))
}
