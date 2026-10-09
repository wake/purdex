package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// teamViewTwoSessions is fakeView with distinct session ids, so the join
// has something to tell apart: member 1 is cc-sid-member-1, member 2 is
// cc-sid-member-2.
func teamViewTwoSessions() team.TeamView {
	v := fakeView()
	v.Members[1].SessionID = "cc-sid-member-2"
	return v
}

// teamWithResources is a daemon that answers /api/team and /api/resources.
func teamWithResources(res answer) *fakeResourcesDaemon {
	return &fakeResourcesDaemon{next: &fakeTeamCmdDaemon{view: answer{body: teamViewTwoSessions()}}, res: res}
}

func teamRows(t *testing.T, stdout string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q, want a header and two rows", stdout)
	}
	return lines
}

// Review #14: CPU and MEM are the member's share of the host in D-1 units,
// whole percents, joined on session id. Member 2 has no row in the sample.
func TestTeam_CPUMEMJoined(t *testing.T) {
	snap := fakeSnapshot()
	snap.Sessions = []resources.SessionUse{
		{SessionID: "cc-sid-member-1", CPU: 4.4, Mem: 2.2, Use: 5, RSSBytes: 600000000},
		{SessionID: "someone-else", CPU: 50, Mem: 40, Use: 50},
	}
	d := teamWithResources(answer{body: snap})
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	lines := teamRows(t, stdout)
	if got, want := fieldsOf(lines[0]), "ADDRESS HOST REF TITLE STATE CTX CPU MEM MODEL EFFORT TASK LAST CWD TMUX"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	if got, want := fieldsOf(lines[1]), "mlab/_m1m1m1 - _m1m1m1 p4 tester active 42% 4% 2% claude-sonnet-4-5 low - - /w/a tm-1111111122"; got != want {
		t.Errorf("row 1 = %q, want %q", got, want)
	}
	if got, want := fieldsOf(lines[2]), "mlab/_m2m2m2 - _m2m2m2 - killed - - - - - - - /w/b tm-2222222222"; got != want {
		t.Errorf("row 2 = %q, want %q", got, want)
	}
	if d.hits() != 1 {
		t.Errorf("resources asked %d times, want once", d.hits())
	}
}

// The team table must never break because of the resources call.
func TestTeam_ResourcesFailureShowsDash(t *testing.T) {
	old := teamResourcesTimeout
	teamResourcesTimeout = 150 * time.Millisecond
	t.Cleanup(func() { teamResourcesTimeout = old })

	unavailable := fakeSnapshot()
	unavailable.Available, unavailable.Reason = false, resources.ReasonSampleFailed
	cases := map[string]*fakeResourcesDaemon{
		"500": teamWithResources(answer{status: http.StatusInternalServerError, body: team.APIError{Error: "storage", Detail: "boom"}}),
		"404": {next: &fakeTeamCmdDaemon{view: answer{body: teamViewTwoSessions()}}}, // no route: an older daemon
		"not json": {next: &fakeTeamCmdDaemon{view: answer{body: teamViewTwoSessions()}},
			serve: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>nope")) }},
		"unavailable": teamWithResources(answer{body: unavailable}),
		"hung": {next: &fakeTeamCmdDaemon{view: answer{body: teamViewTwoSessions()}},
			serve: func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
			if code != ExitOK || stderr != "" {
				t.Fatalf("code=%d stderr=%q, want a clean exit 0", code, stderr)
			}
			lines := teamRows(t, stdout)
			if got, want := fieldsOf(lines[1]), "mlab/_m1m1m1 - _m1m1m1 p4 tester active 42% - - claude-sonnet-4-5 low - - /w/a tm-1111111122"; got != want {
				t.Errorf("row 1 = %q, want %q", got, want)
			}
			if got, want := fieldsOf(lines[2]), "mlab/_m2m2m2 - _m2m2m2 - killed - - - - - - - /w/b tm-2222222222"; got != want {
				t.Errorf("row 2 = %q, want %q", got, want)
			}
		})
	}
}

// Codex attack (medium): the optional resources call shares the command's
// daemon client, which prints "daemon 重啟中" on a retryable failure. A daemon
// that restarts right after /api/team answered must not put a line on stderr
// for a call whose failure the table already hides.
func TestTeam_ResourcesRestartDiagnosticStaysSilent(t *testing.T) {
	old := teamResourcesTimeout
	teamResourcesTimeout = 150 * time.Millisecond
	t.Cleanup(func() { teamResourcesTimeout = old })

	d := &fakeResourcesDaemon{
		next: &fakeTeamCmdDaemon{view: answer{body: teamViewTwoSessions()}},
		serve: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"shutting_down"}`))
		},
	}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q, want exit 0 and a silent stderr", code, stderr)
	}
	if got, want := fieldsOf(teamRows(t, stdout)[1]), "mlab/_m1m1m1 - _m1m1m1 p4 tester active 42% - - claude-sonnet-4-5 low - - /w/a tm-1111111122"; got != want {
		t.Errorf("row 1 = %q, want %q", got, want)
	}
}

// Codex attack (medium): two rows for one session id (a daemon that lists a
// lingering twin) must not let the later, smaller one hide the real load.
func TestTeam_DuplicateSessionKeepsTheLargerShare(t *testing.T) {
	for name, rows := range map[string][]resources.SessionUse{
		"big first":   {{SessionID: "cc-sid-member-1", CPU: 30, Mem: 9, Use: 30}, {SessionID: "cc-sid-member-1", CPU: 0, Mem: 0, Use: 0}},
		"small first": {{SessionID: "cc-sid-member-1", CPU: 0, Mem: 0, Use: 0}, {SessionID: "cc-sid-member-1", CPU: 30, Mem: 9, Use: 30}},
	} {
		t.Run(name, func(t *testing.T) {
			snap := fakeSnapshot()
			snap.Sessions = rows
			code, stdout, stderr := driveTeamCmd(t, runTeamCmd, teamWithResources(answer{body: snap}))
			if code != ExitOK || stderr != "" {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			if got, want := fieldsOf(teamRows(t, stdout)[1]), "mlab/_m1m1m1 - _m1m1m1 p4 tester active 42% 30% 9% claude-sonnet-4-5 low - - /w/a tm-1111111122"; got != want {
				t.Errorf("row 1 = %q, want %q", got, want)
			}
		})
	}
}

// --json is the daemon's team view as is, and costs no resources request.
func TestTeam_JSONUnchanged(t *testing.T) {
	inner := &fakeTeamCmdDaemon{view: answer{body: teamViewTwoSessions()}}
	d := &fakeResourcesDaemon{next: inner, res: answer{body: fakeSnapshot()}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d, "--json")
	if code != ExitOK || stderr != "" || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, `"cpu"`) || strings.Contains(stdout, `"mem"`) {
		t.Errorf("--json gained resource fields: %q", stdout)
	}
	if d.hits() != 0 {
		t.Errorf("--json asked for resources %d time(s)", d.hits())
	}
}
