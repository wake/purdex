package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// U20 (c)'s line and spec §7.2's brief prefix, spelled out here rather than
// read from internal/team, so dropping or rewording them is red here too.
const (
	spawnNoModelReminder = "提醒：沒有指定 --model，member 會用這台主機當下的預設模型。"
	spawnBriefPrefix     = "[pdx team] 你是 mlab/purdex-lead 的 member（team " + fakeTeamID + "）。接力由 lead 決定，不要自己接力。"
	fakeTeamID           = "8f2c0f8e-3b1a-4c6e-9d2a-0e5b7c1d9a44"
)

// answer is one canned daemon answer: a status (0 means 200) and a JSON body.
type answer struct {
	status int
	body   any
}

func write(w http.ResponseWriter, a answer) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	w.WriteHeader(a.status)
	_ = json.NewEncoder(w).Encode(a.body)
}

// fakeTeamCmdDaemon speaks the routes `pdx spawn|kill|team` call. Spawn
// POSTs get spawns[n] (the last repeats), built from the request; a POST
// for which hold(n) is true is held until the client goes, after onSpawn(n)
// ran. Every request body is recorded.
type fakeTeamCmdDaemon struct {
	mu       sync.Mutex
	requests int
	spawnReq []team.SpawnRequest
	spawns   []func(team.SpawnRequest) answer
	hold     func(n int) bool
	onSpawn  func(n int)
	sendReq  []ipeers.SendRequest
	send     answer
	dropSend bool
	holdSend bool
	killReq  []team.KillRequest
	kill     answer
	queries  []string
	view     answer
}

func (f *fakeTeamCmdDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/team/spawns":
		var req team.SpawnRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.spawnReq = append(f.spawnReq, req)
		n := len(f.spawnReq)
		if f.onSpawn != nil {
			f.onSpawn(n)
		}
		if f.hold != nil && f.hold(n) {
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		write(w, f.spawns[min(n, len(f.spawns))-1](req))
	case r.Method == http.MethodPost && r.URL.Path == "/api/peers/send":
		var req ipeers.SendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.sendReq = append(f.sendReq, req)
		if f.holdSend {
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		if f.dropSend {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		write(w, f.send)
	case r.Method == http.MethodPost && r.URL.Path == "/api/team/kill":
		var req team.KillRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.killReq = append(f.killReq, req)
		write(w, f.kill)
	case r.Method == http.MethodGet && r.URL.Path == "/api/team":
		f.queries = append(f.queries, r.URL.RawQuery)
		write(w, f.view)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeTeamCmdDaemon) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// fakeMember is the member a done op carries.
func fakeMember(req team.SpawnRequest) *team.Member {
	return &team.Member{SessionID: "cc-sid-member-1", Ref: "_m1m1m1", Address: "mlab/_m1m1m1", TeamID: fakeTeamID,
		HostID: "host-mlab", Cwd: req.Cwd, TmuxSession: "tm-1111111122", State: team.MemberActive,
		Model: req.Model, Effort: req.Effort, SpawnOp: req.ID}
}

func spawnOp(req team.SpawnRequest, state team.SpawnState, reason string) answer {
	op := team.SpawnOp{ID: req.ID, TeamID: fakeTeamID, HostID: "host-mlab", State: state, Reason: reason,
		Cwd: req.Cwd, Model: req.Model, Effort: req.Effort, TmuxSession: "tm-1111111122", LeadAddress: "mlab/purdex-lead"}
	if state == team.SpawnDone {
		op.Member = fakeMember(req)
	}
	return answer{body: op}
}

func spawnDone(req team.SpawnRequest) answer    { return spawnOp(req, team.SpawnDone, "") }
func spawnRunning(req team.SpawnRequest) answer { return spawnOp(req, team.SpawnRunning, "") }
func spawnFailed(reason string) func(team.SpawnRequest) answer {
	return func(req team.SpawnRequest) answer { return spawnOp(req, team.SpawnFailed, reason) }
}
func refuse(status int, code string) func(team.SpawnRequest) answer {
	return func(team.SpawnRequest) answer {
		return answer{status: status, body: team.APIError{Error: code, Detail: "the daemon says so"}}
	}
}

type teamCmdFunc func(context.Context, []string, func(string) string, io.Writer, io.Writer, ...daemonclient.Option) int

// driveTeamCmd runs run against d with the lead's inbox set, a fake clock
// and --config appended.
func driveTeamCmd(t *testing.T, run teamCmdFunc, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	return driveTeamCmdWith(t, run, d, leadEnv(), []daemonclient.Option{leadClockOpt()}, args...)
}

func driveTeamCmdWith(t *testing.T, run teamCmdFunc, d http.Handler, getenv func(string) string, opts []daemonclient.Option, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), append(append([]string{}, args...), "--config", cfgPath), getenv, &stdout, &stderr, opts...)
	return code, stdout.String(), stderr.String()
}

// lastToken is the last whitespace-separated stderr token: the API code (plan v3 Global constraints).
func lastToken(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// U20 (a), spec §15: a model or effort the daemon would refuse, both briefs,
// an unreadable brief file, a bad title and stray arguments are exit 2
// before the config is read or the daemon is asked; the daemon here would
// accept anything.
func TestSpawnCmd_UsageErrorsExit2(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("do it"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--model", "a b"}, {"--model", "'x'"}, {"--model", "x;y"}, {"--model", "$(x)"}, {"--model", ""},
		{"--model", "opus[2m]"}, {"--effort", "High"}, {"--effort", "ultra"},
		{"--brief", "x", "--brief-file", brief}, {"--brief-file", filepath.Join(t.TempDir(), "missing.md")},
		{"--brief", " \n"}, {"--brief", strings.Repeat("x", ipeers.MaxTextBytes)},
		{"--title", "bad\x1btitle"}, {"--title", ""}, {"extra"}, {"--bogus"},
	} {
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx spawn: ") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		if strings.Contains(stderr, spawnNoModelReminder) {
			t.Errorf("%q: a usage error printed the reminder", args)
		}
	}
	if n := d.count(); n != 0 {
		t.Errorf("the daemon saw %d request(s), want 0", n)
	}
}

// U20 (c), spec §15: without --model the reminder goes to stderr once and the
// spawn still exits 0; with --model there is no reminder.
func TestSpawnCmd_NoModelPrintsTheReminderAndExits0(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--cwd", "/w")
	if code != ExitOK || !strings.Contains(stdout, `"ref":"_m1m1m1"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if n := strings.Count(stderr, spawnNoModelReminder+"\n"); n != 1 {
		t.Errorf("stderr has the reminder %d times, want 1: %q", n, stderr)
	}
	if strings.Contains(stdout, "提醒") {
		t.Errorf("stdout carries the reminder: %q", stdout)
	}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--cwd", "/w", "--model", "sonnet"); code != ExitOK || strings.Contains(stderr, "提醒") {
		t.Errorf("with --model: code=%d stderr=%q", code, stderr)
	}
}

// U20 (a): model and effort reach the request as given (opus[1m] included);
// cwd is made absolute and defaults to the working directory (coordinator
// decision 6); done prints the member on stdout, one JSON line.
func TestSpawnCmd_ModelAndEffortReachTheRequest(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--cwd", "rel/dir", "--title", "p4 tester",
		"--model", "opus[1m]", "--effort", "xhigh")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	wd, _ := os.Getwd()
	req := d.spawnReq[0]
	if req.OriginInbox != "/tmp/cc-socks/1.sock" || req.Cwd != filepath.Join(wd, "rel/dir") || req.Title != "p4 tester" ||
		req.Model != "opus[1m]" || req.Effort != "xhigh" || !isCanonicalUUIDv4ForTest(req.ID) {
		t.Errorf("request = %+v", req)
	}
	var out map[string]string
	if strings.Count(stdout, "\n") != 1 || json.Unmarshal([]byte(stdout), &out) != nil {
		t.Fatalf("stdout = %q, want one JSON line", stdout)
	}
	want := map[string]string{"ref": "_m1m1m1", "address": "mlab/_m1m1m1", "tmux_session": "tm-1111111122",
		"session_id": "cc-sid-member-1", "host_id": "host-mlab", "spawn_op": req.ID}
	if len(out) != len(want) {
		t.Errorf("stdout = %v, want %v", out, want)
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("stdout[%s] = %q, want %q", k, out[k], v)
		}
	}
	if _, _, _ = driveTeamCmd(t, runSpawnCmd, d); d.spawnReq[1].Cwd != wd {
		t.Errorf("no --cwd: cwd = %q, want %q", d.spawnReq[1].Cwd, wd)
	}
}

func isCanonicalUUIDv4ForTest(s string) bool {
	_, err := team.SpawnTmuxName(s)
	return err == nil
}

// While the op runs the CLI posts the same body again (the daemon joins);
// three consecutive attempts with no answer at all are exit 20.
func TestSpawnCmd_PostsAgainWhileRunning(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnRunning, spawnRunning, spawnDone}}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if len(d.spawnReq) != 3 || d.spawnReq[0] != d.spawnReq[1] || d.spawnReq[1] != d.spawnReq[2] {
		t.Errorf("spawn requests = %+v, want the same body three times", d.spawnReq)
	}

	clock := newLeadClock()
	hung := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone},
		hold: func(int) bool { return true }, onSpawn: func(int) { clock.fireNext() }}
	code, stdout, stderr := driveTeamCmdWith(t, runSpawnCmd, hung, leadEnv(), []daemonclient.Option{clock.opt()}, "--model", "sonnet")
	if code != ExitUnavailable || stdout != "" || !strings.Contains(stderr, "daemon 沒有回應") {
		t.Fatalf("hung: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if n := hung.count(); n != 3 {
		t.Errorf("hung: %d spawn POSTs, want 3", n)
	}
}

// A daemon that answers running forever (PR P4-7 review): the whole wait is
// bounded by spawnSettleBound (9 min, under the Bash tool's 10). The op may
// still run, so this is not the daemon's member_start_timeout (exit 14, a
// killed session and a freed slot) but exit 1 with the op id on stderr, the
// advice not to spawn again, and the CLI's spawn_wait_timeout last (critic
// ruling); no member on stdout.
func TestSpawnCmd_AnOpThatNeverSettlesEndsAtTheBound(t *testing.T) {
	defer func(b time.Duration) { spawnSettleBound = b }(spawnSettleBound)
	spawnSettleBound = 50 * time.Millisecond
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnRunning}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet")
	if code != ExitError || stdout != "" || !strings.Contains(stderr, d.spawnReq[0].ID) ||
		!strings.Contains(stderr, "不要直接重開") || lastToken(stderr) != "spawn_wait_timeout" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, r := range d.spawnReq {
		if r != d.spawnReq[0] {
			t.Fatalf("a POST changed the op: %+v", r)
		}
	}
}

// Spec §14: member_start_timeout exits 14 with the hooks hint; any other
// failure reason exits 1. Neither sends the brief; the reason is the last
// stderr token.
func TestSpawnCmd_StartTimeoutExits14(t *testing.T) {
	for reason, want := range map[string]int{team.SpawnReasonStartTimeout: ExitMemberFailed, team.SpawnReasonLaunchFailed: ExitError} {
		d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnFailed(reason)}}
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief", "hi")
		if code != want || stdout != "" || lastToken(stderr) != reason || len(d.sendReq) != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q sends=%d", reason, code, stdout, stderr, len(d.sendReq))
		}
		hint := "member 沒有在 20 秒內啟動（這台主機需要 Purdex hooks：pdx setup --agent cc）"
		if strings.Contains(stderr, hint) != (reason == team.SpawnReasonStartTimeout) {
			t.Errorf("%s: stderr = %q", reason, stderr)
		}
	}
}

// Team-rule refusals are exit 13, other API errors exit 1, a daemon without
// the route exit 21; the code is always the last stderr token.
func TestSpawnCmd_RefusalsExit13CodeLast(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		want   int
	}{
		{http.StatusConflict, team.ErrNotLead, ExitRefused},
		{http.StatusConflict, team.ErrTeamFull, ExitRefused},
		{http.StatusConflict, team.ErrCwdOutsideGrant, ExitRefused},
		{http.StatusConflict, team.ErrIDConflict, ExitError},
		{http.StatusBadRequest, team.ErrBadRequest, ExitError},
		{http.StatusBadRequest, team.ErrOriginUnknown, ExitError},
	} {
		d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{refuse(tc.status, tc.code)}}
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet")
		if code != tc.want || stdout != "" || lastToken(stderr) != tc.code || !strings.HasPrefix(stderr, "pdx spawn: the daemon says so") {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", tc.code, code, stdout, stderr)
		}
	}
	code, _, stderr := driveTeamCmd(t, runSpawnCmd, http.NotFoundHandler(), "--model", "sonnet")
	if code != ExitUnsupported || lastToken(stderr) != "unsupported" {
		t.Errorf("plain 404: code=%d stderr=%q", code, stderr)
	}
}

// Spec §7.2: after done the CLI sends the brief once, from the lead's inbox
// to the member's address, with the one-line prefix; --brief-file reads the
// file; without a brief nothing is sent.
func TestSpawnCmd_BriefFromTheLeadInboxWithThePrefix(t *testing.T) {
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("line one\nline two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		text string
	}{
		{[]string{"--brief", "做 P4-7"}, "做 P4-7"},
		{[]string{"--brief-file", brief}, "line one\nline two\n"},
	} {
		d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}, send: answer{body: ipeers.SendResponse{MsgID: "m1"}}}
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, append([]string{"--model", "sonnet"}, tc.args...)...)
		if code != ExitOK || !strings.Contains(stdout, `"ref":"_m1m1m1"`) {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", tc.args, code, stdout, stderr)
		}
		want := ipeers.SendRequest{To: "mlab/_m1m1m1", Text: spawnBriefPrefix + "\n" + tc.text, OriginInbox: "/tmp/cc-socks/1.sock"}
		if len(d.sendReq) != 1 || d.sendReq[0] != want {
			t.Errorf("%q: sends = %+v, want %+v", tc.args, d.sendReq, want)
		}
	}
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	if code, _, _ := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet"); code != ExitOK || len(d.sendReq) != 0 {
		t.Errorf("no brief: code=%d sends=%d", code, len(d.sendReq))
	}
}

// The brief's limit is exact (PR P4-7 review): with the longest lead address
// the rules allow (a 64-byte alias, "/", a 64-byte name) and a 36-byte team
// id, a brief of exactly the limit makes a message of exactly the peers
// limit; one byte more is exit 2 before a member opens, naming the limit.
func TestSpawnCmd_BriefLimitIsExact(t *testing.T) {
	maxAddr := strings.Repeat("a", 64) + "/" + strings.Repeat("b", 64)
	limit := ipeers.MaxTextBytes - len("[pdx team] 你是 "+maxAddr+" 的 member（team "+fakeTeamID+"）。接力由 lead 決定，不要自己接力。\n")
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{func(req team.SpawnRequest) answer {
		a := spawnDone(req)
		op := a.body.(team.SpawnOp)
		op.LeadAddress = maxAddr
		return answer{body: op}
	}}}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief", strings.Repeat("x", limit)); code != ExitOK {
		t.Fatalf("a brief of exactly %d bytes: code=%d stderr=%q", limit, code, stderr)
	}
	if len(d.sendReq) != 1 || len(d.sendReq[0].Text) != ipeers.MaxTextBytes || ipeers.ValidateText(d.sendReq[0].Text) != nil {
		t.Fatalf("sent %d message(s), want one of exactly %d bytes", len(d.sendReq), ipeers.MaxTextBytes)
	}
	over := &fakeTeamCmdDaemon{spawns: d.spawns}
	code, _, stderr := driveTeamCmd(t, runSpawnCmd, over, "--model", "sonnet", "--brief", strings.Repeat("x", limit+1))
	if code != ExitUsage || over.count() != 0 || !strings.Contains(stderr, strconv.Itoa(limit)) {
		t.Errorf("one byte over: code=%d requests=%d stderr=%q", code, over.count(), stderr)
	}
}

// --brief-file reads at most one byte past the limit (PR P4-7 R1): a FIFO
// whose writer never closes, /dev/zero and a 1 GiB file are exit 2 naming
// the limit, without waiting for an EOF or holding the file in memory; a
// FIFO nobody writes to gives up after briefReadTimeout. No request is made.
func TestSpawnCmd_BriefFileReadIsBounded(t *testing.T) {
	defer func(d time.Duration) { briefReadTimeout = d }(briefReadTimeout)
	briefReadTimeout = 2 * time.Second
	dir := t.TempDir()
	held, idle, big := filepath.Join(dir, "held"), filepath.Join(dir, "idle"), filepath.Join(dir, "big")
	for _, p := range []string{held, idle} {
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	defer close(release)
	go func() { // writes one byte past the limit and never closes until the test ends
		if w, err := os.OpenFile(held, os.O_WRONLY, 0); err == nil {
			_, _ = w.Write(bytes.Repeat([]byte("x"), briefMaxBytes+1))
			<-release
			w.Close()
		}
	}()
	if f, err := os.Create(big); err != nil || f.Truncate(1<<30) != nil || f.Close() != nil {
		t.Fatal("sparse file", err)
	}
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	for _, p := range []string{held, "/dev/zero", big} { // held first: an unbounded read fails here, before /dev/zero
		code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief-file", p)
		if code != ExitUsage || !strings.Contains(stderr, strconv.Itoa(briefMaxBytes)) {
			t.Fatalf("%s: code=%d stderr=%q", p, code, stderr)
		}
	}
	code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief-file", idle)
	if code != ExitUsage || !strings.Contains(stderr, "--brief-file") || d.count() != 0 {
		t.Errorf("idle FIFO: code=%d requests=%d stderr=%q", code, d.count(), stderr)
	}
	if w, err := os.OpenFile(idle, os.O_WRONLY, 0); err == nil { // lets the abandoned open return
		w.Close()
	}
}

// Coordinator decision 14: a brief that fails after the spawn is exit 1, the
// member stays on stdout, stderr says how to send it by hand, and the send
// is never replayed (a dropped connection is one send). The code is the
// last stderr token (PR P4-7 review): the daemon's, else the CLI's.
func TestSpawnCmd_BriefFailureExits1KeepsStdout(t *testing.T) {
	defer func(d time.Duration) { briefTimeout = d }(briefTimeout)
	briefTimeout = 100 * time.Millisecond
	for code, d := range map[string]*fakeTeamCmdDaemon{
		"text_invalid":       {send: answer{status: http.StatusBadRequest, body: ipeers.APIError{Error: "text_invalid", Detail: "bad"}}},
		"not_ready":          {send: answer{status: http.StatusServiceUnavailable, body: ipeers.APIError{Error: "not_ready", Detail: "retry"}}},
		"host_unknown":       {send: answer{status: http.StatusNotFound, body: ipeers.APIError{Error: "host_unknown", Detail: "no peer host"}}},
		"unsupported":        {send: answer{status: http.StatusNotFound, body: "404 page not found"}},
		"no_answer":          {holdSend: true},
		"daemon_unavailable": {dropSend: true},
	} {
		d.spawns = []func(team.SpawnRequest) answer{spawnDone}
		exit, stdout, stderr := driveTeamCmdWith(t, runSpawnCmd, d, leadEnv(), []daemonclient.Option{leadClockOpt(), leadNoKeepAlive()},
			"--model", "sonnet", "--brief", "hi")
		if exit != ExitError || !strings.Contains(stdout, `"ref":"_m1m1m1"`) || len(d.sendReq) != 1 {
			t.Errorf("%s: code=%d stdout=%q sends=%d", code, exit, stdout, len(d.sendReq))
		}
		if !strings.HasPrefix(stderr, "pdx spawn: member 已開啟，但 brief 沒送出") ||
			!strings.Contains(stderr, "；請用 pdx msg send mlab/_m1m1m1 手動送 ") || lastToken(stderr) != code {
			t.Errorf("%s: stderr = %q", code, stderr)
		}
	}
}

// main.go dispatches the lead's commands to their switch targets and its
// hand-written Commands line names them (the TestMainUsageListsPath pattern:
// main() exits, so the switch is read from source).
func TestDispatch_SpawnKillTeam(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	usage := ""
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "Commands:") {
			usage = l
		}
	}
	for cmd, target := range map[string]string{"spawn": "runSpawn"} {
		if !strings.Contains(s, "case \""+cmd+"\":\n\t\t"+target+"(os.Args[2:])\n") {
			t.Errorf("main.go does not dispatch %q to %s", cmd, target)
		}
		if !strings.Contains(usage, " "+cmd+",") {
			t.Errorf("the Commands line does not list %q: %s", cmd, usage)
		}
	}
}

// The lead's commands run inside a Claude Code session: no inbox is exit 1
// before any request.
func TestTeamCmds_NoInboxExit1(t *testing.T) {
	for name, run := range map[string]teamCmdFunc{"spawn": runSpawnCmd} {
		d := &fakeTeamCmdDaemon{}
		args := map[string][]string{"spawn": {"--model", "sonnet"}, "kill": {"_m1m1m1"}, "team": nil}[name]
		code, _, stderr := driveTeamCmdWith(t, run, d, fakeGetenv(nil), []daemonclient.Option{leadClockOpt()}, args...)
		if code != ExitError || !strings.Contains(stderr, "CLAUDE_CODE_MESSAGING_SOCKET") || d.count() != 0 {
			t.Errorf("%s: code=%d stderr=%q requests=%d", name, code, stderr, d.count())
		}
	}
}
