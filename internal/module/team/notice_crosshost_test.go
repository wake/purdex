package teammod

import (
	"net/http"
	"strings"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// A notice to the lead about a member of ANOTHER host (its session is not in this registry): until now it fell back to the lead's own
// inbox through the plain send and was refused self_target, so every such notice was silently dropped (the Aigora asks all expired). It now
// goes through the self-notice sender, from the lead's own inbox - the one send that may address its origin.

// refuseSelfTarget makes the fake plain send behave as peers does for an origin that is the target: the lead's inbox sends to the lead.
func (f *fixture) refuseSelfTarget() {
	f.sender.mu.Lock()
	defer f.sender.mu.Unlock()
	f.sender.selfTarget = func(req ipeers.SendRequest) bool { return req.OriginInbox == leadInbox }
}

func (f *fixture) selfNotices() []ipeers.SendRequest {
	f.sender.mu.Lock()
	defer f.sender.mu.Unlock()
	return append([]ipeers.SendRequest(nil), f.sender.selfNotices...)
}

// Mutation gates: the fallback back to the plain send from the lead's inbox → the remote case is refused (red); the member's inbox used
// for a local member no more → the local case sends from the lead's (red).
func TestNoticeToLead_RemoteMemberGoesFromTheLeadsInboxBySelfNotice_LocalFromTheMembers(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("3")
	f.refuseSelfTarget()
	mr, tm, ok, err := f.m.store.ActiveMemberInLiveTeam("sid-m1")
	if err != nil || !ok {
		t.Fatalf("member: %v %v", ok, err)
	}
	// local member (one the registry has an inbox for): from its own inbox by the plain send
	local := mr
	local.SessionID, local.Ref = "sid-2", "_def456"
	if !f.m.noticeToLead(local, tm, "local text", "test") {
		t.Fatal("the local notice did not go")
	}
	calls := f.sender.calls()
	if len(calls) != 1 || calls[0].OriginInbox != "/tmp/20.sock" || len(f.selfNotices()) != 0 {
		t.Fatalf("local: calls %+v, self notices %+v: want one plain send from the member's inbox", calls, f.selfNotices())
	}
	// remote member: its session has no inbox here
	remote := mr
	remote.SessionID, remote.Ref, remote.HostID = "sid-on-another-host", "_rem777", "host-elsewhere"
	if !f.m.noticeToLead(remote, tm, "remote text", "test") {
		t.Fatal("the remote notice did not go")
	}
	self := f.selfNotices()
	if len(self) != 1 || self[0].Text != "remote text" || self[0].OriginInbox != leadInbox {
		t.Fatalf("self notices = %+v, want one from the lead's inbox", self)
	}
	if n := len(f.sender.calls()); n != 2 { // sent records both kinds; the plain Send was refused for nobody
		t.Fatalf("recorded sends = %d, want 2", n)
	}
	// a member of THIS host that the registry does not list right now is not a remote member: nothing is sent as if it were
	gone := mr
	gone.SessionID, gone.Ref = "sid-not-listed", "_gone99"
	before := len(f.selfNotices())
	if f.m.noticeToLead(gone, tm, "gone text", "test") || len(f.selfNotices()) != before {
		t.Fatal("a local member missing from the registry was told as a remote one")
	}
	// no self-notice sender: not sent, not a panic
	f.m.selfNotice = nil
	if f.m.noticeToLead(remote, tm, "remote text", "test") {
		t.Fatal("a notice went with nothing to send it")
	}
}

// The mirror ask of a remote member reaches the lead and is marked notified (it used to be refused and retried until it expired).
// Mutation gate: send it by the plain send → notified_at stays 0 (red).
func TestRelayAskFact_TheLeadIsToldBySelfNoticeAndTheAskIsMarkedNotified(t *testing.T) {
	f, _ := leadAskFixture(t)
	f.refuseSelfTarget()
	if code, body := f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(820), 71, 300)); code != http.StatusOK {
		t.Fatalf("fact: %d %s", code, body)
	}
	waitFor(t, func() bool { return len(f.selfNotices()) >= 1 })
	self := f.selfNotices()
	if len(self) != 1 || !strings.Contains(self[0].Text, "pdx relay lead/_rabc12") || self[0].OriginInbox != leadInbox {
		t.Fatalf("self notices = %+v", self)
	}
	waitFor(t, func() bool { return f.ask(rid(820)).NotifiedAt != 0 })
}

// The moved notice names the member on ITS host (the remote alias), and goes.
func TestMovedFact_TheLeadIsToldBySelfNotice(t *testing.T) {
	f, fc := factsFixture(t)
	f.setLeadHost(true)
	fc.aliases = map[string]string{"lead": "lead:1"}
	f.m.cmdCaller = fc
	f.refuseSelfTarget()
	f.approveLead(uid(1))
	f.remoteRow("abc12", "lead:1", "mk1", rowActive)
	if code, body := f.postFact(leadPrincipal(), movedFact(factUUID1, "mk1")); code != http.StatusOK {
		t.Fatalf("moved = %d %s", code, body)
	}
	waitFor(t, func() bool { return len(f.selfNotices()) >= 1 })
	self := f.selfNotices()
	want := "[pdx team] member 由使用者手動接力：lead/_rabc12 → lead/_nnn222"
	if len(self) != 1 || self[0].Text != want {
		t.Fatalf("self notices = %+v, want %q (the member host's alias, not the lead host's)", self, want)
	}
}

// The unattended accept of a remote member's mirror ask tells the lead too.
func TestRelayAskFact_UnattendedAcceptTellsTheLeadBySelfNotice(t *testing.T) {
	f, fc := leadAskFixture(t)
	fc.caps["lead:1"] = ipeers.TeamCaps{Kinds: append(append([]string{}, allKinds...), CmdRelay), AllowTeam: true, FactKinds: []string{team.FactRelayAsk}}
	f.refuseSelfTarget()
	f.setPool("sid-1", 2)
	f.setQuota("sid-1", 5)
	f.unatt.set(true)
	f.qrule.set(true, nil)
	f.postFact(leadPrincipal(), relayAskFact(factUUID1, "mk1", rid(821), 71, 300))
	f.livenessTick()
	time.Sleep(100 * time.Millisecond)
	var told bool
	for _, n := range f.selfNotices() {
		told = told || strings.HasPrefix(n.Text, "[pdx team] 無人值守：member ")
	}
	if !told {
		t.Fatalf("self notices = %+v, want the unattended-accept notice", f.selfNotices())
	}
}
