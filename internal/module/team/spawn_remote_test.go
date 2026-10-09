// internal/module/team/spawn_remote_test.go
package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// A spawn forwarded from a lead host (cross-host team spec §5.5, §6.2, plan X4a-2): accepted as a spawn_ops row marked
// with the lead host, run by the same runner against the roots that host's entry grants, ended by a fact.

func grantLeadHost(f *fixture, allow bool, roots ...string) {
	f.core.CfgMu.Lock()
	defer f.core.CfgMu.Unlock()
	f.core.Cfg.Peers.Hosts = []config.PeerHost{{Alias: "lead", URL: "https://lead.example", HostID: "lead:1", InboundToken: "i", AllowTeam: allow, TeamRoots: roots}}
}

func spawnCommand(id, cwd string) team.TeamCommand {
	return team.TeamCommand{ID: id, Kind: team.CommandSpawn, ToHostID: "h:1", TeamID: "team-L", TeamName: "T", MK: id, Cwd: cwd,
		Title: "ios", Model: "sonnet", Lead: team.TeamLead{SessionID: "lead-sid", Ref: "_lead01", Address: "lead/x [lead01]", PID: 7, ProcStart: "ps1"}}
}

func remoteSpawnFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f, root := newSpawnFixture(t, 2)
	grantLeadHost(f, true, root)
	return f, root
}

// jsonHas says whether the JSON object body has the string field key equal to want.
func jsonHas(body, key, want string) bool {
	var m map[string]any
	return json.Unmarshal([]byte(body), &m) == nil && m[key] == want
}

func (f *fixture) factsToLead() []factRow {
	f.t.Helper()
	rows, err := f.m.store.FactsOfHost("lead:1")
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func TestRemoteSpawn_RegisteredMakesARemoteMemberAndOneFact(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	f.register("%0", "sid-m1")
	code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root))
	if code != http.StatusOK || outcomeState(t, body) != "accepted" {
		t.Fatalf("spawn = %d %s", code, body)
	}
	f.m.spawnWG.Wait()

	op, _, _ := f.m.store.GetSpawnOp(cmdUUID1)
	if op.State != team.SpawnDone || op.LeadHostID != "lead:1" || op.TeamID != "team-L" {
		t.Fatalf("op = %+v", op)
	}
	row, ok, err := f.m.store.RemoteMember(cmdUUID1)
	if err != nil || !ok || row.State != remoteActive || row.MemberSessionID != "sid-m1" || row.Origin != team.MemberOriginSpawned ||
		row.LeadHostID != "lead:1" || row.LeadSessionID != "lead-sid" || row.TeamName != "T" || row.PID != 31 || row.Cwd != root || row.Title != "ios" {
		t.Fatalf("remote member = %+v ok=%v err=%v", row, ok, err)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE session_id = 'sid-m1'`).Scan(&n)
	if n != 0 {
		t.Fatal("a forwarded spawn wrote a local team member row")
	}
	facts := f.factsToLead()
	if len(facts) != 1 || facts[0].Kind != team.FactRegistered || facts[0].MK != cmdUUID1 || facts[0].TeamID != "team-L" {
		t.Fatalf("facts = %+v", facts)
	}
	if !jsonHas(facts[0].BodyJSON, "member_session_id", "sid-m1") || !jsonHas(facts[0].BodyJSON, "ref", row.Ref) {
		t.Fatalf("fact body = %s", facts[0].BodyJSON)
	}
	// the very same command again: the stored answer, no second op, no second runner
	if code, _ := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("replay = %d", code)
	}
	f.m.spawnWG.Wait()
	if len(f.factsToLead()) != 1 {
		t.Fatal("a replay queued another fact")
	}
}

func TestRemoteSpawn_CwdOutsideTheGrantIsRefusedAndNothingIsStored(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	outside := t.TempDir()
	i := 0
	for name, cwd := range map[string]string{"outside": outside, "relative": "sub", "sibling with the root as string prefix": root + "-evil"} {
		i++ // a refusal is stored under its id: each case is a command of its own
		id := fmt.Sprintf("%08x-0000-4000-8000-000000000000", 0x100+i)
		if name == "sibling with the root as string prefix" {
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(cwd) })
		}
		code, body := f.postCmd(leadPrincipal(), spawnCommand(id, cwd))
		if name == "relative" {
			if code != http.StatusBadRequest {
				t.Fatalf("%s = %d %s, want 400", name, code, body)
			}
			continue
		}
		if code != http.StatusConflict || errCode(t, body) != team.ErrCwdOutsideGrant {
			t.Fatalf("%s = %d %s", name, code, body)
		}
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM spawn_ops WHERE lead_host_id <> ''`).Scan(&n)
	if n != 0 {
		t.Fatal("a refused spawn left an op")
	}
}

// A symlink inside a root that points out of it is outside; a root that is no longer a directory grants nothing.
func TestRemoteSpawn_ResolveUnderRoots(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	inside := filepath.Join(root, "work")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveUnderRoots([]string{root}, inside); !ok || got != inside {
		t.Fatalf("inside = %q %v", got, ok)
	}
	if _, ok := resolveUnderRoots([]string{root}, filepath.Join(root, "escape")); ok {
		t.Fatal("a symlink out of the root was inside")
	}
	if _, ok := resolveUnderRoots(nil, inside); ok {
		t.Fatal("no roots granted a spawn")
	}
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveUnderRoots([]string{file}, file); ok {
		t.Fatal("a root that is a file granted a spawn")
	}
	// the root swapped for a symlink to elsewhere after it was granted: judged by where it resolves NOW
	swapped := filepath.Join(t.TempDir(), "granted")
	if err := os.Symlink(outside, swapped); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveUnderRoots([]string{swapped}, inside); ok {
		t.Fatal("a swapped root still admitted the directory it used to be")
	}
	// ... and not what it points to now either: a root that no longer resolves to itself grants nothing
	if _, ok := resolveUnderRoots([]string{swapped}, outside); ok {
		t.Fatal("a root replaced by a symlink admitted its target")
	}
}

// The title is held to the limits a local spawn's is, before the command is accepted.
func TestRemoteSpawn_TitleIsValidated(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	c := spawnCommand(cmdUUID3, root)
	c.Title = strings.Repeat("t", 65)
	if code, body := f.postCmd(leadPrincipal(), c); code != http.StatusBadRequest {
		t.Fatalf("spawn with a 65 byte title = %d %s", code, body)
	}
	if _, ok, _ := f.m.store.GetSpawnOp(cmdUUID3); ok {
		t.Fatal("a refused spawn left an op")
	}
}

func TestRemoteSpawn_NeedsConsent(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	grantLeadHost(f, false, root)
	code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root))
	if code != http.StatusForbidden || errCode(t, body) != team.ErrCommandHostNotAllowed {
		t.Fatalf("spawn = %d %s", code, body)
	}
	if _, ok, _ := f.m.store.GetSpawnOp(cmdUUID1); ok {
		t.Fatal("a refused spawn left an op")
	}
}

// A runner that fails ends the op and queues the lead host's spawn_failed fact in the same transaction; here the grant
// is withdrawn between the accept and the runner's first step.
func TestRemoteSpawn_FailureIsAFactNotASilence(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	f.m.beforeSpawnStep = func(spawnRow) { grantLeadHost(f, false, root) }
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	f.m.spawnWG.Wait()
	op, _, _ := f.m.store.GetSpawnOp(cmdUUID1)
	if op.State != team.SpawnFailed || op.Reason != team.SpawnReasonCreateFailed {
		t.Fatalf("op = %+v", op)
	}
	facts := f.factsToLead()
	if len(facts) != 1 || facts[0].Kind != team.FactSpawnFailed || facts[0].MK != cmdUUID1 || !jsonHas(facts[0].BodyJSON, "reason", team.SpawnReasonCreateFailed) {
		t.Fatalf("facts = %+v", facts)
	}
	if _, ok, _ := f.m.store.RemoteMember(cmdUUID1); ok {
		t.Fatal("a failed spawn left a remote member")
	}
}

// A LOCAL op that fails writes no fact (it has no lead host to tell).
func TestRemoteSpawn_LocalFailureWritesNoFact(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.acceptOp(1, filepath.Join(root, "missing"), nil)
	f.m.startSpawn(spawnID(1))
	f.m.spawnWG.Wait()
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_facts`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d facts for a local op", n)
	}
}
