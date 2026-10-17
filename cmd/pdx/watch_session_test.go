package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// sessionServer answers POST /api/watch/<kind> with code and body, and records the request body.
func sessionServer(t *testing.T, kind string, code int, answer string, got *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/api/watch/"+kind || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = string(raw)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(answer))
	}))
}

func TestWatchStart_SaysWhereTheWatcherIsAndIsInProgressWhenItIsComingUp(t *testing.T) {
	srv := sessionServer(t, "start", 200, `{"host":"air26","host_id":"h1","state":"awake","session_id":"s-1"}`, nil)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"start", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb), errb.String())
	require.Contains(t, out.String(), "air26")
	require.Contains(t, out.String(), "s-1")

	srv2 := sessionServer(t, "start", 202, `{"host":"mlab","host_id":"h2"}`, nil)
	defer srv2.Close()
	out.Reset()
	require.Equal(t, 0, watchMain([]string{"start", "--config", watchCfgFile(t, srv2, "tok")}, &out, &errb), errb.String())
	require.Contains(t, out.String(), "啟動中")
}

func TestWatchStart_ACooldownIsAFailureThatNamesTheTime(t *testing.T) {
	srv := sessionServer(t, "start", 409, `{"error":"cooldown","detail":"2026-10-17T12:30:00Z"}`, nil)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 1, watchMain([]string{"start", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))
	require.Contains(t, errb.String(), "2026-10-17T12:30:00Z")
}

func TestWatchWake_SendsThePromptOnlyWhenGiven(t *testing.T) {
	var got string
	srv := sessionServer(t, "wake", 200, `{"host_id":"h1","state":"awake","session_id":"s-1"}`, &got)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"wake", "--config", cfg}, &out, &errb), errb.String())
	require.JSONEq(t, `{}`, got)
	require.Equal(t, 0, watchMain([]string{"wake", "--prompt", "查磁碟", "--config", cfg}, &out, &errb), errb.String())
	require.JSONEq(t, `{"prompt":"查磁碟"}`, got)
	require.Equal(t, 0, watchMain([]string{"wake", "--prompt", "", "--config", cfg}, &out, &errb), errb.String())
	require.JSONEq(t, `{"prompt":""}`, got, "an empty prompt is sent as given")
}

func TestWatchStartWake_ARefusalIsExit1WithTheDaemonsWordsAndExtraWordsAreUsage(t *testing.T) {
	srv := sessionServer(t, "wake", 400, `{"error":"bad_prompt","detail":"the prompt must be at most 4 KB"}`, nil)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 1, watchMain([]string{"wake", "--prompt", "x", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))
	require.Contains(t, errb.String(), "bad_prompt")
	require.Equal(t, 2, watchMain([]string{"start", "extra"}, &out, &errb))
}
