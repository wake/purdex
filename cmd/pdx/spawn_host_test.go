// cmd/pdx/spawn_host_test.go
package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// `pdx spawn --host <alias>` (cross-host team spec §5.5; plan X4b-2): the cwd is a path on the member host, so it is required
// and taken as given; the request carries the alias; a failure the member host reported exits by its kind.

func TestSpawnHost_TheRequestCarriesTheAliasAndTheCwdAsGiven(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--host", "air26", "--cwd", "/Users/wake/Workspace/x", "--model", "sonnet")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if req := d.spawnReq[0]; req.Host != "air26" || req.Cwd != "/Users/wake/Workspace/x" {
		t.Fatalf("request = %+v", req)
	}
}

func TestSpawnHost_WithoutHostTheRequestHasNone(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	if code, _, stderr := driveTeamCmd(t, runSpawnCmd, d, "--cwd", "/w", "--model", "sonnet"); code != ExitOK || d.spawnReq[0].Host != "" {
		t.Fatalf("code=%d host=%q stderr=%q", code, d.spawnReq[0].Host, stderr)
	}
}

// The lead's working directory means nothing on another host, and a relative path would be made absolute against it.
func TestSpawnHost_NeedsAnAbsoluteCwdOfItsOwn(t *testing.T) {
	d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnDone}}
	for _, args := range [][]string{
		{"--host", "air26"},
		{"--host", "air26", "--cwd", "rel/dir"},
		{"--host", ""},
		{"--host", "bad host", "--cwd", "/w"},
	} {
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, args...)
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx spawn: ") {
			t.Errorf("%q: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if n := d.count(); n != 0 {
		t.Errorf("the daemon saw %d request(s), want 0", n)
	}
}

// What the member host (or the 10 minute void) reported: exit by kind, the code last.
func TestSpawnHost_FailureReasonsExitByKind(t *testing.T) {
	for reason, want := range map[string]int{
		team.SpawnReasonStartTimeout: ExitMemberFailed,
		"remote_unreachable":         ExitMemberFailed,
		team.ErrCwdOutsideGrant:      ExitRefused,
		"host_not_allowed":           ExitRefused,
		"capacity_exceeded":          ExitRefused,
		team.SpawnReasonLaunchFailed: ExitError,
	} {
		d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{spawnFailed(reason)}}
		code, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--host", "air26", "--cwd", "/w", "--model", "sonnet")
		if code != want || stdout != "" || lastToken(stderr) != reason {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", reason, code, stdout, stderr)
		}
	}
}

// Refusals the lead host makes before anything is created (rule 7) are exit 13, the code last.
func TestSpawnHost_CapabilityRefusalsExit13(t *testing.T) {
	for _, code := range []string{team.ErrRemoteUnsupported, "host_not_allowed"} {
		d := &fakeTeamCmdDaemon{spawns: []func(team.SpawnRequest) answer{refuse(http.StatusConflict, code)}}
		got, stdout, stderr := driveTeamCmd(t, runSpawnCmd, d, "--host", "air26", "--cwd", "/w", "--model", "sonnet")
		if got != ExitRefused || stdout != "" || lastToken(stderr) != code {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", code, got, stdout, stderr)
		}
	}
}
