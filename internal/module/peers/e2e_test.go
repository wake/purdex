package peers

// Two-daemon end-to-end test (Task 10): two real Modules, A and B, each
// behind its own httptest server wrapped in the real PeerAuth chain, paired
// both ways with real tokens, exchanging one message and its native reply
// over real HTTP (fetchRemote, postDeliver), real Unix sockets
// (ccuds.WriteFrame) and fake `pdx peer-proxy` helpers. The two daemons run
// on ONE machine, so — as in production — they share the helper socket dir
// and the Claude Code registry dir: every helper either spawns is visible
// to both, and each must recognise the other's as a proxy (D9).
//
// The "Claude Code sessions" are test-owned Unix listeners with a registry
// entry each: origin.sock is A's tmux session mt1, target.sock is B's foo.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
	"github.com/wake/purdex/internal/store"
)

const (
	e2eHostA  = "hostA:1"
	e2eHostB  = "hostB:1"
	e2eAdminA = "admin-token-a"
	e2eAdminB = "admin-token-b"
	// e2eTokenAtoB is what A presents to B: A's outbound Token for "b" and
	// B's InboundToken for "a". e2eTokenBtoA is the reverse.
	e2eTokenAtoB = "tok-a-presents-to-b"
	e2eTokenBtoA = "tok-b-presents-to-a"

	e2eOriginPID  = 70001
	e2eOriginSID  = "aaaaaaaa-1111-4111-8111-111111111111"
	e2eOriginName = "origin-47"
	e2eTargetPID  = 70002
	e2eTargetSID  = "bbbbbbbb-2222-4222-8222-222222222222"
	e2eTargetName = "target-11"
	// e2eCCProcStart is the procStart both fake Claude Code sessions report.
	e2eCCProcStart = "Mon Sep 14 08:00:00 2026"

	// e2eFrameWait bounds every wait for a frame on a fake inbox (the
	// brief's "within 2 s").
	e2eFrameWait = 2 * time.Second
	// e2ePollWait bounds every audit-row / helper-release poll.
	e2ePollWait = 3 * time.Second
)

// e2eFakeHelperPID is the first pid proxyhelpertest hands out: every pid
// at or above it is a fake helper, everything below is a fake Claude Code
// session (mirrors deliverLiveness).
const e2eFakeHelperPID = 900000

// e2eLiveness is the Liveness both daemons share: every registry entry
// whose inbox exists is live unless its pid was marked dead; the Info seam
// classifies helper pids as `pdx peer-proxy` (D9) and everything else as
// claude.
type e2eLiveness struct {
	dead sync.Map // pid → struct{}
}

func (l *e2eLiveness) markDead(pid int) { l.dead.Store(pid, struct{}{}) }

func (l *e2eLiveness) startOf(pid int) time.Time {
	if pid >= e2eFakeHelperPID {
		s, _ := proxyhelpertest.ProcStart(pid)
		ts, _ := ipeers.ParseProcStart(s)
		return ts
	}
	ts, _ := ipeers.ParseProcStart(e2eCCProcStart)
	return ts
}

func (l *e2eLiveness) liveness() ipeers.Liveness {
	return ipeers.Liveness{
		Stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		PidAlive: func(pid int) bool {
			_, dead := l.dead.Load(pid)
			return !dead
		},
		StartTime: func(pid int) (time.Time, error) { return l.startOf(pid), nil },
		Info: func(pid int) (iagent.ProcessInfo, error) {
			argv := []string{"claude"}
			if pid >= e2eFakeHelperPID {
				argv = []string{"/usr/local/bin/pdx", "peer-proxy"}
			}
			return iagent.ProcessInfo{PID: pid, Argv: argv, StartTime: l.startOf(pid)}, nil
		},
	}
}

// e2eRegistryJSON is a fake Claude Code session's <pid>.json, inside tmux.
func e2eRegistryJSON(pid int, sid, name, tmux, inbox string) string {
	return `{"pid":` + strconv.Itoa(pid) + `,"sessionId":"` + sid + `","cwd":"/w","procStart":"` + e2eCCProcStart +
		`","version":"2.1.270","tmux":"` + tmux + `","messagingSocketPath":"` + inbox + `","name":"` + name + `","status":"idle"}`
}

// fakeInbox is a test-owned Claude Code inbox: a Unix listener that reads
// each connection to EOF, closes it (the healthy harness behaviour
// WriteFrame waits for) and hands every line to lines.
type fakeInbox struct {
	t     *testing.T
	sock  string
	ln    net.Listener
	lines chan string
	once  sync.Once
}

func startFakeInbox(t *testing.T, sock string) *fakeInbox {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	b := &fakeInbox{t: t, sock: sock, ln: ln, lines: make(chan string, 64)}
	t.Cleanup(b.close)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				data, _ := io.ReadAll(conn)
				conn.Close()
				for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
					if len(line) > 0 {
						b.lines <- string(line)
					}
				}
			}()
		}
	}()
	return b
}

// recv waits (bounded by e2eFrameWait) for the next frame line.
func (b *fakeInbox) recv(what string) string {
	b.t.Helper()
	select {
	case l := <-b.lines:
		return l
	case <-time.After(e2eFrameWait):
		b.t.Fatalf("%s: %s received no frame within %v", what, filepath.Base(b.sock), e2eFrameWait)
		return ""
	}
}

// none asserts no frame is pending right now (non-blocking: it is called
// only after a positive signal proved the producer has finished).
func (b *fakeInbox) none(what string) {
	b.t.Helper()
	select {
	case l := <-b.lines:
		b.t.Fatalf("%s: %s received an unexpected frame: %s", what, filepath.Base(b.sock), l)
	default:
	}
}

// close stops the listener; net unlinks the socket path, so the session
// looks gone to every liveness Stat.
func (b *fakeInbox) close() { b.once.Do(func() { b.ln.Close() }) }

// e2eDaemon is one real daemon: a Module over the fixture, its core, and
// the httptest server fronting it through the real PeerAuth chain.
type e2eDaemon struct {
	t    *testing.T
	f    *moduleFixture
	m    *Module
	core *core.Core
	srv  *httptest.Server
	// sess is this daemon's live tmux inventory, kept so a test can rename
	// one of its sessions mid-run (setSessions is mutex-guarded; the
	// server's handler goroutines read it).
	sess  *fakeSessions
	admin string
	alias string
}

type e2eDaemonOpts struct {
	hostID, alias, admin string
	peer                 config.PeerHost // the other daemon's entry (URL filled in later)
	sessions             []session.SessionInfo
	owners               map[string]agent.PaneOwner
	sockDir, regDir      string // shared with the other daemon
	live                 *e2eLiveness
}

func newE2EDaemon(t *testing.T, o e2eDaemonOpts) *e2eDaemon {
	t.Helper()
	cfg := &config.Config{
		HostID: o.hostID,
		Token:  o.admin,
		Peers:  config.PeersConfig{Alias: o.alias, Hosts: []config.PeerHost{o.peer}, Deliver: true},
	}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	sess := &fakeSessions{sessions: o.sessions}
	f := newTestModuleWith(t, fixtureOpts{
		core:        c,
		sessions:    sess,
		owners:      &fakeOwners{owners: o.owners},
		registryDir: o.regDir,
		liveness:    o.live.liveness(),
		budget:      2 * time.Second,
	})
	// Production seams the fixture leaves faked: the outbound /deliver
	// call is the real client, and the helpers bind their sockets in the
	// SHARED socket dir (the fixture's own dir would hide each daemon's
	// helpers from the other, which is exactly what this test must not do).
	f.m.post = postDeliver
	f.m.helpers.sockDir = o.sockDir

	mux := http.NewServeMux()
	f.m.RegisterRoutes(mux)
	srv := httptest.NewServer(buildOuterHandler(c, mux))
	t.Cleanup(srv.Close)
	return &e2eDaemon{t: t, f: f, m: f.m, core: c, srv: srv, sess: sess, admin: o.admin, alias: o.alias}
}

// setPeer mutates this daemon's one peer entry under the config lock.
func (d *e2eDaemon) setPeer(fn func(h *config.PeerHost)) {
	d.core.CfgMu.Lock()
	defer d.core.CfgMu.Unlock()
	fn(&d.core.Cfg.Peers.Hosts[0])
}

// do performs one real HTTP request against the daemon's server.
func (d *e2eDaemon) do(method, path, bearer string, body any) (int, []byte) {
	d.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			d.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, d.srv.URL+path, rd)
	if err != nil {
		d.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		d.t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, raw
}

// send is POST /api/peers/send as the local admin (pdx msg send).
func (d *e2eDaemon) send(req ipeers.SendRequest) (int, []byte) {
	d.t.Helper()
	return d.do(http.MethodPost, "/api/peers/send", d.admin, req)
}

// sendOK is send asserting a 200 and decoding the response.
func (d *e2eDaemon) sendOK(req ipeers.SendRequest) ipeers.SendResponse {
	d.t.Helper()
	status, raw := d.send(req)
	if status != http.StatusOK {
		d.t.Fatalf("%s: POST /send = %d, want 200; body=%s", d.alias, status, raw)
	}
	var resp ipeers.SendResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		d.t.Fatalf("%s: decode send response: %v; body=%s", d.alias, err, raw)
	}
	return resp
}

// assertAPIError asserts a refused request's status and error code.
func (d *e2eDaemon) assertAPIError(status int, raw []byte, wantStatus int, wantCode, what string) ipeers.APIError {
	d.t.Helper()
	if status != wantStatus {
		d.t.Fatalf("%s: %s: status = %d, want %d; body=%s", d.alias, what, status, wantStatus, raw)
	}
	var ae ipeers.APIError
	if err := json.Unmarshal(raw, &ae); err != nil {
		d.t.Fatalf("%s: %s: decode error body: %v; body=%s", d.alias, what, err, raw)
	}
	if ae.Error != wantCode {
		d.t.Fatalf("%s: %s: error = %q, want %q (detail %q)", d.alias, what, ae.Error, wantCode, ae.Detail)
	}
	return ae
}

// peers is GET /api/peers (local scope) as the admin.
func (d *e2eDaemon) peers() ipeers.Envelope {
	d.t.Helper()
	status, raw := d.do(http.MethodGet, "/api/peers", d.admin, nil)
	if status != http.StatusOK {
		d.t.Fatalf("%s: GET /api/peers = %d; body=%s", d.alias, status, raw)
	}
	var env ipeers.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		d.t.Fatalf("%s: decode /api/peers: %v; body=%s", d.alias, err, raw)
	}
	if !env.OK {
		d.t.Fatalf("%s: /api/peers ok=false: %s", d.alias, env.Error)
	}
	return env
}

// peerByInbox finds the inventory row whose agent inbox is sock.
func (d *e2eDaemon) peerByInbox(env ipeers.Envelope, sock string) ipeers.PeerRecord {
	d.t.Helper()
	for _, rec := range env.Peers {
		if rec.Agent != nil && rec.Agent.Inbox == sock {
			return rec
		}
	}
	d.t.Fatalf("%s: no inventory row with inbox %s: %+v", d.alias, sock, env.Peers)
	return ipeers.PeerRecord{}
}

// log is GET /api/peers/log as the admin: every audit row, oldest first.
func (d *e2eDaemon) log() []ipeers.LogEntry {
	d.t.Helper()
	status, raw := d.do(http.MethodGet, "/api/peers/log?tail=100", d.admin, nil)
	if status != http.StatusOK {
		d.t.Fatalf("%s: GET /api/peers/log = %d; body=%s", d.alias, status, raw)
	}
	var out ipeers.LogResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		d.t.Fatalf("%s: decode /api/peers/log: %v; body=%s", d.alias, err, raw)
	}
	return out.Messages
}

// awaitLog polls the audit log (bounded) until cond holds and returns it.
func (d *e2eDaemon) awaitLog(what string, cond func(rows []ipeers.LogEntry) bool) []ipeers.LogEntry {
	d.t.Helper()
	deadline := time.Now().Add(e2ePollWait)
	for {
		rows := d.log()
		if cond(rows) {
			return rows
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("%s: audit log did not reach %q within %v: %+v", d.alias, what, e2ePollWait, rows)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// proxiesJSON is the daemon's proxies.json content, trimmed.
func (d *e2eDaemon) proxiesJSON() string {
	d.t.Helper()
	data, err := os.ReadFile(d.f.proxiesPath)
	if err != nil {
		d.t.Fatalf("%s: read proxies.json: %v", d.alias, err)
	}
	return strings.TrimSpace(string(data))
}

// stop runs Module.Stop, bounded.
func (d *e2eDaemon) stop() {
	d.t.Helper()
	done := make(chan struct{})
	go func() { d.m.Stop(context.Background()); close(done) }()
	waitClosed(d.t, done, 10*time.Second, d.alias+" Module.Stop")
}

// findLogRow returns the first row matching every non-empty selector.
func findLogRow(rows []ipeers.LogEntry, direction, result, nativeMsgID string) (ipeers.LogEntry, bool) {
	for _, r := range rows {
		if direction != "" && r.Direction != direction {
			continue
		}
		if result != "" && r.Result != result {
			continue
		}
		if nativeMsgID != "" && r.NativeMsgID != nativeMsgID {
			continue
		}
		return r, true
	}
	return ipeers.LogEntry{}, false
}

func countLogRows(rows []ipeers.LogEntry, direction string) int {
	n := 0
	for _, r := range rows {
		if r.Direction == direction {
			n++
		}
	}
	return n
}

// deliveredFrame parses a frame a fake inbox received: the frame, its
// unwrapped cross-session-message and the helper socket it names.
func deliveredFrame(t *testing.T, line string) (ccuds.Frame, ccuds.Wrapper, string) {
	t.Helper()
	fr, err := ccuds.ParseFrame([]byte(line))
	if err != nil {
		t.Fatalf("ParseFrame: %v; line=%s", err, line)
	}
	w, ok := ccuds.Parse(fr.Message.Content)
	if !ok {
		t.Fatalf("content is not a cross-session-message wrapper: %q", fr.Message.Content)
	}
	sock, ok := ccuds.FromSocket(fr.From)
	if !ok {
		t.Fatalf("frame from = %q, want uds:<sock>", fr.From)
	}
	return fr, w, sock
}

// assertWrapper checks the wrapper of a delivered frame field by field.
func assertWrapper(t *testing.T, what string, w ccuds.Wrapper, helperSock, fromName, fromMode, hop, text string) {
	t.Helper()
	if w.From != "uds:"+helperSock {
		t.Errorf("%s: wrapper from = %q, want %q", what, w.From, "uds:"+helperSock)
	}
	if w.FromName != fromName {
		t.Errorf("%s: wrapper from-name = %q, want %q", what, w.FromName, fromName)
	}
	if w.FromMode != fromMode {
		t.Errorf("%s: wrapper from-mode = %q, want %q", what, w.FromMode, fromMode)
	}
	if w.HopChain != hop {
		t.Errorf("%s: wrapper hop-chain = %q, want %q", what, w.HopChain, hop)
	}
	if w.Text != text {
		t.Errorf("%s: wrapper text = %q, want %q", what, w.Text, text)
	}
}

// TestE2E_PartialOriginInventoryKeepsHelper (R2-A) drives the reply path
// between two real daemons while the ORIGIN daemon's inventory is
// partial: A's owner lookup for its tmux session mt1 fails, so A's own
// inventory has no row for the origin tuple even though the session (its
// registry entry and inbox) is alive. B delivers a message for A's origin
// through a direct /deliver (as A's daemon would), the target replies
// natively into B's helper, and B forwards the reply to A. A must answer
// 503 not_ready ("inventory partial"), never 409 target_gone: B audits the
// reply row not_ready and KEEPS its helper for a/mt1 — releasing it would
// cut the still-alive origin's reply route on a transient lookup failure.
func TestE2E_PartialOriginInventoryKeepsHelper(t *testing.T) {
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	originSock := filepath.Join(root, "origin.sock")
	targetSock := filepath.Join(root, "target.sock")
	origin := startFakeInbox(t, originSock)
	target := startFakeInbox(t, targetSock)
	// v2: every live, non-proxy registry entry gets its own entry row
	// (spec §3.4) whether or not its tmux session is listed — so a
	// decodable origin entry would resolve locally on A through its OWN
	// entry row even while mt1's owner lookup fails, defeating this test's
	// premise. The origin's registry file is undecodable instead (an
	// "unknown" candidate, Task 2's ReadRegistryDiag): no Entry exists for
	// it at all, so A's inventory genuinely carries no row for the origin
	// — partial for the reason the still-failing mt1 owner lookup gives.
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eOriginPID)+".json", "not json")
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eTargetPID)+".json", e2eRegistryJSON(e2eTargetPID, e2eTargetSID, e2eTargetName, "foo:@2.%2", targetSock))
	live := &e2eLiveness{}

	a := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostA, alias: "a", admin: e2eAdminA,
		peer:     config.PeerHost{Alias: "b", HostID: e2eHostB, Token: e2eTokenAtoB, InboundToken: e2eTokenBtoA},
		sessions: []session.SessionInfo{{Code: "mt1code", Name: "mt1", Cwd: "/w"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	// A's owner lookup for mt1 fails from the start: agent:null, partial.
	a.m.owners = &fakeOwners{errs: map[string]error{"mt1code": errors.New("resolver timeout")}}
	b := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostB, alias: "b", admin: e2eAdminB,
		peer:     config.PeerHost{Alias: "a", HostID: e2eHostA, Token: e2eTokenBtoA, InboundToken: e2eTokenAtoB},
		sessions: []session.SessionInfo{{Code: "foocode", Name: "foo", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"foocode": {AgentType: "cc", SessionID: e2eTargetSID, TmuxPaneID: "%2"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	a.setPeer(func(h *config.PeerHost) { h.URL = b.srv.URL })
	b.setPeer(func(h *config.PeerHost) { h.URL = a.srv.URL })

	if env := a.peers(); !env.Partial {
		t.Fatalf("A's inventory partial = false, want true: %+v", env.Peers)
	}

	// 1. B delivers for A's origin (the request A's daemon would send).
	originFrom := ipeers.WireFrom{
		HostID: e2eHostA, AgentSessionID: e2eOriginSID, PID: e2eOriginPID, ProcStart: e2eCCProcStart,
		PeerName: e2eOriginName, SessionName: "mt1", DeclaredMode: ipeers.ModePrompting,
	}
	ping := ipeers.DeliverRequest{
		MsgID: uuid.NewString(), From: originFrom,
		To:   ipeers.WireTo{AgentSessionID: e2eTargetSID, PID: e2eTargetPID, ProcStart: e2eCCProcStart},
		Text: "ping",
	}
	status, raw := b.do(http.MethodPost, "/api/peers/deliver", e2eTokenAtoB, ping)
	if status != http.StatusOK {
		t.Fatalf("step 1: B /deliver = %d; body=%s", status, raw)
	}
	_, _, bHelperSock := deliveredFrame(t, target.recv("step 1"))
	if _, ok := b.m.helpers.FindBySock(bHelperSock); !ok {
		t.Fatalf("step 1: reply address %q is not one of B's helpers", bHelperSock)
	}

	// 2. The target replies natively; B forwards to A, whose inventory is
	// partial: not_ready, not target_gone.
	native := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, frameLine(t, native, "user", "uds:"+targetSock, ccuds.Wrapper{
		From: "uds:" + targetSock, FromName: "foo", FromMode: ipeers.ModePrompting, Text: "pong",
	}.Format()))
	bRows := b.awaitLog("reply not_ready", func(rows []ipeers.LogEntry) bool {
		_, ok := findLogRow(rows, store.DirReply, ipeers.ErrNotReady, native)
		return ok
	})
	rep, _ := findLogRow(bRows, store.DirReply, ipeers.ErrNotReady, native)
	if rep.Error != "inventory partial" || rep.FromSessionID != e2eTargetSID || rep.ToSessionID != e2eOriginSID {
		t.Errorf("step 2: B reply row = %+v, want not_ready/\"inventory partial\" from the target to the origin", rep)
	}
	if _, gone := findLogRow(bRows, store.DirReply, ipeers.ErrTargetGone, ""); gone {
		t.Errorf("step 2: B audited a target_gone reply: %+v", bRows)
	}
	a.awaitLog("in not_ready for the unresolved origin", func(rows []ipeers.LogEntry) bool {
		r, ok := findLogRow(rows, store.DirIn, ipeers.ErrNotReady, "")
		return ok && r.MsgID == rep.MsgID && r.Error == "inventory partial"
	})
	origin.none("step 2")

	// 3. B's helper for a/mt1 survives: same instance, socket on disk.
	time.Sleep(50 * time.Millisecond) // a wrong Release would be in flight by now
	h, still := b.m.helpers.FindBySock(bHelperSock)
	if !still {
		t.Fatalf("step 3: B's helper for a/mt1 released on not_ready")
	}
	b.m.helpers.mu.Lock()
	state := h.state
	b.m.helpers.mu.Unlock()
	if state != helperReady {
		t.Errorf("step 3: B's helper state = %v, want ready", state)
	}
	if !proxyhelpertest.Exists(bHelperSock) {
		t.Errorf("step 3: B's helper socket %s gone", bHelperSock)
	}
	if b.f.fake.Stops() != 0 {
		t.Errorf("step 3: B helper stops = %d, want 0", b.f.fake.Stops())
	}

	a.stop()
	b.stop()
}

// TestE2E_TwoDaemons drives the whole bridge between two real daemons on
// one machine (see the file comment): the nine steps of the Task 10 brief.
func TestE2E_TwoDaemons(t *testing.T) {
	// One shared short socket dir and one shared registry dir: two
	// daemons on one machine share /tmp/cc-socks and ~/.claude/sessions.
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	originSock := filepath.Join(root, "origin.sock")
	targetSock := filepath.Join(root, "target.sock")
	origin := startFakeInbox(t, originSock) // A's tmux session mt1
	target := startFakeInbox(t, targetSock) // B's tmux session foo
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eOriginPID)+".json", e2eRegistryJSON(e2eOriginPID, e2eOriginSID, e2eOriginName, "mt1:@1.%1", originSock))
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eTargetPID)+".json", e2eRegistryJSON(e2eTargetPID, e2eTargetSID, e2eTargetName, "foo:@2.%2", targetSock))
	live := &e2eLiveness{}

	a := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostA, alias: "a", admin: e2eAdminA,
		peer:     config.PeerHost{Alias: "b", HostID: e2eHostB, Token: e2eTokenAtoB, InboundToken: e2eTokenBtoA},
		sessions: []session.SessionInfo{{Code: "mt1code", Name: "mt1", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"mt1code": {AgentType: "cc", SessionID: e2eOriginSID, TmuxPaneID: "%1"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	b := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostB, alias: "b", admin: e2eAdminB,
		peer:     config.PeerHost{Alias: "a", HostID: e2eHostA, Token: e2eTokenBtoA, InboundToken: e2eTokenAtoB},
		sessions: []session.SessionInfo{{Code: "foocode", Name: "foo", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"foocode": {AgentType: "cc", SessionID: e2eTargetSID, TmuxPaneID: "%2"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	a.setPeer(func(h *config.PeerHost) { h.URL = b.srv.URL })
	b.setPeer(func(h *config.PeerHost) { h.URL = a.srv.URL })

	originFrom := ipeers.WireFrom{
		HostID: e2eHostA, AgentSessionID: e2eOriginSID, PID: e2eOriginPID, ProcStart: e2eCCProcStart,
		PeerName: e2eOriginName, SessionName: "mt1", DeclaredMode: ipeers.ModePrompting,
	}
	targetTo := ipeers.WireTo{AgentSessionID: e2eTargetSID, PID: e2eTargetPID, ProcStart: e2eCCProcStart}
	// v3 (spec §4.5): the resolved row's address is
	// "<alias>/<canonical>:<suffix>". Neither session has claimed a label
	// and it would not matter if they had — the head is derived from the
	// sessionId either way.
	targetAddr := "b/" + ipeers.CanonicalID(e2eTargetSID) + ":foo-" + e2eTargetName
	// from-name (spec §4.4): each side names the other's helper after the
	// sender's own "<alias>/<canonical>:<suffix>" address.
	originName := "a/" + ipeers.CanonicalID(e2eOriginSID) + ":mt1-" + e2eOriginName
	targetName := targetAddr

	// The baseline for step 9 includes every long-lived goroutine of the
	// environment (servers, listeners, database/sql's opener); anything
	// the traffic below starts must be gone after both Stops.
	before := runtime.NumGoroutine()

	// ---- 1. A sends "ping" to b/foo; target.sock receives one frame. ----
	sent := a.sendOK(ipeers.SendRequest{To: "b/foo", Text: "ping", OriginInbox: originSock})
	if sent.OneWay {
		t.Errorf("step 1: one_way = true, want false (B has a verified route back to A)")
	}
	if sent.Result != ipeers.ResultDelivered || sent.EffectiveMode != ipeers.ModePrompting {
		t.Errorf("step 1: result/effective_mode = %q/%q, want delivered/prompting", sent.Result, sent.EffectiveMode)
	}
	if sent.ToHostID != e2eHostB || sent.ToAddress != targetAddr || sent.To != targetTo {
		t.Errorf("step 1: response to = %s %s %+v, want %s %s %+v", sent.ToHostID, sent.ToAddress, sent.To, e2eHostB, targetAddr, targetTo)
	}
	if !ipeers.IsUUID(sent.MsgID) {
		t.Errorf("step 1: msg_id = %q, want a UUID", sent.MsgID)
	}

	line := target.recv("step 1")
	fr, w, bHelperSock := deliveredFrame(t, line)
	target.none("step 1")
	if fr.MsgID != sent.MsgID || fr.Type != "user" {
		t.Errorf("step 1: frame msg_id/type = %q/%q, want %q/user", fr.MsgID, fr.Type, sent.MsgID)
	}
	if !strings.HasPrefix(bHelperSock, sockDir+"/") {
		t.Errorf("step 1: reply address %q is not under the shared sock dir %s", bHelperSock, sockDir)
	}
	bHelper, ok := b.m.helpers.FindBySock(bHelperSock)
	if !ok {
		t.Fatalf("step 1: reply address %q is not one of B's helpers", bHelperSock)
	}
	if got := b.m.helpers.Name(bHelper); got != originName {
		t.Errorf("step 1: B's helper name = %q, want %s", got, originName)
	}
	assertWrapper(t, "step 1", w, bHelperSock, originName, ipeers.ModePrompting, "", "ping")

	// nativeReply is what the target Claude writes into B's helper socket:
	// a native reply wrapped by its own harness (D3), naming target.sock.
	nativeReply := func(msgID, mode, text string) string {
		from := "uds:" + targetSock
		return frameLine(t, msgID, "user", from, ccuds.Wrapper{
			From: from, FromName: "foo", FromMode: mode, HopChain: "abc", Text: text,
		}.Format())
	}

	// ---- 2. The target replies natively into B's helper. ----
	native1 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, nativeReply(native1, ipeers.ModeBypass, "pong"))

	// ---- 3. origin.sock receives the reply through A's helper for b/foo. ----
	line = origin.recv("step 3")
	fr, w, aHelperSock := deliveredFrame(t, line)
	origin.none("step 3")
	reply1ID := fr.MsgID
	if !ipeers.IsUUID(reply1ID) || reply1ID == native1 {
		t.Errorf("step 3: frame msg_id = %q, want a fresh UUID (never the native %s)", reply1ID, native1)
	}
	if !strings.HasPrefix(aHelperSock, sockDir+"/") || aHelperSock == bHelperSock {
		t.Errorf("step 3: reply address %q, want A's own helper under %s", aHelperSock, sockDir)
	}
	aHelper, ok := a.m.helpers.FindBySock(aHelperSock)
	if !ok {
		t.Fatalf("step 3: reply address %q is not one of A's helpers", aHelperSock)
	}
	if got := a.m.helpers.Name(aHelper); got != targetName {
		t.Errorf("step 3: A's helper name = %q, want %s", got, targetName)
	}
	// A's entry for B has AllowBypass false: the declared bypass is clamped.
	assertWrapper(t, "step 3", w, aHelperSock, targetName, ipeers.ModePrompting, "abc", "pong")

	// ---- 4. Audit on both sides. ----
	aRows := a.awaitLog("out delivered + in delivered", func(rows []ipeers.LogEntry) bool {
		_, out := findLogRow(rows, store.DirOut, ipeers.ResultDelivered, "")
		_, in := findLogRow(rows, store.DirIn, ipeers.ResultDelivered, "")
		return out && in
	})
	if len(aRows) != 2 {
		t.Errorf("step 4: A audit rows = %d, want 2: %+v", len(aRows), aRows)
	}
	aOut, _ := findLogRow(aRows, store.DirOut, ipeers.ResultDelivered, "")
	if aOut.MsgID != sent.MsgID || aOut.FromHostID != e2eHostA || aOut.FromSessionID != e2eOriginSID ||
		aOut.ToHostID != e2eHostB || aOut.ToSessionID != e2eTargetSID ||
		aOut.DeclaredMode != ipeers.ModePrompting || aOut.EffectiveMode != ipeers.ModePrompting || aOut.Error != "" {
		t.Errorf("step 4: A out row = %+v", aOut)
	}
	aIn, _ := findLogRow(aRows, store.DirIn, ipeers.ResultDelivered, "")
	if aIn.MsgID != reply1ID || aIn.FromHostID != e2eHostB || aIn.FromSessionID != e2eTargetSID ||
		aIn.ToHostID != e2eHostA || aIn.ToSessionID != e2eOriginSID ||
		aIn.DeclaredMode != ipeers.ModeBypass || aIn.EffectiveMode != ipeers.ModePrompting || aIn.Error != "" {
		t.Errorf("step 4: A in row = %+v", aIn)
	}
	bRows := b.awaitLog("in delivered + reply delivered", func(rows []ipeers.LogEntry) bool {
		_, in := findLogRow(rows, store.DirIn, ipeers.ResultDelivered, "")
		_, rep := findLogRow(rows, store.DirReply, ipeers.ResultDelivered, native1)
		return in && rep
	})
	if len(bRows) != 2 {
		t.Errorf("step 4: B audit rows = %d, want 2: %+v", len(bRows), bRows)
	}
	bIn, _ := findLogRow(bRows, store.DirIn, ipeers.ResultDelivered, "")
	if bIn.MsgID != sent.MsgID || bIn.FromHostID != e2eHostA || bIn.FromSessionID != e2eOriginSID ||
		bIn.ToHostID != e2eHostB || bIn.ToSessionID != e2eTargetSID ||
		bIn.DeclaredMode != ipeers.ModePrompting || bIn.EffectiveMode != ipeers.ModePrompting || bIn.Error != "" {
		t.Errorf("step 4: B in row = %+v", bIn)
	}
	bReply, _ := findLogRow(bRows, store.DirReply, ipeers.ResultDelivered, native1)
	if bReply.MsgID != reply1ID || bReply.NativeMsgID != native1 || bReply.FromHostID != e2eHostB || bReply.FromSessionID != e2eTargetSID ||
		bReply.ToHostID != e2eHostA || bReply.ToSessionID != e2eOriginSID ||
		bReply.DeclaredMode != ipeers.ModeBypass || bReply.EffectiveMode != ipeers.ModePrompting || bReply.Error != "" {
		t.Errorf("step 4: B reply row = %+v", bReply)
	}

	// ---- 5. A allows bypass from B: the same reply now arrives as bypass. ----
	a.setPeer(func(h *config.PeerHost) { h.AllowBypass = true })
	native2 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, nativeReply(native2, ipeers.ModeBypass, "pong2"))
	line = origin.recv("step 5")
	fr, w, sock := deliveredFrame(t, line)
	origin.none("step 5")
	if sock != aHelperSock {
		t.Errorf("step 5: reply address = %q, want the same A helper %q", sock, aHelperSock)
	}
	assertWrapper(t, "step 5", w, aHelperSock, targetName, ipeers.ModeBypass, "abc", "pong2")
	reply2ID := fr.MsgID
	b.awaitLog("reply 2 delivered as bypass", func(rows []ipeers.LogEntry) bool {
		r, ok := findLogRow(rows, store.DirReply, ipeers.ResultDelivered, native2)
		return ok && r.MsgID == reply2ID && r.EffectiveMode == ipeers.ModeBypass
	})
	a.awaitLog("in 2 delivered as bypass", func(rows []ipeers.LogEntry) bool {
		for _, r := range rows {
			if r.MsgID == reply2ID && r.Direction == store.DirIn {
				return r.Result == ipeers.ResultDelivered && r.EffectiveMode == ipeers.ModeBypass
			}
		}
		return false
	})

	// ---- 6. Shared-registry boundary (M10). ----
	// (a) A's inventory lists B's helper (and hides its own) as a proxy.
	env := a.peers()
	for _, sock := range []string{bHelperSock, aHelperSock} {
		rec := a.peerByInbox(env, sock)
		if rec.Agent.Type != "proxy" || rec.Deliverable || rec.Reason != "proxy" {
			t.Errorf("step 6a: A's row for %s = type %q deliverable %v reason %q, want proxy/false/proxy", filepath.Base(sock), rec.Agent.Type, rec.Deliverable, rec.Reason)
		}
	}
	// (b) A refuses to attribute a send to B's helper as its origin.
	status, raw := a.send(ipeers.SendRequest{To: "b/foo", Text: "from a proxy", OriginInbox: bHelperSock})
	a.assertAPIError(status, raw, http.StatusBadRequest, ipeers.ErrOriginUnknown, "step 6b: send with a helper as origin")
	// (c) B refuses to deliver to A's helper: it is a proxy row, not a target.
	aHelperTo := ipeers.WireTo{
		AgentSessionID: helperRegistrySessionID(t, regDir, aHelper.pid),
		PID:            aHelper.pid,
		ProcStart:      aHelper.procStart,
	}
	toHelper := ipeers.DeliverRequest{MsgID: uuid.NewString(), From: originFrom, To: aHelperTo, Text: "to a proxy"}
	if err := toHelper.Validate(); err != nil {
		t.Fatalf("step 6c: request does not validate: %v", err)
	}
	status, raw = b.do(http.MethodPost, "/api/peers/deliver", e2eTokenAtoB, toHelper)
	b.assertAPIError(status, raw, http.StatusConflict, ipeers.ErrTargetGone, "step 6c: deliver to a helper")
	b.awaitLog("in target_gone for the helper target", func(rows []ipeers.LogEntry) bool {
		r, ok := findLogRow(rows, store.DirIn, ipeers.ErrTargetGone, "")
		return ok && r.MsgID == toHelper.MsgID
	})
	// (d) A frame in B's helper naming A's helper as its reply address is
	// audited proxy_to_proxy and forwarded nowhere.
	aInBefore := countLogRows(a.log(), store.DirIn)
	native3 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, frameLine(t, native3, "user", "uds:"+aHelperSock, ccuds.Wrapper{
		From: "uds:" + aHelperSock, FromName: "b/foo", FromMode: ipeers.ModePrompting, Text: "loop",
	}.Format()))
	bRows = b.awaitLog("reply proxy_to_proxy", func(rows []ipeers.LogEntry) bool {
		_, ok := findLogRow(rows, store.DirReply, ipeers.ErrProxyToProxy, native3)
		return ok
	})
	p2p, _ := findLogRow(bRows, store.DirReply, ipeers.ErrProxyToProxy, native3)
	if p2p.ToHostID != e2eHostA || p2p.ToSessionID != e2eOriginSID || p2p.FromSessionID != "" {
		t.Errorf("step 6d: proxy_to_proxy row = %+v, want bound to the helper's origin with no replier", p2p)
	}
	origin.none("step 6d")
	if n := countLogRows(a.log(), store.DirIn); n != aInBefore {
		t.Errorf("step 6d: A in rows = %d, want %d (nothing may be forwarded)", n, aInBefore)
	}

	// ---- 7. The origin session dies: B's next reply is target_gone and
	// B's helper for a/mt1 is released. ----
	origin.close()
	if err := os.Remove(filepath.Join(regDir, strconv.Itoa(e2eOriginPID)+".json")); err != nil {
		t.Fatal(err)
	}
	live.markDead(e2eOriginPID)
	native4 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, nativeReply(native4, ipeers.ModeBypass, "pong4"))
	bRows = b.awaitLog("reply target_gone", func(rows []ipeers.LogEntry) bool {
		_, ok := findLogRow(rows, store.DirReply, ipeers.ErrTargetGone, native4)
		return ok
	})
	gone, _ := findLogRow(bRows, store.DirReply, ipeers.ErrTargetGone, native4)
	if gone.FromSessionID != e2eTargetSID || gone.ToHostID != e2eHostA || gone.ToSessionID != e2eOriginSID {
		t.Errorf("step 7: B target_gone row = %+v", gone)
	}
	a.awaitLog("in target_gone for the dead origin", func(rows []ipeers.LogEntry) bool {
		r, ok := findLogRow(rows, store.DirIn, ipeers.ErrTargetGone, "")
		return ok && r.MsgID == gone.MsgID
	})
	eventually(t, e2ePollWait, func() bool {
		_, still := b.m.helpers.FindBySock(bHelperSock)
		return !still && !proxyhelpertest.Exists(bHelperSock)
	}, "B's helper for a/mt1 released after target_gone")
	if _, still := a.m.helpers.FindBySock(aHelperSock); !still {
		t.Errorf("step 7: A's helper for b/foo was released; only B's origin helper may go")
	}

	// ---- 8. A can no longer attribute the dead origin. ----
	status, raw = a.send(ipeers.SendRequest{To: "b/foo", Text: "again", OriginInbox: originSock})
	a.assertAPIError(status, raw, http.StatusBadRequest, ipeers.ErrOriginUnknown, "step 8: send from the dead origin")

	// ---- 9. Stop both: every helper socket gone, both proxies.json empty,
	// no goroutine left behind. ----
	a.stop()
	b.stop()
	for _, sock := range []string{aHelperSock, bHelperSock} {
		if proxyhelpertest.Exists(sock) {
			t.Errorf("step 9: helper socket %s still exists after Stop", sock)
		}
	}
	entries, err := os.ReadDir(regDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json")); err == nil && pid >= e2eFakeHelperPID {
			t.Errorf("step 9: helper registry file %s still exists after Stop", e.Name())
		}
	}
	if got := a.proxiesJSON(); got != "[]" {
		t.Errorf("step 9: A proxies.json = %s, want []", got)
	}
	if got := b.proxiesJSON(); got != "[]" {
		t.Errorf("step 9: B proxies.json = %s, want []", got)
	}
	// The servers' Close also closes the client side's idle keep-alive
	// connections (http.DefaultTransport), so their goroutines are counted.
	a.srv.Close()
	b.srv.Close()
	target.close()
	assertNoGoroutineGrowth(t, before)
}

// TestE2E_Labels drives the address primitives across the same two-daemon
// bridge as TestE2E_TwoDaemons, now under v3: claiming a label on B,
// sending to that conversation by its CANONICAL id, the 404 a bare label
// gets instead (D3 — a name is not an address), the "tmux:" bypass form,
// the retired "cc:" form, and what survives of the R2-1 scenario (spec
// §3.3). R2-1's first half inverts here: a Desktop session holding the
// label "foo" no longer shadows a same-named tmux session, so a bare
// "b/foo" reaches the tmux session. Its second half is unchanged and is
// the part that mattered: once the holder's own registry file becomes
// unreadable the inventory is partial, and a bare name must refuse
// not_ready rather than fall through to tier 2 — while "tmux:<name>"
// still bypasses tier 1 entirely and keeps delivering.
func TestE2E_Labels(t *testing.T) {
	// Setup identical to TestE2E_TwoDaemons: A's tmux session mt1 is the
	// origin, B's tmux session foo is the target.
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	originSock := filepath.Join(root, "origin.sock")
	targetSock := filepath.Join(root, "target.sock")
	origin := startFakeInbox(t, originSock)
	target := startFakeInbox(t, targetSock)
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eOriginPID)+".json", e2eRegistryJSON(e2eOriginPID, e2eOriginSID, e2eOriginName, "mt1:@1.%1", originSock))
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eTargetPID)+".json", e2eRegistryJSON(e2eTargetPID, e2eTargetSID, e2eTargetName, "foo:@2.%2", targetSock))
	live := &e2eLiveness{}

	a := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostA, alias: "a", admin: e2eAdminA,
		peer:     config.PeerHost{Alias: "b", HostID: e2eHostB, Token: e2eTokenAtoB, InboundToken: e2eTokenBtoA},
		sessions: []session.SessionInfo{{Code: "mt1code", Name: "mt1", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"mt1code": {AgentType: "cc", SessionID: e2eOriginSID, TmuxPaneID: "%1"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	b := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostB, alias: "b", admin: e2eAdminB,
		peer:     config.PeerHost{Alias: "a", HostID: e2eHostA, Token: e2eTokenBtoA, InboundToken: e2eTokenAtoB},
		sessions: []session.SessionInfo{{Code: "foocode", Name: "foo", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"foocode": {AgentType: "cc", SessionID: e2eTargetSID, TmuxPaneID: "%2"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	a.setPeer(func(h *config.PeerHost) { h.URL = b.srv.URL })
	b.setPeer(func(h *config.PeerHost) { h.URL = a.srv.URL })

	targetTo := ipeers.WireTo{AgentSessionID: e2eTargetSID, PID: e2eTargetPID, ProcStart: e2eCCProcStart}

	// ---- 1. B's target claims "purdex-tester" through B's own /self/label. ----
	status, body := b.do(http.MethodPut, "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: targetSock, Label: "purdex-tester"})
	if status != http.StatusOK {
		t.Fatalf("step 1: claim = %d %s", status, body)
	}

	// ---- 2. A sends to the target's canonical id; delivered to
	// target.sock, and to_address is the resolved row's own address. The
	// label claimed in step 1 is NOT a way to reach it: the same send
	// addressed to "b/purdex-tester" is a plain 404 and nothing is
	// delivered. That is D3, end to end. ----
	sent := a.sendOK(ipeers.SendRequest{To: "b/" + ipeers.CanonicalID(e2eTargetSID), Text: "ping", OriginInbox: originSock})
	wantAddr := "b/" + ipeers.CanonicalID(e2eTargetSID) + ":foo-" + e2eTargetName
	if sent.ToAddress != wantAddr || sent.To != targetTo {
		t.Errorf("step 2: to = %s %+v, want %s %+v", sent.ToAddress, sent.To, wantAddr, targetTo)
	}
	target.recv("step 2")

	st, raw := a.send(ipeers.SendRequest{To: "b/purdex-tester", Text: "by label", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusNotFound, ipeers.ErrPeerNotFound, "a label is not an address")
	target.none("step 2: a label must never route")

	// ---- 3. tmux: form and bare tmux name still deliver; cc: does not. ----
	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo", Text: "x", OriginInbox: originSock})
	target.recv("step 3a")
	a.sendOK(ipeers.SendRequest{To: "b/foo", Text: "x", OriginInbox: originSock})
	target.recv("step 3b")
	st, raw = a.send(ipeers.SendRequest{To: "b/cc:" + e2eTargetName, Text: "x", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusNotFound, ipeers.ErrPeerNotFound, "cc: form")

	// ---- 4. Native reply from the target through B's helper reaching the
	// origin is the same reply path TestE2E_TwoDaemons drives end to end
	// (v2 from-name included, spec §3.5); not repeated here. ----

	// ---- 5. R2-1 (spec §3.3), as v3 leaves it: a Desktop session on B
	// holds the LABEL "foo" while B also has a tmux session named foo.
	// The label no longer shadows the tmux name — a bare "b/foo" misses
	// tier 1 and lands on the tmux session (D3). But when the holder's own
	// registry file becomes unreadable, B's inventory goes partial and the
	// same bare "b/foo" must be 503 not_ready — never a tier-2 delivery on
	// an inventory that may be hiding the row that would have matched —
	// and "b/tmux:foo" still delivers. ----
	holderSock := filepath.Join(root, "holder.sock")
	holder := startFakeInbox(t, holderSock)
	const holderPID, holderSID = 31337, "holder-sid"
	writeRegistryFixture(t, regDir, strconv.Itoa(holderPID)+".json", e2eRegistryJSON(holderPID, holderSID, "holder-1", "", holderSock)) // no tmux: entry row on B
	st, raw = b.do(http.MethodPut, "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: holderSock, Label: "foo"})
	if st != http.StatusOK {
		t.Fatalf("step 5: holder claim = %d %s", st, raw)
	}
	a.sendOK(ipeers.SendRequest{To: "b/foo", Text: "to-tmux", OriginInbox: originSock})
	target.recv("step 5a: a label no longer shadows the tmux name")
	holder.none("step 5a")

	writeRegistryFixture(t, regDir, strconv.Itoa(holderPID)+".json", "{") // holder unreadable, pid still alive per fake liveness
	st, raw = a.send(ipeers.SendRequest{To: "b/foo", Text: "x", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusServiceUnavailable, ipeers.ErrNotReady, "partial bare name")
	target.none("step 5b: no tier-2 delivery on a partial inventory")

	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo", Text: "x", OriginInbox: originSock})
	target.recv("step 5c")

	origin.close()
	target.close()
	holder.close()
	a.stop()
	b.stop()
}

// TestE2E_CanonicalSurvivesATmuxRename is spec §9's end-to-end rename
// requirement, and it is the one property this whole change exists to
// deliver: an address follows the conversation's sessionId, so renaming the
// tmux session the target lives in cannot move it.
//
// It has to be end to end. The unit tests around CanonicalID and Resolve
// prove the head is name-independent in isolation, but the failure v2 had
// (spec §2 P1) lived in the JOIN — the remote fetch, normalizeRemoteRows,
// Resolve, and the delivery tuple that comes out of it — where a tmux name
// entered the address by a route no single unit covered.
//
// The rename is modelled exactly as production presents it, and the two
// halves are what make the test worth writing:
//
//   - B's LIVE tmux inventory reports the new name. A rename leaves the
//     tmux session id alone, so B's session code ("foocode") and its owner
//     resolution are untouched.
//   - B's REGISTRY entry for the target keeps "foo:@2.%2". Claude Code
//     froze that field when the agent started and copies it through every
//     rewrite, so it stays stale for the agent's whole life.
//
// Four things are asserted across the rename: the same canonical still
// delivers; the delivery TUPLE is unchanged (same process, not a lookalike
// re-resolved by name); the reported address's suffix follows the LIVE name
// (the display half, spec §5.4 — pass the frozen one here and the operator
// is handed an address naming a session that no longer exists); and the
// target's label is not an address before or after (D3).
//
// The bare tmux name is the deliberate contrast: "b/foo" delivered before
// the rename and is a 404 after, because tier 2 addresses a PLACE and a
// place is exactly the thing a rename moves. That is the difference the
// canonical id buys.
func TestE2E_CanonicalSurvivesATmuxRename(t *testing.T) {
	// Setup identical to TestE2E_Labels: A's tmux session mt1 is the
	// origin, B's tmux session foo is the target.
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	originSock := filepath.Join(root, "origin.sock")
	targetSock := filepath.Join(root, "target.sock")
	origin := startFakeInbox(t, originSock)
	target := startFakeInbox(t, targetSock)
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eOriginPID)+".json", e2eRegistryJSON(e2eOriginPID, e2eOriginSID, e2eOriginName, "mt1:@1.%1", originSock))
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eTargetPID)+".json", e2eRegistryJSON(e2eTargetPID, e2eTargetSID, e2eTargetName, "foo:@2.%2", targetSock))
	live := &e2eLiveness{}

	a := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostA, alias: "a", admin: e2eAdminA,
		peer:     config.PeerHost{Alias: "b", HostID: e2eHostB, Token: e2eTokenAtoB, InboundToken: e2eTokenBtoA},
		sessions: []session.SessionInfo{{Code: "mt1code", Name: "mt1", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"mt1code": {AgentType: "cc", SessionID: e2eOriginSID, TmuxPaneID: "%1"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	b := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostB, alias: "b", admin: e2eAdminB,
		peer:     config.PeerHost{Alias: "a", HostID: e2eHostA, Token: e2eTokenBtoA, InboundToken: e2eTokenAtoB},
		sessions: []session.SessionInfo{{Code: "foocode", Name: "foo", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"foocode": {AgentType: "cc", SessionID: e2eTargetSID, TmuxPaneID: "%2"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	a.setPeer(func(h *config.PeerHost) { h.URL = b.srv.URL })
	b.setPeer(func(h *config.PeerHost) { h.URL = a.srv.URL })

	canonical := "b/" + ipeers.CanonicalID(e2eTargetSID)
	targetTo := ipeers.WireTo{AgentSessionID: e2eTargetSID, PID: e2eTargetPID, ProcStart: e2eCCProcStart}

	// ---- 1. The target names itself, so the label is in play throughout. ----
	status, body := b.do(http.MethodPut, "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: targetSock, Label: "purdex-tester"})
	if status != http.StatusOK {
		t.Fatalf("step 1: claim = %d %s", status, body)
	}

	// ---- 2. Before the rename: the canonical delivers; the label does not. ----
	sent := a.sendOK(ipeers.SendRequest{To: canonical, Text: "before", OriginInbox: originSock})
	if want := canonical + ":foo-" + e2eTargetName; sent.ToAddress != want || sent.To != targetTo {
		t.Fatalf("step 2: to = %s %+v, want %s %+v", sent.ToAddress, sent.To, want, targetTo)
	}
	target.recv("step 2")
	st, raw := a.send(ipeers.SendRequest{To: "b/purdex-tester", Text: "by label", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusNotFound, ipeers.ErrPeerNotFound, "step 2: a label is not an address")

	// The bare tmux name works here, which is what makes step 5 meaningful.
	a.sendOK(ipeers.SendRequest{To: "b/foo", Text: "by place", OriginInbox: originSock})
	target.recv("step 2: bare tmux name")

	// ---- 3. `tmux rename-session foo foo-renamed` on B. The registry file
	// is NOT rewritten: the target's entry still says "foo:@2.%2". ----
	b.sess.setSessions([]session.SessionInfo{{Code: "foocode", Name: "foo-renamed", Cwd: "/w"}})

	// ---- 4. The SAME canonical still reaches the SAME process, and the
	// address B reports for it now names the live session. ----
	sent = a.sendOK(ipeers.SendRequest{To: canonical, Text: "after", OriginInbox: originSock})
	if sent.To != targetTo {
		t.Errorf("step 4: to tuple = %+v, want the unchanged %+v", sent.To, targetTo)
	}
	if want := canonical + ":foo-renamed-" + e2eTargetName; sent.ToAddress != want {
		t.Errorf("step 4: to_address = %q, want %q (the suffix must follow the live tmux name, spec §5.4)", sent.ToAddress, want)
	}
	target.recv("step 4: the canonical survived the rename")
	target.none("step 4")

	// ---- 5. What the rename DID move: the place. The old bare name is
	// gone, the new one works, and the label is still not an address. ----
	st, raw = a.send(ipeers.SendRequest{To: "b/foo", Text: "stale place", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusNotFound, ipeers.ErrPeerNotFound, "step 5: the old tmux name")
	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo-renamed", Text: "new place", OriginInbox: originSock})
	target.recv("step 5: the new tmux name")
	st, raw = a.send(ipeers.SendRequest{To: "b/purdex-tester", Text: "by label", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusNotFound, ipeers.ErrPeerNotFound, "step 5: a label is still not an address")
	target.none("step 5: neither a label nor a stale place delivered")

	origin.close()
	target.close()
	a.stop()
	b.stop()
}

// TestE2E_LabelAmbiguityUnderUnknownFile drives X1 (spec §3.2, "tier 1
// exactly one match, registry incomplete") across the two-daemon bridge:
// ONE conversation with TWO live processes outside tmux on B — two
// registry entries sharing a sessionId, each with its own fake inbox. The
// head both rows share is that conversation's canonical id (v3: one
// sessionId, one address, nothing claimed), and by it the pair is 409
// ambiguous. When one process's registry file becomes unreadable while its
// pid is still alive, the remaining single row must NOT be delivered to —
// the hidden file may be the other process of that very conversation — so
// the send is 503 not_ready and NEITHER inbox receives a frame.
// "b/tmux:foo" bypasses tier 1 and still delivers to B's tmux target.
func TestE2E_LabelAmbiguityUnderUnknownFile(t *testing.T) {
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	originSock := filepath.Join(root, "origin.sock")
	targetSock := filepath.Join(root, "target.sock")
	origin := startFakeInbox(t, originSock)
	target := startFakeInbox(t, targetSock)
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eOriginPID)+".json", e2eRegistryJSON(e2eOriginPID, e2eOriginSID, e2eOriginName, "mt1:@1.%1", originSock))
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eTargetPID)+".json", e2eRegistryJSON(e2eTargetPID, e2eTargetSID, e2eTargetName, "foo:@2.%2", targetSock))
	live := &e2eLiveness{}

	a := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostA, alias: "a", admin: e2eAdminA,
		peer:     config.PeerHost{Alias: "b", HostID: e2eHostB, Token: e2eTokenAtoB, InboundToken: e2eTokenBtoA},
		sessions: []session.SessionInfo{{Code: "mt1code", Name: "mt1", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"mt1code": {AgentType: "cc", SessionID: e2eOriginSID, TmuxPaneID: "%1"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	b := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostB, alias: "b", admin: e2eAdminB,
		peer:     config.PeerHost{Alias: "a", HostID: e2eHostA, Token: e2eTokenBtoA, InboundToken: e2eTokenAtoB},
		sessions: []session.SessionInfo{{Code: "foocode", Name: "foo", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"foocode": {AgentType: "cc", SessionID: e2eTargetSID, TmuxPaneID: "%2"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	a.setPeer(func(h *config.PeerHost) { h.URL = b.srv.URL })
	b.setPeer(func(h *config.PeerHost) { h.URL = a.srv.URL })

	// ---- 1. Two live processes of ONE conversation outside tmux on B. ----
	const twinSID = "cccccccc-3333-4333-8333-333333333333"
	const twin1PID, twin2PID = 41001, 41002
	twin1Sock := filepath.Join(root, "twin1.sock")
	twin2Sock := filepath.Join(root, "twin2.sock")
	twin1 := startFakeInbox(t, twin1Sock)
	twin2 := startFakeInbox(t, twin2Sock)
	writeRegistryFixture(t, regDir, strconv.Itoa(twin1PID)+".json", e2eRegistryJSON(twin1PID, twinSID, "twin-1", "", twin1Sock))
	writeRegistryFixture(t, regDir, strconv.Itoa(twin2PID)+".json", e2eRegistryJSON(twin2PID, twinSID, "twin-2", "", twin2Sock))
	// The head both rows share is the conversation's canonical id, which
	// they carry by construction: one sessionId, one address, nothing
	// claimed and nothing to claim. The X1 rule under test — a single
	// tier-1 hit beside an undecodable live file is 503, never a delivery
	// — is what this exercises.
	head := ipeers.CanonicalID(twinSID)

	// Both are entry rows on B carrying that one canonical id.
	env := b.peers()
	if env.Partial || len(env.UnknownRegistryFiles) != 0 {
		t.Fatalf("step 1: B's inventory partial=%v unknown=%v, want complete", env.Partial, env.UnknownRegistryFiles)
	}
	for _, sock := range []string{twin1Sock, twin2Sock} {
		if rec := b.peerByInbox(env, sock); rec.Canonical != head || rec.RowKind != "entry" {
			t.Fatalf("step 1: row for %s = %+v, want entry row with canonical %q", filepath.Base(sock), rec, head)
		}
	}

	// ---- 2. By the canonical id: ambiguous, both candidates, nothing
	// delivered. ----
	st, raw := a.send(ipeers.SendRequest{To: "b/" + head, Text: "x", OriginInbox: originSock})
	ae := a.assertAPIError(st, raw, http.StatusConflict, ipeers.ErrAmbiguous, "two processes, one address")
	// Both candidates, each named well enough to be told apart: the two
	// share the address, so agent name, pid and cwd are the only things
	// that say WHICH live process is which (spec §6.4).
	if len(ae.Candidates) != 2 {
		t.Fatalf("step 2: candidates = %+v, want 2", ae.Candidates)
	}
	for i, want := range []struct {
		name string
		pid  int
	}{{"twin-1", twin1PID}, {"twin-2", twin2PID}} {
		got := ae.Candidates[i]
		if got.Address != "b/"+head+":"+want.name || got.AgentName != want.name || got.PID != want.pid || got.Cwd != "/w" {
			t.Errorf("step 2: candidate %d = %+v, want address b/%s:%s, agent %s, pid %d, cwd /w", i, got, head, want.name, want.name, want.pid)
		}
	}
	twin1.none("step 2")
	twin2.none("step 2")

	// ---- 3. twin2's registry file becomes unreadable, its pid still alive:
	// only twin1's row remains, and it must NOT be delivered to. ----
	writeRegistryFixture(t, regDir, strconv.Itoa(twin2PID)+".json", "{")
	env = b.peers()
	if !env.Partial || len(env.UnknownRegistryFiles) != 1 {
		t.Fatalf("step 3: B's inventory partial=%v unknown=%v, want partial with one unknown file", env.Partial, env.UnknownRegistryFiles)
	}
	if rec := b.peerByInbox(env, twin1Sock); rec.Canonical != head || !rec.Deliverable {
		t.Fatalf("step 3: twin1 row = %+v, want the single deliverable tier-1 hit", rec)
	}
	st, raw = a.send(ipeers.SendRequest{To: "b/" + head, Text: "x", OriginInbox: originSock})
	ae = a.assertAPIError(st, raw, http.StatusServiceUnavailable, ipeers.ErrNotReady, "single hit under an unknown file")
	if !ae.Partial {
		t.Errorf("step 3: body = %+v, want partial:true", ae)
	}
	// The explicit tmux form still bypasses tier 1 and delivers;
	// its positive signal proves the send path has finished, so the
	// twins' "no frame" checks below are not racing anything.
	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo", Text: "x", OriginInbox: originSock})
	target.recv("step 3: tmux: form")
	twin1.none("step 3: the one readable process must not be picked")
	twin2.none("step 3")

	// ---- 4. Once the file is readable again the pair is ambiguous again
	// (never silently delivered), and by ITS OWN sessionId-free form there
	// is still no way to name one process outside tmux. ----
	writeRegistryFixture(t, regDir, strconv.Itoa(twin2PID)+".json", e2eRegistryJSON(twin2PID, twinSID, "twin-2", "", twin2Sock))
	st, raw = a.send(ipeers.SendRequest{To: "b/" + head, Text: "x", OriginInbox: originSock})
	a.assertAPIError(st, raw, http.StatusConflict, ipeers.ErrAmbiguous, "two processes readable again")
	twin1.none("step 4")
	twin2.none("step 4")

	origin.close()
	target.close()
	twin1.close()
	twin2.close()
	a.stop()
	b.stop()
}

// helperRegistryName reads the "name" field of a helper's own registry
// entry — the ground truth ApplyAddress/RewriteRegistryName write to,
// independent of the in-memory helperManager state Name() reports.
func helperRegistryName(t *testing.T, regDir string, pid int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(regDir, strconv.Itoa(pid)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Name
}

// TestE2E_HelperRename drives Task 14's five checks across the same
// two-daemon bridge as TestE2E_Labels, now under v3 where the thing they
// were written to observe has been designed away: the from-name a fresh
// helper is spawned with, and then — steps 3 and 5b, which used to assert
// a rename — the fact that claiming a label does NOT rename anything,
// because it does not move the claimant's address (D3). The rename
// machinery itself (same socket, same pid, ApplyAddress) is still
// exercised, but from the only sender that can still move an address: a
// v2 peer. A v3 origin reports address_rev 0 for ever (spec §4.4), so no
// two of its requests can be stale or fresh relative to each other and
// the rename path is inert for it. Step 4 therefore drives the
// stale-revision guard (spec §3.5 Freshness) with hand-built v2 requests,
// each with a fresh msg_id so dedup cannot be the reason an old address
// is refused.
func TestE2E_HelperRename(t *testing.T) {
	sockDir, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	originSock := filepath.Join(root, "origin.sock")
	targetSock := filepath.Join(root, "target.sock")
	origin := startFakeInbox(t, originSock)
	target := startFakeInbox(t, targetSock)
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eOriginPID)+".json", e2eRegistryJSON(e2eOriginPID, e2eOriginSID, e2eOriginName, "mt1:@1.%1", originSock))
	writeRegistryFixture(t, regDir, strconv.Itoa(e2eTargetPID)+".json", e2eRegistryJSON(e2eTargetPID, e2eTargetSID, e2eTargetName, "foo:@2.%2", targetSock))
	live := &e2eLiveness{}

	a := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostA, alias: "a", admin: e2eAdminA,
		peer:     config.PeerHost{Alias: "b", HostID: e2eHostB, Token: e2eTokenAtoB, InboundToken: e2eTokenBtoA},
		sessions: []session.SessionInfo{{Code: "mt1code", Name: "mt1", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"mt1code": {AgentType: "cc", SessionID: e2eOriginSID, TmuxPaneID: "%1"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	b := newE2EDaemon(t, e2eDaemonOpts{
		hostID: e2eHostB, alias: "b", admin: e2eAdminB,
		peer:     config.PeerHost{Alias: "a", HostID: e2eHostA, Token: e2eTokenBtoA, InboundToken: e2eTokenAtoB},
		sessions: []session.SessionInfo{{Code: "foocode", Name: "foo", Cwd: "/w"}},
		owners:   map[string]agent.PaneOwner{"foocode": {AgentType: "cc", SessionID: e2eTargetSID, TmuxPaneID: "%2"}},
		sockDir:  sockDir, regDir: regDir, live: live,
	})
	a.setPeer(func(h *config.PeerHost) { h.URL = b.srv.URL })
	b.setPeer(func(h *config.PeerHost) { h.URL = a.srv.URL })

	// A's origin's wire address, from the first frame to the last: step 5's
	// claim does not change it, which is the point (spec §4.5).
	originAddr := "a/" + ipeers.CanonicalID(e2eOriginSID) + ":mt1-" + e2eOriginName

	// ---- 1. B's target claims "purdex-tester"; A sends to its canonical
	// address (the claim gave it a name, not a second address — D3); the
	// frame the target receives names A's helper after A's own address. ----
	claimStatus, claimBody := b.do(http.MethodPut, "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: targetSock, Label: "purdex-tester"})
	if claimStatus != http.StatusOK {
		t.Fatalf("step 1: claim purdex-tester = %d %s", claimStatus, claimBody)
	}
	var claimResp ipeers.SelfResponse
	if err := json.Unmarshal(claimBody, &claimResp); err != nil {
		t.Fatalf("step 1: decode claim response: %v; body=%s", err, claimBody)
	}
	oldRev := claimResp.Peer.LabelRev // the "purdex-tester" claim's revision — the stale one step 4 replays

	sent := a.sendOK(ipeers.SendRequest{To: "b/" + ipeers.CanonicalID(e2eTargetSID), Text: "ping", OriginInbox: originSock})
	wantAddr1 := "b/" + ipeers.CanonicalID(e2eTargetSID) + ":foo-" + e2eTargetName
	if sent.ToAddress != wantAddr1 {
		t.Errorf("step 1: to_address = %q, want %q", sent.ToAddress, wantAddr1)
	}
	line := target.recv("step 1")
	_, w, bHelperSock := deliveredFrame(t, line)
	target.none("step 1")
	if w.FromName != originAddr {
		t.Errorf("step 1: frame from-name = %q, want %q", w.FromName, originAddr)
	}
	bHelper, ok := b.m.helpers.FindBySock(bHelperSock)
	if !ok {
		t.Fatalf("step 1: reply address %q is not one of B's helpers", bHelperSock)
	}
	if got := b.m.helpers.Name(bHelper); got != originAddr {
		t.Errorf("step 1: B's helper name = %q, want %q", got, originAddr)
	}

	// nativeReply is what the target writes into bHelperSock: a native
	// reply naming target.sock, the way TestE2E_TwoDaemons drives it.
	nativeReply := func(msgID, text string) string {
		from := "uds:" + targetSock
		return frameLine(t, msgID, "user", from, ccuds.Wrapper{
			From: from, FromName: "foo", FromMode: ipeers.ModePrompting, Text: text,
		}.Format())
	}

	// ---- 2. The target replies natively; the reply reaches A's origin
	// inbox, and A's helper for the replier is named after the target's
	// address — its canonical one, the only one it has. ----
	native1 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, nativeReply(native1, "pong"))
	line = origin.recv("step 2")
	_, _, aHelperSock := deliveredFrame(t, line)
	origin.none("step 2")
	aHelper, ok := a.m.helpers.FindBySock(aHelperSock)
	if !ok {
		t.Fatalf("step 2: reply address %q is not one of A's helpers", aHelperSock)
	}
	aHelperPID := aHelper.pid
	if got := a.m.helpers.Name(aHelper); got != wantAddr1 {
		t.Errorf("step 2: A's helper name = %q, want %q", got, wantAddr1)
	}
	if got := helperRegistryName(t, regDir, aHelperPID); got != wantAddr1 {
		t.Errorf("step 2: A's helper registry file name = %q, want %q", got, wantAddr1)
	}

	// ---- 3. The target re-claims "purdex-tester-2" and replies again: the
	// SAME helper instance on A (same socket, same pid) survives, and its
	// name is UNCHANGED — the claim bumped label_rev, so a rename really
	// was attempted, and it resolved to the same string because the
	// address the helper is named after never moved (D3). ----
	st, body := b.do(http.MethodPut, "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: targetSock, Label: "purdex-tester-2"})
	if st != http.StatusOK {
		t.Fatalf("step 3: claim purdex-tester-2 = %d %s", st, body)
	}
	native2 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, nativeReply(native2, "pong2"))
	line = origin.recv("step 3")
	_, _, sock3 := deliveredFrame(t, line)
	origin.none("step 3")
	if sock3 != aHelperSock {
		t.Errorf("step 3: reply address = %q, want the SAME A helper %q", sock3, aHelperSock)
	}
	aHelper2, ok := a.m.helpers.FindBySock(aHelperSock)
	if !ok {
		t.Fatalf("step 3: reply address %q is no longer one of A's helpers", aHelperSock)
	}
	if aHelper2.pid != aHelperPID {
		t.Errorf("step 3: A's helper pid = %d, want the SAME pid %d (in-place rename, not a respawn)", aHelper2.pid, aHelperPID)
	}
	wantAddr2 := wantAddr1 // a new label does not make a new address
	if got := a.m.helpers.Name(aHelper2); got != wantAddr2 {
		t.Errorf("step 3: A's helper name = %q, want %q", got, wantAddr2)
	}
	if got := helperRegistryName(t, regDir, aHelperPID); got != wantAddr2 {
		t.Errorf("step 3: A's helper registry file name = %q, want %q", got, wantAddr2)
	}

	// ---- 4. Stale-revision guard (spec §3.5 Freshness), dedup ruled out.
	// Under v3 the guard has no v3 sender left to guard against: every v3
	// origin reports address_rev 0 permanently (spec §4.4), so the replies
	// in steps 2–3 all named A's helper at revision 0 and none of them
	// could be stale. What the field still protects against is a V2 peer,
	// whose address genuinely can move — so that is what this drives,
	// straight at A's /deliver as B, with a fresh msg_id each time so
	// dedup can never be the reason something is refused. ----
	v2Deliver := func(step, addr string, rev int64) {
		t.Helper()
		req := ipeers.DeliverRequest{
			MsgID: uuid.NewString(),
			From: ipeers.WireFrom{
				HostID: e2eHostB, AgentSessionID: e2eTargetSID, PID: e2eTargetPID, ProcStart: e2eCCProcStart,
				PeerName: e2eTargetName, SessionName: "foo", DeclaredMode: ipeers.ModePrompting,
				Address: addr, AddressRev: rev,
			},
			To:   ipeers.WireTo{AgentSessionID: e2eOriginSID, PID: e2eOriginPID, ProcStart: e2eCCProcStart},
			Text: "v2 " + step,
		}
		if err := req.Validate(); err != nil {
			t.Fatalf("%s: request does not validate: %v", step, err)
		}
		status, raw := a.do(http.MethodPost, "/api/peers/deliver", e2eTokenBtoA, req)
		if status != http.StatusOK {
			t.Fatalf("%s: A /deliver = %d; body=%s", step, status, raw)
		}
		var delResp ipeers.DeliverResponse
		if err := json.Unmarshal(raw, &delResp); err != nil {
			t.Fatalf("%s: decode /deliver response: %v; body=%s", step, err, raw)
		}
		if delResp.Result != ipeers.ResultDelivered {
			t.Errorf("%s: result = %q, want %q", step, delResp.Result, ipeers.ResultDelivered)
		}
		origin.recv(step)
		origin.none(step)
	}
	assertAHelperName := func(step, want string) {
		t.Helper()
		h, ok := a.m.helpers.FindBySock(aHelperSock)
		if !ok || h.pid != aHelperPID {
			t.Fatalf("%s: A's helper for the replier changed instance: %+v ok=%v, want pid %d", step, h, ok, aHelperPID)
		}
		if got := a.m.helpers.Name(h); got != want {
			t.Errorf("%s: A's helper name = %q, want %q", step, got, want)
		}
		if got := helperRegistryName(t, regDir, aHelperPID); got != want {
			t.Errorf("%s: A's helper registry file name = %q, want %q", step, got, want)
		}
	}

	// 4a. A v2 peer announcing a v2-style address at a revision ABOVE the
	// 0 every v3 request carries: the rename applies. This is why neither
	// the field nor deliver.go's rename path is deleted.
	v2Addr := "purdex-tester-2:foo-" + e2eTargetName
	v2Deliver("step 4a", v2Addr, oldRev+1)
	wantV2Addr := "b/" + v2Addr
	assertAHelperName("step 4a", wantV2Addr)

	// 4b. The stale replay: the OLD address at the OLD revision, fresh
	// msg_id ⇒ 200 delivered (the message itself is unaffected), yet the
	// helper's name and registry file are untouched.
	v2Deliver("step 4b", "purdex-tester:foo-"+e2eTargetName, oldRev)
	assertAHelperName("step 4b", wantV2Addr)

	// ---- 5. Reverse-direction limit (spec §3.5 Freshness): A's origin
	// claims a new label; B's helper for A keeps ITS name — through a mere
	// reply, and then through A's own next send, which is where a v2
	// address change WOULD have landed. Under v3 there is no new address
	// to land, so the name is the same string at both checkpoints. ----
	st, body = a.do(http.MethodPut, "/api/peers/self/label", a.admin, ipeers.ClaimLabelRequest{OriginInbox: originSock, Label: "purdex-dev"})
	if st != http.StatusOK {
		t.Fatalf("step 5: claim purdex-dev = %d %s", st, body)
	}
	native3 := uuid.NewString()
	proxyhelpertest.WriteToSock(t, bHelperSock, nativeReply(native3, "pong3"))
	origin.recv("step 5a")
	origin.none("step 5a")
	if got := b.m.helpers.Name(bHelper); got != originAddr {
		t.Errorf("step 5a: B's helper name = %q after a mere reply, want unchanged %q", got, originAddr)
	}
	if got := helperRegistryName(t, regDir, bHelper.pid); got != originAddr {
		t.Errorf("step 5a: B's helper registry file name = %q after a mere reply, want unchanged %q", got, originAddr)
	}

	newOriginAddr := originAddr // claiming "purdex-dev" did not move A
	a.sendOK(ipeers.SendRequest{To: "b/" + ipeers.CanonicalID(e2eTargetSID), Text: "again", OriginInbox: originSock})
	target.recv("step 5b")
	if got := b.m.helpers.Name(bHelper); got != newOriginAddr {
		t.Errorf("step 5b: B's helper name = %q after A's next send, want %q", got, newOriginAddr)
	}
	if got := helperRegistryName(t, regDir, bHelper.pid); got != newOriginAddr {
		t.Errorf("step 5b: B's helper registry file name = %q after A's next send, want %q", got, newOriginAddr)
	}

	origin.close()
	target.close()
	a.stop()
	b.stop()
}
