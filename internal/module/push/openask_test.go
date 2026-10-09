package push

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// PU-3 Task 4: the open hook_ask set (spec §5.2 rule 8), built from the approval feed's open snapshot and kept by its
// events, plus "opened in the last 10 s".

func ask(id, sid, tmux string) team.Approval {
	return team.Approval{ID: id, Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":[{"question":"q"}]}`),
		Origin: team.Origin{SessionID: sid, Tmux: tmux}}
}

func newAsks() (*openAsks, *fakeClock) {
	c := &fakeClock{now: time.Unix(3_000_000, 0)}
	return newOpenAsks(c.Now), c
}

func TestOpenAsks_OpenedIsSeenBySessionIDOrTmuxName(t *testing.T) {
	o, _ := newAsks()
	o.Opened(ask("a1", "sid-1", "dev:@1.%2"))
	if !o.Has("sid-1", "") || !o.Has("", "dev") || !o.Has("sid-1", "other") || !o.Has("other", "dev") {
		t.Fatal("an open ask is not matched by its session id or its tmux session name")
	}
	if o.Has("sid-2", "other") {
		t.Fatal("matched an unrelated session")
	}
	if o.Has("", "") {
		t.Fatal("empty identifiers matched")
	}
}

// An origin without a session id or tmux never matches the empty string. Mutation gate: compare without the
// non-empty guard → red.
func TestOpenAsks_AnAskWithNoIdentityMatchesNothing(t *testing.T) {
	o, _ := newAsks()
	o.Opened(ask("a1", "", ""))
	if o.Has("", "") || o.Has("sid", "dev") {
		t.Fatal("an ask with no identity matched")
	}
}

// Closed removes it from the open set, but "opened in the last 10 s" still holds until 10 s have passed. Mutation
// gate: drop the recent window → red; drop the removal → red.
func TestOpenAsks_ClosedStaysFor10SecondsThenGoes(t *testing.T) {
	o, c := newAsks()
	o.Opened(ask("a1", "sid-1", "dev:@1.%2"))
	c.advance(20 * time.Second)
	if !o.Has("sid-1", "") {
		t.Fatal("an ask still open stopped counting")
	}
	o.Closed("a1")
	if o.Has("sid-1", "") != false {
		// opened 20 s ago and closed: neither open nor recent
		t.Fatal("a closed ask that was opened 20 s ago still counts")
	}
	o.Opened(ask("a2", "sid-2", "ops:@1.%2"))
	c.advance(3 * time.Second)
	o.Closed("a2")
	if !o.Has("sid-2", "") {
		t.Fatal("an ask opened 3 s ago and already closed does not count (the waiting event may still be in flight)")
	}
	c.advance(8 * time.Second) // 11 s since it opened
	if o.Has("sid-2", "") {
		t.Fatal("an ask opened 11 s ago and closed still counts")
	}
}

// A new subscription (a restart) loads the snapshot; an event that ran before the snapshot was loaded is kept.
// Mutation gate: ignore the snapshot → red; clear before loading → red.
func TestOpenAsks_LoadAddsTheSnapshotAndKeepsEarlierEvents(t *testing.T) {
	o, c := newAsks()
	o.Opened(ask("early", "sid-early", "early:@1.%1"))
	snap := ask("a1", "sid-1", "dev:@1.%2")
	snap.CreatedAt = c.Now().Add(-5 * time.Minute).UnixMilli()
	o.Load([]team.Approval{snap})
	if !o.Has("sid-1", "") {
		t.Fatal("an ask in the snapshot is not open")
	}
	if !o.Has("sid-early", "") {
		t.Fatal("loading the snapshot dropped an ask that opened first")
	}
}

// Only an answerable hook_ask counts: a terminal_only ask is not pushed to the phone, so the agent's own event must
// still be. Other kinds are not asks. Mutation gate: count every kind → red.
func TestOpenAsks_OnlyAnAnswerableHookAskCounts(t *testing.T) {
	o, _ := newAsks()
	terminal := ask("t1", "sid-t", "t:@1.%1")
	terminal.Payload = json.RawMessage(`{"terminal_only":true,"questions":[{"question":"q"}]}`)
	lead := ask("l1", "sid-l", "l:@1.%1")
	lead.Kind = team.KindLead
	perm := ask("p1", "sid-p", "p:@1.%1")
	perm.Kind = team.KindHookPermission
	for _, a := range []team.Approval{terminal, lead, perm} {
		o.Opened(a)
	}
	o.Load([]team.Approval{terminal, lead, perm})
	if o.Has("sid-t", "t") || o.Has("sid-l", "l") || o.Has("sid-p", "p") {
		t.Fatal("something that is not an answerable hook_ask counted")
	}
}

// The set stays bounded: closed asks older than the window are dropped as new ones arrive. Mutation gate: never
// prune → red.
func TestOpenAsks_ClosedEntriesArePruned(t *testing.T) {
	o, c := newAsks()
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("a%d", i)
		o.Opened(ask(id, id, ""))
		o.Closed(id)
		c.advance(time.Second)
	}
	if n := o.Len(); n > 20 {
		t.Fatalf("%d entries held for closed asks", n)
	}
}

// An unknown id closing is harmless.
func TestOpenAsks_ClosingAnUnknownIDIsHarmless(t *testing.T) {
	o, _ := newAsks()
	o.Closed("nope")
	o.Opened(ask("a1", "sid-1", ""))
	o.Closed("nope")
	if !o.Has("sid-1", "") {
		t.Fatal("closing an unknown id removed another")
	}
}
