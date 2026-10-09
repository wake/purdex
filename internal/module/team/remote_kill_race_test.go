// internal/module/team/remote_kill_race_test.go
package teammod

import (
	"net/http"
	"testing"
)

// The consent is read again right before the signal: one withdrawn between the committed decision and the signal sends
// nothing (the decision stands and is answered).
func TestRemoteKill_ConsentWithdrawnBetweenDecisionAndSignalSendsNothing(t *testing.T) {
	f, rec := killFixture(t, true)
	f.m.beforeKillSignal = func() { f.setLeadHost(false) }
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusOK || outcomeState(t, body) != "killed" {
		t.Fatalf("kill = %d %s", code, body)
	}
	if pids := rec.got(); len(pids) != 0 {
		t.Fatalf("signalled %v after the consent was withdrawn", pids)
	}
}
