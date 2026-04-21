package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSessionHome_ReturnsShellHome(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("my-sess", "/initial")
	mod.shellHomeReader = func(pid string) (string, error) {
		return "/Users/test", nil
	}

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/home")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Home string `json:"home"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "/Users/test", body.Home)
}

func TestHandleSessionHome_FallbackToDaemonHome(t *testing.T) {
	mod, _, fake := newTestModule(t)
	t.Setenv("HOME", "/Users/fallback")

	fake.AddSession("my-sess", "/initial")
	mod.shellHomeReader = func(pid string) (string, error) {
		return "", errors.New("boom")
	}

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/home")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Home string `json:"home"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "/Users/fallback", body.Home)
}

func TestHandleSessionHome_NoHomeAtAll(t *testing.T) {
	mod, _, fake := newTestModule(t)
	t.Setenv("HOME", "")

	fake.AddSession("my-sess", "/initial")
	mod.shellHomeReader = func(pid string) (string, error) {
		return "", errors.New("boom")
	}

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/home")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHandleSessionHome_SessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/nosession/home")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
