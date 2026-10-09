// cmd/pdx/team_cmd_host_shares_test.go
package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// CPU / MEM are the lead host's own numbers, joined by session id: a remote member whose session id collides with a
// local one (ids are not unique across hosts) must not show them.
func TestTeamCmd_RemoteMemberNeverTakesLocalCPUMEM(t *testing.T) {
	snap := fakeSnapshot()
	snap.Sessions = []resources.SessionUse{{SessionID: "cc-sid-member-1", CPU: 4.4, Mem: 2.2, Use: 5}}
	v := teamViewTwoSessions()
	remote := fakeMember(team.SpawnRequest{ID: "op-3", Cwd: "/r", Model: "sonnet"})
	remote.SessionID, remote.Ref, remote.Address, remote.HostID, remote.HostAlias = "cc-sid-member-1", "_r3r3r3", "air26/_r3r3r3", "hostM", "air26"
	v.Members = append(v.Members, *remote)
	d := &fakeResourcesDaemon{next: &fakeTeamCmdDaemon{view: answer{body: v}}, res: answer{body: snap}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	local, rem := strings.Fields(lines[1]), strings.Fields(lines[len(lines)-1])
	if local[7] != "4%" {
		t.Fatalf("the local member lost its CPU: %q", lines[1])
	}
	if rem[1] != "air26" || rem[7] != "-" || rem[8] != "-" {
		t.Fatalf("the remote member took the local numbers: %q", lines[len(lines)-1])
	}
}
