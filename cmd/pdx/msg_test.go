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

// TestRunMsgUsage_TeachesTheV4AddressForms pins what the grammar
// rejection prints about addresses: all four forms of spec §5.5, and the
// two commands that print a current one.
//
// The bracket form is listed quoted because it contains a space, and a
// usage line that showed it unquoted would teach an invocation the shell
// splits into two positionals — rejected, right here, with this same
// message.
//
// The closing sentence is the point of the block, not decoration: the
// reader most likely to be here is one who just ran `pdx msg name` and
// assumed the string they chose was now reachable. It is a title; it
// routes nothing. The v3 text said the same thing about a "label", and
// the stale assertions below keep the older grammars from creeping back.
func TestRunMsgUsage_TeachesTheV4AddressForms(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send"}, fakeGetenv(nil), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{
		"<host>/<name>",
		`"<host>/<name> [<ref>]"`,
		"<host>/_<ref>",
		"<host>/tmux:<name>",
		"mlab/_q34psn",
		"pdx peers --all",
		"pdx msg whoami",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "never an address") {
		t.Errorf("usage does not say the claimed title is never an address:\n%s", out)
	}
	for _, stale := range []string{"<host>/<label>", "<host>/<canonical>", "<suffix>", "_3k9f2mq4"} {
		if strings.Contains(out, stale) {
			t.Errorf("usage still offers the retired form %s:\n%s", stale, out)
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
		title   string
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
		if ok != c.ok || (ok && (inv.verb != c.verb || inv.title != c.title || inv.release != c.release)) {
			t.Errorf("%v ⇒ ok=%v inv=%+v, want ok=%v verb=%q title=%q release=%v", c.args, ok, inv, c.ok, c.verb, c.title, c.release)
		}
	}
}

// --- whoami: POST /api/peers/self -------------------------------------------

// TestRunMsgWhoami_Text pins the whoami block, including the canonical:
// line (spec 7). An agent cannot derive its own canonical id -- it is a
// hash of a sessionId the agent never sees -- so asking is the only way to
// learn it, and whoami is where it asks.
func TestRunMsgWhoami_Text(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/peers/self" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: ipeers.PeerRecord{
			Host: "air", HostID: "air:9k2m4q", Address: "air/_3k9f2mq4:purdex-3f",
			Ref: "_3k9f2mq4", Title: "purdex-tester", TitleSource: "user", TitleRev: 7,
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
	want := "address:    air/_3k9f2mq4:purdex-3f\n" +
		"ref:        _3k9f2mq4\n" +
		"title:      purdex-tester (user, rev 7)\n" +
		"host:       air (air:9k2m4q)\n" +
		"session:    fa5d4c07-0000 pid 76973\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRunMsgWhoami_JSONPassthrough(t *testing.T) {
	raw := `{"address":"air/x1:y","title":"x1"}` + "\n"
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

// TestRunMsgSelf_BarePeerRecordIsAVersionMismatch pins the other half of
// the mixed-version story, the one that bites on a single host: `pdx` was
// updated and the daemon was not yet restarted, so a self route answers
// 200 with the v2 shape — a BARE PeerRecord, no envelope.
//
// encoding/json ignores unknown root fields, so that body unmarshals
// happily into a zero SelfResponse and the CLI used to print a whoami
// block of empty strings and exit 0: an agent asking who it is was told
// it is nobody, successfully. The shape is checked instead, and a body
// with no "peer" key is reported as what it is.
//
// Compatibility was deliberately not attempted. A v2 record's address head
// is a label, so accepting it would hand the caller a v2-semantics address
// while every other part of this build treats a head as a canonical id —
// a wrong answer delivered confidently, which is worse than no answer.
func TestRunMsgSelf_BarePeerRecordIsAVersionMismatch(t *testing.T) {
	bare := `{"address":"air/x1:y","label":"x1","host":"air"}`
	for _, c := range []struct {
		name string
		args []string
	}{
		{"whoami", []string{"whoami"}},
		{"name", []string{"name", "purdex-tester"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, bare)
			}))
			defer srv.Close()
			cfgPath := writeTestConfig(t, srv.URL, "t")

			var out, errb bytes.Buffer
			code := runMsgCmd(append(c.args, "--config", cfgPath),
				fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
			if code == 0 {
				t.Fatalf("exit 0 on a v2 response; out %q", out.String())
			}
			if out.Len() != 0 {
				t.Errorf("printed an identity anyway:\n%s", out.String())
			}
			for _, want := range []string{"daemon", "older", "restart"} {
				if !strings.Contains(errb.String(), want) {
					t.Errorf("stderr %q does not mention %q", errb.String(), want)
				}
			}
		})
	}
}

// TestRunMsgSelf_EnvelopeStillWorks pins that the shape check reads the
// envelope and nothing else: a 200 carrying "peer" is decoded exactly as
// before, warning included.
func TestRunMsgSelf_EnvelopeStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ipeers.SelfResponse{
			Peer:    ipeers.PeerRecord{Address: "air/_3k9f2mq4:p-3f", Ref: "_3k9f2mq4", Title: "purdex-tester", TitleSource: "user"},
			Warning: &ipeers.SelfWarning{Code: ipeers.WarnTitleInUse, LiveTitles: []string{"purdex-tester"}},
		})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"whoami", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "ref:        _3k9f2mq4") {
		t.Errorf("out:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), ipeers.WarnTitleInUse) {
		t.Errorf("stderr:\n%s", errb.String())
	}
}

// --- name: PUT/DELETE /api/peers/self/title ---------------------------------

// TestRunMsgName_ClaimPrintsLabelAndUnchangedAddress pins the success line
// (spec 7): it names the title that was set AND the address, which did not
// move. The agent that just named itself is the reader most likely to
// assume the name is now reachable, and this line is where that assumption
// is cheapest to refuse.
func TestRunMsgName_ClaimPrintsLabelAndUnchangedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.ClaimTitleRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPut || r.URL.Path != "/api/peers/self/title" || req.Title != "purdex-tester" || req.OriginInbox != "/tmp/x.sock" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: ipeers.PeerRecord{
			Address: "air/_3k9f2mq4:purdex-3f", Ref: "_3k9f2mq4",
			Title: "purdex-tester", TitleSource: "user", TitleRev: 1,
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
	want := "named: purdex-tester (address unchanged: air/_3k9f2mq4:purdex-3f)\n" +
		"address:    air/_3k9f2mq4:purdex-3f\n" +
		"ref:        _3k9f2mq4\n" +
		"title:      purdex-tester (user, rev 1)\n" +
		"host:       air (air:1)\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// TestRunMsgName_DuplicateWarnsAndExitsZero: a claim that lands on a title
// another live session already holds is a SUCCESS (spec D5). The title is
// set, the address is printed as usual, the warning names the other
// holders and every live title — and the exit code is 0, because nothing
// failed. Anything else would make the serial-number convention look like
// an enforced rule again.
func TestRunMsgName_DuplicateWarnsAndExitsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.ClaimTitleRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPut || r.URL.Path != "/api/peers/self/title" || req.Title != "purdex-tester" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{
			Peer: ipeers.PeerRecord{
				Address: "air/_1c4m7dkz:mt0-n10", Title: "purdex-tester", TitleSource: "user", TitleRev: 3,
				Host: "air", HostID: "air:1",
			},
			Warning: &ipeers.SelfWarning{
				Code:       ipeers.WarnTitleInUse,
				Detail:     `"purdex-tester" is also held by 1 other live session`,
				Holders:    []ipeers.PeerRecord{{Address: "air/_9x2pq0af:n20", Title: "purdex-tester"}},
				LiveTitles: []string{"purdex-dev", "purdex-tester"},
			},
		})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "purdex-tester", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, want 0 — a duplicate title is a warning, not a failure; stderr=%s", code, errb.String())
	}
	wantErr := "pdx msg: warning: title_in_use: \"purdex-tester\" is also held by 1 other live session\n" +
		"  also held by: air/_9x2pq0af:n20\n" +
		"  live title: purdex-dev\n" +
		"  live title: purdex-tester\n"
	if errb.String() != wantErr {
		t.Errorf("stderr:\n%s\nwant:\n%s", errb.String(), wantErr)
	}
	if !strings.HasPrefix(out.String(), "named: purdex-tester (address unchanged: air/_1c4m7dkz:mt0-n10)\n") {
		t.Errorf("stdout:\n%s\nwant the title set and the unchanged address printed", out.String())
	}
}

// TestRunMsgName_NotReadyRendersGenericLine pins that not_ready now gets
// the same one-line shape as any other error. It used to append the
// registry files the daemon could not classify, but nothing sets that list
// any more, so an extra indented section would only ever be empty.
func TestRunMsgName_NotReadyRendersGenericLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "registry has unreadable files"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "x1", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if errb.String() != "pdx msg: not_ready: registry has unreadable files\n" {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestRunMsgName_Release_UsesDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.SelfRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodDelete || r.URL.Path != "/api/peers/self/title" || req.OriginInbox != "/tmp/x.sock" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.SelfResponse{Peer: ipeers.PeerRecord{Address: "air/_3k9f2mq4:purdex-3f", Ref: "_3k9f2mq4", TitleRev: 2, Host: "air", HostID: "air:1"}})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "t")

	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "--release", "--config", cfgPath}, fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock"}), &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), "released: the title (address unchanged: air/_3k9f2mq4:purdex-3f)\n") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
}

func TestRunMsgName_JSONPassthrough(t *testing.T) {
	raw := `{"address":"air/x1:y","title":"x1"}` + "\n"
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

// The unknown-flag message must not teach a grammar that no longer
// resolves: not v1's <host>/<session>, not v2's <host>/<label>, and no
// longer v3's <host>/<canonical>[:<suffix>] — v4 dropped the suffix and
// renamed the head, so a message still offering it would send the reader
// to type a string this daemon cannot route. It defers to the usage block
// for the forms themselves rather than keeping a second copy of them.
func TestRunMsgCmd_UnknownFlagMessage_V4Grammar(t *testing.T) {
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"send", "air/x", "- first item"}, fakeGetenv(nil), &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	for _, stale := range []string{"<host>/<session>", "<host>/<label>", "<host>/<canonical>", "<suffix>"} {
		if strings.Contains(errb.String(), stale) {
			t.Errorf("stderr still uses the stale grammar %s: %q", stale, errb.String())
		}
	}
	if !strings.Contains(errb.String(), "<address> <text>") {
		t.Errorf("stderr = %q, want it to point at the v4 <address> form", errb.String())
	}
}

// --- send: the three v4 input forms -----------------------------------------

// TestParseMsgInvocation_KeepsCombinedAddressVerbatim pins that the CLI
// hands the address to the daemon exactly as typed.
//
// The bracket group in `<name> [<ref>]` is a check digit, not decoration:
// Resolve compares the typed name against the ref's current name and
// refuses a mismatch (spec §5.4). A CLI that trimmed the brackets, split
// on the space or "normalised" the head would leave the ref alone —
// which resolves fine — and so would silently switch that check off for
// everyone who pasted the form the peers table prints. The parser is not
// entitled to drop it.
func TestParseMsgInvocation_KeepsCombinedAddressVerbatim(t *testing.T) {
	const addr = "mlab/purdex-b0 [q34psn]"
	inv, unknownFlag, ok := parseMsgInvocation([]string{"send", addr, "hi"})
	if !ok {
		t.Fatalf("parseMsgInvocation returned !ok (unknownFlag=%q)", unknownFlag)
	}
	if inv.to != addr {
		t.Errorf("to = %q, want the address verbatim %q", inv.to, addr)
	}
}

// TestRunMsgSend_InputFormsPostedVerbatim drives the same rule one level
// out: whatever `to` holds is what lands in SendRequest.To. Checking the
// parser alone would not catch a runMsgSend that tidied the string on its
// way into the body.
func TestRunMsgSend_InputFormsPostedVerbatim(t *testing.T) {
	for _, addr := range []string{
		"mlab/purdex-b0",          // everyday
		"mlab/purdex-b0 [q34psn]", // verbatim copy; the name is checked
		"mlab/_q34psn",            // exact, survives a rename
	} {
		t.Run(addr, func(t *testing.T) {
			var gotReq ipeers.SendRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&gotReq)
				json.NewEncoder(w).Encode(ipeers.SendResponse{
					MsgID: "m", ToAddress: addr, Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting,
				})
			}))
			defer srv.Close()
			cfgPath := writeTestConfig(t, srv.URL, "sekret")

			var stdout, stderr bytes.Buffer
			code := runMsgCmd([]string{"send", addr, "hi", "--config", cfgPath},
				fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}
			if gotReq.To != addr {
				t.Errorf("posted to = %q, want the address verbatim %q", gotReq.To, addr)
			}
		})
	}
}

// TestRunMsgSend_NameMismatchRendering pins what a person actually sees
// when a combined address is refused.
//
// The daemon's detail carries the three values that decide what happened —
// the name typed, the name the ref answers to now, and the ref — and the
// generic renderer would have printed a bare `pdx msg: name_mismatch` for
// a code it did not know, throwing all three away at the last step. It
// also has to say what to do next, because the two causes have different
// answers: re-read the address, or use `<host>/_<ref>` when the rename was
// expected.
func TestRunMsgSend_NameMismatchRendering(t *testing.T) {
	detail := `the typed name does not match the ref's current name: typed "purdex-b0", but q34psn is now "purdex-b3"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: ipeers.ErrCodeNameMismatch, Detail: detail})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")

	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "mlab/purdex-b0 [q34psn]", "hi", "--config", cfgPath},
		fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	out := stderr.String()
	// The three values survive to the terminal.
	for _, want := range []string{`"purdex-b0"`, `"purdex-b3"`, "q34psn"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr does not carry %s:\n%s", want, out)
		}
	}
	// ... and so does what to do about them. The escape-hatch hint names
	// the host the caller typed, so it is a form they can act on rather
	// than a sentence about addresses in general.
	for _, want := range []string{"mlab/_<ref>", "pdx peers --all"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr does not say what to do (%s):\n%s", want, out)
		}
	}
}

// --- whoami/name: the shared record block -----------------------------------

// TestRenderSelfRecord_UnsetTitle pins the unset case, which under v4 is
// the COMMON case: nothing derives a title any more and nothing routes on
// one, so most conversations never set it. The old line rendered that as
// `label:      (, rev 0)` — an empty value, an empty source and a
// revision that means nothing yet, in a tuple that reads like a
// malfunction rather than like "not set" (spec §6.1).
//
// The ref line is asserted here too because it is unconditional: an agent
// cannot compute its own ref (it hashes a sessionId the agent never
// handles), so asking is the only way to learn it.
func TestRenderSelfRecord_UnsetTitle(t *testing.T) {
	var buf bytes.Buffer
	renderSelfRecord(ipeers.PeerRecord{
		Address: "mlab/purdex-53", Ref: "_d8dc4a", Host: "mlab", HostID: "mlab:278cbm",
	}, &buf)
	out := buf.String()
	if strings.Contains(out, "(, rev 0)") {
		t.Errorf("unset title still renders as an empty tuple:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("unset title does not say so:\n%s", out)
	}
	if !strings.Contains(out, "_d8dc4a") {
		t.Errorf("ref line missing:\n%s", out)
	}
}

// TestRenderSelfRecord_SetTitle pins the other half: a set title keeps its
// source and revision, because those are what make a surprising title
// legible (who set it, and how many times it has moved).
func TestRenderSelfRecord_SetTitle(t *testing.T) {
	var buf bytes.Buffer
	renderSelfRecord(ipeers.PeerRecord{
		Address: "mlab/purdex-53", Ref: "_d8dc4a", Title: "Purdex Tester 01",
		TitleSource: "user", TitleRev: 4,
	}, &buf)
	if got := buf.String(); !strings.Contains(got, "Purdex Tester 01 (user, rev 4)") {
		t.Errorf("set title block:\n%s", got)
	}
}
