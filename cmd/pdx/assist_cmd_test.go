package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type assistReq struct {
	Method, Path, Query string
	Body                map[string]any
}

// assistServer answers every request with code and answer, and records what it was asked.
func assistServer(t *testing.T, code int, answer string, got *[]assistReq) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		req := assistReq{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
		_ = json.Unmarshal(raw, &req.Body)
		*got = append(*got, req)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(answer))
	}))
}

func callAssist(t *testing.T, code int, answer string, args ...string) (int, string, string, []assistReq) {
	var got []assistReq
	srv := assistServer(t, code, answer, &got)
	defer srv.Close()
	var out, errb bytes.Buffer
	rc := assistMain(append(args, "--config", watchCfgFile(t, srv, "tok")), &out, &errb)
	return rc, out.String(), errb.String(), got
}

func TestAssistHostAddSendsOnlyTheFieldsGiven(t *testing.T) {
	rc, _, errb, got := callAssist(t, 200, `{"result":"added"}`, "host", "add", "air26", "--scripts", "/s", "--ssh", "air26-lan", "--slots", "3",
		"--can", "vitest", "--can", "tsc", "--cannot", "go vet ./...", "--load-limit", "1.5", "--idle-min", "20", "--hold-min", "7")
	require.Equal(t, 0, rc, errb)
	require.Len(t, got, 1)
	require.Equal(t, "POST", got[0].Method)
	require.Equal(t, "/api/assist/host", got[0].Path)
	b := got[0].Body
	require.Equal(t, "air26", b["alias"])
	require.Equal(t, "/s", b["scripts"])
	require.Equal(t, "air26-lan", b["ssh"])
	require.EqualValues(t, 3, b["slots"])
	require.Equal(t, []any{"vitest", "tsc"}, b["can"])
	require.Equal(t, []any{"go vet ./..."}, b["cannot"])
	require.EqualValues(t, 1.5, b["load_limit"])
	require.EqualValues(t, 20, b["idle_min"])
	require.EqualValues(t, 7, b["hold_min"])
}

func TestAssistHostSetSendsNothingItWasNotGiven(t *testing.T) {
	rc, _, errb, got := callAssist(t, 200, `{"result":"changed"}`, "host", "set", "air26", "--slots", "4")
	require.Equal(t, 0, rc, errb)
	b := got[0].Body
	require.Equal(t, map[string]any{"alias": "air26", "slots": float64(4)}, b)
	rc, _, _, got = callAssist(t, 200, `{"result":"changed"}`, "host", "set", "air26", "--clear-can", "--clear-cannot")
	require.Equal(t, 0, rc)
	require.Equal(t, true, got[0].Body["clear_can"])
	require.Equal(t, true, got[0].Body["clear_cannot"])
	require.NotContains(t, got[0].Body, "can")
}

func TestAssistHostRemove(t *testing.T) {
	rc, _, errb, got := callAssist(t, 200, `{"result":"removed"}`, "host", "remove", "air26")
	require.Equal(t, 0, rc, errb)
	require.Equal(t, "DELETE", got[0].Method)
	require.Equal(t, "/api/assist/host/air26", got[0].Path)
}

func TestAssistOnOffNeedAReason(t *testing.T) {
	rc, _, errb, got := callAssist(t, 200, `{"host":"air26","on":true,"detail":"on ok"}`, "on", "air26")
	require.Equal(t, 2, rc)
	require.Contains(t, errb, "--reason")
	require.Empty(t, got)
	rc, out, errb, got := callAssist(t, 200, `{"host":"air26","on":true,"detail":"on ok"}`, "on", "air26", "--reason", "測試")
	require.Equal(t, 0, rc, errb)
	require.Contains(t, out, "on ok")
	require.Equal(t, "/api/assist/switch", got[0].Path)
	require.Equal(t, true, got[0].Body["on"])
	require.Equal(t, "air26", got[0].Body["host"])
	require.Equal(t, "測試", got[0].Body["reason"])
	_, _, _, got = callAssist(t, 200, `{}`, "off", "air26", "--reason", "x")
	require.Equal(t, false, got[0].Body["on"])
}

func TestAssistSwitchFailureAndBusy(t *testing.T) {
	rc, _, errb, _ := callAssist(t, 502, `{"error":"switch_failed","detail":"no launchd"}`, "on", "air26", "--reason", "x")
	require.Equal(t, 1, rc)
	require.Contains(t, errb, "no launchd")
	rc, _, errb, _ = callAssist(t, 409, `{"error":"busy","detail":"a switch of this host is running"}`, "off", "air26", "--reason", "x")
	require.Equal(t, 1, rc)
	require.Contains(t, errb, "busy")
}

func TestAssistAllowDenyTargetsAndBy(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-9")
	rc, out, errb, got := callAssist(t, 200, `{"result":"allowed","id":"t1","name":"alpha"}`, "allow", "--team", "alpha", "--host", "air26", "--reason", "r")
	require.Equal(t, 0, rc, errb)
	require.Contains(t, out, "alpha")
	require.Equal(t, "/api/assist/allow", got[0].Path)
	require.Equal(t, map[string]any{"kind": "team", "id": "alpha", "host": "air26", "reason": "r", "by": "sess-9"}, got[0].Body)
	_, _, _, got = callAssist(t, 200, `{"result":"removed"}`, "deny", "--session", "_abcdef")
	require.Equal(t, "/api/assist/deny", got[0].Path)
	require.Equal(t, "session", got[0].Body["kind"])
	require.Equal(t, "_abcdef", got[0].Body["id"])
	// one target, and allow needs its reason
	rc, _, _, _ = callAssist(t, 200, `{}`, "allow", "--team", "a", "--session", "_abcdef", "--reason", "r")
	require.Equal(t, 2, rc)
	rc, _, errb, _ = callAssist(t, 200, `{}`, "allow", "--team", "a")
	require.Equal(t, 2, rc)
	require.Contains(t, errb, "--reason")
	rc, _, _, _ = callAssist(t, 200, `{}`, "allow", "--reason", "r")
	require.Equal(t, 2, rc)
}

func TestAssistAllowAmbiguousAndAlreadyAreSaid(t *testing.T) {
	rc, _, errb, _ := callAssist(t, 409, `{"error":"ambiguous","detail":"2 live teams are named \"same\": t1, t2"}`, "allow", "--team", "same", "--reason", "r")
	require.Equal(t, 1, rc)
	require.Contains(t, errb, "t1, t2")
	rc, out, _, _ := callAssist(t, 200, `{"result":"already","id":"t1","name":"alpha"}`, "allow", "--team", "alpha", "--reason", "r")
	require.Equal(t, 0, rc)
	require.Contains(t, out, "已在名單")
}

func TestAssistStatusJSONAndText(t *testing.T) {
	answer := `{"file":{"Version":1,"Hosts":{"air26":{"SSH":"air26","Scripts":"/s","On":true,"Gen":3,"Can":["vitest"]}},"Allowed":[{"Kind":"team","ID":"t1","Name":"alpha","Host":"","Reason":"r"}]},
	"ready":{"air26":{"gen":3,"at":"2026-10-17T12:00:00Z","answer":{"assist":true,"switch":"on","repos":{"purdex":{"ready":true}}}}},"now":"2026-10-17T12:00:10Z"}`
	rc, out, errb, got := callAssist(t, 200, answer, "status")
	require.Equal(t, 0, rc, errb)
	require.Equal(t, "GET", got[0].Method)
	for _, want := range []string{"air26", "開", "purdex", "alpha"} {
		require.Contains(t, out, want)
	}
	rc, out, _, _ = callAssist(t, 200, answer, "--json")
	require.Equal(t, 0, rc)
	require.True(t, json.Valid([]byte(out)))
	rc, out, _, _ = callAssist(t, 200, `{"file":{"Hosts":{}},"assist_error":"assist.json: bad"}`)
	require.Equal(t, 0, rc)
	require.Contains(t, out, "assist.json: bad")
}

func TestAssistUsage(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"host"}, {"host", "add"}, {"host", "frob", "x"}, {"on"}} {
		rc, _, errb, got := callAssist(t, 200, `{}`, args...)
		require.Equal(t, 2, rc, args)
		require.NotEmpty(t, errb)
		require.Empty(t, got)
	}
}

func TestGateHostsTextAndJSON(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")
	answer := `{"repo":"purdex","hosts":[{"host":"air26","on":true,"ready":true,"allowed":true,"usable":true},
	{"host":"mserv","on":false,"ready":false,"allowed":false,"usable":false,"why":"assist is off"}]}`
	var got []assistReq
	srv := assistServer(t, 200, answer, &got)
	defer srv.Close()
	cfg := watchCfgFile(t, srv, "tok")
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"hosts", "--config", cfg}, &out, &errb), errb.String())
	require.Equal(t, "/api/assist/hosts", got[0].Path)
	require.Contains(t, got[0].Query, "session=sess-1")
	require.Contains(t, got[0].Query, "cwd=")
	for _, want := range []string{"purdex", "mlab", "air26", "可用", "mserv", "assist is off"} {
		require.Contains(t, out.String(), want)
	}
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"hosts", "--json", "--session", "other", "--config", cfg}, &out, &errb))
	require.Contains(t, got[1].Query, "session=other")
	require.True(t, json.Valid(out.Bytes()))
}

func TestGateHostsWithNoAssistHostsSaysSo(t *testing.T) {
	var got []assistReq
	srv := assistServer(t, 200, `{"repo":"purdex","hosts":[]}`, &got)
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"hosts", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb))
	require.Contains(t, out.String(), "no assist hosts")
}
