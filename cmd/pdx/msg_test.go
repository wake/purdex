package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

// fakeGetenv returns a getenv func backed by kv, "" for any key not
// present — a stand-in for os.Getenv in tests that must not depend on the
// real process environment.
func fakeGetenv(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// --- grammar rejections: exit 2, zero requests, before any config load ----

func TestRunMsgCmd_GrammarRejections(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")

	cases := []struct {
		name string
		args []string
	}{
		{"no args", []string{}},
		{"unknown verb", []string{"frobnicate"}},
		{"send missing text", []string{"send", "alias/sess"}},
		{"send missing everything", []string{"send"}},
		{"send extra positional", []string{"send", "alias/sess", "hello", "extra"}},
		{"send bad mode", []string{"send", "alias/sess", "hello", "--mode", "weird"}},
		{"send mode missing value", []string{"send", "alias/sess", "hello", "--mode"}},
		{"send with --tail", []string{"send", "alias/sess", "hello", "--tail", "5"}},
		{"send with --timeout", []string{"send", "alias/sess", "hello", "--timeout", "5s"}},
		{"send unknown flag", []string{"send", "alias/sess", "hello", "--bogus"}},
		{"log extra positional", []string{"log", "extra"}},
		{"log with --mode", []string{"log", "--mode", "prompting"}},
		{"log with --timeout", []string{"log", "--timeout", "5s"}},
		{"log --tail non-numeric", []string{"log", "--tail", "abc"}},
		{"log --tail negative", []string{"log", "--tail", "-1"}},
		{"log --tail missing value", []string{"log", "--tail"}},
		{"deliver missing arg", []string{"deliver"}},
		{"deliver bad arg", []string{"deliver", "maybe"}},
		{"deliver extra positional", []string{"deliver", "on", "extra"}},
		{"deliver with --mode", []string{"deliver", "on", "--mode", "prompting"}},
		{"deliver with --tail", []string{"deliver", "on", "--tail", "5"}},
		{"selftest with --json", []string{"selftest", "--json"}},
		{"selftest with --mode", []string{"selftest", "--mode", "prompting"}},
		{"selftest extra positional", []string{"selftest", "extra"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := atomic.LoadInt64(&reqCount)
			args := append(append([]string{}, tc.args...), "--config", cfgPath)
			var stdout, stderr bytes.Buffer
			code := runMsgCmd(args, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/sock"}), &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
			}
			if stderr.String() == "" {
				t.Errorf("stderr is empty, want a usage/error message")
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			after := atomic.LoadInt64(&reqCount)
			if after != before {
				t.Errorf("server saw %d request(s), want 0", after-before)
			}
		})
	}
}

// TestRunMsgCmd_ConfigFlagMissingValue is separate from the table above:
// appending the harness's own "--config <path>" after a bare trailing
// "--config" would just supply the missing value, defeating the case.
func TestRunMsgCmd_ConfigFlagMissingValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"--config"}, fakeGetenv(nil), &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

// --- send: origin_unknown pre-check ----------------------------------------

func TestRunMsgSend_OriginUnknown(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "alias/sess", "hello", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "origin_unknown") {
		t.Errorf("stderr = %q, want it to contain origin_unknown", stderr.String())
	}
	wantMsg := "pdx msg: origin_unknown: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session\n"
	if stderr.String() != wantMsg {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantMsg)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if atomic.LoadInt64(&reqCount) != 0 {
		t.Errorf("server saw %d request(s), want 0", reqCount)
	}
}

// --- send: success ----------------------------------------------------------

func TestRunMsgSend_Success(t *testing.T) {
	cases := []struct {
		name     string
		oneWay   bool
		wantTail string
	}{
		{"delivered, return route", false, ""},
		{"delivered, one-way", true, ", one-way"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotAuth string
			var gotReq ipeers.SendRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				resp := ipeers.SendResponse{
					MsgID:         "11111111-2222-3333-4444-555555555555",
					ToHostID:      "air-host-id",
					ToAddress:     "air/wake-cc",
					Result:        ipeers.ResultDelivered,
					EffectiveMode: ipeers.ModePrompting,
					OneWay:        tc.oneWay,
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()
			cfgPath := writeTestConfig(t, srv.URL, "sekret")

			var stdout, stderr bytes.Buffer
			code := runMsgCmd([]string{"send", "air/wake-cc", "hello there", "--config", cfgPath},
				fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)

			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != "/api/peers/send" {
				t.Errorf("path = %q, want /api/peers/send", gotPath)
			}
			if gotAuth != "Bearer sekret" {
				t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
			}
			wantReq := ipeers.SendRequest{To: "air/wake-cc", Text: "hello there", Mode: "", OriginInbox: "/tmp/cc.sock"}
			if gotReq != wantReq {
				t.Errorf("posted request = %+v, want %+v", gotReq, wantReq)
			}

			want := "sent 11111111-2222-3333-4444-555555555555 → air/wake-cc (delivered, mode prompting" + tc.wantTail + ")\n"
			if stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}
			if stderr.String() != "" {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestRunMsgSend_ModeFlag(t *testing.T) {
	var gotReq ipeers.SendRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotReq)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ipeers.SendResponse{
			MsgID: "id", ToAddress: "air/x", Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModeBypass,
		})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "air/x", "go", "--mode", "bypass", "--config", cfgPath},
		fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotReq.Mode != ipeers.ModeBypass {
		t.Errorf("posted mode = %q, want %q", gotReq.Mode, ipeers.ModeBypass)
	}
}

// --- send: error rendering ---------------------------------------------------

func TestRunMsgSend_ErrorRendering(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       ipeers.APIError
		wantStderr string
	}{
		{
			name:   "remote_error with remote detail",
			status: http.StatusBadGateway,
			body: ipeers.APIError{
				Error:  ipeers.ErrRemoteError,
				Detail: "the peer refused the delivery",
				Remote: &ipeers.RemoteError{Status: http.StatusConflict, Error: ipeers.ErrNotDeliverable, Detail: "shell session"},
			},
			wantStderr: "pdx msg: air: not_deliverable: shell session\n",
		},
		{
			name:   "remote_error without remote detail",
			status: http.StatusBadGateway,
			body: ipeers.APIError{
				Error:  ipeers.ErrRemoteError,
				Detail: "the deliver call to the peer failed",
				Remote: &ipeers.RemoteError{Status: 0, Error: "dial tcp: connection refused"},
			},
			wantStderr: "pdx msg: air: dial tcp: connection refused\n",
		},
		{
			name:   "ambiguous",
			status: http.StatusConflict,
			body: ipeers.APIError{
				Error:      ipeers.ErrAmbiguous,
				Detail:     `peer address "wake" is ambiguous (2 candidates)`,
				Candidates: []string{"air/wake-cc-1", "air/wake-cc-2"},
			},
			wantStderr: "pdx msg: ambiguous: wake\n  air/wake-cc-1\n  air/wake-cc-2\n",
		},
		{
			name:   "origin_unknown from daemon",
			status: http.StatusBadRequest,
			body: ipeers.APIError{
				Error:  ipeers.ErrOriginUnknown,
				Detail: "origin_inbox is not a live, deliverable Claude Code session on this host",
			},
			wantStderr: "pdx msg: origin_unknown: origin_inbox is not a live, deliverable Claude Code session on this host\n",
		},
		{
			name:       "host_unknown (default rendering, no detail)",
			status:     http.StatusNotFound,
			body:       ipeers.APIError{Error: ipeers.ErrHostUnknown, Detail: `no peer host "air"`},
			wantStderr: "pdx msg: host_unknown: no peer host \"air\"\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()
			cfgPath := writeTestConfig(t, srv.URL, "sekret")

			var stdout, stderr bytes.Buffer
			code := runMsgCmd([]string{"send", "air/wake", "hi", "--config", cfgPath},
				fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)

			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if stderr.String() != tc.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

// --- send: --json passthrough -----------------------------------------------

func TestRunMsgSend_JSONPassthrough(t *testing.T) {
	const body = `{"msg_id":"id","to_address":"air/x","result":"delivered","effective_mode":"prompting"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "air/x", "hi", "--json", "--config", cfgPath},
		fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != body {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), body)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunMsgSend_JSONPassthroughOnError(t *testing.T) {
	const body = `{"error":"ambiguous","detail":"peer address \"wake\" is ambiguous (2 candidates)","candidates":["air/a","air/b"]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "air/wake", "hi", "--json", "--config", cfgPath},
		fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.String() != body {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), body)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

// --- send: sanitisation ------------------------------------------------------

// controlCharResult is a daemon-supplied `result` string carrying an
// ESC-CSI sequence — a stand-in for a compromised/misbehaving peer or
// daemon trying to inject an escape sequence into the operator's terminal.
var controlCharResult = "ok" + string(rune(0x1b)) + "[31m!"

func TestRunMsgSend_SanitizesControlCharacters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ipeers.SendResponse{
			MsgID: "id", ToAddress: "air/x", Result: controlCharResult, EffectiveMode: ipeers.ModePrompting,
		})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "air/x", "hi", "--config", cfgPath},
		fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.ContainsRune(stdout.String(), 0x1b) {
		t.Errorf("stdout = %q, want no raw ESC byte", stdout.String())
	}
	if !strings.Contains(stdout.String(), `\x1b[31m!`) {
		t.Errorf("stdout = %q, want the escaped form", stdout.String())
	}
}

// --- log: golden table --------------------------------------------------

func msgLogFixture() []msgLogEntry {
	return []msgLogEntry{
		{
			MsgID: "abcdef12-3456-7890-abcd-ef1234567890", Direction: "out",
			TS:         "2026-09-14T03:04:05.000Z",
			FromHostID: "mini", FromSessionID: "sessA1234567",
			ToHostID: "air", ToSessionID: "sessB7654321",
			DeclaredMode: "prompting", EffectiveMode: "prompting",
			Bytes:  42,
			Result: "delivered",
		},
		{
			MsgID: "22222222-3333-4444-5555-666666666666", Direction: "in",
			TS:         "2026-09-14T13:00:00.500Z",
			FromHostID: "air", FromSessionID: "xsess0001",
			ToHostID: "mini", ToSessionID: "ysess0002",
			DeclaredMode: "bypass", EffectiveMode: "prompting",
			Bytes:  7,
			Result: "delivered",
		},
		{
			MsgID: "deadbeef-3456-7890-abcd-ef1234567890", Direction: "reply",
			TS:         "2026-09-14T23:59:59.999Z",
			FromHostID: "mini", FromSessionID: "z",
			ToHostID: "air", ToSessionID: "",
			DeclaredMode: "bypass", EffectiveMode: "bypass",
			Bytes:  100,
			Result: "delivery_uncertain",
			Error:  "no_return_route",
		},
	}
}

func TestFormatMsgLogTable(t *testing.T) {
	got := formatMsgLogTable(msgLogFixture(), time.UTC)
	t.Logf("got:\n%s", got)

	wantLines := []string{
		"TIME      DIR    MSG_ID    FROM           TO             MODE  BYTES  RESULT              ERROR",
		"03:04:05  out    abcdef12  mini/sessA123  air/sessB765   p→p   42     delivered           ",
		"13:00:00  in     22222222  air/xsess000   mini/ysess000  b→p   7      delivered           ",
		"23:59:59  reply  deadbeef  mini/z         air/           b→b   100    delivery_uncertain  no_return_route",
	}
	want := strings.Join(wantLines, "\n") + "\n"
	if got != want {
		t.Errorf("formatMsgLogTable mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatMsgLogTable_SanitizesControlCharacters(t *testing.T) {
	entries := []msgLogEntry{
		{MsgID: "id", Direction: "out", TS: "2026-09-14T00:00:00.000Z",
			FromHostID: "mini", FromSessionID: "s", ToHostID: "air", ToSessionID: "s",
			DeclaredMode: "prompting", EffectiveMode: "prompting",
			Result: controlCharResult},
	}
	got := formatMsgLogTable(entries, time.UTC)
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("formatMsgLogTable = %q, want no raw ESC byte", got)
	}
	if !strings.Contains(got, `\x1b[31m!`) {
		t.Errorf("formatMsgLogTable = %q, want the escaped form", got)
	}
}

// --- log: through HTTP -------------------------------------------------------

func TestRunMsgLog_DefaultTail(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(msgLogResponse{})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"log", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/peers/log" {
		t.Errorf("path = %q, want /api/peers/log", gotPath)
	}
	if gotQuery != "tail=50" {
		t.Errorf("query = %q, want tail=50", gotQuery)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
}

func TestRunMsgLog_CustomTail(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(msgLogResponse{})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"log", "--tail", "5", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotQuery != "tail=5" {
		t.Errorf("query = %q, want tail=5", gotQuery)
	}
}

func TestRunMsgLog_JSONPassthrough(t *testing.T) {
	const body = `{"messages":[{"id":1,"msg_id":"m","direction":"out"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"log", "--json", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != body {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), body)
	}
}

func TestRunMsgLog_ErrorRendering(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: ipeers.ErrAuditUnavailable, Detail: "audit store is not available"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"log", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	want := "pdx msg: audit_unavailable: audit store is not available\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// --- deliver: on|off|status --------------------------------------------------

func TestRunMsgDeliver_On(t *testing.T) {
	var gotMethod string
	var gotBody msgPutSettingsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(msgSettingsResponse{Deliver: true, Alias: "mini"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"deliver", "on", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotBody.Deliver == nil || *gotBody.Deliver != true {
		t.Errorf("posted deliver = %v, want true", gotBody.Deliver)
	}
	if stdout.String() != "deliver: on\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "deliver: on\n")
	}
}

func TestRunMsgDeliver_Off(t *testing.T) {
	var gotBody msgPutSettingsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(msgSettingsResponse{Deliver: false, Alias: "mini"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"deliver", "off", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotBody.Deliver == nil || *gotBody.Deliver != false {
		t.Errorf("posted deliver = %v, want false", gotBody.Deliver)
	}
	if stdout.String() != "deliver: off\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "deliver: off\n")
	}
}

func TestRunMsgDeliver_Status(t *testing.T) {
	var gotMethod string
	var gotBodyBytes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		buf := make([]byte, 1)
		n, _ := r.Body.Read(buf)
		gotBodyBytes = n
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(msgSettingsResponse{Deliver: true, Alias: "mini"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"deliver", "status", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotBodyBytes != 0 {
		t.Errorf("request body was non-empty for a status check")
	}
	if stdout.String() != "deliver: on\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "deliver: on\n")
	}
}

func TestRunMsgDeliver_JSONPassthrough(t *testing.T) {
	const body = `{"deliver":true,"alias":"mini"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"deliver", "on", "--json", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != body {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), body)
	}
}

func TestRunMsgDeliver_ErrorRendering(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: ipeers.ErrForbidden, Detail: "admin only"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"deliver", "on", "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	want := "pdx msg: forbidden: admin only\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// --- selftest: the verb wrapper ----------------------------------------------
//
// The body itself is covered in msg_selftest_test.go with every seam faked;
// here only the grammar-to-body wrapper is exercised, and only on the paths
// that never reach tmux.

func TestRunMsgSelftest_InvalidTimeoutIsGrammarError(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	for _, raw := range []string{"abc", "0", "-5s"} {
		var stdout, stderr bytes.Buffer
		code := runMsgCmd([]string{"selftest", "--timeout", raw, "--config", cfgPath}, fakeGetenv(nil), &stdout, &stderr)
		if code != 2 {
			t.Errorf("--timeout %q: exit code = %d, want 2; stderr=%q", raw, code, stderr.String())
		}
		want := "pdx msg: invalid --timeout " + raw + "\n"
		if stderr.String() != want {
			t.Errorf("--timeout %q: stderr = %q, want %q", raw, stderr.String(), want)
		}
		if stdout.Len() != 0 {
			t.Errorf("--timeout %q: stdout = %q, want empty", raw, stdout.String())
		}
	}
	if atomic.LoadInt64(&reqCount) != 0 {
		t.Errorf("server saw %d request(s), want 0", reqCount)
	}
}
