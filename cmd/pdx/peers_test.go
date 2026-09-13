package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
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
	code := runPeersCmd([]string{"--all"}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "pdx peers: unknown flag") || !strings.Contains(stderr.String(), "--all") {
		t.Errorf("stderr = %q, want it to mention unknown flag --all", stderr.String())
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
		for i := 0; i < 17; i++ {                      // 17 MiB total, streamed
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
