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
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// U20 (c)'s line, spelled out here rather than read from internal/team,
// so dropping or rewording it is red here too.
const (
	spawnNoModelReminder = "提醒：沒有指定 --model，member 會用這台主機當下的預設模型。"
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

// U20 (a), spec §15: a model or effort the daemon would refuse, a bad
// title and stray arguments are exit 2
// before the config is read or the daemon is asked; the daemon here would
// accept anything.
func TestSpawnCmd_UsageErrorsExit2(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	for _, args := range [][]string{
		{"--model", "a b"}, {"--model", "'x'"}, {"--model", "x;y"}, {"--model", "$(x)"}, {"--model", ""},
		{"--model", "opus[2m]"}, {"--effort", "High"}, {"--effort", "ultra"},
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

// Spec §14: member_start_timeout exits 14 with the hooks hint; any other
// failure reason exits 1; the reason is the last stderr token.
func TestSpawnCmd_StartTimeoutExits14(t *testing.T) {
	for reason, want := range map[string]int{team.SpawnReasonStartTimeout: ExitMemberFailed, team.SpawnReasonLaunchFailed: ExitError} {
		d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnFailed(reason)}}
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet")
		if code != want || stdout != "" || lastToken(stderr) != reason {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", reason, code, stdout, stderr)
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
