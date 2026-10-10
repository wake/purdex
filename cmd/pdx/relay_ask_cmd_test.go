package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// `pdx relay ask` (member relay ask §5): the member's mod asks its lead. Exit 0 ok, 13 refused (the code is the last
// word on stderr), 20 daemon unreachable, 2 for a bad grammar.

type fakeRelayAskDaemon struct {
	status int
	body   any
	got    team.RelayAskRequest
}

func (d *fakeRelayAskDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/health":
		_ = json.NewEncoder(w).Encode(map[string]string{"boot_id": "b1"})
	case "/api/relay/ask":
		_ = json.NewDecoder(r.Body).Decode(&d.got)
		if d.status != 0 {
			w.WriteHeader(d.status)
		}
		_ = json.NewEncoder(w).Encode(d.body)
	default:
		http.NotFound(w, r)
	}
}

func TestRelayCmd_AskPostsAndPrintsTheAnswer(t *testing.T) {
	d := &fakeRelayAskDaemon{body: team.RelayAskResponse{ID: "ask-1", State: team.RelayAskOpen, ExpiresAt: 99}}
	code, stdout, stderr := driveRelay(t, context.Background(), d, "ask", "--session", "sid-m", "--used", "71", "--window", "1000000", "--request-id", "11111111-1111-4111-8111-111111111111")
	if code != ExitOK || strings.TrimSpace(stdout) != `{"id":"ask-1","state":"open","expires_at":99}` {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if d.got.SessionID != "sid-m" || d.got.UsedPct != 71 || d.got.Window != 1000000 || d.got.RequestID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("sent = %+v", d.got)
	}
}

// Without --request-id the CLI mints one, so a retry of one call is the same ask.
func TestRelayCmd_AskMintsARequestID(t *testing.T) {
	d := &fakeRelayAskDaemon{body: team.RelayAskResponse{ID: "ask-1", State: team.RelayAskOpen}}
	if code, _, stderr := driveRelay(t, context.Background(), d, "ask", "--session", "sid-m", "--used", "71", "--window", "1"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if len(d.got.RequestID) != 36 {
		t.Fatalf("request id = %q", d.got.RequestID)
	}
}

func TestRelayCmd_AskRefusalsAreExit13WithTheCodeLast(t *testing.T) {
	for _, code := range []string{team.ErrNotMember, team.ErrRelayOpen, team.ErrRelayUnsupported} {
		d := &fakeRelayAskDaemon{status: http.StatusConflict, body: team.APIError{Error: code, Detail: "no"}}
		got, stdout, stderr := driveRelay(t, context.Background(), d, "ask", "--session", "sid-m", "--used", "71", "--window", "1")
		toks := strings.Fields(stderr)
		if got != ExitRefused || stdout != "" || len(toks) == 0 || toks[len(toks)-1] != code {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", code, got, stdout, stderr)
		}
	}
}

func TestRelayCmd_AskGrammar(t *testing.T) {
	for _, args := range [][]string{
		{"ask"},
		{"ask", "--used", "71", "--window", "1"}, // no session
		{"ask", "--session", "s", "--window", "1"},                      // no --used
		{"ask", "--session", "s", "--used", "101", "--window", "1"},     // over 100
		{"ask", "--session", "s", "--used", "71"},                       // no --window
		{"ask", "--session", "s", "--used", "71", "--window", "1", "x"}, // stray argument
		{"ask", "--session", "s", "--used", "71", "--window", "1", "--request-id", "nope"},
	} {
		if code, _, _ := driveRelay(t, context.Background(), &fakeRelayAskDaemon{}, args...); code != ExitUsage {
			t.Errorf("%v: code=%d, want %d", args, code, ExitUsage)
		}
	}
}

// `pdx team` shows an open ask in the TASK column with the minutes left.
func TestTeamCmd_TaskColumnShowsAnOpenRelayAsk(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	old := taskNow
	taskNow = func() time.Time { return now }
	t.Cleanup(func() { taskNow = old })

	v := fakeView()
	v.Members[0].RelayAskUntil = now.Add(150 * time.Second).UnixMilli() // 2m30s left → 3
	d := &fakeTeamCmdDaemon{view: answer{body: v}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK || !strings.Contains(stdout, "接力申請 (剩 3 分)") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if lines := strings.Split(stdout, "\n"); strings.Contains(lines[2], "接力申請") {
		t.Fatalf("the other member shows an ask: %q", lines[2])
	}
	v.Members[0].RelayAskUntil = now.Add(-time.Second).UnixMilli() // a window that passed is not shown
	d = &fakeTeamCmdDaemon{view: answer{body: v}}
	if _, stdout, _ := driveTeamCmd(t, runTeamCmd, d); strings.Contains(stdout, "接力申請") {
		t.Fatalf("an expired ask is shown: %q", stdout)
	}
}
