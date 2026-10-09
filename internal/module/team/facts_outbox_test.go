// internal/module/team/facts_outbox_test.go
package teammod

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// M's facts pump (cross-host team spec §3.1 rules 1, 4, 6; §3.2; plan X2c-2): the generic outbox pump of X3a-2 driving
// team_facts, against a scripted HostCaller, with the fixture clock.

// factsFixture is a fixture whose facts outbox is wired to a fake caller and a pump nobody runs yet.
func factsFixture(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	f := newFixture(t)
	fc := &fakeHostCaller{}
	f.m.cmdCaller = fc
	f.m.factPump = newOutboxPump("facts", fc, f.m.newFactOutbox(), f.m.now, f.m.logf, f.m.stopCtx, &f.m.sweepWG)
	return f, fc
}

// queueEnded makes mk an ended fact for host-L by the real cause (the member's session went), due now.
func (f *fixture) queueEnded(mk, sid string) {
	f.t.Helper()
	seedRemote(f.t, f.m.store, mk, sid, f.clock.Load())
	if gone, err := f.m.store.MarkRemoteMemberGone(mk, sid, "fact-"+mk, f.clock.Load()); err != nil || !gone {
		f.t.Fatalf("queue %s: gone=%v err=%v", mk, gone, err)
	}
}

func (f *fixture) factState(id string) factRow {
	f.t.Helper()
	for _, fr := range factsOf(f.t, f.m.store, "host-L") {
		if fr.ID == id {
			return fr
		}
	}
	f.t.Fatalf("no fact %s", id)
	return factRow{}
}

func doneFor(host string) func(string, map[string]any) peersmod.CallResult {
	return func(_ string, body map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(fmt.Sprintf(`{"id":%q,"host_id":%q,"outcome":{"state":"ok"}}`, body["id"], host))}
	}
}

func TestFactPump_SendsTheFactToTheLeadHostAndMarksItDone(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = doneFor("host-L")
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	calls := fc.sent()
	if len(calls) != 1 || calls[0].Host != "host-L" || calls[0].Path != factsPath {
		t.Fatalf("calls = %+v", calls)
	}
	var sent team.TeamFact
	if err := json.Unmarshal(calls[0].Body, &sent); err != nil || sent.ID != "fact-mk-1" || sent.Kind != team.FactEnded || sent.ToHostID != "host-L" || sent.MK != "mk-1" {
		t.Fatalf("body = %s (%v)", calls[0].Body, err)
	}
	if st := f.factState("fact-mk-1"); st.State != factDone {
		t.Fatalf("state = %s, want done", st.State)
	}
}

// FIFO per host; a JSON refusal (the lead host will not take this fact) is recorded and dropped, never blocking the next.
func TestFactPump_APermanentRefusalDropsThatFactAndTheNextGoes(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		switch body["id"] {
		case "fact-mk-1":
			return peersmod.CallResult{Class: peersmod.ClassRefused, Code: "not_your_member"}
		case "fact-mk-2":
			return peersmod.CallResult{Class: peersmod.ClassWrongHost, Code: "wrong_host"}
		}
		return doneFor("host-L")(host, body)
	}
	f.queueEnded("mk-1", "sid-1")
	f.queueEnded("mk-2", "sid-2")
	f.queueEnded("mk-3", "sid-3")
	f.m.factPump.drain("host-L")
	for id, want := range map[string]string{"fact-mk-1": factDropped, "fact-mk-2": factDropped, "fact-mk-3": factDone} {
		if got := f.factState(id).State; got != want {
			t.Fatalf("%s = %s, want %s", id, got, want)
		}
	}
	if len(fc.sent()) != 3 {
		t.Fatalf("calls = %d, want 3", len(fc.sent()))
	}
}

// The lead host has no facts route yet (X3b-2): 404 blocks that host's queue and is retried — expected until L is deployed.
func TestFactPump_UnsupportedBlocksTheQueueAndBacksOff(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnsupported, Status: 404}
	}
	f.queueEnded("mk-1", "sid-1")
	f.queueEnded("mk-2", "sid-2")
	f.m.factPump.drain("host-L")
	a, b := f.factState("fact-mk-1"), f.factState("fact-mk-2")
	if a.State != factPending || b.State != factPending || len(fc.sent()) != 1 || a.Attempts != 1 || a.NextAt != f.clock.Load()+30_000 {
		t.Fatalf("a=%+v b=%+v calls=%d", a, b, len(fc.sent()))
	}
	// Not due yet: a pass sends nothing.
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 1 {
		t.Fatal("retried before its time")
	}
	f.clock.Add(30_000)
	fc.script = doneFor("host-L")
	f.m.factPump.drain("host-L")
	if f.factState("fact-mk-1").State != factDone || f.factState("fact-mk-2").State != factDone {
		t.Fatal("the queue did not drain once the route appeared")
	}
}

// Rule 6, 401: transient for 10 minutes from the first one, then unpaired_by_peer — and on M that is the §3.2 clean-up
// of the host: its live members end (no notice, no fact), its queued facts are dropped. Terminal rows stay.
func TestFactPump_A401ForTenMinutesIsUnpairedByPeer(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = func(string, map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassUnauthorized, Status: 401}
	}
	f.queueEnded("mk-gone", "sid-gone")
	seedRemote(t, f.m.store, "mk-live", "sid-live", f.clock.Load())
	other := newRemote("mk-other", "sid-other", "host-B", f.clock.Load())
	if err := f.m.store.InsertRemoteMember(other); err != nil {
		t.Fatal(err)
	}
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-gone"); st.State != factPending || st.First401At != f.clock.Load() {
		t.Fatalf("first 401: %+v", st)
	}
	f.clock.Add(9 * 60_000)
	f.m.factPump.drain("host-L")
	if f.factState("fact-mk-gone").State != factPending {
		t.Fatal("unpaired_by_peer before 10 minutes")
	}
	f.clock.Add(61_000)
	f.m.factPump.drain("host-L")
	if st := f.factState("fact-mk-gone").State; st != factDropped {
		t.Fatalf("after 10 minutes of 401 the fact is %s, want dropped", st)
	}
	for mk, want := range map[string]string{"mk-live": remoteEnded, "mk-gone": remoteGone, "mk-other": remoteActive} {
		if row, _, _ := f.m.store.RemoteMember(mk); row.State != want {
			t.Fatalf("%s = %s, want %s", mk, row.State, want)
		}
	}
	if len(noticesOf(t, f.m.store, "mk-live")) != 0 || len(factsOf(t, f.m.store, "host-B")) != 0 {
		t.Fatal("the clean-up sent a notice or queued a fact")
	}
}

// Rule 1: the fact is addressed to the host id it was queued for, never re-resolved by alias.
func TestFactPump_AnAnswerForAnotherFactIsNotApplied(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = func(host string, body map[string]any) peersmod.CallResult {
		return peersmod.CallResult{Class: peersmod.ClassDone, Body: json.RawMessage(`{"id":"someone-else","host_id":"host-L"}`)}
	}
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if f.factState("fact-mk-1").State != factPending {
		t.Fatal("a fact was settled with another fact's answer")
	}
}

// End to end with the pump running: the cause kicks it, the fact goes out at once, and Stop leaves nothing running.
func TestFactPump_ACauseKicksItAndStopDrainsIt(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = doneFor("host-L")
	f.m.sweepWG.Add(1)
	go f.m.factPump.run()

	// a member's session went (the sweeper)
	f.m.bootAt = 0
	seedRemote(t, f.m.store, "mk-1", "sid-gone", f.clock.Load())
	f.origins.markDead("sid-gone")
	f.m.markGoneRemoteMembers()
	doneCount := func() int {
		n := 0
		for _, fr := range factsOf(t, f.m.store, "host-L") {
			if fr.State == factDone {
				n++
			}
		}
		return n
	}
	waitFor(t, func() bool { return doneCount() == 1 })

	// the operator ended a member (the admin route)
	seedRemote(t, f.m.store, "mk-2", "sid-2", f.clock.Load())
	if code, body := f.do(http.MethodPost, team.RemoteMembersEndRoute, team.RemoteMemberEndRequest{MK: "mk-2"}); code != http.StatusOK {
		t.Fatalf("end: %d %s", code, body)
	}
	waitFor(t, func() bool { return doneCount() == 2 })
}

// The store half of the M-side clean-up for one host.
func TestEndRemoteMembersOfHost(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000) // host-L, live
	seedRemote(t, s, "mk-2", "sid-2", 1000)
	other := newRemote("mk-b", "sid-b", "host-B", 1000)
	if err := s.InsertRemoteMember(other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkRemoteMemberGone("mk-2", "sid-2", "fact-2", 1500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkRemoteMemberGone("mk-b", "sid-b", "fact-b", 1500); err != nil {
		t.Fatal(err)
	}
	n, err := s.EndRemoteMembersOfHost("host-L", 2000)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v, want 1 (mk-1)", n, err)
	}
	if row, _, _ := s.RemoteMember("mk-1"); row.State != remoteEnded {
		t.Fatalf("mk-1 = %s", row.State)
	}
	if row, _, _ := s.RemoteMember("mk-2"); row.State != remoteGone {
		t.Fatalf("a terminal row was rewritten: %s", row.State)
	}
	for _, fr := range factsOf(t, s, "host-L") {
		if fr.State != factDropped {
			t.Fatalf("host-L fact %s = %s, want dropped", fr.ID, fr.State)
		}
	}
	if fr := factsOf(t, s, "host-B"); len(fr) != 1 || fr[0].State != factPending {
		t.Fatalf("another host's facts were touched: %+v", fr)
	}
	if len(noticesOf(t, s, "mk-1")) != 0 {
		t.Fatal("an unpaired end owes a notice")
	}
}
