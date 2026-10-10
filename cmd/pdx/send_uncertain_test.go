package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// #2595: a message that went out through POST /api/peers/send and ended delivery_uncertain may or may not have arrived. Every
// command that sends one says so on stderr — that it may not have arrived, that it must not be sent again, and how to look — keeps
// its exit code (the result is not an error: a script that re-ran the command on a non-zero exit would send it twice), and never
// resends by itself.

func uncertainSend() ipeers.SendResponse {
	return ipeers.SendResponse{MsgID: "11111111-2222-3333-4444-555555555555", ToAddress: "air/_abc123", Result: ipeers.ResultDeliveryUncertain}
}

func deliveredSend() ipeers.SendResponse {
	return ipeers.SendResponse{MsgID: "11111111-2222-3333-4444-555555555555", ToAddress: "air/_abc123", Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting}
}

// wantUncertainNote fails unless stderr carries the three things the note must say.
func wantUncertainNote(t *testing.T, who, stderr string) {
	t.Helper()
	for _, part := range []string{who, "delivery_uncertain", "11111111-2222-3333-4444-555555555555", "pdx msg log"} {
		if !strings.Contains(stderr, part) {
			t.Errorf("stderr lacks %q: %q", part, stderr)
		}
	}
	if !strings.Contains(stderr, "不要重送") && !strings.Contains(stderr, "do not send it again") {
		t.Errorf("stderr does not say to leave it unsent again: %q", stderr)
	}
}

func TestReport_AnUncertainUpMessageIsSaidOnStderrAndNotResent(t *testing.T) {
	fixedReportID(t, fakeReportID)
	d := &fakeReportDaemon{sendBody: uncertainSend()}
	code, stdout, stderr := driveReport(t, d, "ack", "--task", fakeTaskID, "--summary", "starting now")
	if code != ExitOK {
		t.Fatalf("code = %d, want the exit code unchanged (0); stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "reported ack") {
		t.Errorf("stdout = %q", stdout)
	}
	wantUncertainNote(t, "pdx report", stderr)
	if len(d.sendReq) != 1 {
		t.Errorf("sends = %d, want exactly one (never resent)", len(d.sendReq))
	}
}

func TestReport_ADeliveredUpMessageSaysNothingOnStderr(t *testing.T) {
	fixedReportID(t, fakeReportID)
	d := &fakeReportDaemon{sendBody: deliveredSend()}
	if code, _, stderr := driveReport(t, d, "ack", "--task", fakeTaskID, "--summary", "starting now"); code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q, want a quiet success", code, stderr)
	}
}

func TestTaskAdd_AnUncertainDownMessageIsSaidOnStderrAndNotResent(t *testing.T) {
	d := &fakeTaskDaemon{send: answer{body: uncertainSend()}}
	code, _, stderr := driveTask(t, d, addArgs...)
	if code != ExitOK {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr)
	}
	wantUncertainNote(t, "pdx task", stderr)
	if len(d.sendReq) != 1 {
		t.Errorf("sends = %d, want one", len(d.sendReq))
	}
	d = &fakeTaskDaemon{send: answer{body: deliveredSend()}}
	if code, _, stderr := driveTask(t, d, addArgs...); code != ExitOK || stderr != "" {
		t.Fatalf("delivered: code=%d stderr=%q, want quiet", code, stderr)
	}
}

func TestSpawn_AnUncertainBriefIsSaidOnStderrAndNotResent(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}, send: answer{body: uncertainSend()}}
	code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief", "hi")
	if code != ExitOK || !strings.Contains(stdout, `"ref"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want the member and exit 0", code, stdout, stderr)
	}
	wantUncertainNote(t, "pdx spawn", stderr)
	if len(d.sendReq) != 1 {
		t.Errorf("sends = %d, want one", len(d.sendReq))
	}
	d = &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}, send: answer{body: deliveredSend()}}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--model", "sonnet", "--brief", "hi"); code != ExitOK || stderr != "" {
		t.Fatalf("delivered: code=%d stderr=%q, want quiet", code, stderr)
	}
}

// `pdx msg send` already printed the result on stdout; it now says the same thing on stderr as the others.
func TestMsgSend_AnUncertainSendIsSaidOnStderr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(uncertainSend())
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "sekret")
	var stdout, stderr bytes.Buffer
	code := runMsgCmd([]string{"send", "air/_abc123", "hello", "--config", cfgPath},
		fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc.sock"}), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "delivery_uncertain") {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
	wantUncertainNote(t, "pdx msg", stderr.String())
}
