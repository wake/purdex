package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

type compactStub struct {
	mu     sync.Mutex
	bodies []string
	status int
	reply  any
}

func (s *compactStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	var raw json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&raw)
	s.mu.Lock()
	s.bodies = append(s.bodies, string(raw))
	s.mu.Unlock()
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
	_ = json.NewEncoder(w).Encode(s.reply)
}

func bp(b bool) *bool { return &b }
func ip(i int) *int   { return &i }

func compactView(o team.CompactionOverride, e team.CompactionEffective) team.MemberCompactionView {
	return team.MemberCompactionView{Address: "mlab/purdex-m1", Ref: "_m1m1m1",
		CompactionView: team.CompactionView{Role: "member", Override: o, Effective: e}}
}

func TestCompactCmd_Usage(t *testing.T) {
	s := &compactStub{reply: compactView(team.CompactionOverride{}, team.CompactionEffective{Managed: true})}
	for _, args := range [][]string{nil, {"a", "b"}, {"_m1m1m1", "--auto", "maybe"}, {"_m1m1m1", "--idle", "1"},
		{"_m1m1m1", "--threshold", "0"}, {"_m1m1m1", "--threshold", "101"}, {"_m1m1m1", "--threshold", "5x"},
		{"_m1m1m1", "--auto", ""}, {"_m1m1m1", "--idle", ""}, {"_m1m1m1", "--threshold", ""},
		{"_m1m1m1", "--reset", "--auto", "on"}, {"_m1m1m1", "--reset", "--threshold", "50"}, {"_m1m1m1", "--bogus"}} {
		code, stdout, stderr := driveTeamCmd(t, runCompactCmd, s, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx compact: ") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if len(s.bodies) != 0 {
		t.Errorf("usage errors reached the daemon: %v", s.bodies)
	}
}

func TestCompactCmd_Bodies(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"mlab/purdex-m1 [m1m1m1]"}, `{"origin_inbox":"/tmp/cc-socks/1.sock","target":"mlab/purdex-m1 [m1m1m1]"}`},
		{[]string{"_m1m1m1", "--threshold", "50", "--idle", "off"}, `{"origin_inbox":"/tmp/cc-socks/1.sock","target":"_m1m1m1","set":{"auto":null,"threshold":50,"idle":false}}`},
		{[]string{"--auto", "on", "_m1m1m1"}, `{"origin_inbox":"/tmp/cc-socks/1.sock","target":"_m1m1m1","set":{"auto":true,"threshold":null,"idle":null}}`},
		{[]string{"_m1m1m1", "--reset"}, `{"origin_inbox":"/tmp/cc-socks/1.sock","target":"_m1m1m1","reset":true}`},
	} {
		s := &compactStub{reply: compactView(team.CompactionOverride{}, team.CompactionEffective{Managed: true})}
		if code, _, stderr := driveTeamCmd(t, runCompactCmd, s, tc.args...); code != ExitOK || len(s.bodies) != 1 || s.bodies[0] != tc.want {
			t.Errorf("%q: code=%d stderr=%q bodies=%v want %s", tc.args, code, stderr, s.bodies, tc.want)
		}
	}
}

func TestCompactCmd_Output(t *testing.T) {
	for _, tc := range []struct {
		name string
		view team.MemberCompactionView
		want string
	}{
		{"auto, one override", compactView(team.CompactionOverride{Threshold: ip(50)}, team.CompactionEffective{Managed: true, Auto: true, Threshold: 50, Idle: true}),
			"member mlab/purdex-m1 [_m1m1m1]\n生效  自動 50%  閒置 開\n覆寫  門檻 50\n"},
		{"manual, no override", compactView(team.CompactionOverride{}, team.CompactionEffective{Managed: true}),
			"member mlab/purdex-m1 [_m1m1m1]\n生效  手動  閒置 關\n覆寫  無\n"},
		{"all overrides", compactView(team.CompactionOverride{Auto: bp(false), Threshold: ip(50), Idle: bp(true)}, team.CompactionEffective{Managed: true, Idle: true}),
			"member mlab/purdex-m1 [_m1m1m1]\n生效  手動  閒置 開\n覆寫  自動 關  門檻 50  閒置 開\n"},
		{"self relay", compactView(team.CompactionOverride{}, team.CompactionEffective{Reason: "self_relay"}),
			"member mlab/purdex-m1 [_m1m1m1]\n生效  不介入（自我接力）\n覆寫  無\n"},
		{"member auto", compactView(team.CompactionOverride{}, team.CompactionEffective{Reason: "member_auto"}),
			"member mlab/purdex-m1 [_m1m1m1]\n生效  不介入（member 由 lead 的接力設定處理）\n覆寫  無\n"},
	} {
		code, stdout, stderr := driveTeamCmd(t, runCompactCmd, &compactStub{reply: tc.view}, "_m1m1m1")
		if code != ExitOK || stdout != tc.want {
			t.Errorf("%s: code=%d stdout=%q want %q stderr=%q", tc.name, code, stdout, tc.want, stderr)
		}
	}
}

func TestCompactCmd_RefusalsExit13(t *testing.T) {
	for _, code := range []string{team.ErrNotYourMember, team.ErrRemoteUnsupported, team.ErrNotLead} {
		s := &compactStub{status: http.StatusConflict, reply: team.APIError{Error: code, Detail: "no"}}
		if got, _, stderr := driveTeamCmd(t, runCompactCmd, s, "_m1m1m1"); got != ExitRefused || lastToken(stderr) != code {
			t.Errorf("%s: code=%d stderr=%q", code, got, stderr)
		}
	}
}

func TestCompactCmd_Now(t *testing.T) {
	for now, want := range map[string]string{"now": "已叫 member 壓縮", "turn_end": "member 正在跑回合，這個回合結束時壓縮"} {
		v := compactView(team.CompactionOverride{}, team.CompactionEffective{Managed: true})
		v.Now = now
		s := &compactStub{reply: v}
		code, stdout, stderr := driveTeamCmd(t, runCompactCmd, s, "_m1m1m1", "--now")
		if code != ExitOK || strings.TrimSpace(stdout) != want || len(s.bodies) != 1 ||
			s.bodies[0] != `{"origin_inbox":"/tmp/cc-socks/1.sock","target":"_m1m1m1","now":true}` {
			t.Errorf("%s: code=%d stdout=%q stderr=%q bodies=%v", now, code, stdout, stderr, s.bodies)
		}
	}
	s := &compactStub{reply: compactView(team.CompactionOverride{}, team.CompactionEffective{})}
	for _, args := range [][]string{{"_m1m1m1", "--now", "--reset"}, {"_m1m1m1", "--now", "--auto", "on"}, {"_m1m1m1", "--now", "--threshold", "50"}, {"_m1m1m1", "--now", "--idle", "off"}} {
		if code, _, stderr := driveTeamCmd(t, runCompactCmd, s, args...); code != ExitUsage || !strings.HasPrefix(stderr, "pdx compact: ") {
			t.Errorf("%q: code=%d stderr=%q", args, code, stderr)
		}
	}
	if len(s.bodies) != 0 {
		t.Errorf("usage errors reached the daemon: %v", s.bodies)
	}
	for _, code := range []string{team.ErrRelayOpen, team.ErrCompactUnsupported, team.ErrCompactFailed} {
		s := &compactStub{status: http.StatusConflict, reply: team.APIError{Error: code, Detail: "no"}}
		if got, _, stderr := driveTeamCmd(t, runCompactCmd, s, "_m1m1m1", "--now"); got != ExitRefused || lastToken(stderr) != code {
			t.Errorf("%s: code=%d stderr=%q", code, got, stderr)
		}
	}
}

func TestTeamCmd_CompactColumn(t *testing.T) {
	mk := func(c *team.CompactionEffective) team.Member {
		m := fakeMember(team.SpawnRequest{ID: "op-1", Cwd: "/w"})
		m.Compaction = c
		return *m
	}
	view := team.TeamView{Team: team.Team{ID: fakeTeamID}, Members: []team.Member{
		mk(&team.CompactionEffective{Managed: true, Auto: true, Threshold: 70}),
		mk(&team.CompactionEffective{Managed: true}),
		mk(&team.CompactionEffective{Reason: "self_relay"}),
		mk(nil),
	}}
	_, stdout, _ := driveTeamCmd(t, runTeamCmd, &fakeTeamCmdDaemon{view: answer{body: view}})
	var header string
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.HasPrefix(l, "ADDRESS") {
			header = l
		} else if strings.HasPrefix(l, "mlab/") {
			rows = append(rows, l)
		}
	}
	hf := strings.Fields(header)
	if len(hf) < 11 || hf[9] != "EFFORT" || hf[10] != "COMPACT" || len(rows) != 4 {
		t.Fatalf("header=%q rows=%d\n%s", header, len(rows), stdout)
	}
	for i, want := range []string{"自動 70%", "手動", "接力", "-"} {
		// Every cell before COMPACT is one word, and TASK after it is "-": the COMPACT cell is what follows the 10th field.
		if rest := strings.Join(strings.Fields(rows[i])[10:], " "); !strings.HasPrefix(rest, want+" -") {
			t.Errorf("row %d COMPACT cell is not %q: %s", i, want, rows[i])
		}
	}
}
