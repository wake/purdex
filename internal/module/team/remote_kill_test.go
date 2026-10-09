// internal/module/team/remote_kill_test.go
package teammod

import (
	"encoding/json"
	"net/http"
	"syscall"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// The `kill` command on the member host (cross-host team spec §6.2, plan X4a). sid-2 (_def456, pid 20) is live in the fake
// registry; the lead host "lead:1" holds it as the remote member mk-1 of team "team-L".

func killFixture(t *testing.T, allow bool) (*fixture, *killRec) {
	t.Helper()
	f := newFixture(t)
	f.setLeadHost(allow)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	row := newRemote("mk-1", "sid-2", "lead:1", f.clock.Load())
	if err := f.m.store.InsertRemoteMember(row); err != nil {
		t.Fatal(err)
	}
	return f, rec
}

func killCmd(id, mk string) team.TeamCommand {
	return team.TeamCommand{ID: id, Kind: team.CommandKill, ToHostID: "h:1", TeamID: "team-L", MK: mk}
}

func outcomeState(t *testing.T, body []byte) string {
	t.Helper()
	var ans team.TeamCommandAnswer
	var out struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &ans); err != nil || json.Unmarshal(ans.Outcome, &out) != nil {
		t.Fatalf("answer %q: %v", body, err)
	}
	return out.State
}

func rowState(t *testing.T, f *fixture, mk string) string {
	t.Helper()
	r, ok, err := f.m.store.RemoteMember(mk)
	if err != nil || !ok {
		t.Fatalf("remote member %s: %v %v", mk, ok, err)
	}
	return r.State
}

func TestRemoteKill_SignalsTheProcessAndMarksKilled(t *testing.T) {
	f, rec := killFixture(t, true)
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusOK || outcomeState(t, body) != "killed" {
		t.Fatalf("kill = %d %s", code, body)
	}
	if pids := rec.got(); len(pids) != 1 || pids[0] != 20 {
		t.Fatalf("signalled %v, want the one live pid 20", pids)
	}
	if rowState(t, f, "mk-1") != remoteKilled {
		t.Fatalf("row = %s", rowState(t, f, "mk-1"))
	}
	// the very same command again: the stored answer, no second signal
	code, body = f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusOK || outcomeState(t, body) != "killed" || len(rec.got()) != 1 {
		t.Fatalf("replay = %d %s signals=%v", code, body, rec.got())
	}
	// another command id for the same member: the row's state, still no signal
	code, body = f.postCmd(leadPrincipal(), killCmd(cmdUUID2, "mk-1"))
	if code != http.StatusOK || outcomeState(t, body) != "killed" || len(rec.got()) != 1 {
		t.Fatalf("second kill = %d %s signals=%v", code, body, rec.got())
	}
}

// A reused pid is never signalled; nothing left to signal is `gone`.
func TestRemoteKill_NothingLeftToSignalIsGone(t *testing.T) {
	f, rec := killFixture(t, true)
	f.origins.mu.Lock()
	f.origins.otherProc = map[int]bool{20: true}
	f.origins.mu.Unlock()
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusOK || outcomeState(t, body) != "gone" || len(rec.got()) != 0 {
		t.Fatalf("kill = %d %s signals=%v", code, body, rec.got())
	}
	if rowState(t, f, "mk-1") != remoteGone {
		t.Fatalf("row = %s", rowState(t, f, "mk-1"))
	}
}

func TestRemoteKill_ESRCHIsGone(t *testing.T) {
	f, rec := killFixture(t, true)
	rec.err = syscall.ESRCH
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusOK || outcomeState(t, body) != "gone" {
		t.Fatalf("kill = %d %s", code, body)
	}
}

// A signal that failed marks nothing and is answered retryably (5xx), so the lead host sends it again.
func TestRemoteKill_FailedSignalMarksNothing(t *testing.T) {
	f, rec := killFixture(t, true)
	rec.err = syscall.EPERM
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code < 500 {
		t.Fatalf("kill = %d %s, want a 5xx", code, body)
	}
	if rowState(t, f, "mk-1") != remoteActive {
		t.Fatalf("row = %s, want still active", rowState(t, f, "mk-1"))
	}
}

// Consent: allow_team off → 403 host_not_allowed, no signal, nothing changes.
func TestRemoteKill_NeedsConsent(t *testing.T) {
	f, rec := killFixture(t, false)
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusForbidden || errCode(t, body) != team.ErrCommandHostNotAllowed {
		t.Fatalf("kill = %d %s", code, body)
	}
	if len(rec.got()) != 0 || rowState(t, f, "mk-1") != remoteActive {
		t.Fatalf("signals=%v row=%s", rec.got(), rowState(t, f, "mk-1"))
	}
}

// Only the lead host's own live row of that team: another team, another mk, a released row → not_your_member, no signal.
func TestRemoteKill_OnlyTheLeadHostsOwnLiveRow(t *testing.T) {
	f, rec := killFixture(t, true)
	other := killCmd(cmdUUID1, "mk-1")
	other.TeamID = "team-other"
	if code, body := f.postCmd(leadPrincipal(), other); code != http.StatusConflict || errCode(t, body) != team.ErrCommandNotYourMember {
		t.Fatalf("other team = %d %s", code, body)
	}
	if code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID2, "no-such-mk")); code != http.StatusConflict || errCode(t, body) != team.ErrCommandNotYourMember {
		t.Fatalf("unknown mk = %d %s", code, body)
	}
	if ok, err := f.m.store.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteReleased, f.clock.Load()); err != nil || !ok {
		t.Fatal(err)
	}
	if code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID3, "mk-1")); code != http.StatusConflict || errCode(t, body) != team.ErrCommandNotYourMember {
		t.Fatalf("released = %d %s", code, body)
	}
	if len(rec.got()) != 0 {
		t.Fatalf("signalled %v for a row that was not the lead host's live member", rec.got())
	}
}

// A row of ANOTHER lead host with the same mk is not killable by this one.
func TestRemoteKill_AnotherLeadHostsRowIsNotMine(t *testing.T) {
	f, rec := killFixture(t, true)
	if _, err := f.m.store.db.Exec(`UPDATE remote_members SET lead_host_id = 'someone-else' WHERE mk = 'mk-1'`); err != nil {
		t.Fatal(err)
	}
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusConflict || errCode(t, body) != team.ErrCommandNotYourMember || len(rec.got()) != 0 {
		t.Fatalf("kill = %d %s signals=%v", code, body, rec.got())
	}
}

func TestRemoteKill_RequiresMK(t *testing.T) {
	f, _ := killFixture(t, true)
	if code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "")); code != http.StatusBadRequest {
		t.Fatalf("kill without mk = %d %s", code, body)
	}
}
