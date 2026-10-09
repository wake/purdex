package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// `pdx adopt` / `pdx release` (adopt plan PL-1e).

func driveAdopt(t *testing.T, ctx context.Context, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runAdoptCmd(ctx, append(append([]string{}, args...), "--config", cfgPath), leadEnv(), &stdout, &stderr, fixedID(), nil, leadClockOpt())
	return code, stdout.String(), stderr.String()
}

func adoptApproved() team.Approval {
	p, _ := json.Marshal(team.AdoptPayload{TeamID: "team-1", LeadSessionID: "sid-lead", TargetRef: "_def456", TargetSessionID: "sid-def",
		TargetAddress: "mlab/def-worker"})
	return team.Approval{Kind: team.KindAdopt, State: team.StateApproved, Payload: p}
}

func TestAdoptCmd_UsageErrorsExit2(t *testing.T) {
	d := newFakeTeamDaemon(adoptApproved())
	for _, args := range [][]string{
		nil, {"_def456", "_abc123"}, {"just-a-name"}, {"host/just-a-name"}, {"not a ref"}, {"_def456", "--wait", "0"}, {"_def456", "--wait", "11m"},
		{"_def456", "--wait", "500ms"}, {"--bogus", "_def456"},
	} {
		code, stdout, stderr := driveAdopt(t, context.Background(), d, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx adopt: ") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if creates, _, _, _ := d.snapshot(); len(creates) != 0 {
		t.Errorf("usage errors reached the daemon: %+v", creates)
	}
}

func TestAdoptCmd_ApprovedPrintsOneJSONLine(t *testing.T) {
	for _, target := range []string{"_def456", "def456", "mlab/_def456", "mlab/def-worker [def456]", "11111111-2222-4333-8444-666666666666"} {
		d := newFakeTeamDaemon(adoptApproved())
		code, stdout, stderr := driveAdopt(t, context.Background(), d, target)
		if code != ExitOK || strings.Count(stdout, "\n") != 1 {
			t.Fatalf("%q: code=%d stdout=%q stderr=%q", target, code, stdout, stderr)
		}
		var out adoptOutput
		if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.RequestID != fixedID()() || out.TeamID != "team-1" || out.Ref != "_def456" || out.Address != "mlab/def-worker" || out.SessionID != "sid-def" {
			t.Fatalf("%q: output = %+v (%v)", target, out, err)
		}
		creates, _, _, _ := d.snapshot()
		if len(creates) != 1 || creates[0].Kind != team.KindAdopt || creates[0].Target != target || creates[0].OriginInbox != "/tmp/cc-socks/1.sock" || creates[0].WaitS != team.DefaultWaitS {
			t.Fatalf("%q: create = %+v", target, creates)
		}
		if !strings.Contains(stderr, "Bash timeout 600000") {
			t.Errorf("stderr lacks the foreground reminder: %q", stderr)
		}
	}
}

func TestAdoptCmd_DeniedTimeoutCancelled(t *testing.T) {
	for st, want := range map[team.State]int{team.StateDenied: ExitDenied, team.StateTimeout: ExitTimeout, team.StateCancelled: ExitCancelled, team.StateAbandoned: ExitCancelled} {
		d := newFakeTeamDaemon(team.Approval{Kind: team.KindAdopt, State: st})
		if code, stdout, stderr := driveAdopt(t, context.Background(), d, "_def456"); code != want || stdout != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q, want %d", st, code, stdout, stderr, want)
		}
	}
}

// A request the click's re-check cancelled carries the rule's code as its close_reason: that is a refusal, not a cancel.
// Mutation gate: map cancelled + close_reason to 12 → red.
func TestAdoptCmd_CancelledWithCloseReasonExits13CodeLast(t *testing.T) {
	for _, code := range []string{team.ErrAdoptAlreadyMember, team.ErrTeamFull, team.ErrAdoptTargetNotFound} {
		d := newFakeTeamDaemon(team.Approval{Kind: team.KindAdopt, State: team.StateCancelled, CloseReason: code})
		got, stdout, stderr := driveAdopt(t, context.Background(), d, "_def456")
		if got != ExitRefused || stdout != "" || lastToken(stderr) != code {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", code, got, stdout, stderr)
		}
	}
}

func TestAdoptCmd_RefusalsExit13CodeLast(t *testing.T) {
	for _, code := range []string{team.ErrNotLead, team.ErrTeamFull, team.ErrAdoptSelf, team.ErrAdoptTargetIsLead, team.ErrAdoptAlreadyMember,
		team.ErrAdoptTargetNotFound, team.ErrRemoteUnsupported, team.ErrAdoptTargetAmbiguous} {
		d := newFakeTeamDaemon(team.Approval{})
		d.createStatus, d.refuseCode = http.StatusConflict, code
		got, stdout, stderr := driveAdopt(t, context.Background(), d, "_def456")
		if got != ExitRefused || stdout != "" || !strings.Contains(stderr, code) {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", code, got, stdout, stderr)
		}
		if _, polls, _, _ := d.snapshot(); len(polls) != 0 {
			t.Errorf("%s: polled a refused request", code)
		}
		if code == team.ErrAdoptTargetAmbiguous && !strings.Contains(stderr, "session id") {
			t.Errorf("ambiguous: stderr lacks the session id hint: %q", stderr)
		}
	}
}

func TestAdoptCmd_SignalCancels(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.hold = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-d.pollStarted
		cancel()
	}()
	code, stdout, _ := driveAdopt(t, ctx, d, "_def456")
	if code != ExitCancelled || stdout != "" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if _, _, deletes, _ := d.snapshot(); len(deletes) != 1 || deletes[0] != fixedID()() {
		t.Errorf("deletes = %v, want the request's id", deletes)
	}
}

// ---- pdx release ----

func TestReleaseCmd_ReleasedAndRefusals(t *testing.T) {
	m := fakeMember(team.SpawnRequest{ID: "op-1", Cwd: "/w"})
	m.State = team.MemberReleased
	d := &fakeTeamCmdDaemon{release: answer{body: m}}
	code, stdout, stderr := driveTeamCmd(t, runReleaseCmd, d, "_m1m1m1")
	if code != ExitOK || strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, `"state":"released"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if want := (team.ReleaseRequest{OriginInbox: "/tmp/cc-socks/1.sock", Target: "_m1m1m1"}); len(d.releaseReq) != 1 || d.releaseReq[0] != want {
		t.Errorf("release requests = %+v, want %+v", d.releaseReq, want)
	}
	for _, args := range [][]string{nil, {"_aaaaaa", "_bbbbbb"}, {" "}} {
		if code, _, stderr := driveTeamCmd(t, runReleaseCmd, d, args...); code != ExitUsage || !strings.HasPrefix(stderr, "pdx release: ") {
			t.Errorf("%q: code=%d stderr=%q", args, code, stderr)
		}
	}
	for _, e := range []team.APIError{{Error: team.ErrNotYourMember}, {Error: team.ErrNotLead}, {Error: team.ErrRelayOpen, Op: &team.RelayOp{ID: "op-r"}}} {
		d := &fakeTeamCmdDaemon{release: answer{status: http.StatusConflict, body: e}}
		code, stdout, stderr := driveTeamCmd(t, runReleaseCmd, d, "_m1m1m1")
		if code != ExitRefused || stdout != "" || lastToken(stderr) != e.Error {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", e.Error, code, stdout, stderr)
		}
	}
}

func TestDispatch_AdoptRelease(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{`case "adopt":`, `runAdopt(os.Args[2:])`, `case "release":`, `runRelease(os.Args[2:])`, "lead, adopt, release, spawn"} {
		if !strings.Contains(src, want) {
			t.Errorf("main.go lacks %q", want)
		}
	}
}

// A released member stays in the table with its state, and an adopted one has no spawn tmux session to show.
func TestTeamCmd_ShowsReleasedState(t *testing.T) {
	v := fakeView()
	v.Members[1].State = team.MemberReleased
	v.Members[1].Origin = team.MemberOriginAdopted
	v.Members[1].TmuxSession = ""
	d := &fakeTeamCmdDaemon{view: answer{body: v}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK || !strings.Contains(stdout, "released") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
