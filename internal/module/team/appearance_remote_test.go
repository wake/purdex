// internal/module/team/appearance_remote_test.go
package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// #2288: a rename on the lead host reaches the member hosts as team.appearance, queued in the transaction that stores it.
// Mutation gates: no enqueue / enqueue for a host that does not announce it / a spawn-only host left out → red.

func appearanceCmds(f *fixture) map[string]team.TeamCommand {
	f.t.Helper()
	out := map[string]team.TeamCommand{}
	for _, c := range f.commandsOf(CmdAppearance) {
		var tc team.TeamCommand
		if err := json.Unmarshal(c.Body, &tc); err != nil {
			f.t.Fatal(err)
		}
		out[c.HostID] = tc
	}
	return out
}

// pairHosts makes the config carry the member hosts the fake caller answers for (the fan-out asks every paired host).
func pairHosts(f *fixture, ids ...string) {
	f.core.CfgMu.Lock()
	defer f.core.CfgMu.Unlock()
	f.core.Cfg.Peers.Hosts = nil
	for _, id := range ids {
		f.core.Cfg.Peers.Hosts = append(f.core.Cfg.Peers.Hosts, config.PeerHost{Alias: "a-" + id, URL: "https://" + id, HostID: id, InboundToken: "i"})
	}
}

func TestAppearance_TheRenameIsQueuedForEveryAnnouncingMemberHost(t *testing.T) {
	f, fc := remoteFixture(t)
	pairHosts(f, "hostM", "hostN", "hostO")
	fc.aliases["old"] = "hostO"
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdAppearance), AllowTeam: true}
	fc.caps["hostN"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdAppearance), AllowTeam: true}
	fc.caps["hostO"] = ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true} // an older daemon: no team.appearance
	f.remoteRow("abc12", "hostM", "mk1", rowActive)
	f.remoteRow("abc13", "hostO", "mk2", rowActive)
	f.remoteSpawn("spawn-n", "hostN", "n") // only a running forwarded spawn

	if code, body := f.putAppearance(appearanceBody(nil)); code != http.StatusOK {
		t.Fatalf("put = %d %s", code, body)
	}
	got := appearanceCmds(f)
	if len(got) != 2 || got["hostO"].ID != "" {
		t.Fatalf("commands = %+v, want hostM and hostN only", got)
	}
	for _, h := range []string{"hostM", "hostN"} {
		c := got[h]
		if c.TeamName != "資源線：租約" || c.TeamLabel != "資源線" || c.TeamColor == nil || *c.TeamColor != 3 || c.TeamID != uid(1) || c.Lead.SessionID == "" {
			t.Fatalf("%s command = %+v", h, c)
		}
	}
	// automatic colour: absent
	if code, body := f.putAppearance(appearanceBody(map[string]any{"team_color": nil})); code != http.StatusOK {
		t.Fatalf("put = %d %s", code, body)
	}
	if rows := f.commandsOf(CmdAppearance); len(rows) != 4 {
		t.Fatalf("%d commands after the second rename, want 4", len(rows))
	}
	var last team.TeamCommand
	rows := f.commandsOf(CmdAppearance)
	_ = json.Unmarshal(rows[len(rows)-1].Body, &last)
	if last.TeamColor != nil {
		t.Fatalf("automatic colour sent as %d", *last.TeamColor)
	}
}

func TestAppearance_NoMemberHostQueuesNothingAndTheRenameStands(t *testing.T) {
	f, fc := remoteFixture(t)
	pairHosts(f, "hostM")
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdAppearance), AllowTeam: true}
	if code, body := f.putAppearance(appearanceBody(nil)); code != http.StatusOK {
		t.Fatalf("put = %d %s", code, body)
	}
	if n := len(f.commandsOf(CmdAppearance)); n != 0 {
		t.Fatalf("%d commands", n)
	}
	if f.teamRow().TeamName != "資源線：租約" {
		t.Fatal("rename lost")
	}
}

// The member host's side.
func TestCommands_AppearanceUpdatesTheLeadHostsLiveRowsOfThatTeamOnly(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w"})
	if code, body := f.postCmd(leadPrincipal(), wireAdopt(cmdUUID1, cmdUUID1, "sid-t")); code != http.StatusOK {
		t.Fatalf("adopt: %d %s", code, body)
	}
	one := 5
	ap := relCmd(cmdUUID2, team.CommandAppearance, "")
	ap.ToHostID, ap.TeamName, ap.TeamLabel, ap.TeamColor = "h:1", "新名字", "新", &one
	other := ap
	other.ID, other.TeamID = cmdUUID3, "team-other"
	other.TeamName = "不相干"
	if code, body := f.postCmd(leadPrincipal(), other); code != http.StatusOK {
		t.Fatalf("other team: %d %s", code, body)
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID1); row.TeamName == "不相干" || row.TeamLabel != "" {
		t.Fatalf("another team's appearance touched the row: %+v", row)
	}
	if code, body := f.postCmd(leadPrincipal(), ap); code != http.StatusOK {
		t.Fatalf("appearance: %d %s", code, body)
	}
	row, _, _ := f.m.store.RemoteMember(cmdUUID1)
	if row.TeamName != "新名字" || row.TeamLabel != "新" || !row.TeamColor.Valid || row.TeamColor.Int64 != 5 {
		t.Fatalf("row = %+v", row)
	}
	// the same command again is the stored answer; a later one with the colour automatic clears it
	if code, _ := f.postCmd(leadPrincipal(), ap); code != http.StatusOK {
		t.Fatalf("replay = %d", code)
	}
	auto := ap
	auto.ID, auto.TeamColor = cmdUUID4, nil
	if code, body := f.postCmd(leadPrincipal(), auto); code != http.StatusOK {
		t.Fatalf("auto: %d %s", code, body)
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID1); row.TeamColor.Valid {
		t.Fatalf("colour kept: %+v", row)
	}
	// a name or label that is not the normalised form is refused (the lead host never stores one)
	for _, edit := range []func(*team.TeamCommand){
		func(c *team.TeamCommand) { c.TeamName = " padded " },
		func(c *team.TeamCommand) { c.TeamLabel = "far too long a label for the chip" },
	} {
		ap.ID = "66666666-6666-4666-8666-666666666666"
		bad := ap
		edit(&bad)
		if code, _ := f.postCmd(leadPrincipal(), bad); code != http.StatusBadRequest {
			t.Fatalf("unnormalised %+v = %d, want 400", bad, code)
		}
	}
	// out of range is refused as bad_request, nothing stored
	bad := 9
	ap.ID, ap.TeamColor = "55555555-5555-4555-8555-555555555555", &bad
	if code, _ := f.postCmd(leadPrincipal(), ap); code != http.StatusBadRequest {
		t.Fatalf("colour 9 = %d, want 400", code)
	}
}

// A forwarded spawn still running registers under the new look.
func TestCommands_AppearanceReachesARunningForwardedSpawn(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	f.register("%0", "sid-m1")
	waitReached, release := holdAt(f, team.StepLaunched)
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	waitReached()
	two := 2
	ap := relCmd(cmdUUID2, team.CommandAppearance, "")
	ap.ToHostID, ap.TeamID, ap.TeamName, ap.TeamLabel, ap.TeamColor = "h:1", "team-L", "改過", "改", &two
	if code, body := f.postCmd(leadPrincipal(), ap); code != http.StatusOK {
		t.Fatalf("appearance: %d %s", code, body)
	}
	release()
	f.m.spawnWG.Wait()
	row, ok, _ := f.m.store.RemoteMember(cmdUUID1)
	if !ok || row.TeamName != "改過" || row.TeamLabel != "改" || !row.TeamColor.Valid || row.TeamColor.Int64 != 2 {
		t.Fatalf("registered row = %+v ok=%v", row, ok)
	}
}

// A running op whose stored lead is empty or damaged is left as it is; the command itself still applies.
func TestCommands_AppearanceWithADamagedRunningOpStillApplies(t *testing.T) {
	f, root := remoteSpawnFixture(t)
	waitReached, release := holdAt(f, team.StepLaunched)
	if code, body := f.postCmd(leadPrincipal(), spawnCommand(cmdUUID1, root)); code != http.StatusOK {
		t.Fatalf("spawn = %d %s", code, body)
	}
	waitReached()
	if _, err := f.m.store.db.Exec(`UPDATE spawn_ops SET lead_json = '{not json' WHERE id = ?`, cmdUUID1); err != nil {
		t.Fatal(err)
	}
	ap := relCmd(cmdUUID2, team.CommandAppearance, "")
	ap.ToHostID, ap.TeamID, ap.TeamName, ap.TeamLabel = "h:1", "team-L", "改過", "改"
	if code, body := f.postCmd(leadPrincipal(), ap); code != http.StatusOK {
		t.Fatalf("appearance: %d %s", code, body)
	}
	release()
	f.m.spawnWG.Wait()
}
