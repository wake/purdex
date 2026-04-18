package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSessionCwd_ReturnsCwd(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("code-X", "/tmp/initial")
	fake.SetPaneCwd("code-X", "/home/user/proj")

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/code-X/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Cwd string `json:"cwd"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "/home/user/proj", body.Cwd)
}

func TestHandleSessionCwd_NotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/missing/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	// FakeExecutor returns an error for unset pane cwd → handler returns 500
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
}
