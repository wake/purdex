package teammod

import (
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The lead-handover notice (P6-1′): after an applied `cleared`, every active member hears from the NEW lead's inbox.

// relayLeadTo drives a lead's self relay of sid-1 to cleared, moving it to newSID; it returns the op id.
func (f *fixture) relayLeadTo(newSID string) string {
	f.t.Helper()
	out := f.begin("sid-1")
	if code, body := f.decide(out.RequestID, "approve"); code != 200 {
		f.t.Fatalf("approve relay: %d %s", code, body)
	}
	for _, st := range []team.RelayState{team.RelayWriting, team.RelayWritten} {
		if code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: st}); code != 200 {
			f.t.Fatalf("report %s: %d %+v", st, code, ae)
		}
	}
	f.origins.markDead("sid-1")
	if code, _, ae := f.report(out.Op.ID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: newSID}); code != 200 {
		f.t.Fatalf("cleared: %d %+v", code, ae)
	}
	return out.Op.ID
}

// Mutation gates: notify on a re-send (Noop) → the once case red; send from another inbox → the origin case red.
func TestHandover_EachActiveMemberHearsFromTheNewLeadOnce(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	seedMember(t, f.m.store, "op-a", uid(1), "sid-ma", f.clock.Load())
	seedMember(t, f.m.store, "op-b", uid(1), "sid-mb", f.clock.Load())
	seedMember(t, f.m.store, "op-c", uid(1), "sid-mc", f.clock.Load())
	if err := f.m.store.SetMemberState("op-c", team.MemberKilled, f.clock.Load()); err != nil { // not active: not told
		t.Fatal(err)
	}
	opID := f.relayLeadTo("sid-1b")
	waitFor(t, func() bool { return len(f.sender.calls()) == 2 })
	newRef := f.op(opID).NewRef
	alias, _ := f.m.selfHost()
	want := fmt.Sprintf(HandoverNoticeFmt, alias+"/"+newRef, newRef)
	got := map[string]bool{}
	for _, c := range f.sender.calls() {
		if c.OriginInbox != "/tmp/10.sock" || c.Text != want {
			t.Fatalf("send = %+v, want from the new lead's inbox with %q", c, want)
		}
		got[c.To] = true
	}
	if !got[alias+"/_mem"+"op-a"] || !got[alias+"/_mem"+"op-b"] || len(got) != 2 {
		t.Fatalf("recipients = %v, want the two active members", got)
	}
	// the mod's idempotent re-send of the same state: no second notice
	f.report(opID, team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"})
	time.Sleep(200 * time.Millisecond)
	if n := len(f.sender.calls()); n != 2 {
		t.Fatalf("%d sends after a re-send, want still 2", n)
	}
}

// A relay of a session that leads no live team announces nothing, and a team with no members has nobody to tell.
func TestHandover_NothingWhenThereIsNobodyToTell(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1)) // no members
	f.relayLeadTo("sid-1b")
	time.Sleep(200 * time.Millisecond)
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d sends for a team without members", n)
	}
	// a handover for a session that leads nothing
	f.m.handoverNotice(team.RelayOp{ID: "x", State: team.RelayCleared, NewSessionID: "sid-nobody"})
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("%d sends for a session that leads no team", n)
	}
}

// A sender that refuses does not fail the report, and the notice is not retried.
func TestHandover_AFailedSendIsLoggedNotFatal(t *testing.T) {
	f := newFixture(t)
	logs := f.logs()
	f.approveLead(uid(1))
	seedMember(t, f.m.store, "op-a", uid(1), "sid-ma", f.clock.Load())
	f.sender.setErr(fmt.Errorf("peer_not_found"))
	f.relayLeadTo("sid-1b") // the cleared still answers 200 (relayLeadTo fails the test otherwise)
	waitFor(t, func() bool { return countLines(logs(), "handover notice to") == 1 })
}
