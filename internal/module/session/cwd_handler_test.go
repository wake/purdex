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

	// Create a real session via fake; this generates a tmux ID like "$0" and stores name "my-sess"
	fake.AddSession("my-sess", "/initial")
	fake.SetPaneCwd("my-sess", "/home/user/proj")

	// Find the code by listing sessions — codec maps tmuxID → code
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/cwd")
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

	// Use a syntactically valid but non-existent code
	resp, err := http.Get(srv.URL + "/api/sessions/nosession/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleSessionCwd_TmuxError(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("my-sess", "/initial")
	// NOT calling SetPaneCwd → PaneCurrentPath will return an error

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}
