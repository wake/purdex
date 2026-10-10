// internal/module/team/appearance_join_test.go
package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #2346: a member that joins AFTER a rename gets the team's label and colour with its adopt / spawn command, so its
// member host shows the look at once. Fields are optional on the wire: an older lead host sends none, and a command
// without them leaves what the row already holds.

// L: the commands carry the stored look.
func TestJoin_SpawnAndAdoptCommandsCarryTheStoredLook(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	if code, body := f.putAppearance(appearanceBody(nil)); code != http.StatusOK { // name, label 資源線, colour 3
		t.Fatalf("put = %d %s", code, body)
	}
	if code, _, e := f.spawnRemote(1, "air26", nil); code != 200 {
		t.Fatalf("spawn = %d %+v", code, e)
	}
	var tc team.TeamCommand
	_ = json.Unmarshal(f.commandsOf(CmdSpawn)[0].Body, &tc)
	if tc.TeamName != "資源線：租約" || tc.TeamLabel != "資源線" || tc.TeamColor == nil || *tc.TeamColor != 3 {
		t.Fatalf("spawn body = %+v", tc)
	}
	tm := f.teamRow()
	payload := team.AdoptPayload{TeamID: tm.ID, TargetHostID: "hostM", TargetSessionID: "sid-t", TargetRef: "_tgt001"}
	storedAdopt := func(id string) team.TeamCommand {
		t.Helper()
		ad, err := f.m.adoptRemoteCommand(id, payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.m.store.EnqueueCommand(f.m.store.db, *ad, f.clock.Load()); err != nil {
			t.Fatal(err)
		}
		var tc team.TeamCommand
		for _, c := range f.commandsOf(CmdAdopt) {
			if c.ID == id {
				_ = json.Unmarshal(c.Body, &tc)
			}
		}
		return tc
	}
	if tc = storedAdopt(cmdUUID1); tc.TeamLabel != "資源線" || tc.TeamColor == nil || *tc.TeamColor != 3 {
		t.Fatalf("adopt body = %+v", tc)
	}
	// automatic colour: absent
	if code, _ := f.putAppearance(appearanceBody(map[string]any{"team_color": nil})); code != http.StatusOK {
		t.Fatal("put")
	}
	if tc = storedAdopt(cmdUUID2); tc.TeamColor != nil {
		t.Fatalf("automatic colour sent as %d", *tc.TeamColor)
	}
}

// The look is read where the command is stored, not where it was built: a rename between the two is not lost, and enqueueing
// the same command again after a rename is a replay, not "the id is taken".
func TestJoin_TheLookIsReadInTheEnqueuingTransaction(t *testing.T) {
	f, _ := leadSpawnFixture(t)
	tm := f.teamRow()
	ad, err := f.m.adoptRemoteCommand(cmdUUID1, team.AdoptPayload{TeamID: tm.ID, TargetHostID: "hostM", TargetSessionID: "sid-t", TargetRef: "_tgt001"})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := f.putAppearance(appearanceBody(nil)); code != http.StatusOK { // after the build, before the enqueue
		t.Fatalf("put = %d %s", code, body)
	}
	if err := f.m.store.EnqueueCommand(f.m.store.db, *ad, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	var tc team.TeamCommand
	_ = json.Unmarshal(f.commandsOf(CmdAdopt)[0].Body, &tc)
	if tc.TeamName != "資源線：租約" || tc.TeamLabel != "資源線" || tc.TeamColor == nil || *tc.TeamColor != 3 {
		t.Fatalf("stored body = %+v, want the look as of the enqueue", tc)
	}
	if code, _ := f.putAppearance(appearanceBody(map[string]any{"team_name": "改名", "team_label": "改"})); code != http.StatusOK {
		t.Fatal("put")
	}
	if err := f.m.store.EnqueueCommand(f.m.store.db, *ad, f.clock.Load()); err != nil {
		t.Fatalf("the same command after a rename = %v, want a replay", err)
	}
	other := *ad
	other.Body = []byte(strings.Replace(string(ad.Body), `"target_ref":"_tgt001"`, `"target_ref":"_other1"`, 1))
	if err := f.m.store.EnqueueCommand(f.m.store.db, other, f.clock.Load()); err == nil {
		t.Fatal("a different command under the same id was accepted")
	}
}

// M: an adopt that carries the look stores it; one that carries none (an older lead host) stores none, and a replay of
// it does not clear what team.appearance wrote since.
func TestJoin_AdoptStoresTheLookAndAnOlderLeadHostLeavesIt(t *testing.T) {
	f := newFixture(t)
	f.setLeadHost(true)
	f.origins.show(team.Origin{SessionID: "sid-t", Ref: "_tgt001", PID: 42, ProcStart: "ps2", Cwd: "/w"})
	f.origins.show(team.Origin{SessionID: "sid-u", Ref: "_tgt002", PID: 43, ProcStart: "ps3", Cwd: "/w"})

	two := 2
	withLook := wireAdopt(cmdUUID1, cmdUUID1, "sid-t")
	withLook.TeamLabel, withLook.TeamColor = "線", &two
	if code, body := f.postCmd(leadPrincipal(), withLook); code != http.StatusOK {
		t.Fatalf("adopt: %d %s", code, body)
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID1); row.TeamLabel != "線" || !row.TeamColor.Valid || row.TeamColor.Int64 != 2 {
		t.Fatalf("row = %+v", row)
	}

	old := wireAdopt(cmdUUID2, cmdUUID2, "sid-u") // no label, no colour
	old.TargetRef = "_tgt002"
	if code, body := f.postCmd(leadPrincipal(), old); code != http.StatusOK {
		t.Fatalf("adopt: %d %s", code, body)
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID2); row.TeamLabel != "" || row.TeamColor.Valid {
		t.Fatalf("row = %+v", row)
	}
	// team.appearance writes the look; the old-format adopt, sent again, is the stored answer and leaves it
	five := 5
	ap := relCmd(cmdUUID3, team.CommandAppearance, "")
	ap.ToHostID, ap.TeamName, ap.TeamLabel, ap.TeamColor = "h:1", "新", "新", &five
	if code, body := f.postCmd(leadPrincipal(), ap); code != http.StatusOK {
		t.Fatalf("appearance: %d %s", code, body)
	}
	if code, _ := f.postCmd(leadPrincipal(), old); code != http.StatusOK {
		t.Fatal("replay")
	}
	if row, _, _ := f.m.store.RemoteMember(cmdUUID2); row.TeamLabel != "新" || row.TeamColor.Int64 != 5 {
		t.Fatalf("a replay cleared the look: %+v", row)
	}
	// out-of-form values are refused on the join commands too
	bad := wireAdopt("77777777-7777-4777-8777-777777777777", "77777777-7777-4777-8777-777777777777", "sid-t")
	bad.TeamLabel = " padded "
	if code, _ := f.postCmd(leadPrincipal(), bad); code != http.StatusBadRequest {
		t.Fatalf("unnormalised label = %d, want 400", code)
	}
}

// M: a forwarded spawn registers under the look its command carried (and a later team.appearance still wins).
func TestJoin_SpawnRegistersUnderTheLookItCarried(t *testing.T) {
	for _, carries := range []bool{true, false} {
		f, root := remoteSpawnFixture(t)
		f.register("%0", "sid-m1")
		c := spawnCommand(cmdUUID1, root)
		if carries {
			four := 4
			c.TeamLabel, c.TeamColor = "線", &four
		}
		if code, body := f.postCmd(leadPrincipal(), c); code != http.StatusOK {
			t.Fatalf("spawn = %d %s", code, body)
		}
		f.m.spawnWG.Wait()
		row, ok, _ := f.m.store.RemoteMember(cmdUUID1)
		if !ok {
			t.Fatal("no row")
		}
		if carries && (row.TeamLabel != "線" || !row.TeamColor.Valid || row.TeamColor.Int64 != 4) {
			t.Fatalf("row = %+v", row)
		}
		if !carries && (row.TeamLabel != "" || row.TeamColor.Valid) {
			t.Fatalf("row = %+v", row)
		}
	}
}
