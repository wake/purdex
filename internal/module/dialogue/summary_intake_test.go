package dialogue

import (
	"fmt"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	agentmod "github.com/wake/purdex/internal/module/agent"
)

// pickCase builds n ended turns t1..tn (ended at 1000*i) and the WriteResult that numbered them all.
func pickCase(n int, first bool) (convturns.TurnsFromResult, WriteResult) {
	var turns []convmodel.Turn
	var wr WriteResult
	wr.First = first
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("t%d", i)
		t := basicTurn(id)
		end := int64(1000 * i)
		t.EndedAt = &end
		turns = append(turns, t)
		wr.Turns = append(wr.Turns, WrittenTurn{ID: id, No: i, EndedAt: end})
	}
	return convturns.TurnsFromResult{Turns: turns}, wr
}

func pickIDs(ps []summaryPick) string {
	s := ""
	for _, p := range ps {
		s += p.turn.ID + ","
	}
	return s
}

var pickEv = agentmod.TurnEndEvent{SessionID: sidS, At: 777000, Seq: 9, Text: "hook"}

// 2: a session recorded for the first time gets only the event's turn and what came after it, never the history.
func TestPickSummaries_FirstTimeTakesTheEventsTurnOnward(t *testing.T) {
	res, wr := pickCase(5, true)
	if got := pickIDs(pickSummaries(pickEv, res, wr, "t4")); got != "t4,t5," {
		t.Fatalf("target t4: %s", got)
	}
	if got := pickIDs(pickSummaries(pickEv, res, wr, "")); got != "t5," {
		t.Fatalf("no target: %s", got)
	}
	// the event's own turn is the last one a Write reaches, so a first event with target t5 yields just t5
	wr.Turns = wr.Turns[:5]
	if got := pickIDs(pickSummaries(pickEv, res, wr, "t5")); got != "t5," {
		t.Fatalf("target t5: %s", got)
	}
}

// 3: five new turns make three summaries, the newest.
func TestPickSummaries_AtMostThreeTheNewest(t *testing.T) {
	res, wr := pickCase(5, false)
	if got := pickIDs(pickSummaries(pickEv, res, wr, "")); got != "t3,t4,t5," {
		t.Fatalf("not first, no target: %s", got)
	}
	res, wr = pickCase(5, true)
	if got := pickIDs(pickSummaries(pickEv, res, wr, "t1")); got != "t3,t4,t5," {
		t.Fatalf("first, target t1: %s", got)
	}
}

// The event's time and order go to the turn it names (the push matches on it); the others keep their own end times.
func TestPickSummaries_StampsAndNumbers(t *testing.T) {
	res, wr := pickCase(3, false)
	ps := pickSummaries(pickEv, res, wr, "t2")
	// t3 is after the target in wr.Turns but the target stops the Write in real life; here it is simply another pick
	want := []struct {
		id        string
		no        int
		at        int64
		seq       int64
		fromEvent bool
	}{{"t1", 1, 1000, 0, false}, {"t2", 2, 777000, 9, true}, {"t3", 3, 3000, 0, false}}
	if len(ps) != len(want) {
		t.Fatalf("picks %s", pickIDs(ps))
	}
	for i, w := range want {
		p := ps[i]
		if p.turn.ID != w.id || p.no != w.no || p.at != w.at || p.seq != w.seq || p.fromEvent != w.fromEvent {
			t.Errorf("pick %d = %+v", i, p)
		}
	}
	// no target: the newest turn carries the event's stamp
	ps = pickSummaries(pickEv, res, wr, "")
	if last := ps[len(ps)-1]; last.turn.ID != "t3" || last.at != 777000 || last.seq != 9 || !last.fromEvent {
		t.Errorf("newest without a target = %+v", last)
	}
	if ps[0].fromEvent {
		t.Error("an older turn took the event's stamp")
	}
}

// A turn's own time falls back from its end to its start to the event's.
func TestPickSummaries_TimeFallbacks(t *testing.T) {
	res, wr := pickCase(3, false)
	res.Turns[0].EndedAt = nil
	res.Turns[0].StartedAt = 55
	res.Turns[1].EndedAt = nil
	res.Turns[1].StartedAt = 0
	ps := pickSummaries(pickEv, res, wr, "t3")
	if ps[0].at != 55 || ps[1].at != pickEv.At {
		t.Fatalf("at = %d, %d", ps[0].at, ps[1].at)
	}
}

// Turns with nothing to say, turns that are not in the data in hand and running turns other than the target are left out.
func TestPickSummaries_Filters(t *testing.T) {
	res, wr := pickCase(4, false)
	res.Turns[0] = done("t1", userItem("u", "hi")) // no assistant text, no tool step: nothing to summarise
	res.Turns = res.Turns[:3]                      // t4 is not in hand
	res.Turns[1].Outcome = convmodel.OutcomeRunning
	if got := pickIDs(pickSummaries(pickEv, res, wr, "")); got != "t3," {
		t.Fatalf("picks %s", got)
	}
	// the running turn is taken when it is the event's own
	if got := pickIDs(pickSummaries(pickEv, res, wr, "t2")); got != "t2,t3," {
		t.Fatalf("picks with a running target %s", got)
	}
	if got := pickSummaries(pickEv, res, WriteResult{}, ""); len(got) != 0 {
		t.Fatalf("nothing written but %s", pickIDs(got))
	}
}
