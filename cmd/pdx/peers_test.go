package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/peers"
)

// --- formatPeersTable golden fixture -------------------------------------

func peersTableFixture() peers.Envelope {
	return peers.Envelope{
		HostID:  "mini:abc123",
		OK:      true,
		Partial: true,
		Peers: []peers.PeerRecord{
			{ // deliverable cc row
				Address: "alias/sess1",
				Agent: &peers.AgentInfo{
					Type:     "cc",
					PeerName: "wake-cc",
					Status:   "working",
				},
				Deliverable: true,
				Cwd:         "/home/wake/project",
			},
			{ // not_cc codex row
				Address: "alias/sess2",
				Agent: &peers.AgentInfo{
					Type:   "codex",
					Status: "idle",
				},
				Deliverable: false,
				Reason:      "not_cc",
				Cwd:         "/home/wake/codex",
			},
			{ // shell row — no agent at all, reason set, NOT counted toward partial
				Address:     "alias/sess3",
				Agent:       nil,
				Deliverable: false,
				Reason:      "no_agent",
				Cwd:         "/home/wake/shell",
			},
			{ // unresolved row — no agent, no reason, counted toward partial (N=1)
				Address:     "alias/sess4",
				Agent:       nil,
				Deliverable: false,
				Reason:      "",
				Cwd:         "",
			},
		},
	}
}

const wantPeersTable = "ADDRESS      AGENT  NAME     STATUS   DELIVERABLE  CWD\n" +
	"alias/sess1  cc     wake-cc  working  yes          /home/wake/project\n" +
	"alias/sess2  codex  -        idle     not_cc       /home/wake/codex\n" +
	"alias/sess3  -      -        -        no_agent     /home/wake/shell\n" +
	"alias/sess4  -      -        -        -            \n" +
	"(partial: 1 sessions not resolved within budget)\n"

func TestFormatPeersTable(t *testing.T) {
	got := formatPeersTable(peersTableFixture())
	if got != wantPeersTable {
		t.Errorf("formatPeersTable mismatch\ngot:\n%s\nwant:\n%s", got, wantPeersTable)
	}
}

func TestFormatPeersTable_NoPartialLine(t *testing.T) {
	resp := peers.Envelope{
		OK: true,
		Peers: []peers.PeerRecord{
			{Address: "alias/sess1", Deliverable: false, Reason: "no_agent"},
		},
	}
	got := formatPeersTable(resp)
	if strings.Contains(got, "partial:") {
		t.Errorf("formatPeersTable printed a partial line when nothing was unresolved:\n%s", got)
	}
}

// --- sanitizeCell -----------------------------------------------------

func TestSanitizeCell(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ASCII passes through", "wake-cc", "wake-cc"},
		{"non-ASCII printable passes through", "wake-中文", "wake-中文"},
		{"ESC sequence escaped", "x\x1b[31my", `x\x1b[31my`},
		{"tab and newline escaped", "a\tb\nc", `a\tb\nc`},
		{"DEL escaped", "a\x7fb", `a\x7fb`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeCell(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeCell(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x7f) {
				t.Errorf("sanitizeCell(%q) = %q, want no raw control bytes", tc.in, got)
			}
		})
	}
}

func TestFormatPeersTable_EscapesControlCharacters(t *testing.T) {
	resp := peers.Envelope{
		OK: true,
		Peers: []peers.PeerRecord{
			{
				Address: "alias/sess1",
				Agent: &peers.AgentInfo{
					Type:     "cc",
					PeerName: "x\x1b[31my",
					Status:   "working",
				},
				Deliverable: true,
				Cwd:         "/home/wake/project",
			},
		},
	}
	got := formatPeersTable(resp)
	if !strings.Contains(got, `x\x1b[31my`) {
		t.Errorf("formatPeersTable = %q, want literal escaped %q", got, `x\x1b[31my`)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("formatPeersTable = %q, want no raw ESC byte", got)
	}
}

func TestFormatPeersAllTable_EscapesUnreachableError(t *testing.T) {
	resp := peers.AllEnvelope{
		Hosts: []peers.HostResult{
			{
				Alias: "down",
				OK:    false,
				Error: "\x1b]52;c;YXR0YWNrCg==\x07",
				Peers: []peers.PeerRecord{},
			},
		},
	}
	got := formatPeersAllTable(resp)
	if !strings.Contains(got, `(unreachable: \x1b]52;c;YXR0YWNrCg==\a)`) {
		t.Errorf("formatPeersAllTable = %q, want escaped unreachable line", got)
	}
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Errorf("formatPeersAllTable = %q, want no raw ESC/BEL bytes", got)
	}
}

// --- runPeersCmd against an httptest server -------------------------------

// writeTestConfig writes a config.toml pointing bind/port at addr (host:port
// from an httptest.Server) with the given token, returning the file path.
func writeTestConfig(t *testing.T, addr, token string) string {
	t.Helper()
	host, portStr, err := splitHostPort(addr)
	if err != nil {
		t.Fatalf("split host/port %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}

	cfg := config.Config{
		Bind:  host,
		Port:  port,
		Token: token,
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("config.WriteFile: %v", err)
	}
	return path
}

func splitHostPort(rawURLOrAddr string) (string, string, error) {
	if u, err := url.Parse(rawURLOrAddr); err == nil && u.Host != "" {
		return net.SplitHostPort(u.Host)
	}
	return net.SplitHostPort(rawURLOrAddr)
}

const testPeersBody = `{"host_id":"mini:abc123","ok":true,"error":"","partial":false,"peers":[]}`
const testPeersNotOKBody = `{"host_id":"mini:abc123","ok":false,"error":"boom","partial":false,"peers":[]}`

func TestRunPeersCmd_TableSuccess(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotPath != "/api/peers" {
		t.Errorf("request path = %q, want /api/peers", gotPath)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer sekret")
	}
	if !strings.Contains(stdout.String(), "ADDRESS") {
		t.Errorf("stdout missing table header: %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunPeersCmd_JSONPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath, "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != testPeersBody {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), testPeersBody)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

// controlCharPeerName is a peer_name carrying an ESC-CSI sequence — a
// stand-in for a malicious peer trying to inject an escape sequence into
// the operator's terminal via the CLI.
var controlCharPeerName = "x" + string(rune(0x1b)) + "[31my"

// peersControlCharBody JSON-marshals a peers.Envelope containing
// controlCharPeerName, so the wire body carries it in the properly
// \u-escaped JSON form (as encoding/json always produces for control
// characters), never as a literal control byte.
func peersControlCharBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(peers.Envelope{
		HostID: "mini:abc123",
		OK:     true,
		Peers: []peers.PeerRecord{
			{
				Address: "alias/sess1",
				Agent: &peers.AgentInfo{
					Type:     "cc",
					PeerName: controlCharPeerName,
					Status:   "working",
				},
				Deliverable: true,
				Cwd:         "/home/wake/project",
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return body
}

// TestRunPeersCmd_JSONPassthrough_RawControlCharacters pins --json as an
// exact, unsanitized passthrough of the daemon's response bytes: valid
// JSON necessarily carries controlCharPeerName's ESC byte pre-escaped as
// "\u001b" (encoding/json's own control-character escaping — a raw ESC
// byte cannot legally appear unescaped in a JSON string, and our own
// client-side json.Unmarshal would reject it if it did), so this checks
// that escaped form survives byte-for-byte rather than being run through
// sanitizeCell's "\x1b" table-rendering form.
func TestRunPeersCmd_JSONPassthrough_RawControlCharacters(t *testing.T) {
	body := peersControlCharBody(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath, "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != string(body) {
		t.Errorf("stdout = %q, want verbatim body %q (--json must not sanitize)", stdout.String(), string(body))
	}
	// The daemon's own JSON escaping ("\u001b") must survive untouched.
	jsonEscapedESC := fmt.Sprintf(`\u%04x`, 0x1b)
	if !strings.Contains(stdout.String(), jsonEscapedESC) {
		t.Errorf("stdout = %q, want the daemon's own %q escape present unmodified", stdout.String(), jsonEscapedESC)
	}
	// sanitizeCell's own escaping form must NOT have been applied to --json.
	if strings.Contains(stdout.String(), sanitizeCell(controlCharPeerName)) {
		t.Errorf("stdout = %q, want no sanitizeCell-style escaping in --json output", stdout.String())
	}
}

func TestRunPeersCmd_TableRendering_EscapesControlCharacters(t *testing.T) {
	body := peersControlCharBody(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `x\x1b[31my`) {
		t.Errorf("stdout = %q, want literal escaped %q", stdout.String(), `x\x1b[31my`)
	}
	if strings.ContainsRune(stdout.String(), 0x1b) {
		t.Errorf("stdout = %q, want no raw ESC byte in table output", stdout.String())
	}
}

func TestRunPeersCmd_TableOKFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersNotOKBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "pdx peers:") || !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr = %q, want it to mention pdx peers: and boom", stderr.String())
	}
}

func TestRunPeersCmd_JSONOKFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersNotOKBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath, "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.String() != testPeersNotOKBody {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), testPeersNotOKBody)
	}
}

func TestRunPeersCmd_Unauthorized(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "wrong-token")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if gotAuth != "Bearer wrong-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer wrong-token")
	}
	if !strings.Contains(stderr.String(), "pdx peers:") {
		t.Errorf("stderr = %q, want it to start with pdx peers:", stderr.String())
	}
}

func TestRunPeersCmd_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "invalid response") {
		t.Errorf("stderr = %q, want it to mention invalid response", stderr.String())
	}
}

func TestRunPeersCmd_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--bogus"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "pdx peers: unknown flag") || !strings.Contains(stderr.String(), "--bogus") {
		t.Errorf("stderr = %q, want it to mention unknown flag --bogus", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunPeersCmd_JSONAndConfigStillWork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--json", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != testPeersBody {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), testPeersBody)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

// TestRunPeersCmd_LargeErrorBody_BoundedStderr pins Item 6: a non-200
// response with a huge body must not be read in full before being printed —
// only up to 4 KiB of it is used for the error detail, so stderr stays
// small regardless of how much the server sent.
func TestRunPeersCmd_LargeErrorBody_BoundedStderr(t *testing.T) {
	bigBody := strings.Repeat("e", 1024*1024) // 1 MiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(bigBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stderr.Len() >= 5*1024 {
		t.Errorf("stderr length = %d, want under 5 KiB", stderr.Len())
	}
	if !strings.Contains(stderr.String(), "pdx peers:") || !strings.Contains(stderr.String(), "HTTP 500") {
		t.Errorf("stderr = %q, want it to mention pdx peers: and HTTP 500", stderr.String())
	}
}

// TestRunPeersCmd_OversizedOKBody_RejectedBounded pins Item 6: a 200 response
// whose body exceeds 16 MiB must not be read into memory in full — the CLI
// caps the read and reports the body as too large rather than hanging onto
// (or trying to json.Unmarshal) an unbounded payload.
func TestRunPeersCmd_OversizedOKBody_RejectedBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := bytes.Repeat([]byte("x"), 1024*1024) // 1 MiB per write
		for i := 0; i < 17; i++ {                     // 17 MiB total, streamed
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "pdx peers: response too large") {
		t.Errorf("stderr = %q, want it to mention response too large", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunPeersCmd_UnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close() // closed before any request can land — connection refused

	cfgPath := writeTestConfig(t, addr, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "pdx peers:") {
		t.Errorf("stderr = %q, want it to start with pdx peers:", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

// --- formatPeersAllTable golden fixture -----------------------------------

func peersAllTableFixture() peers.AllEnvelope {
	return peers.AllEnvelope{
		Hosts: []peers.HostResult{
			{
				Alias:  "local",
				HostID: "mini:abc123",
				OK:     true,
				Peers: []peers.PeerRecord{
					{
						Address:     "local/sess1",
						Agent:       &peers.AgentInfo{Type: "cc", PeerName: "wake-cc", Status: "working"},
						Deliverable: true,
						Cwd:         "/home/wake/project",
					},
				},
			},
			{
				Alias:  "air",
				HostID: "air:def456",
				OK:     true,
				Peers: []peers.PeerRecord{
					{
						Address:     "air/sess2",
						Agent:       &peers.AgentInfo{Type: "codex", Status: "idle"},
						Deliverable: false,
						Reason:      "not_cc",
						Cwd:         "/home/wake/codex",
					},
				},
			},
			{
				Alias: "down",
				OK:    false,
				Error: "connection refused",
				Peers: []peers.PeerRecord{},
			},
		},
	}
}

const wantPeersAllTable = "HOST   ADDRESS      AGENT  NAME     STATUS   DELIVERABLE  CWD\n" +
	"local  local/sess1  cc     wake-cc  working  yes          /home/wake/project\n" +
	"air    air/sess2    codex  -        idle     not_cc       /home/wake/codex\n" +
	"down  (unreachable: connection refused)\n"

func TestFormatPeersAllTable(t *testing.T) {
	got := formatPeersAllTable(peersAllTableFixture())
	if got != wantPeersAllTable {
		t.Errorf("formatPeersAllTable mismatch\ngot:\n%s\nwant:\n%s", got, wantPeersAllTable)
	}
}

// --- runPeersCmd --all against an httptest server -------------------------

const testPeersAllBody = `{"hosts":[` +
	`{"alias":"local","host_id":"mini:abc","ok":true,"partial":false,"peers":[` +
	`{"address":"local/sess1","agent":{"type":"cc","peer_name":"wake-cc","status":"working"},"deliverable":true,"cwd":"/home/wake/p1"}` +
	`]},` +
	`{"alias":"air","host_id":"air:def","ok":true,"partial":false,"peers":[` +
	`{"address":"air/sess2","agent":{"type":"codex","status":"idle"},"deliverable":false,"reason":"not_cc","cwd":"/home/wake/p2"}` +
	`]},` +
	`{"alias":"down","host_id":"","ok":false,"error":"connection refused","partial":false,"peers":[]}` +
	`]}`

func TestRunPeersCmd_AllTableSuccess(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersAllBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath, "--all"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotPath != "/api/peers" {
		t.Errorf("request path = %q, want /api/peers", gotPath)
	}
	if gotQuery != "scope=all" {
		t.Errorf("request query = %q, want scope=all", gotQuery)
	}
	if !strings.Contains(stdout.String(), "HOST") {
		t.Errorf("stdout missing HOST column header: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "down  (unreachable: connection refused)") {
		t.Errorf("stdout missing unreachable-host line: %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunPeersCmd_AllJSONPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersAllBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--all", "--json", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stdout.String() != testPeersAllBody {
		t.Errorf("stdout = %q, want verbatim body %q", stdout.String(), testPeersAllBody)
	}
}

func TestRunPeersCmd_AllLocalNotOK(t *testing.T) {
	body := `{"hosts":[{"alias":"local","host_id":"","ok":false,"error":"tmux unreachable","partial":false,"peers":[]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath, "--all"}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "pdx peers:") || !strings.Contains(stderr.String(), "tmux unreachable") {
		t.Errorf("stderr = %q, want it to mention pdx peers: and tmux unreachable", stderr.String())
	}
}

func TestRunPeersCmd_AllLocalOK_ExitZeroDespiteRemoteFailure(t *testing.T) {
	// Pins the brief's "Exit 0 when the local row is ok (remote failures
	// are rows, not errors)" rule using testPeersAllBody's "down" host.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testPeersAllBody))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config", cfgPath, "--all"}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (a remote host failure must not fail the command)", code)
	}
}

// --- formatHostsTable golden fixture ---------------------------------------

func hostsTableFixture() []cliHostRow {
	return []cliHostRow{
		{Alias: "air", URL: "https://air.mlab.host", HostID: "air:def456", Verified: true, HasToken: true, HasInboundToken: true, AllowBypass: false},
		{Alias: "phone", URL: "https://phone.mlab.host", HostID: "", Verified: false, HasToken: false, HasInboundToken: true, AllowBypass: true},
	}
}

const wantHostsTable = "ALIAS  URL                      HOST_ID     VERIFIED  TOKEN  INBOUND  ALLOW_BYPASS\n" +
	"air    https://air.mlab.host    air:def456  yes       yes    yes      no\n" +
	"phone  https://phone.mlab.host              no        no     yes      yes\n"

func TestFormatHostsTable(t *testing.T) {
	got := formatHostsTable(hostsTableFixture())
	if got != wantHostsTable {
		t.Errorf("formatHostsTable mismatch\ngot:\n%s\nwant:\n%s", got, wantHostsTable)
	}
}

// --- runPeersCmd host list/add/set-token/remove against an httptest server -

func TestRunPeersCmd_HostList(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	body := `{"hosts":[{"alias":"air","url":"https://air.mlab.host","host_id":"air:def456","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "list", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/peers/hosts" {
		t.Errorf("path = %q, want /api/peers/hosts", gotPath)
	}
	if gotAuth != "Bearer admin-tok" {
		t.Errorf("Authorization = %q, want Bearer admin-tok", gotAuth)
	}
	if !strings.Contains(stdout.String(), "ALIAS") || !strings.Contains(stdout.String(), "air") {
		t.Errorf("stdout = %q, want a table containing ALIAS and air", stdout.String())
	}
}

func TestRunPeersCmd_HostAdd(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody cliAddHostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(cliAddHostResponse{
			Alias: "air", URL: "https://air.mlab.host", HostID: "air:def456",
			InboundToken: "pdxp_inbound123", Verified: true,
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "add", "air", "https://air.mlab.host", "--token", "pdxp_out123", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/peers/hosts" {
		t.Errorf("path = %q, want /api/peers/hosts", gotPath)
	}
	wantBody := cliAddHostRequest{Alias: "air", URL: "https://air.mlab.host", Token: "pdxp_out123"}
	if gotBody != wantBody {
		t.Errorf("request body = %+v, want %+v", gotBody, wantBody)
	}
	wantStdout := "added air (https://air.mlab.host)  verified: yes\n" +
		"inbound token for air to use when adding this host:\n" +
		"  pdxp_inbound123\n"
	if stdout.String() != wantStdout {
		t.Errorf("stdout = %q, want %q", stdout.String(), wantStdout)
	}
}

func TestRunPeersCmd_HostAdd_FlagBeforePositionals(t *testing.T) {
	// Pins "flags may appear anywhere after peers": --token here precedes
	// the alias/url positionals it applies to.
	var gotBody cliAddHostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(cliAddHostResponse{Alias: "air", URL: "https://air.mlab.host", Verified: false})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "add", "--token", "pdxp_out123", "air", "https://air.mlab.host", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	wantBody := cliAddHostRequest{Alias: "air", URL: "https://air.mlab.host", Token: "pdxp_out123"}
	if gotBody != wantBody {
		t.Errorf("request body = %+v, want %+v", gotBody, wantBody)
	}
}

func TestRunPeersCmd_HostAdd_ErrorPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "alias already exists"})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "add", "air", "https://air.mlab.host", "--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "alias already exists") {
		t.Errorf("stderr = %q, want it to mention alias already exists", stderr.String())
	}
}

func TestRunPeersCmd_HostSetToken_DottedAlias(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody cliPutHostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliHostRow{
			Alias: "air.2026", URL: "https://air.mlab.host", HostID: "air:def456",
			Verified: true, HasToken: true, HasInboundToken: true, AllowBypass: true,
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "set-token", "air.2026", "pdxp_new123", "--allow-bypass=true", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/api/peers/hosts/air.2026" {
		t.Errorf("path = %q, want /api/peers/hosts/air.2026", gotPath)
	}
	wantBypass := true
	if gotBody.Token != "pdxp_new123" || gotBody.AllowBypass == nil || *gotBody.AllowBypass != wantBypass {
		t.Errorf("request body = %+v, want token=pdxp_new123 allow_bypass=true", gotBody)
	}
	if !strings.Contains(stdout.String(), "air.2026") {
		t.Errorf("stdout = %q, want it to mention air.2026", stdout.String())
	}
}

func TestRunPeersCmd_HostSetToken_NoAllowBypass(t *testing.T) {
	var gotBody map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliHostRow{Alias: "air", Verified: true, HasToken: true})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "set-token", "air", "pdxp_new123", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if _, present := gotBody["allow_bypass"]; present {
		t.Errorf("request body included allow_bypass when it was not passed: %v", gotBody)
	}
}

func TestRunPeersCmd_HostRemove(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "remove", "air.2026", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/peers/hosts/air.2026" {
		t.Errorf("path = %q, want /api/peers/hosts/air.2026", gotPath)
	}
	if !strings.Contains(stdout.String(), "air.2026") {
		t.Errorf("stdout = %q, want it to mention air.2026", stdout.String())
	}
}

func TestRunPeersCmd_HostRemove_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown alias"})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "remove", "ghost", "--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown alias") {
		t.Errorf("stderr = %q, want it to mention unknown alias", stderr.String())
	}
}

// TestRunPeersCmd_HostSetToken_EscapesAlias pins Item 5: an alias
// containing characters with special meaning in a URL path (here "?",
// which would otherwise start a query string) must be percent-escaped
// when building the PUT target, not concatenated raw.
func TestRunPeersCmd_HostSetToken_EscapesAlias(t *testing.T) {
	var gotEscapedPath, gotDecodedPath, gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		gotDecodedPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliHostRow{Alias: "a?x", Verified: true, HasToken: true})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "set-token", "a?x", "pdxp_new123", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotEscapedPath != "/api/peers/hosts/a%3Fx" {
		t.Errorf("escaped path on the wire = %q, want /api/peers/hosts/a%%3Fx", gotEscapedPath)
	}
	if gotRawQuery != "" {
		t.Errorf("raw query = %q, want empty (the ? must be escaped, not start a query string)", gotRawQuery)
	}
	if gotDecodedPath != "/api/peers/hosts/a?x" {
		t.Errorf("decoded path = %q, want /api/peers/hosts/a?x (the alias, round-tripped)", gotDecodedPath)
	}
}

// TestRunPeersCmd_HostRemove_EscapesAlias is TestRunPeersCmd_HostSetToken_EscapesAlias
// for DELETE, and also pins that an ordinary dotted alias still round-trips
// unescaped (PathEscape leaves "." untouched).
func TestRunPeersCmd_HostRemove_EscapesAlias(t *testing.T) {
	var gotEscapedPath, gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "remove", "a?x", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotEscapedPath != "/api/peers/hosts/a%3Fx" {
		t.Errorf("escaped path on the wire = %q, want /api/peers/hosts/a%%3Fx", gotEscapedPath)
	}
	if gotRawQuery != "" {
		t.Errorf("raw query = %q, want empty (the ? must be escaped, not start a query string)", gotRawQuery)
	}

	// air.2026 should round-trip unescaped (PathEscape leaves "." alone).
	code = runPeersCmd([]string{"host", "remove", "air.2026", "--config", cfgPath}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotEscapedPath != "/api/peers/hosts/air.2026" {
		t.Errorf("escaped path = %q, want /api/peers/hosts/air.2026", gotEscapedPath)
	}
}

// --- grammar rejections: exit 2, zero requests, before any config load ----

func TestRunPeersCmd_GrammarRejections(t *testing.T) {
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
		{"host missing verb", []string{"host"}},
		{"host unknown verb", []string{"host", "bogus"}},
		{"host add missing url", []string{"host", "add", "air"}},
		{"host add extra positional", []string{"host", "add", "air", "https://x", "extra"}},
		{"host list extra positional", []string{"host", "list", "extra"}},
		{"host remove missing alias", []string{"host", "remove"}},
		{"host remove extra positional", []string{"host", "remove", "air", "extra"}},
		{"--all with host", []string{"--all", "host", "list"}},
		{"--json with host", []string{"host", "list", "--json"}},
		{"--token with set-token", []string{"host", "set-token", "air", "tok", "--token", "x"}},
		{"--allow-bypass with add", []string{"host", "add", "air", "https://x", "--allow-bypass=true"}},
		{"--token with remove", []string{"host", "remove", "air", "--token", "x"}},
		{"--token with list", []string{"host", "list", "--token", "x"}},
		{"--allow-bypass with remove", []string{"host", "remove", "air", "--allow-bypass=true"}},
		{"--token at top level", []string{"--token", "x"}},
		{"--allow-bypass at top level", []string{"--allow-bypass=true"}},
		{"unexpected top-level positional", []string{"foo"}},
		{"host add alias with slash", []string{"host", "add", "a/b", "https://x"}},
		{"host set-token alias with slash", []string{"host", "set-token", "a/b", "tok"}},
		{"host remove alias with slash", []string{"host", "remove", "a/b"}},
		{"invalid allow-bypass value", []string{"host", "set-token", "air", "tok", "--allow-bypass=maybe"}},
		{"unknown flag at top level", []string{"--bogus"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := atomic.LoadInt64(&reqCount)
			args := append(append([]string{}, tc.args...), "--config", cfgPath)
			var stdout, stderr bytes.Buffer
			code := runPeersCmd(args, &stdout, &stderr)

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

// TestRunPeersCmd_ConfigFlagMissingValue is separate from the table above:
// appending the harness's own "--config <path>" after a bare trailing
// "--config" would just supply the missing value, defeating the case. Bare
// "--config" is instead the command's only argument.
func TestRunPeersCmd_ConfigFlagMissingValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"--config"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	if stderr.String() == "" {
		t.Errorf("stderr is empty, want a usage/error message")
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}
