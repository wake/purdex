// cmd/pdx/team_cmd_host_test.go
package main

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// X5 (cross-host team spec §8): `pdx team` has a HOST column; a remote member shows its host's alias, its state as the
// lead host holds it, and `(主機無回應)` in CTX when its host did not answer.
func TestTeamCmd_HostColumnAndUnreachableHost(t *testing.T) {
	v := fakeView()
	pct := 61.0
	remote := fakeMember(team.SpawnRequest{ID: "op-3", Cwd: "/r", Model: "sonnet"})
	remote.Ref, remote.Address, remote.HostID, remote.HostAlias, remote.State = "_r3r3r3", "air26/_r3r3r3", "hostM", "air26", team.MemberJoining
	remote.Context = &team.MemberContext{UsedPercentage: &pct, ModelID: "claude-opus-5-5", Effort: "high"}
	down := fakeMember(team.SpawnRequest{ID: "op-4", Cwd: "/r", Model: "sonnet"})
	down.Ref, down.Address, down.HostID, down.HostAlias, down.ContextUnavailable = "_r4r4r4", "air26/_r4r4r4", "hostM", "air26", true
	v.Members = append(v.Members, *remote, *down)

	d := &fakeTeamCmdDaemon{view: answer{body: v}}
	code, stdout, stderr := driveTeamCmd(t, runTeamCmd, d)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("stdout = %q", stdout)
	}
	if h := strings.Fields(lines[0]); h[1] != "HOST" {
		t.Fatalf("header = %q", lines[0])
	}
	local := strings.Fields(lines[1])
	if local[1] != "-" {
		t.Errorf("a local member's HOST = %q, want -", local[1])
	}
	r := strings.Fields(lines[3])
	if r[0] != "air26/_r3r3r3" || r[1] != "air26" || r[4] != "joining" || r[5] != "61%" || r[8] != "claude-opus-5-5" {
		t.Errorf("remote row = %q", lines[3])
	}
	if !strings.Contains(lines[4], "(主機無回應)") || strings.Contains(lines[3], "(主機無回應)") {
		t.Errorf("unreachable marker: %q / %q", lines[3], lines[4])
	}
}
