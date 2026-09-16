package main

import (
	"bytes"
	"encoding/json"
	"io"
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
		{"send text starting with dash without --", []string{"send", "alias/sess", "- first item"}},
		{"send flag after -- is positional", []string{"send", "--", "alias/sess", "hello", "--json"}},
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

// TestRunMsgUsage_TeachesBothDefaultLabelForms pins what the grammar
// rejection prints about addresses. Since an unnamed agent's default label
// is now its tmux session name and the "_k3x9qz" hash is only the fallback
// (default-label spec §2, §4.1), the usage text has to say both — a stale
// hash address is the most likely reason someone is reading it — and point
// at the one command that lists the current addresses.
func TestRunMsgUsage_TeachesBothDefaultLabelForms(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send"}, fakeGetenv(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"tmux session", "mini-lab/purdex1", "_k3x9qz", "pdx peers --all", "pdx msg name"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage does not mention %q:\n%s", want, out)
		}
	}
}

// TestRunMsgSend_DoubleDash pins the option terminator: everything after
// "--" is positional, so text that starts with a dash (or that looks like
// one of pdx msg's own flags) can be sent; flags before "--" still apply.
func TestRunMsgSend_DoubleDash(t *testing.T) {
	for _, text := range []string{"--json", "- first item", "--mode bypass"} {
		t.Run(text, func(t *testing.T) {
			var gotReq ipeers.SendRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&gotReq)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(ipeers.SendResponse{
					MsgID: "id", ToAddress: "air/x", Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting,
				})
			}))
			defer srv.Close()
			cfgPath := writeTestConfig(t, srv.URL, "sekret")

			var stdout, stderr bytes.Buffer
			code := runMsgCmd([]string{"send", "--config", cfgPath, "--", "air/x", text},
				fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			if gotReq.To != "air/x" || gotReq.Text != text || gotReq.Mode != "" {
				t.Errorf("posted request = %+v, want to air/x text %q, no mode", gotReq, text)
			}
		})
	}
	t.Run("dash text without -- is an unknown flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runMsgCmd([]string{"send", "air/x", "- first item"}, fakeGetenv(nil), &stdout, &stderr)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "unknown flag - first item") || !strings.Contains(stderr.String(), "[--]") {
			t.Errorf("stderr = %q, want the unknown flag named and the usage mentioning --", stderr.String())
		}
	})
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
			// Spec §4.1/§7: an ambiguity refusal is SAFE, and the operator
			// has to be able to SEE that. One line per candidate carrying
			// agent name, pid and cwd is what turns "my address stopped
			// working" into "two of my conversations share this address";
			// without it the reader goes hunting a bug that is not there.
			name:   "ambiguous",
			status: http.StatusConflict,
			body: ipeers.APIError{
				Error:  ipeers.ErrAmbiguous,
				Detail: `peer address "wake" is ambiguous (2 candidates)`,
				Candidates: []ipeers.AmbiguousCandidate{
					{Address: "air/_1c4m7dkz:mt0-twin-1", AgentName: "twin-1", PID: 41001, Cwd: "/w/one"},
					{Address: "air/_1c4m7dkz:mt0-twin-2", AgentName: "twin-2", PID: 41002, Cwd: "/w/two"},
				},
			},
			wantStderr: "pdx msg: ambiguous: wake\n" +
				"  air/_1c4m7dkz:mt0-twin-1  agent twin-1  pid 41001  cwd /w/one\n" +
				"  air/_1c4m7dkz:mt0-twin-2  agent twin-2  pid 41002  cwd /w/two\n",
		},
		{
			// A candidate the daemon knows only by address still gets a
			// line: the extra fields are advisory, the line is not.
			name:   "ambiguous with a bare candidate",
			status: http.StatusConflict,
			body: ipeers.APIError{
				Error:      ipeers.ErrAmbiguous,
				Detail:     `peer address "wake" is ambiguous (2 candidates)`,
				Candidates: []ipeers.AmbiguousCandidate{{Address: "air/_1c4m7dkz:mt0-twin-1"}, {Address: "air/tmux:zz", Cwd: "/w/two"}},
			},
			wantStderr: "pdx msg: ambiguous: wake\n" +
				"  air/_1c4m7dkz:mt0-twin-1\n" +
				"  air/tmux:zz  cwd /w/two\n",
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

func msgLogFixture() []ipeers.LogEntry {
	return []ipeers.LogEntry{
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
	entries := []ipeers.LogEntry{
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
		json.NewEncoder(w).Encode(ipeers.LogResponse{})
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
		json.NewEncoder(w).Encode(ipeers.LogResponse{})
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
	var gotBody ipeers.PutSettingsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ipeers.SettingsResponse{Deliver: true, Alias: "mini"})
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
	var gotBody ipeers.PutSettingsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ipeers.SettingsResponse{Deliver: false, Alias: "mini"})
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
		json.NewEncoder(w).Encode(ipeers.SettingsResponse{Deliver: true, Alias: "mini"})
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

// --- name / whoami: the grammar --------------------------------------------

func TestParseMsgInvocation_NameAndWhoami(t *testing.T) {
	cases := []struct {
		args    []string
		ok      bool
		verb    string
		label   string
		release bool
	}{
		{[]string{"name", "purdex-tester"}, true, "name", "purdex-tester", false},
		{[]string{"name", "--release"}, true, "name", "", true},
		{[]string{"name"}, false, "", "", false},
		{[]string{"name", "a", "b"}, false, "", "", false},
		{[]string{"name", "a", "--release"}, false, "", "", false},
		{[]string{"whoami"}, true, "whoami", "", false},
		{[]string{"whoami", "x"}, false, "", "", false},
		{[]string{"whoami", "--release"}, false, "", "", false},
		{[]string{"send", "--release", "a/b", "t"}, false, "", "", false},
		{[]string{"log", "--release"}, false, "", "", false},
		{[]string{"deliver", "on", "--release"}, false, "", "", false},
		{[]string{"selftest", "--release"}, false, "", "", false},
		{[]string{"name", "x", "--mode", "bypass"}, false, "", "", false},
		{[]string{"name", "x", "--tail", "5"}, false, "", "", false},
		{[]string{"name", "x", "--timeout", "5s"}, false, "", "", false},
	}
	for _, c := range cases {
		inv, _, ok := parseMsgInvocation(c.args)
		if ok != c.ok || (ok && (inv.verb != c.verb || inv.label != c.label || inv.release != c.release)) {
			t.Errorf("%v ⇒ ok=%v inv=%+v, want ok=%v verb=%q label=%q release=%v", c.args, ok, inv, c.ok, c.verb, c.label, c.release)
		}
	}
}

// --- whoami: POST /api/peers/self -------------------------------------------

func TestRunMsgWhoami_Text(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/peers/self" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: ipeers.PeerRecord{
			Host: "air", HostID: "air:9k2m4q", Address: "air/purdex-tester:purdex-3f",
			Label: "purdex-tester", LabelSource: "user", LabelRev: 7,
			Agent: &ipeers.AgentInfo{Type: "cc", SessionID: "fa5d4c07-0000", PID: 76973},
		}})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"whoami", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "address:  air/purdex-tester:purdex-3f\nlabel:    purdex-tester (user, rev 7)\nhost:     air (air:9k2m4q)\nsession:  fa5d4c07-0000 pid 76973\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRunMsgWhoami_JSONPassthrough(t *testing.T) {
	raw := `{"address":"air/x1:y","label":"x1"}` + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, raw)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"whoami", "--json", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 || out.String() != raw {
		t.Fatalf("exit %d out %q", code, out.String())
	}
}

// --- name: PUT/DELETE /api/peers/self/label ---------------------------------

func TestRunMsgName_ClaimUsesPut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.ClaimLabelRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPut || r.URL.Path != "/api/peers/self/label" || req.Label != "purdex-tester" || req.OriginInbox != "/tmp/x.sock" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: ipeers.PeerRecord{
			Address: "air/purdex-tester:purdex-3f", Label: "purdex-tester", LabelSource: "user", LabelRev: 1,
			Host: "air", HostID: "air:1",
		}})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "purdex-tester", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "named: air/purdex-tester:purdex-3f\naddress:  air/purdex-tester:purdex-3f\nlabel:    purdex-tester (user, rev 1)\nhost:     air (air:1)\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// TestRunMsgName_DuplicateWarnsAndExitsZero: a claim that lands on a label
// another live session already holds is a SUCCESS (spec D5). The label is
// set, the address is printed as usual, the warning names the other
// holders and every live label — and the exit code is 0, because nothing
// failed. Anything else would make the serial-number convention look like
// an enforced rule again.
func TestRunMsgName_DuplicateWarnsAndExitsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.ClaimLabelRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPut || r.URL.Path != "/api/peers/self/label" || req.Label != "purdex-tester" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{
			Peer: ipeers.PeerRecord{
				Address: "air/_1c4m7dkz:mt0-n10", Label: "purdex-tester", LabelSource: "user", LabelRev: 3,
				Host: "air", HostID: "air:1",
			},
			Warning: &ipeers.SelfWarning{
				Code:       ipeers.WarnLabelInUse,
				Detail:     `"purdex-tester" is also held by 1 other live session`,
				Holders:    []ipeers.PeerRecord{{Address: "air/_9x2pq0af:n20", Label: "purdex-tester"}},
				LiveLabels: []string{"purdex-dev", "purdex-tester"},
			},
		})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "purdex-tester", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0 — a duplicate label is a warning, not a failure; stderr=%s", code, errb.String())
	}
	wantErr := "pdx msg: warning: label_in_use: \"purdex-tester\" is also held by 1 other live session\n" +
		"  also held by: air/_9x2pq0af:n20\n" +
		"  live label: purdex-dev\n" +
		"  live label: purdex-tester\n"
	if errb.String() != wantErr {
		t.Errorf("stderr:\n%s\nwant:\n%s", errb.String(), wantErr)
	}
	if !strings.HasPrefix(out.String(), "named: air/_1c4m7dkz:mt0-n10\n") {
		t.Errorf("stdout:\n%s\nwant the label set and the address printed", out.String())
	}
}

func TestRunMsgName_NotReadyRendersSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "registry has unreadable files", Skipped: []string{"/r/4242.json"}})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "x1", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if errb.String() != "pdx msg: not_ready: registry has unreadable files\n  /r/4242.json\n" {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestRunMsgName_Release_UsesDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.SelfRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodDelete || r.URL.Path != "/api/peers/self/label" || req.OriginInbox != "/tmp/x.sock" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: ipeers.PeerRecord{Address: "air/_k3x9qz:purdex-3f", LabelRev: 2, Host: "air", HostID: "air:1"}})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "--release", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), "released: air/_k3x9qz:purdex-3f\n") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
}

func TestRunMsgName_JSONPassthrough(t *testing.T) {
	raw := `{"address":"air/x1:y","label":"x1"}` + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, raw)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "x1", "--json", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 || out.String() != raw {
		t.Fatalf("exit %d out %q", code, out.String())
	}
}

func TestRunMsgName_NoSocketEnv(t *testing.T) {
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "x1"}, fakeGetenv(nil), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "pdx msg: origin_unknown: CLAUDE_CODE_MESSAGING_SOCKET is unset") {
		t.Fatalf("exit %d err %q", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	code = runMsgCmd([]string{"whoami"}, fakeGetenv(nil), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "pdx msg: origin_unknown: CLAUDE_CODE_MESSAGING_SOCKET is unset") {
		t.Fatalf("whoami exit %d err %q", code, errb.String())
	}
}

// pins the Task 6 deferred Minor: the unknown-flag message must speak the
// v2 grammar (<host>/<label>[:<suffix>] | <host>/tmux:<name>), not the old
// <host>/<session>.
func TestRunMsgCmd_UnknownFlagMessage_V2Grammar(t *testing.T) {
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"send", "air/x", "- first item"}, fakeGetenv(nil), &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(errb.String(), "<host>/<session>") {
		t.Errorf("stderr still uses the old grammar: %q", errb.String())
	}
	if !strings.Contains(errb.String(), "<host>/<label>[:<suffix>]") || !strings.Contains(errb.String(), "<host>/tmux:<name>") {
		t.Errorf("stderr = %q, want the v2 grammar", errb.String())
	}
}
