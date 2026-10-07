package peers

import (
	"fmt"
	"testing"
)

// twinJSON is a registry file whose session id is chosen by the caller, so two
// files (a live process and a zombie) can share one.
func twinJSON(pid int, sid string) string {
	return fmt.Sprintf(
		`{"pid":%d,"sessionId":%q,"cwd":"/tmp","procStart":%q,"messagingSocketPath":"/tmp/%d.sock","name":"n","version":"v"}`,
		pid, sid, wantProcStart.Format(ProcStartLayout), pid,
	)
}

func TestReadRegistry_ZombieTwinOfLiveSessionIsDropped(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "100.json", twinJSON(100, "sid-shared")) // live
	writeFixture(t, dir, "200.json", twinJSON(200, "sid-shared")) // <defunct>, same session id
	writeFixture(t, dir, "300.json", twinJSON(300, "sid-other"))  // unrelated live

	live := allTrueLiveness(wantProcStart)
	asked := map[int]bool{}
	live.Zombie = func(pid int) bool { asked[pid] = true; return pid == 200 }

	entries, diag, err := ReadRegistryDiag(dir, live)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]bool{}
	for _, e := range entries {
		got[e.PID] = true
	}
	if len(entries) != 2 || !got[100] || !got[300] || got[200] {
		t.Fatalf("entries = %+v, want pids 100 and 300 only", entries)
	}
	if diag.Dead != 1 {
		t.Errorf("diag.Dead = %d, want 1 (the zombie is confirmed dead)", diag.Dead)
	}
	if asked[300] {
		t.Error("Zombie was asked about an entry with a unique session id (a fork per live entry)")
	}
}

func TestReadRegistry_NoZombieSeamKeepsBothTwins(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "100.json", twinJSON(100, "sid-shared"))
	writeFixture(t, dir, "200.json", twinJSON(200, "sid-shared"))
	entries, _, err := ReadRegistryDiag(dir, allTrueLiveness(wantProcStart))
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%d err=%v, want both kept when no Zombie seam is set", len(entries), err)
	}
}

func TestZombieState(t *testing.T) {
	for stat, want := range map[string]bool{"Z": true, "Z+": true, " Zs\n": true, "Ss": false, "S+": false, "R": false, "": false, "UE": false} {
		if got := zombieState(stat); got != want {
			t.Errorf("zombieState(%q) = %v, want %v", stat, got, want)
		}
	}
}
