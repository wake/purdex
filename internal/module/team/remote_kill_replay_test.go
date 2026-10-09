// internal/module/team/remote_kill_replay_test.go
package teammod

import (
	"net/http"
	"syscall"
	"testing"
)

// Consent is the CURRENT consent at every signal: a replay after the user turned allow_team off answers the stored
// decision but sends no signal.
func TestRemoteKill_ReplayAfterConsentRevokedDoesNotSignal(t *testing.T) {
	f, rec := killFixture(t, true)
	rec.err = syscall.EPERM
	if code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1")); code < 500 {
		t.Fatalf("first try = %d %s, want a 5xx", code, body)
	}
	rec.mu.Lock()
	rec.err = nil
	rec.mu.Unlock()
	f.setLeadHost(false) // the user revokes
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusOK {
		t.Fatalf("replay = %d %s", code, body)
	}
	if pids := rec.got(); len(pids) != 1 {
		t.Fatalf("signals %v: the replay signalled after the consent was revoked", pids)
	}
}

// A row that recorded no process cannot be matched to one: gone, never a signal at whatever the registry shows now.
func TestRemoteKill_RowWithoutARecordedProcessIsNeverMatched(t *testing.T) {
	for name, set := range map[string]string{"no pid": `pid = 0`, "no start": `proc_start = ''`} {
		t.Run(name, func(t *testing.T) {
			f, rec := killFixture(t, true)
			if _, err := f.m.store.db.Exec(`UPDATE remote_members SET ` + set + ` WHERE mk = 'mk-1'`); err != nil {
				t.Fatal(err)
			}
			code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
			if code != http.StatusOK || outcomeState(t, body) != "gone" || len(rec.got()) != 0 {
				t.Fatalf("kill = %d %s signals=%v", code, body, rec.got())
			}
		})
	}
}
