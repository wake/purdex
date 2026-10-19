package teammod

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A spawn on another host (name pool v2 spec §5.2). The lead host chooses the member's name with its pair history and sends it in the
// `spawn` command; only the member host holds a reservation (the lead host has no session to name), so the lead host's choice is
// given back at once.

func TestSpawnHostPoolName_TheLeadHostChoosesAndTheCommandCarriesIt(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	n := withNames(f, "mia")
	code, op, e := f.spawnRemote(1, "air26", nil)
	if code != 200 || op.PoolName != "mia" {
		t.Fatalf("spawn = %d %+v %+v, want the answer to carry pool_name mia", code, op, e)
	}
	if len(n.reserved) != 1 || n.reserved[0].lead != "sid-1" || n.reserved[0].op != spawnID(1) {
		t.Fatalf("reserve calls = %+v, want one for the lead sid-1, op 1", n.reserved)
	}
	var tc team.TeamCommand
	cmds := f.commandsOf(CmdSpawn)
	if len(cmds) != 1 {
		t.Fatalf("commands = %+v", cmds)
	}
	_ = json.Unmarshal(cmds[0].Body, &tc)
	if tc.PoolName != "mia" {
		t.Fatalf("command = %+v, want pool_name mia", tc)
	}
	var stored string
	if err := f.m.store.db.QueryRow(`SELECT pool_name FROM remote_spawns WHERE id = ?`, spawnID(1)).Scan(&stored); err != nil || stored != "mia" {
		t.Fatalf("stored = %q err=%v, want the row to keep the choice for a replay's answer", stored, err)
	}
	// only a choice: this host holds nothing for a session that is not its own
	if got := n.releasedOps(); len(got) != 1 || got[0] != spawnID(1) {
		t.Fatalf("released = %v, want the lead host's choice given back at once", got)
	}
}

// A replay of the request answers with the name the op was created with, and chooses nothing again.
func TestSpawnHostPoolName_AReplayKeepsTheFirstChoice(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	n := withNames(f, "mia")
	f.spawnRemote(1, "air26", nil)
	n.code = "anna" // what a second choice would give
	code, op, e := f.spawnRemote(1, "air26", nil)
	if code != 200 || op.PoolName != "mia" || n.reserveCount() != 1 {
		t.Fatalf("replay = %d %+v %+v, %d reserve calls; want mia and one choice", code, op, e, n.reserveCount())
	}
}

// Without a name service, or with nothing free, the command carries no name (an older member host would ignore it anyway).
func TestSpawnHostPoolName_NoNameNoField(t *testing.T) {
	for name, setup := range map[string]func(f *fixture){
		"no name service": func(f *fixture) {},
		"nothing free":    func(f *fixture) { withNames(f, "") },
	} {
		f, _ := leadSpawnFixture(t)
		setup(f)
		code, op, e := f.spawnRemote(1, "air26", nil)
		if code != 200 || op.PoolName != "" {
			t.Fatalf("%s: spawn = %d %+v %+v", name, code, op, e)
		}
		var tc map[string]any
		_ = json.Unmarshal(f.commandsOf(CmdSpawn)[0].Body, &tc)
		if _, has := tc["pool_name"]; has {
			t.Fatalf("%s: the command carries pool_name: %v", name, tc)
		}
	}
}
