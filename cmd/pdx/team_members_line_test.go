package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
)

// `pdx team` prints `members M/N` in its header: the active members over the team's limit (the user's, set in the App).
// A released or killed member is not counted. --json carries team.grant.max_members as it is.
func TestTeamCmd_PrintsMembersOverTheLimit(t *testing.T) {
	d := &fakeTeamCmdDaemon{view: answer{body: fakeView()}} // one active, one killed, limit 3
	code, stdout, stderr := driveTeamCmdWith(t, runTeamCmd, d, leadEnv(), []daemonclient.Option{leadClockOpt()})
	if code != ExitOK || !strings.HasPrefix(stdout, "members 1/3\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a members 1/3 header", code, stdout, stderr)
	}
	code, stdout, _ = driveTeamCmdWith(t, runTeamCmd, d, leadEnv(), []daemonclient.Option{leadClockOpt()}, "--json")
	if code != ExitOK || !strings.Contains(stdout, `"grant":{"max_members":3`) {
		t.Fatalf("--json = %q", stdout)
	}
}
