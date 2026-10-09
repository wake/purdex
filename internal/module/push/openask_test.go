package push

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// PU-3 Task 4: the hook_ask intervals (spec §5.2 rule 8): a waiting event is a duplicate only when its own window
// overlaps the interval an ask was open.

func ask(id, sid, tmux string) team.Approval {
	return team.Approval{ID: id, Kind: team.KindHookAsk, Payload: json.RawMessage(`{"questions":[{"question":"q"}]}`),
		Origin: team.Origin{SessionID: sid, Tmux: tmux}}
}

func newAsks() (*openAsks, *fakeClock) {
	c := &fakeClock{now: time.Unix(3_000_000, 0)}
	return newOpenAsks(c.Now), c
}

// window is a waiting event that arrived at `from` and whose hold ended 2 s later.
func overlapsWindow(o *openAsks, sid, name string, from time.Time) bool {
	return o.Overlaps(sid, name, from, from.Add(2*time.Second))
}

func TestOpenAsks_MatchedBySessionIDOrTmuxName(t *testing.T) {
	o, c := newAsks()
	o.Opened(ask("a1", "sid-1", "dev:@1.%2"))
	now := c.Now()
	if !overlapsWindow(o, "sid-1", "", now) || !overlapsWindow(o, "", "dev", now) || !overlapsWindow(o, "sid-1", "other", now) || !overlapsWindow(o, "other", "dev", now) {
		t.Fatal("an open ask is not matched by its session id or its tmux session name")
	}
	if overlapsWindow(o, "sid-2", "other", now) {
		t.Fatal("matched an unrelated session")
	}
	if overlapsWindow(o, "", "", now) {
		t.Fatal("empty identifiers matched")
	}
}

// An origin without a session id or tmux never matches the empty string. Mutation gate: compare without the
// non-empty guard → red.
func TestOpenAsks_AnAskWithNoIdentityMatchesNothing(t *testing.T) {
	o, c := newAsks()
	o.Opened(ask("a1", "", ""))
	if overlapsWindow(o, "", "", c.Now()) || overlapsWindow(o, "sid", "dev", c.Now()) {
		t.Fatal("an ask with no identity matched")
	}
}

// The overlap of [arrival, arrival+2s] with [opened, closed). Mutation gates: ignore closed → red; use <= for the
// half-open end → red; ignore opened (an ask that opens after the window) → red.
func TestOpenAsks_IntervalOverlap(t *testing.T) {
	base := time.Unix(3_000_000, 0)
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec * float64(time.Second))) }
	cases := []struct {
		name           string
		opened, closed float64 // closed < 0: still open
		arrival        float64
		want           bool
	}{
		{"open before the waiting frame, still open", 0, -1, 10, true},
		{"open before the frame, closes inside its window", 0, 11, 10, true},
		{"opens inside the window (the ask is later than the frame)", 11, -1, 10, true},
		{"opens at the end of the window", 12, -1, 10, true},
		{"opens after the window", 12.001, -1, 10, false},
		{"opened and answered long before", 0, 3, 10, false},
		{"closed just before the frame", 0, 9.999, 10, false},
		{"closed exactly at the frame (the interval is half open)", 0, 10, 10, false},
		{"closed just after the frame", 0, 10.001, 10, true},
		{"open across the whole window", 0, 30, 10, true},
		{"opened and closed inside the window", 10.5, 11, 10, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clock := &fakeClock{now: at(c.opened)}
			o := newOpenAsks(clock.Now)
			o.Opened(ask("a1", "sid-1", "dev:@1.%2"))
			if c.closed >= 0 {
				clock.now = at(c.closed)
				o.Closed("a1")
			}
			if got := overlapsWindow(o, "sid-1", "", at(c.arrival)); got != c.want {
				t.Fatalf("overlap = %v, want %v", got, c.want)
			}
		})
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
	if !overlapsWindow(o, "sid-1", "", c.Now()) {
		t.Fatal("an ask in the snapshot is not open")
	}
	if !overlapsWindow(o, "sid-early", "", c.Now()) {
		t.Fatal("loading the snapshot dropped an ask that opened first")
	}
}

// Only an answerable hook_ask counts: a terminal_only ask is not pushed, so the agent's own event must still be.
// Other kinds are not asks. Mutation gate: count every kind → red.
func TestOpenAsks_OnlyAnAnswerableHookAskCounts(t *testing.T) {
	o, c := newAsks()
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
	for _, id := range [][2]string{{"sid-t", "t"}, {"sid-l", "l"}, {"sid-p", "p"}} {
		if overlapsWindow(o, id[0], id[1], c.Now()) {
			t.Fatalf("%v: something that is not an answerable hook_ask counted", id)
		}
	}
}

// Closed intervals older than a minute are dropped as new asks arrive. Mutation gate: never prune → red.
func TestOpenAsks_ClosedIntervalsArePruned(t *testing.T) {
	o, c := newAsks()
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("a%d", i)
		o.Opened(ask(id, id, ""))
		o.Closed(id)
		c.advance(time.Second)
	}
	if n := o.Len(); n > 70 {
		t.Fatalf("%d intervals held for closed asks", n)
	}
}

// The interval table is capped; open asks are never dropped to make room. Mutation gate: no cap → red.
func TestOpenAsks_TheTableIsCappedAndNeverDropsAnOpenAsk(t *testing.T) {
	o, c := newAsks()
	o.Opened(ask("keep", "sid-keep", "keep:@1.%1"))
	for i := 0; i < maxAskIntervals+200; i++ {
		id := fmt.Sprintf("a%d", i)
		o.Opened(ask(id, id, ""))
		o.Closed(id)
	}
	if n := o.Len(); n > maxAskIntervals {
		t.Fatalf("%d intervals held, cap %d", n, maxAskIntervals)
	}
	if !overlapsWindow(o, "sid-keep", "", c.Now()) {
		t.Fatal("an open ask was dropped to make room")
	}
}

// An unknown id closing is harmless, and the first close wins.
func TestOpenAsks_ClosingTwiceOrAnUnknownIDIsHarmless(t *testing.T) {
	o, c := newAsks()
	o.Closed("nope")
	o.Opened(ask("a1", "sid-1", ""))
	c.advance(5 * time.Second)
	o.Closed("a1")
	c.advance(5 * time.Second)
	o.Closed("a1") // a duplicate close does not move the end
	if overlapsWindow(o, "sid-1", "", c.Now().Add(-2*time.Second)) {
		t.Fatal("a second close extended the interval")
	}
}
