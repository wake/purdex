package workbook

import (
	"testing"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/module/agent"
)

// The catch-up cursor is the newest recorded TRANSCRIPT turn of the session. A refresh row is not a turn of the transcript:
// once one existed, the cursor was a turn id the transcript never has, the catch-up took the whole window as new, and the
// Stop that came while the newest turn still read as running (its last words and duration row not yet written) found no
// turn it could adopt - so the turn right after a refresh was never recorded (live report, 2026-10-10 10:56).
// Mutation gate: let NewestTurn see refresh rows again → red.
func TestRefreshRowIsNotTheCatchUpCursor(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	k.turns.set("s1", endedTurn("t1", 100, "first"))
	k.event("s1", 1000)
	mustRefresh(t, k, "s1", "")
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, refreshAnswer("狀況", `{"done":[],"dropped":[],"add":[]}`))
	k.clock.Add(1)

	// the next turn: the Stop hook comes before the transcript has its last words and its duration row
	k.turns.set("s1", endedTurn("t1", 100, "first"), runningTurn("t2"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "second", At: 2000, Seq: 2000})
	for i := 0; i < settleRetries+1 && len(k.afters) > 0; i++ { // the settle timers fire, the transcript never catches up
		f := k.afters[0]
		k.afters = k.afters[1:]
		f()
	}
	var turns []Entry
	for _, e := range k.entries("s1") {
		if e.Kind == KindTurn {
			turns = append(turns, e)
		}
	}
	if len(turns) != 2 || turns[1].TurnID != "t2" {
		t.Fatalf("turn entries = %+v", turns)
	}
}

// The push hold finds a Stop's own entry among the turns, never a refresh row that happens to sit near its time.
func TestRefreshRowIsNotAStopsEntry(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	mustRefresh(t, k, "s1", "")
	at := k.clock.Now().UnixMilli()
	if _, ok, err := k.st.ClosestTurn("s1", at, 2000, 500); err != nil || ok {
		t.Fatalf("a refresh row was taken for a turn: ok=%v err=%v", ok, err)
	}
}

// The summariser is told about the conversation's previous turns, not about its refreshes.
func TestRefreshEntryIsNotAPreviousTurnInThePrompt(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	k.turns.set("s1", endedTurn("t1", 100, "first"))
	k.event("s1", 1000)
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("事", "推", "第一輪。", "狀況", ""))
	mustRefresh(t, k, "s1", "")
	j = mustNext(t, k, "m", "s1")
	k.finish("m", j, refreshAnswer("狀況二", `{"done":[],"dropped":[],"add":[]}`))
	rows, err := k.st.RecentForPrompt("s1", 5)
	if err != nil || len(rows) != 1 || rows[0].Kind != KindTurn {
		t.Fatalf("recent = %+v err %v", rows, err)
	}
}

// A slash command's own turn (/workbook refresh, /model, /clear ...) sits between the newest recorded turn and the next
// one. It is not summarised, but it made two turns "newer than the cursor", and the Stop of the real turn that beat the
// transcript was then left unadopted: the real turn was lost, and the previous one - caught up at the NEXT Stop - was
// stamped with that Stop's time and words (live, 2026-10-10 11:17: entry 224 described the turn before).
// Mutation gate: require the newest turn to be the only one newer than the cursor again → red.
func TestStopBeforeTranscriptAfterASlashCommandTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "first"))
	k.event("s1", 1000)
	slash := convmodel.Turn{ID: "slash", Outcome: convmodel.OutcomeDone, StartedAt: 150, Items: []convmodel.Item{userItem("/workbook refresh")}}
	next := convmodel.Turn{ID: "t3", Outcome: convmodel.OutcomeRunning, StartedAt: 1500, Items: []convmodel.Item{userItem("the real question")}}
	k.turns.set("s1", endedTurn("t1", 100, "first"), slash, next)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "the real answer", At: 2000, Seq: 2000})
	for i := 0; i < settleRetries+1 && len(k.afters) > 0; i++ {
		f := k.afters[0]
		k.afters = k.afters[1:]
		f()
	}
	rows := k.entries("s1")
	if len(rows) != 2 || rows[1].TurnID != "t3" || rows[1].TurnAt != 2000 {
		t.Fatalf("entries = %+v", rows)
	}
}

// With several turns newer than the cursor, a wordless running newest turn is adopted only when it provably began before
// the Stop; an unknown start time leaves it alone (it may be a later prompt's turn) and the hook's words are never written
// onto it (codex attack). Mutation gate: drop the StartedAt test → red.
func TestStopDoesNotAdoptATurnWithUnknownStart(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "first"))
	k.event("s1", 1000)
	slash := convmodel.Turn{ID: "slash", Outcome: convmodel.OutcomeDone, StartedAt: 150, Items: []convmodel.Item{userItem("/model sonnet")}}
	unknown := convmodel.Turn{ID: "t3", Outcome: convmodel.OutcomeRunning, Items: []convmodel.Item{userItem("some later prompt")}} // StartedAt 0
	k.turns.set("s1", endedTurn("t1", 100, "first"), slash, unknown)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "an older Stop's words", At: 2000, Seq: 2000})
	for i := 0; i < settleRetries+1 && len(k.afters) > 0; i++ {
		f := k.afters[0]
		k.afters = k.afters[1:]
		f()
	}
	for _, e := range k.entries("s1") {
		if e.TurnID == "t3" {
			t.Fatalf("a turn of unknown start was claimed by a Stop: %+v", e)
		}
	}
}

// The turn caught up at a Stop keeps its OWN time and order; only the turn the Stop is about carries the Stop's, so the
// push hold (which matches a Stop to its entry by time) can never take a caught-up older turn for the new one (the live
// push described the previous turn). A catch-up never pushes by itself: a push belongs to a Stop, and takes the entry whose
// turn_at is that Stop's.
func TestCaughtUpTurnKeepsItsOwnTimeAndTheStopsEntryIsTheNewTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "first"))
	k.event("s1", 1000)
	slash := convmodel.Turn{ID: "slash", Outcome: convmodel.OutcomeDone, StartedAt: 350, Items: []convmodel.Item{userItem("/model sonnet")}}
	missed := endedTurn("t2", 300, "missed") // its own Stop was lost (or raced, as before the fix)
	next := convmodel.Turn{ID: "t3", Outcome: convmodel.OutcomeRunning, StartedAt: 1500, Items: []convmodel.Item{userItem("the real question")}}
	k.turns.set("s1", endedTurn("t1", 100, "first"), missed, slash, next)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "the real answer", At: 2000, Seq: 2000})
	for i := 0; i < settleRetries+1 && len(k.afters) > 0; i++ {
		f := k.afters[0]
		k.afters = k.afters[1:]
		f()
	}
	byID := map[string]Entry{}
	for _, e := range k.entries("s1") {
		byID[e.TurnID] = e
	}
	if byID["t2"].TurnAt != 300 || byID["t2"].TurnSeq == 2000 {
		t.Fatalf("the caught-up turn took the Stop's time: %+v", byID["t2"])
	}
	if byID["t3"].TurnAt != 2000 || byID["t3"].TurnSeq != 2000 {
		t.Fatalf("the Stop's turn: %+v", byID["t3"])
	}
	got, ok, err := k.st.ClosestTurn("s1", 2000, 2000, 500)
	if err != nil || !ok || got.TurnID != "t3" {
		t.Fatalf("the push hold's entry for the Stop = %+v ok=%v err=%v", got, ok, err)
	}
}

// A cursor that is a real turn but has aged out of the 6-turn window (or no record at all: a session with history the
// workbook never saw) is the same hole: the Stop that beats the transcript must still find the newest, running turn
// (codex attack on the hotfix). Mutation gate: onlyNewer without the lost-cursor case → red.
func TestStopBeforeTranscriptWithTheCursorOutsideTheWindow(t *testing.T) {
	for name, record := range map[string]bool{"cursor aged out of the window": true, "no record yet": false} {
		k := newKit(t)
		k.capable["s1"] = true
		if record {
			mustInsert(t, k.st, pending("s1", "s1", "gone-from-the-window", 1))
		}
		var turns []convmodel.Turn
		for i := 0; i < 5; i++ {
			turns = append(turns, endedTurn("old"+string(rune('a'+i)), int64(10*(i+1)), "older"))
		}
		// the Stop beat the file: the newest turn has its prompt and nothing else yet
		turns = append(turns, convmodel.Turn{ID: "new", Outcome: convmodel.OutcomeRunning, StartedAt: 1500, Items: []convmodel.Item{userItem("go")}})
		k.turns.set("s1", turns...)
		k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "newest words", At: 2000, Seq: 2000})
		for i := 0; i < settleRetries+1 && len(k.afters) > 0; i++ {
			f := k.afters[0]
			k.afters = k.afters[1:]
			f()
		}
		found := false
		for _, e := range k.entries("s1") {
			found = found || e.TurnID == "new"
		}
		if !found {
			t.Errorf("%s: the newest turn was not recorded: %+v", name, k.entries("s1"))
		}
	}
}
