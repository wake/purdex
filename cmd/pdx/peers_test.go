package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
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
			{ // deliverable cc row — the only one with a ref, so the only bracket
				Address: "alias/sess1",
				RowKind: "session",
				Ref:     "_q34psn",
				Agent: &peers.AgentInfo{
					Type:     "cc",
					PeerName: "wake-cc",
					Status:   "working",
				},
				Deliverable: true,
				TmuxName:    "aigora2",
				Cwd:         "/home/wake/project",
			},
			{ // not_cc codex row — no ref (Ref is set only for a cc agent)
				Address: "alias/sess2",
				RowKind: "session",
				Agent: &peers.AgentInfo{
					Type:   "codex",
					Status: "idle",
				},
				Deliverable: false,
				Reason:      "not_cc",
				TmuxName:    "codex1",
				Cwd:         "/home/wake/codex",
			},
			{ // shell row — no agent at all, reason set, NOT counted toward partial
				Address:     "alias/sess3",
				RowKind:     "session",
				Agent:       nil,
				Deliverable: false,
				Reason:      "no_agent",
				TmuxName:    "shell1",
				Cwd:         "/home/wake/shell",
			},
			{ // unresolved row — no agent, no reason, counted toward partial (N=1)
				Address:     "alias/sess4",
				RowKind:     "session",
				Agent:       nil,
				Deliverable: false,
				Reason:      "",
				Cwd:         "",
			},
		},
	}
}

// wantPeersTable is the v4 column order: TITLE first, ADDRESS second (spec
// §5.7 -- scan the title to find who you want, copy the address to reach
// them), NAME gone because it IS the address's second segment, and TMUX added
// between DELIVERABLE and CWD. Every row of the fixture is untitled, so every
// TITLE cell is blank rather than "-": a dash reads as a value, and there is
// nothing here to name. Only the cc row has a ref, so only it is bracketed;
// the row with no tmux at all shows "-".
const wantPeersTable = "TITLE  ADDRESS               AGENT  STATUS   DELIVERABLE  TMUX     CWD\n" +
	"       alias/sess1 [q34psn]  cc     working  yes          aigora2  /home/wake/project\n" +
	"       alias/sess2           codex  idle     not_cc       codex1   /home/wake/codex\n" +
	"       alias/sess3           -      -        no_agent     shell1   /home/wake/shell\n" +
	"       alias/sess4           -      -        -            -        \n" +
	"(partial: 1 sessions not resolved within budget)\n" +
	"daemon (unknown)\n"

func TestFormatPeersTable(t *testing.T) {
	got := formatPeersTable(peersTableFixture())
	if got != wantPeersTable {
		t.Errorf("formatPeersTable mismatch\ngot:\n%s\nwant:\n%s", got, wantPeersTable)
	}
}

// TestFormatPeersTable_TitleFirstBlankWhenUnsetAndEntryIndent pins the
// rendering rules that survived into v4 (spec §5.7): the title is the FIRST
// column and ADDRESS the second, an unset title renders as a blank cell
// rather than "-", no row carries a "*" marker (the default label it marked
// no longer exists), and entry rows keep their two-space ADDRESS indent and
// the "daemon <version>" trailer.
func TestFormatPeersTable_TitleFirstBlankWhenUnsetAndEntryIndent(t *testing.T) {
	env := peers.Envelope{OK: true, DaemonVersion: "1.0.0-alpha.363", Peers: []peers.PeerRecord{
		{Address: "a/x", RowKind: "session", Ref: "_3k9f2m", Title: "purdex-dev", TitleSource: "user", Agent: &peers.AgentInfo{Type: "cc", PeerName: "x", Status: "idle"}, Deliverable: true, Cwd: "/w"},
		{Address: "a/y", RowKind: "entry", Ref: "_9x2pq0", Agent: &peers.AgentInfo{Type: "cc", PeerName: "y", Status: "busy"}, Deliverable: true, Cwd: "/w"},
		{Address: "a/tmux:shell", RowKind: "session", Reason: "no_agent"},
	}}
	got := formatPeersTable(env)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if cols := headerColumns(lines[0]); cols[0] != "TITLE" || cols[1] != "ADDRESS" {
		t.Errorf("first two columns = %v, want TITLE then ADDRESS", cols[:2])
	}
	if !strings.HasPrefix(lines[1], "purdex-dev ") || !strings.Contains(lines[1], "a/x") {
		t.Errorf("titled session row = %q, want the title first then the address", lines[1])
	}
	if !strings.HasPrefix(lines[2], " ") {
		t.Errorf("untitled entry row = %q, want a BLANK title cell, not a dash", lines[2])
	}
	if !strings.Contains(lines[2], "  a/y") {
		t.Errorf("entry row = %q, want the address indented by two spaces", lines[2])
	}
	if !strings.HasPrefix(lines[3], " ") || !strings.Contains(lines[3], "a/tmux:shell") {
		t.Errorf("agentless row = %q, want a blank title cell", lines[3])
	}
	if strings.Contains(got, "*") {
		t.Errorf("table still carries a * marker -- the default label it marked is gone:\n%s", got)
	}
	if !strings.Contains(got, "daemon 1.0.0-alpha.363") {
		t.Errorf("version trailer missing:\n%s", got)
	}
}

// headerColumns splits a tabwriter header line on runs of two or more
// spaces, so a test can assert the column ORDER without pinning the
// widths the fixture's own cell lengths happen to produce.
func headerColumns(header string) []string {
	return regexp.MustCompile(` {2,}`).Split(strings.TrimSpace(header), -1)
}

// TestFormatPeersTable_SharedLabelRendersBothRows pins spec 7.1's
// deliberate first two rows: two conversations may hold the SAME title
// (D5), because a title is a display name and never a key. Both rows must
// render, each carrying that title verbatim and its own distinct address --
// the address is what tells them apart, and nothing in the table may
// suggest one of them "won" the name.
func TestFormatPeersTable_SharedLabelRendersBothRows(t *testing.T) {
	env := peers.Envelope{OK: true, DaemonVersion: "1.0.0-alpha.363", Peers: []peers.PeerRecord{
		{Address: "mini-lab/purdex-b0", RowKind: "session", Ref: "_3k9f2m", Title: "purdex-tester", TitleSource: "user", Agent: &peers.AgentInfo{Type: "cc", PeerName: "purdex-b0", Status: "busy"}, Deliverable: true, Cwd: "~/Workspace/wake/purdex"},
		{Address: "mini-lab/purdex-69", RowKind: "session", Ref: "_9x2pq0", Title: "purdex-tester", TitleSource: "user", Agent: &peers.AgentInfo{Type: "cc", PeerName: "purdex-69", Status: "idle"}, Deliverable: true, Cwd: "~"},
	}}
	lines := strings.Split(strings.TrimRight(formatPeersTable(env), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("table too short: %q", lines)
	}
	for i, wantAddr := range []string{"mini-lab/purdex-b0", "mini-lab/purdex-69"} {
		row := lines[i+1]
		if !strings.HasPrefix(row, "purdex-tester ") {
			t.Errorf("row %d = %q, want the shared title rendered verbatim and first", i, row)
		}
		if !strings.Contains(row, wantAddr) {
			t.Errorf("row %d = %q, want its own address %q", i, row, wantAddr)
		}
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

// TestFormatPeersTable_UnknownRegistryFilesLine pins the F2 partial-cause
// line for an alive-but-undecodable registry file: it is named, one line,
// every path sanitized, and appears even though nothing was unresolved
// within budget (no "sessions not resolved" line at all here).
func TestFormatPeersTable_UnknownRegistryFilesLine(t *testing.T) {
	resp := peers.Envelope{
		OK:                   true,
		Partial:              true,
		UnknownRegistryFiles: []string{"/reg/1.json", "/reg/2\x1b[31m.json"},
		Peers: []peers.PeerRecord{
			{Address: "alias/sess1", Deliverable: false, Reason: "no_agent"},
		},
	}
	got := formatPeersTable(resp)
	if !strings.Contains(got, `(partial: unknown registry files: /reg/1.json, /reg/2\x1b[31m.json)`) {
		t.Errorf("formatPeersTable = %q, want the unknown-registry-files line", got)
	}
	if strings.Contains(got, "sessions not resolved") {
		t.Errorf("formatPeersTable = %q, want no unresolved-session line", got)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("formatPeersTable = %q, want no raw ESC byte", got)
	}
}

// TestFormatPeersTable_LabelStoreUnavailableLine pins the partial-cause
// line for a title store read failure (spec §3.3): it is rendered from the
// envelope's explicit titles_unavailable flag (X4), never inferred from
// the absence of the other causes.
func TestFormatPeersTable_LabelStoreUnavailableLine(t *testing.T) {
	resp := peers.Envelope{
		OK:                true,
		Partial:           true,
		TitlesUnavailable: true,
		Peers: []peers.PeerRecord{
			{Address: "alias/sess1", Deliverable: true, Agent: &peers.AgentInfo{Type: "cc"}},
		},
	}
	got := formatPeersTable(resp)
	if !strings.Contains(got, "(partial: title store unavailable)\n") {
		t.Errorf("formatPeersTable = %q, want the title-store-unavailable line", got)
	}
	if strings.Contains(got, "sessions not resolved") || strings.Contains(got, "unknown registry files") {
		t.Errorf("formatPeersTable = %q, want only the title-store line", got)
	}

	// Without the flag, nothing infers it — even though the envelope is
	// Partial with no other visible cause.
	resp.TitlesUnavailable = false
	if got := formatPeersTable(resp); strings.Contains(got, "title store") {
		t.Errorf("formatPeersTable = %q, want no title-store line without titles_unavailable", got)
	}
}

// TestFormatPeersTable_AllPartialCauseLines pins that the three partial
// causes are independent lines, each printed whenever its own signal is
// set, in the order count / unknown files / title store — none is
// suppressed by another (X3).
func TestFormatPeersTable_AllPartialCauseLines(t *testing.T) {
	resp := peers.Envelope{
		OK:                   true,
		Partial:              true,
		DaemonVersion:        "1.0.0",
		UnknownRegistryFiles: []string{"/reg/1.json", "/reg/2.json"},
		TitlesUnavailable:    true,
		Peers: []peers.PeerRecord{
			{Address: "alias/sess1", Deliverable: false, Reason: "no_agent"},
			{Address: "alias/sess2"}, // unresolved
			{Address: "alias/sess3"}, // unresolved
		},
	}
	got := formatPeersTable(resp)
	want := "(partial: 2 sessions not resolved within budget)\n" +
		"(partial: unknown registry files: /reg/1.json, /reg/2.json)\n" +
		"(partial: title store unavailable)\n" +
		"daemon 1.0.0\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("formatPeersTable = %q, want it to end with %q", got, want)
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

// TestFormatPeersTable_EscapesControlCharacters pins that every cell fed by
// data someone else controls goes through sanitizeCell. In v4 that is the
// TMUX cell (a tmux session name, or a registry file's frozen copy of one)
// and the TITLE cell (a self-declared title) -- the NAME column that used to
// carry this coverage is gone.
func TestFormatPeersTable_EscapesControlCharacters(t *testing.T) {
	resp := peers.Envelope{
		OK: true,
		Peers: []peers.PeerRecord{
			{
				Address:  "alias/sess1",
				RowKind:  "session",
				Title:    "t\x1b[32mz",
				TmuxName: "x\x1b[31my",
				Agent: &peers.AgentInfo{
					Type:     "cc",
					PeerName: "wake-cc",
					Status:   "working",
				},
				Deliverable: true,
				Cwd:         "/home/wake/project",
			},
		},
	}
	got := formatPeersTable(resp)
	for _, want := range []string{`x\x1b[31my`, `t\x1b[32mz`} {
		if !strings.Contains(got, want) {
			t.Errorf("formatPeersTable = %q, want literal escaped %q", got, want)
		}
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

// fakePeersDaemon starts an httptest.Server running handler and returns it
// alongside a config file whose bind/port/token point at it (writeTestConfig,
// token "admin-tok") — the same wiring TestRunPeersCmd_HostRename and its
// siblings each build inline, factored out for the rotate tests below.
func fakePeersDaemon(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	return srv, cfgPath
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
//
// It lands in tmux_name as well as peer_name: v4 dropped the NAME column, so
// peer_name alone would no longer reach the rendered table at all, and the
// table test below would pass while rendering nothing.
func peersControlCharBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(peers.Envelope{
		HostID: "mini:abc123",
		OK:     true,
		Peers: []peers.PeerRecord{
			{
				Address: "alias/sess1",
				RowKind: "session",
				Agent: &peers.AgentInfo{
					Type:     "cc",
					PeerName: controlCharPeerName,
					Status:   "working",
				},
				Deliverable: true,
				TmuxName:    controlCharPeerName,
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
						RowKind:     "session",
						Ref:         "_q34psn",
						Agent:       &peers.AgentInfo{Type: "cc", PeerName: "wake-cc", Status: "working"},
						Deliverable: true,
						TmuxName:    "aigora2",
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
						RowKind:     "session",
						Agent:       &peers.AgentInfo{Type: "codex", Status: "idle"},
						Deliverable: false,
						Reason:      "not_cc",
						TmuxName:    "codex1",
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

// wantPeersAllTable keeps HOST first -- which host a row lives on is what
// you need before either of the other two columns means anything -- and
// then follows the single-host order exactly: TITLE, then ADDRESS, and TMUX
// between DELIVERABLE and CWD (v4 spec §5.7).
const wantPeersAllTable = "HOST   TITLE  ADDRESS               AGENT  STATUS   DELIVERABLE  TMUX     CWD\n" +
	"local         local/sess1 [q34psn]  cc     working  yes          aigora2  /home/wake/project\n" +
	"air           air/sess2             codex  idle     not_cc       codex1   /home/wake/codex\n" +
	"down  (unreachable: connection refused)\n" +
	"local  daemon (unknown)\n" +
	"air  daemon (unknown)\n"

func TestFormatPeersAllTable(t *testing.T) {
	got := formatPeersAllTable(peersAllTableFixture())
	if got != wantPeersAllTable {
		t.Errorf("formatPeersAllTable mismatch\ngot:\n%s\nwant:\n%s", got, wantPeersAllTable)
	}
}

// TestFormatPeersAllTable_HostThenTitleThenAddress extends
// TestFormatPeersTable_TitleFirstBlankWhenUnsetAndEntryIndent's rules to
// the --all table: HOST stays the first column, TITLE comes second and
// ADDRESS third, entry rows keep their indent, and one "<alias>  daemon
// <version>" trailer line is printed per OK host (the existing
// "(unreachable: ...)" lines for failed hosts stay).
func TestFormatPeersAllTable_HostThenTitleThenAddress(t *testing.T) {
	resp := peers.AllEnvelope{Hosts: []peers.HostResult{
		{
			Alias: "local", OK: true, DaemonVersion: "1.0.0-alpha.342",
			Peers: []peers.PeerRecord{
				{Address: "local/x", RowKind: "session", Ref: "_3k9f2m", Title: "purdex-dev", TitleSource: "user", Agent: &peers.AgentInfo{Type: "cc", PeerName: "x", Status: "idle"}, Deliverable: true, Cwd: "/w"},
				{Address: "local/y", RowKind: "entry", Ref: "_9x2pq0", Agent: &peers.AgentInfo{Type: "cc", PeerName: "y", Status: "busy"}, Deliverable: true, Cwd: "/w"},
			},
		},
		{Alias: "air", OK: true, DaemonVersion: "1.0.0-alpha.340", Peers: []peers.PeerRecord{}},
		{Alias: "down", OK: false, Error: "connection refused", Peers: []peers.PeerRecord{}},
	}}
	got := formatPeersAllTable(resp)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if cols := headerColumns(lines[0]); cols[0] != "HOST" || cols[1] != "TITLE" || cols[2] != "ADDRESS" {
		t.Errorf("first three columns = %v, want HOST TITLE ADDRESS", cols[:3])
	}
	if !strings.Contains(lines[2], "  local/y") {
		t.Errorf("entry row (indented) not where expected: %q", lines[2])
	}
	if strings.Contains(got, "*") {
		t.Errorf("--all table still carries a * marker:\n%s", got)
	}
	if !strings.Contains(got, "down  (unreachable: connection refused)") {
		t.Errorf("unreachable line missing:\n%s", got)
	}
	if !strings.Contains(got, "local  daemon 1.0.0-alpha.342") {
		t.Errorf("local daemon version trailer missing:\n%s", got)
	}
	if !strings.Contains(got, "air  daemon 1.0.0-alpha.340") {
		t.Errorf("air daemon version trailer missing:\n%s", got)
	}
	if strings.Contains(got, "down  daemon") {
		t.Errorf("unreachable host must not get a daemon version trailer:\n%s", got)
	}
}

// TestFormatPeersAllTable_PartialCauseLines pins the per-host
// partial-cause lines: an alias-prefixed unknown-registry-files line for
// the host that has one, and an alias-prefixed title-store-unavailable
// line for the host whose envelope says titles_unavailable — each placed
// ahead of that host's own daemon trailer.
func TestFormatPeersAllTable_PartialCauseLines(t *testing.T) {
	resp := peers.AllEnvelope{Hosts: []peers.HostResult{
		{
			Alias: "local", OK: true, DaemonVersion: "1.0.0",
			Partial:              true,
			UnknownRegistryFiles: []string{"/reg/9999.json"},
			Peers:                []peers.PeerRecord{{Address: "local/sess1", Deliverable: false, Reason: "no_agent"}},
		},
		{
			Alias: "air", OK: true, DaemonVersion: "1.0.1",
			Partial:           true,
			TitlesUnavailable: true,
			Peers:             []peers.PeerRecord{{Address: "air/sess1", Deliverable: true, Agent: &peers.AgentInfo{Type: "cc"}}},
		},
		{Alias: "down", OK: false, Error: "connection refused", Peers: []peers.PeerRecord{}},
	}}
	got := formatPeersAllTable(resp)
	if !strings.Contains(got, "local  (partial: unknown registry files: /reg/9999.json)\n") {
		t.Errorf("formatPeersAllTable = %q, want local's unknown-registry-files line", got)
	}
	if !strings.Contains(got, "air  (partial: title store unavailable)\n") {
		t.Errorf("formatPeersAllTable = %q, want air's title-store-unavailable line", got)
	}
	if strings.Contains(got, "down  (partial:") {
		t.Errorf("formatPeersAllTable = %q, want no partial line for the unreachable host", got)
	}
	wantOrder := "local  (partial: unknown registry files: /reg/9999.json)\nlocal  daemon 1.0.0\n"
	if !strings.Contains(got, wantOrder) {
		t.Errorf("formatPeersAllTable = %q, want the partial-cause line directly ahead of that host's daemon trailer", got)
	}
}

// TestFormatPeersAllTable_OwnerOnlyPartialPrintsCount pins X3: a host in
// --all that is partial ONLY because some owner lookups did not run used
// to print nothing at all. It must print the alias-prefixed unresolved
// count, exactly as the single-host table does.
func TestFormatPeersAllTable_OwnerOnlyPartialPrintsCount(t *testing.T) {
	resp := peers.AllEnvelope{Hosts: []peers.HostResult{
		{
			Alias: "air", OK: true, DaemonVersion: "1.0.1", Partial: true,
			Peers: []peers.PeerRecord{
				{Address: "air/sess1", Deliverable: false, Reason: "no_agent"},
				{Address: "air/sess2"}, // unresolved: no agent, no reason
			},
		},
	}}
	got := formatPeersAllTable(resp)
	want := "air  (partial: 1 sessions not resolved within budget)\nair  daemon 1.0.1\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("formatPeersAllTable = %q, want it to end with %q", got, want)
	}
	if strings.Contains(got, "title store") || strings.Contains(got, "unknown registry") {
		t.Errorf("formatPeersAllTable = %q, want only the count line", got)
	}
}

// TestFormatPeersAllTable_AllPartialCauseLines pins that --all prints
// every applicable cause line per host, alias-prefixed, in the order
// count / unknown files / title store, ahead of that host's trailer —
// the same renderer the single-host table uses.
func TestFormatPeersAllTable_AllPartialCauseLines(t *testing.T) {
	resp := peers.AllEnvelope{Hosts: []peers.HostResult{
		{
			Alias: "local", OK: true, DaemonVersion: "1.0.0", Partial: true,
			UnknownRegistryFiles: []string{"/reg/1.json"},
			TitlesUnavailable:    true,
			Peers: []peers.PeerRecord{
				{Address: "local/sess1"}, // unresolved
			},
		},
		{Alias: "air", OK: true, DaemonVersion: "1.0.1", Peers: []peers.PeerRecord{}},
	}}
	got := formatPeersAllTable(resp)
	want := "local  (partial: 1 sessions not resolved within budget)\n" +
		"local  (partial: unknown registry files: /reg/1.json)\n" +
		"local  (partial: title store unavailable)\n" +
		"local  daemon 1.0.0\n" +
		"air  daemon 1.0.1\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("formatPeersAllTable = %q, want it to end with %q", got, want)
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

const wantHostsTable = "ALIAS  URL                      HOST_ID     VERIFIED  TOKEN  INBOUND  ALLOW_BYPASS  ROTATION\n" +
	"air    https://air.mlab.host    air:def456  yes       yes    yes      no            -\n" +
	"phone  https://phone.mlab.host              no        no     yes      yes           -\n"

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

// TestRunPeersCmd_HostAdd_AliasOmitted pins spec §7.2 at the CLI edge:
// `pdx peers host add <url>` is legal, and it sends an empty alias so the
// daemon adopts the one the peer publishes for itself. The alias is the
// optional positional, so the sole one is the URL.
func TestRunPeersCmd_HostAdd_AliasOmitted(t *testing.T) {
	var gotBody cliAddHostRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(cliAddHostResponse{
			Alias: "air26", URL: "https://air.mlab.host", HostID: "air:def456",
			InboundToken: "pdxp_inbound123", Verified: true,
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "add", "https://air.mlab.host", "--token", "pdxp_out123", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	wantBody := cliAddHostRequest{Alias: "", URL: "https://air.mlab.host", Token: "pdxp_out123"}
	if gotBody != wantBody {
		t.Errorf("request body = %+v, want %+v", gotBody, wantBody)
	}
	// The name echoed back is the daemon's, not the operator's.
	if !strings.Contains(stdout.String(), "added air26 (https://air.mlab.host)") {
		t.Errorf("stdout = %q, want it to report the adopted alias", stdout.String())
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
		{"host add no positionals", []string{"host", "add"}},
		// The alias is optional, so one positional is legal — but only as a
		// URL. A bare word is a missing URL, not a host named "air".
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

// --- v4 columns -----------------------------------------------------------

// v4TableFixture mirrors spec §5.7's worked example: a plain session row, a
// titled one, a row whose name was not routable so its address IS its ref, an
// agentless tmux row, and an entry row whose tmux name is frozen.
func v4TableFixture() peers.Envelope {
	return peers.Envelope{OK: true, Peers: []peers.PeerRecord{
		{Address: "mlab/purdex-b0", Ref: "_q34psn", RowKind: "session",
			SessionName: "aigora2", TmuxName: "aigora2", Cwd: "~/Workspace/wake/aigora",
			Agent: &peers.AgentInfo{Type: "cc", PeerName: "purdex-b0", Status: "idle"}, Deliverable: true},
		{Address: "mlab/purdex-53", Ref: "_d8dc4a", RowKind: "session",
			SessionName: "purdex7", TmuxName: "purdex7", Cwd: "~",
			Title: "Purdex Tester 01",
			Agent: &peers.AgentInfo{Type: "cc", PeerName: "purdex-53", Status: "busy"}, Deliverable: true},
		{Address: "mlab/_df25d0", Ref: "_df25d0", RowKind: "session",
			SessionName: "nexen", TmuxName: "nexen", Cwd: "~",
			Agent: &peers.AgentInfo{Type: "cc", PeerName: "nexen-f2", Status: "idle"}, Reason: "inbox_dead"},
		{Address: "mlab/tmux:aigora3", RowKind: "session",
			SessionName: "aigora3", TmuxName: "aigora3", Cwd: "~", Reason: "no_agent"},
		{Address: "mlab/barbox-a6", Ref: "_n4zeqk", RowKind: "entry",
			TmuxName: "bb2", Cwd: "~",
			Agent: &peers.AgentInfo{Type: "cc", PeerName: "barbox-a6", Status: "idle"}, Deliverable: true},
	}}
}

// TestFormatPeersTable_V4Columns pins spec §5.7: the columns are TITLE
// ADDRESS AGENT STATUS DELIVERABLE TMUX CWD -- NAME is gone (it IS the
// address's second segment) and HOST never belonged to the single-host form
// (it is the address's first segment); TMUX arrives because the tmux name
// left the address with the suffix. Every address carries its ref in
// brackets except one that already IS the ref.
func TestFormatPeersTable_V4Columns(t *testing.T) {
	got := formatPeersTable(v4TableFixture())
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	header := lines[0]

	wantCols := []string{"TITLE", "ADDRESS", "AGENT", "STATUS", "DELIVERABLE", "TMUX", "CWD"}
	if cols := headerColumns(header); strings.Join(cols, "|") != strings.Join(wantCols, "|") {
		t.Errorf("header columns = %v, want %v", cols, wantCols)
	}
	if strings.Contains(header, "NAME") || strings.Contains(header, "HOST") {
		t.Errorf("dropped column still present: %q", header)
	}

	for _, want := range []string{
		"mlab/purdex-b0 [q34psn]", // ref rendered without its underscore
		"mlab/purdex-53 [d8dc4a]",
		"Purdex Tester 01", // TITLE cell
		"inbox_dead",       // DELIVERABLE cell
		"mlab/_df25d0",     // the ref-address row still renders
		"mlab/tmux:aigora3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table lacks %q:\n%s", want, got)
		}
	}

	// A row whose address IS the ref must not get a redundant bracket.
	if strings.Contains(got, "mlab/_df25d0 [df25d0]") {
		t.Errorf("ref-address row got a redundant bracket:\n%s", got)
	}
	// Nor does a row with no ref at all.
	if strings.Contains(got, "mlab/tmux:aigora3 [") {
		t.Errorf("refless row got a bracket:\n%s", got)
	}
	// An entry row keeps its two-space address indent, and its address is
	// bracketed like any other.
	if !strings.Contains(got, "  mlab/barbox-a6 [n4zeqk]") {
		t.Errorf("entry row address not indented/bracketed:\n%s", got)
	}
}

// TestFormatPeersTable_TmuxColumn pins the TMUX cell (spec §5.7): it renders
// TmuxName, never SessionName -- SessionName is empty on an entry row, which
// is the whole reason TmuxName exists -- and an entry row's value carries a
// trailing "?" because it is frozen registry data that may name a session
// since renamed or gone. A session row, read live from the inventory, gets no
// marker; a row with no tmux at all gets "-".
func TestFormatPeersTable_TmuxColumn(t *testing.T) {
	got := formatPeersTable(v4TableFixture())
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	// The entry row's SessionName is "" -- only TmuxName can produce this.
	if !strings.Contains(got, "bb2?") {
		t.Errorf("entry row TMUX cell missing the marked frozen name %q:\n%s", "bb2?", got)
	}
	for _, want := range []string{"aigora2", "purdex7", "nexen", "aigora3"} {
		if !strings.Contains(got, want) {
			t.Errorf("session row TMUX cell lacks %q:\n%s", want, got)
		}
	}
	// Session rows are live, so none of them is marked.
	for _, unwanted := range []string{"aigora2?", "purdex7?", "nexen?", "aigora3?"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("session row TMUX cell was marked %q -- only entry rows are:\n%s", unwanted, got)
		}
	}

	// The TMUX cell sits second from the right, between DELIVERABLE and CWD.
	for i, wantTmux := range []string{"aigora2", "purdex7", "nexen", "aigora3", "bb2?"} {
		cells := headerColumns(lines[i+1])
		// An unset TITLE cell is blank, so headerColumns drops it; index
		// from the right instead, where CWD is last.
		if len(cells) < 2 {
			t.Fatalf("row %d too short: %q", i, lines[i+1])
		}
		if gotTmux := cells[len(cells)-2]; gotTmux != wantTmux {
			t.Errorf("row %d TMUX cell = %q, want %q (row %q)", i, gotTmux, wantTmux, lines[i+1])
		}
	}

	// No tmux at all renders "-", not a blank.
	none := formatPeersTable(peers.Envelope{OK: true, Peers: []peers.PeerRecord{
		{Address: "mlab/loner-11", Ref: "_aaaaaa", RowKind: "entry", Cwd: "~",
			Agent: &peers.AgentInfo{Type: "cc", Status: "idle"}, Deliverable: true},
	}})
	cells := headerColumns(strings.Split(none, "\n")[1])
	if got := cells[len(cells)-2]; got != "-" {
		t.Errorf("tmux-less row TMUX cell = %q, want %q (%q)", got, "-", none)
	}
}

// TestFormatPeersAllTable_V4Columns pins that --all keeps its leading HOST
// column (spec §5.7) and then follows the single-host order exactly.
func TestFormatPeersAllTable_V4Columns(t *testing.T) {
	got := formatPeersAllTable(peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "mlab", OK: true, DaemonVersion: "1.0.0", Peers: v4TableFixture().Peers},
	}})
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	wantCols := []string{"HOST", "TITLE", "ADDRESS", "AGENT", "STATUS", "DELIVERABLE", "TMUX", "CWD"}
	if cols := headerColumns(lines[0]); strings.Join(cols, "|") != strings.Join(wantCols, "|") {
		t.Errorf("--all header columns = %v, want %v", cols, wantCols)
	}
	if strings.Contains(lines[0], "NAME") {
		t.Errorf("--all header still carries NAME: %q", lines[0])
	}
	if !strings.Contains(got, "mlab/purdex-b0 [q34psn]") {
		t.Errorf("--all table lacks the bracketed address:\n%s", got)
	}
	if !strings.Contains(got, "bb2?") {
		t.Errorf("--all table lacks the marked entry-row tmux name:\n%s", got)
	}
	if strings.Contains(got, "mlab/_df25d0 [df25d0]") {
		t.Errorf("--all ref-address row got a redundant bracket:\n%s", got)
	}
}

// TestFormatPeersAllTable_MarksAliasDrift pins spec §7.4: --all is the one
// place the comparison between what we call a host and what that host calls
// itself is live, so it is the place that says the two disagree. The
// agreeing host says nothing at all — a mark on every row is a mark on none.
func TestFormatPeersAllTable_MarksAliasDrift(t *testing.T) {
	got := formatPeersAllTable(peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "mlab", SelfAlias: "mlab", OK: true, DaemonVersion: "1.0.0", Peers: []peers.PeerRecord{}},
		{Alias: "air", SelfAlias: "air26", OK: true, DaemonVersion: "1.0.0", Peers: []peers.PeerRecord{}},
	}})

	if !strings.Contains(got, "air  (alias drift: peer calls itself air26)") {
		t.Errorf("drifted self alias not shown:\n%s", got)
	}
	if n := strings.Count(got, "alias drift"); n != 1 {
		t.Errorf("alias-drift lines = %d, want exactly 1 (only the host that drifted):\n%s", n, got)
	}
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "mlab") && strings.Contains(ln, "alias drift") {
			t.Errorf("agreeing host is marked: %q", ln)
		}
	}
	// Surfaced, never followed: the line reports the disagreement, it does
	// not rename anything. Every row still lives under the local alias.
	if strings.Contains(got, "air26/") {
		t.Errorf("--all rewrote a row to the peer's self-reported name:\n%s", got)
	}
}

// TestFormatPeersAllTable_SilentWhenSelfAliasUnknown: "" is not drift. A
// host that never reported a name — an old daemon, or a fetch that never
// reached a daemon at all — disagrees with nothing.
func TestFormatPeersAllTable_SilentWhenSelfAliasUnknown(t *testing.T) {
	got := formatPeersAllTable(peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "mlab", SelfAlias: "mlab", OK: true, Peers: []peers.PeerRecord{}},
		{Alias: "air", OK: true, Peers: []peers.PeerRecord{}},
		{Alias: "down", OK: false, Error: "connection refused", Peers: []peers.PeerRecord{}},
	}})
	if strings.Contains(got, "alias drift") {
		t.Errorf("a host with no self-reported alias was marked as drifting:\n%s", got)
	}
}

// TestFormatPeersAllTable_AliasDriftIsCaseInsensitive: aliases are matched
// case-insensitively everywhere that routes on them (config.ValidateAlias,
// config.FindPeerHostByAlias), so "MLAB" and "mlab" reach the same host and
// are not a disagreement worth a line.
func TestFormatPeersAllTable_AliasDriftIsCaseInsensitive(t *testing.T) {
	got := formatPeersAllTable(peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "mlab", SelfAlias: "MLAB", OK: true, Peers: []peers.PeerRecord{}},
	}})
	if strings.Contains(got, "alias drift") {
		t.Errorf("a case-only difference was reported as drift:\n%s", got)
	}
}

// TestFormatPeersAllTable_EscapesAliasDrift: self_alias is the peer's own
// report, exactly as attacker-controlled as its error text, and it lands on
// a terminal.
func TestFormatPeersAllTable_EscapesAliasDrift(t *testing.T) {
	got := formatPeersAllTable(peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "air", SelfAlias: "air\x1b[2Jevil\x07", OK: true, Peers: []peers.PeerRecord{}},
	}})
	if !strings.Contains(got, "alias drift") {
		t.Fatalf("drift line missing entirely:\n%s", got)
	}
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("formatPeersAllTable = %q, want no raw ESC/BEL bytes in the drift line", got)
	}
}

// TestFormatPeersAllTable_LocalRowNeverDrifts: the local host is Hosts[0]
// of every fan-out and its SelfAlias equals its Alias by construction
// (internal/module/peers.allEnvelope fills both from the same snapshot).
// It must never be able to disagree with itself.
func TestFormatPeersAllTable_LocalRowNeverDrifts(t *testing.T) {
	got := formatPeersAllTable(peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "mlab", SelfAlias: "mlab", OK: true, DaemonVersion: "1.0.0", Peers: []peers.PeerRecord{}},
	}})
	if strings.Contains(got, "alias drift") {
		t.Errorf("the local row reported drift against itself:\n%s", got)
	}
}

// --- the bracket rule has one owner (spec §5.2) --------------------------

// addressWithRefCases are the three inputs the bracket rule turns on. The
// third is the one a restated rule gets wrong, and it is reachable from the
// very defect refs were added to fix: a name collision is a case where both
// candidates may carry ref-form addresses.
var addressWithRefCases = []struct {
	name    string
	address string
	ref     string
	want    string
}{
	{"a ref is bracketed without its underscore", "mlab/purdex-dd", "_h0h3ln", "mlab/purdex-dd [h0h3ln]"},
	{"no ref, no bracket", "mlab/tmux:zz", "", "mlab/tmux:zz"},
	{"an address that already IS the ref gets no bracket", "mlab/_h0h3ln", "_h0h3ln", "mlab/_h0h3ln"},
}

// TestDisplayAddress_BracketRule pins displayAddress against the three cases
// directly rather than through formatPeersTable's golden table, so that a
// change to the rule fails at the rule rather than in a column-width diff.
func TestDisplayAddress_BracketRule(t *testing.T) {
	for _, tc := range addressWithRefCases {
		t.Run(tc.name, func(t *testing.T) {
			got := displayAddress(peers.PeerRecord{Address: tc.address, Ref: tc.ref})
			if got != tc.want {
				t.Errorf("displayAddress(%q, %q) = %q, want %q", tc.address, tc.ref, got, tc.want)
			}
		})
	}
}

// TestDisplayAddressAndMsgCandidateLineAgree is the reason addressWithRef
// exists as a shared function instead of a rule written twice. `pdx peers`
// shows an address and `pdx msg` refuses with one; if the two rendered the
// same (address, ref) differently, the string the operator copies out of a
// refusal would not be the string they see in the table — and the case they
// would disagree on is precisely the ref-form address, since restating the
// rule from memory yields only its first arm.
func TestDisplayAddressAndMsgCandidateLineAgree(t *testing.T) {
	for _, tc := range addressWithRefCases {
		t.Run(tc.name, func(t *testing.T) {
			fromTable := displayAddress(peers.PeerRecord{Address: tc.address, Ref: tc.ref})
			fromRefusal := strings.TrimPrefix(
				msgCandidateLine(peers.AmbiguousCandidate{Address: tc.address, Ref: tc.ref}),
				"  ")
			if fromTable != fromRefusal {
				t.Errorf("renderers disagree on (%q, %q): peers table %q, msg refusal %q",
					tc.address, tc.ref, fromTable, fromRefusal)
			}
			if fromTable != tc.want {
				t.Errorf("both renderers agree on %q, but the rule wants %q", fromTable, tc.want)
			}
		})
	}
}

// --- host verify (spec §4.4) -------------------------------------------------

func TestRunPeersCmd_HostVerify_OKWithDrift(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alias": "air", "host_id": "wakes-air-2026:oa6drb", "ok": true,
			"self_alias": "air26", "daemon_version": "1.0.0-alpha.377",
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/peers/hosts/air/verify" {
		t.Errorf("request = %s %s, want POST /api/peers/hosts/air/verify", gotMethod, gotPath)
	}
	if gotAuth != "Bearer admin-tok" {
		t.Errorf("Authorization = %q, want the admin token", gotAuth)
	}
	out := stdout.String()
	for _, want := range []string{"ok", "wakes-air-2026:oa6drb", "1.0.0-alpha.377", "alias drift: peer calls itself air26"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
}

func TestRunPeersCmd_HostVerify_NoDriftWhenSameCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": true, "self_alias": "AIR", "daemon_version": "x"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "drift") {
		t.Errorf("stdout = %q, want no drift line for a case-only difference", stdout.String())
	}
}

func TestRunPeersCmd_HostVerify_Failed_Exit1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": false, "error": "no outbound token", "self_alias": "", "daemon_version": ""})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "FAILED: no outbound token") {
		t.Errorf("stdout = %q, want FAILED line", stdout.String())
	}
}

// The peer's self_alias is attacker-controlled and lands in a terminal:
// sanitizeCell must escape it (mutation: print it raw → the ESC byte reaches stdout).
func TestRunPeersCmd_HostVerify_SelfAliasSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": true, "self_alias": "evil\x1b[31mred", "daemon_version": "v\x07"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	runPeersCmd([]string{"host", "verify", "air", "--config", cfgPath}, &stdout, &stderr)
	if strings.ContainsAny(stdout.String(), "\x1b\x07") {
		t.Errorf("stdout contains a raw control byte: %q", stdout.String())
	}
}

func TestRunPeersCmd_HostVerify_JSONPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "host_id": "a:1", "ok": true, "self_alias": "air26", "daemon_version": "x"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "verify", "air", "--json", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	var v map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v; %q", err, stdout.String())
	}
	if v["self_alias"] != "air26" {
		t.Errorf("json self_alias = %v, want air26", v["self_alias"])
	}
	if _, has := v["error"]; has {
		t.Errorf("json has error key = %v, want omitted on success (matching the daemon body)", v["error"])
	}
}

func TestRunPeersCmd_HostVerify_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown alias"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "verify", "ghost", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown alias") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestParsePeersInvocation_HostVerifyGrammar(t *testing.T) {
	if _, _, ok := parsePeersInvocation([]string{"host", "verify"}); ok {
		t.Error("verify with no alias accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "verify", "a", "b"}); ok {
		t.Error("verify with two positionals accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "verify", "a", "--token", "x"}); ok {
		t.Error("verify with --token accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "list", "--json"}); ok {
		t.Error("--json accepted for a verb other than verify")
	}
	inv, _, ok := parsePeersInvocation([]string{"host", "verify", "a", "--json"})
	if !ok || !inv.jsonOutput || inv.verb != "verify" {
		t.Errorf("verify --json: inv=%+v ok=%v", inv, ok)
	}
}

// --- host rename (spec §4.4) -------------------------------------------------

func TestRunPeersCmd_HostRename(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alias": "air26", "url": "http://100.64.0.4:7860", "host_id": "a:1",
			"verified": true, "has_token": true, "has_inbound_token": true, "allow_bypass": false,
		})
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "rename", "air", "air26", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if gotMethod != http.MethodPut || gotPath != "/api/peers/hosts/air" {
		t.Errorf("request = %s %s, want PUT /api/peers/hosts/air", gotMethod, gotPath)
	}
	if gotBody["alias"] != "air26" {
		t.Errorf("body = %v, want alias air26", gotBody)
	}
	if _, has := gotBody["token"]; has && gotBody["token"] != "" {
		t.Errorf("body = %v, want no token", gotBody)
	}
	if !strings.Contains(stdout.String(), "renamed air -> air26") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRunPeersCmd_HostRename_Conflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": `alias "air26" is already used by another host`})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"host", "rename", "air", "air26", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "already used by another host") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// TestRunPeersCmd_HostRename_OldDaemonIgnoredAlias pins the mixed-version
// path: an alpha.376 daemon has no alias field on PUT, answers 200 with the
// entry unchanged, and the CLI must not print "renamed air -> air".
func TestRunPeersCmd_HostRename_OldDaemonIgnoredAlias(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "url": "http://100.64.0.4:7860", "host_id": "a:1"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "rename", "air", "air26", "--config", cfgPath}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.Contains(stdout.String(), "renamed") {
		t.Errorf("stdout = %q, want no success line", stdout.String())
	}
	if !strings.Contains(stderr.String(), "did not apply the rename") {
		t.Errorf("stderr = %q, want the not-applied message", stderr.String())
	}
}

// A case-only rename is a real rename: an old daemon that echoes the old
// spelling must be refused exactly like any other ignored rename.
func TestRunPeersCmd_HostRename_OldDaemonIgnoredCaseOnlyRename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "air", "url": "http://100.64.0.4:7860", "host_id": "a:1"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"host", "rename", "air", "Air", "--config", cfgPath}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.Contains(stdout.String(), "renamed") {
		t.Errorf("stdout = %q, want no success line", stdout.String())
	}
}

func TestRunPeersCmd_HostRename_EscapesAlias(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_ = json.NewEncoder(w).Encode(map[string]any{"alias": "b"})
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	runPeersCmd([]string{"host", "rename", "a?b", "b", "--config", cfgPath}, &stdout, &stderr)
	if gotPath != "/api/peers/hosts/a%3Fb" {
		t.Errorf("path = %q, want the alias percent-escaped", gotPath)
	}
}

func TestParsePeersInvocation_HostRenameGrammar(t *testing.T) {
	if _, _, ok := parsePeersInvocation([]string{"host", "rename", "a"}); ok {
		t.Error("rename with one positional accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "rename", "a", "b", "--json"}); ok {
		t.Error("rename --json accepted")
	}
	if _, _, ok := parsePeersInvocation([]string{"host", "rename", "a", "b", "--allow-bypass=true"}); ok {
		t.Error("rename --allow-bypass accepted")
	}
	inv, _, ok := parsePeersInvocation([]string{"host", "rename", "a", "b"})
	if !ok || inv.verb != "rename" || len(inv.positionals) != 2 {
		t.Errorf("rename a b: inv=%+v ok=%v", inv, ok)
	}
}

// --- host rotate (spec §6.6) ------------------------------------------------

func TestParsePeersInvocation_Rotate(t *testing.T) {
	cases := []struct {
		args   []string
		ok     bool
		commit bool
		cancel bool
		force  bool
	}{
		{[]string{"host", "rotate", "air"}, true, false, false, false},
		{[]string{"host", "rotate", "air", "--commit"}, true, true, false, false},
		{[]string{"host", "rotate", "air", "--cancel", "--force"}, true, false, true, true},
		{[]string{"host", "rotate", "air", "--commit", "--cancel"}, false, false, false, false},
		{[]string{"host", "rotate", "air", "--force"}, false, false, false, false},
		{[]string{"host", "rotate"}, false, false, false, false},
		{[]string{"host", "rotate", "a/b"}, false, false, false, false},
		{[]string{"host", "verify", "air", "--commit"}, false, false, false, false},
		{[]string{"host", "rotate", "air", "--json"}, false, false, false, false},
	}
	for _, tc := range cases {
		inv, _, ok := parsePeersInvocation(tc.args)
		if ok != tc.ok {
			t.Errorf("%v: ok=%v want %v", tc.args, ok, tc.ok)
			continue
		}
		if ok && (inv.rotateCommit != tc.commit || inv.rotateCancel != tc.cancel || inv.rotateForce != tc.force) {
			t.Errorf("%v: parsed %+v", tc.args, inv)
		}
	}
}

func TestRunPeersHostRotate_PrintsNewToken(t *testing.T) {
	var gotPath string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"air","inbound_token":"pdxp_11111111111111111111111111111111"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runPeersCmd([]string{"host", "rotate", "air", "--config", cfgPath}, &out, &errb)
	if code != 0 || gotPath != "POST /api/peers/hosts/air/rotate" {
		t.Fatalf("code=%d path=%q err=%s", code, gotPath, errb.String())
	}
	if !strings.Contains(out.String(), "pdxp_11111111111111111111111111111111") || !strings.Contains(out.String(), "rotated air") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunPeersHostRotate_Commit_UnconfirmedExplainsOnPeer(t *testing.T) {
	var gotBody string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"rotation unconfirmed"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runPeersCmd([]string{"host", "rotate", "air", "--commit", "--config", cfgPath}, &out, &errb)
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "rotation unconfirmed") || !strings.Contains(errb.String(), "pdx peers host verify") || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("stderr = %q", errb.String())
	}
	if strings.Contains(gotBody, `"force":true`) {
		t.Fatalf("commit sent force without --force: %s", gotBody)
	}
}

func TestRunPeersHostRotate_CommitForce_SendsForce(t *testing.T) {
	var gotPath, gotBody string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"air","url":"http://x","host_id":"air:1","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false,"rotation_pending":false,"last_inbound_auth":"current"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	code := runPeersCmd([]string{"host", "rotate", "air", "--commit", "--force", "--config", cfgPath}, &out, &errb)
	if code != 0 || gotPath != "/api/peers/hosts/air/rotate/commit" || !strings.Contains(gotBody, `"force":true`) {
		t.Fatalf("code=%d path=%q body=%q err=%s", code, gotPath, gotBody, errb.String())
	}
	if !strings.Contains(out.String(), "committed air") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestRunPeersHostRotate_Cancel(t *testing.T) {
	var gotPath string
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"alias":"air","url":"http://x","host_id":"air:1","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false,"rotation_pending":false,"last_inbound_auth":"current"}`)
	})
	defer srv.Close()
	var out, errb bytes.Buffer
	if code := runPeersCmd([]string{"host", "rotate", "air", "--cancel", "--config", cfgPath}, &out, &errb); code != 0 || gotPath != "/api/peers/hosts/air/rotate/cancel" {
		t.Fatalf("code=%d path=%q err=%s", code, gotPath, errb.String())
	}
	if !strings.Contains(out.String(), "cancelled rotation for air") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestFormatHostsTable_RotationColumn(t *testing.T) {
	out := formatHostsTable([]cliHostRow{
		{Alias: "a", URL: "http://a", HostID: "a:1"},
		{Alias: "b", URL: "http://b", HostID: "b:1", RotationPending: true},
		{Alias: "c", URL: "http://c", HostID: "c:1", RotationPending: true, LastInboundAuth: "current"},
	})
	if !strings.Contains(out, "ROTATION") {
		t.Fatalf("no ROTATION header: %s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasSuffix(strings.TrimSpace(lines[1]), "-") || !strings.HasSuffix(strings.TrimSpace(lines[2]), "pending") || !strings.HasSuffix(strings.TrimSpace(lines[3]), "pending, confirmed") {
		t.Fatalf("table:\n%s", out)
	}
}

// --- pdx peers alias [<name>|--clear] (self-alias spec §4.2, S-5) ----------

// settingsCapture records what fakeSettingsDaemon saw in its last request.
type settingsCapture struct {
	calls  int
	method string
	path   string
	auth   string
	body   string
}

// fakeSettingsDaemon is a fakePeersDaemon that serves /api/peers/settings
// only, recording the last request's method, bearer and RAW body (the body
// is asserted as a string so no stray key — a `deliver` from the shared
// wire type's pointer field, say — can sneak in unseen), and answering
// with the given status and body.
func fakeSettingsDaemon(t *testing.T, status int, body string) (*httptest.Server, string, *settingsCapture) {
	t.Helper()
	seen := &settingsCapture{}
	srv, cfgPath := fakePeersDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		seen.calls++
		seen.method, seen.path, seen.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		seen.body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	})
	return srv, cfgPath, seen
}

func TestRunPeersCmd_Alias_Query(t *testing.T) {
	srv, cfgPath, seen := fakeSettingsDaemon(t, http.StatusOK, `{"deliver":true,"alias":"mini-lab","alias_source":"host_id"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"alias", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if seen.method != http.MethodGet || seen.path != "/api/peers/settings" {
		t.Errorf("request = %s %s, want GET /api/peers/settings", seen.method, seen.path)
	}
	if seen.auth != "Bearer admin-tok" {
		t.Errorf("Authorization = %q, want the admin bearer", seen.auth)
	}
	if seen.body != "" {
		t.Errorf("GET carried a body: %q", seen.body)
	}
	if got, want := stdout.String(), "alias: mini-lab (host_id)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// The query form accepts either source: a configured alias prints as
// "(config)".
func TestRunPeersCmd_Alias_Query_ConfigSource(t *testing.T) {
	srv, cfgPath, _ := fakeSettingsDaemon(t, http.StatusOK, `{"deliver":false,"alias":"mlab","alias_source":"config"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"alias", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), "alias: mlab (config)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunPeersCmd_Alias_Set(t *testing.T) {
	srv, cfgPath, seen := fakeSettingsDaemon(t, http.StatusOK, `{"deliver":true,"alias":"mlab","alias_source":"config"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"alias", "mlab", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if seen.method != http.MethodPut || seen.path != "/api/peers/settings" {
		t.Errorf("request = %s %s, want PUT /api/peers/settings", seen.method, seen.path)
	}
	if seen.auth != "Bearer admin-tok" {
		t.Errorf("Authorization = %q, want the admin bearer", seen.auth)
	}
	// Exact raw body: only the alias key. A `deliver` key — even null —
	// would be a second field the daemon has to interpret.
	if got, want := strings.TrimSpace(seen.body), `{"alias":"mlab"}`; got != want {
		t.Errorf("PUT body = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "alias: mlab (config)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunPeersCmd_Alias_Clear(t *testing.T) {
	srv, cfgPath, seen := fakeSettingsDaemon(t, http.StatusOK, `{"deliver":true,"alias":"mini-lab","alias_source":"host_id"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"alias", "--clear", "--config", cfgPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if seen.method != http.MethodPut || seen.path != "/api/peers/settings" {
		t.Errorf("request = %s %s, want PUT /api/peers/settings", seen.method, seen.path)
	}
	if got, want := strings.TrimSpace(seen.body), `{"alias":""}`; got != want {
		t.Errorf("PUT body = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "alias: mini-lab (host_id)\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// TestRunPeersCmd_Alias_NotApplied pins S-5 (tightened per codex F5): a
// 200 is success only when the response carries a known alias_source, a
// non-empty alias, and — for set/clear — the value and source the request
// asked for. Every other 200 is "daemon did not apply the alias", exit 1,
// with no success line.
func TestRunPeersCmd_Alias_NotApplied(t *testing.T) {
	cases := []struct {
		name string
		args []string
		body string
	}{
		// An old daemon decodes the PUT without the alias key, ignores it
		// and answers 200 with the previous alias and no alias_source.
		{"set: old daemon, no alias_source", []string{"alias", "mlab"}, `{"deliver":true,"alias":"mini-lab"}`},
		{"clear: old daemon, no alias_source", []string{"alias", "--clear"}, `{"deliver":true,"alias":"mini-lab"}`},
		{"query: old daemon, no alias_source", []string{"alias"}, `{"deliver":true,"alias":"mini-lab"}`},
		{"set: echoed a different alias", []string{"alias", "mlab"}, `{"deliver":true,"alias":"mini-lab","alias_source":"config"}`},
		{"set: case-only difference is a difference", []string{"alias", "mlab"}, `{"deliver":true,"alias":"Mlab","alias_source":"config"}`},
		{"set: right alias, wrong source", []string{"alias", "mlab"}, `{"deliver":true,"alias":"mlab","alias_source":"host_id"}`},
		{"set: empty alias", []string{"alias", "mlab"}, `{"deliver":true,"alias":"","alias_source":"config"}`},
		{"clear: still config", []string{"alias", "--clear"}, `{"deliver":true,"alias":"mlab","alias_source":"config"}`},
		{"clear: empty alias", []string{"alias", "--clear"}, `{"deliver":true,"alias":"","alias_source":"host_id"}`},
		{"query: unknown source", []string{"alias"}, `{"deliver":true,"alias":"mini-lab","alias_source":"weird"}`},
		{"query: empty alias", []string{"alias"}, `{"deliver":true,"alias":"","alias_source":"host_id"}`},
		{"set: unknown source", []string{"alias", "mlab"}, `{"deliver":true,"alias":"mlab","alias_source":"weird"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cfgPath, _ := fakeSettingsDaemon(t, http.StatusOK, tc.body)
			defer srv.Close()

			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, tc.args...), "--config", cfgPath)
			code := runPeersCmd(args, &stdout, &stderr)

			if code != 1 {
				t.Errorf("exit code = %d, want 1; stderr=%q", code, stderr.String())
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q, want no success line", stdout.String())
			}
			if !strings.Contains(stderr.String(), "did not apply the alias") || !strings.Contains(stderr.String(), "daemon too old") {
				t.Errorf("stderr = %q, want the not-applied / too-old message", stderr.String())
			}
		})
	}
}

// The daemon's 400/409 text passes through verbatim (spec §4.2, S-6).
func TestRunPeersCmd_Alias_Conflict(t *testing.T) {
	srv, cfgPath, _ := fakeSettingsDaemon(t, http.StatusConflict, `{"error":"alias \"air26\" is already used by a peer host"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runPeersCmd([]string{"alias", "air26", "--config", cfgPath}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `alias "air26" is already used by a peer host`) {
		t.Errorf("stderr = %q, want the daemon's error text", stderr.String())
	}
}

func TestRunPeersCmd_Alias_BadRequest(t *testing.T) {
	srv, cfgPath, _ := fakeSettingsDaemon(t, http.StatusBadRequest, `{"error":"alias must match ^[a-z0-9][a-z0-9-]*$"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"alias", "Bad_Name", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "alias must match") {
		t.Errorf("stderr = %q, want the daemon's error text", stderr.String())
	}
}

// The echoed alias is the daemon's text landing in a terminal: it goes
// through sanitizeCell on both the success line and the not-applied line.
func TestRunPeersCmd_Alias_EscapesEchoedAlias(t *testing.T) {
	srv, cfgPath, _ := fakeSettingsDaemon(t, http.StatusOK, `{"deliver":true,"alias":"mini\u001b[31mlab","alias_source":"host_id"}`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"alias", "--config", cfgPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b") {
		t.Errorf("stdout = %q, raw escape leaked", stdout.String())
	}
	if !strings.Contains(stdout.String(), `mini\x1b[31mlab`) {
		t.Errorf("stdout = %q, want the escaped form", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPeersCmd([]string{"alias", "mlab", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("stderr = %q, raw escape leaked", stderr.String())
	}
}

func TestRunPeersCmd_Alias_InvalidResponse(t *testing.T) {
	srv, cfgPath, _ := fakeSettingsDaemon(t, http.StatusOK, `not json`)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := runPeersCmd([]string{"alias", "--config", cfgPath}, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "invalid response") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// Grammar: `alias` is a top-level verb, exclusive with host, --all, --json
// and every host-only flag; at most one positional; not with --clear.
// Every rejection is exit 2 with the usage text and no request.
func TestRunPeersCmd_Alias_GrammarRejections(t *testing.T) {
	srv, cfgPath, seen := fakeSettingsDaemon(t, http.StatusOK, `{}`)
	defer srv.Close()

	cases := []struct {
		name string
		args []string
	}{
		{"two positionals", []string{"alias", "a", "b"}},
		{"name with --clear", []string{"alias", "a", "--clear"}},
		{"--clear before name", []string{"alias", "--clear", "a"}},
		{"--all with alias", []string{"--all", "alias"}},
		{"alias with --all", []string{"alias", "--all"}},
		{"--json with alias", []string{"alias", "--json"}},
		{"--json with alias set", []string{"alias", "mlab", "--json"}},
		{"host with alias", []string{"host", "alias"}},
		{"alias then host", []string{"alias", "host", "list"}},
		{"--token with alias", []string{"alias", "mlab", "--token", "x"}},
		{"--allow-bypass with alias", []string{"alias", "--allow-bypass=true"}},
		{"--commit with alias", []string{"alias", "--commit"}},
		{"--force with alias", []string{"alias", "--force"}},
		{"name with slash", []string{"alias", "a/b"}},
		{"--clear at top level", []string{"--clear"}},
		{"--clear with host", []string{"host", "list", "--clear"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := seen.calls
			args := append(append([]string{}, tc.args...), "--config", cfgPath)
			var stdout, stderr bytes.Buffer
			code := runPeersCmd(args, &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit code = %d, want 2; stderr=%q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "usage: pdx peers") {
				t.Errorf("stderr = %q, want the usage text", stderr.String())
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if seen.calls != before {
				t.Errorf("server saw %d request(s), want 0", seen.calls-before)
			}
		})
	}
}

func TestParsePeersInvocation_AliasGrammar(t *testing.T) {
	inv, _, ok := parsePeersInvocation([]string{"alias"})
	if !ok || !inv.aliasMode || inv.aliasSet || inv.aliasClear || inv.hostMode {
		t.Errorf("alias: inv=%+v ok=%v", inv, ok)
	}
	inv, _, ok = parsePeersInvocation([]string{"alias", "mlab"})
	if !ok || !inv.aliasMode || !inv.aliasSet || inv.aliasValue != "mlab" || inv.aliasClear {
		t.Errorf("alias mlab: inv=%+v ok=%v", inv, ok)
	}
	inv, _, ok = parsePeersInvocation([]string{"--clear", "alias"})
	if !ok || !inv.aliasMode || inv.aliasSet || !inv.aliasClear {
		t.Errorf("--clear alias: inv=%+v ok=%v", inv, ok)
	}
	inv, _, ok = parsePeersInvocation([]string{"alias", "mlab", "--config", "/x"})
	if !ok || inv.aliasValue != "mlab" || inv.cfgPath != "/x" {
		t.Errorf("alias mlab --config: inv=%+v ok=%v", inv, ok)
	}
}

// peersUsage documents the three alias forms.
func TestPeersUsage_ListsAliasForms(t *testing.T) {
	for _, line := range []string{
		"pdx peers alias [--config <path>]",
		"pdx peers alias <name> [--config <path>]",
		"pdx peers alias --clear [--config <path>]",
	} {
		if !strings.Contains(peersUsage, line) {
			t.Errorf("peersUsage lacks %q:\n%s", line, peersUsage)
		}
	}
}
