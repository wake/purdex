package workbook

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/module/agent"
)

// WB-1b′-b: the turn-end subscriber and the catch-up (plan D1 / D2 / D11).

func turnIDs(rows []Entry) string {
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.TurnID)
	}
	return strings.Join(ids, ",")
}

// A session with no record yet records only its newest ended turn (no history backfill), stamped with the event's time;
// a turn still running is not recorded.
// Mutation gate: drop the "no record yet → only the newest" cut → red.
func TestCatchUp_FirstEventRecordsOnlyTheNewestEndedTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"), runningTurn("t3"))
	k.event("s1", 9000)
	rows := k.entries("s1")
	if turnIDs(rows) != "t2" {
		t.Fatalf("recorded %q, want t2", turnIDs(rows))
	}
	r := rows[0]
	if r.TurnAt != 9000 || r.TurnSeq != 9000 || r.HostID != "h1" || r.Provider != "claude" || r.PromptVer != PromptVersion {
		t.Fatalf("row = %+v", r)
	}
	if r.TeamID != "team-9" || r.Role != "member" || !strings.HasPrefix(r.Ref, "_") || len(r.Ref) != 7 {
		t.Fatalf("seat = %q %q %q", r.TeamID, r.Role, r.Ref)
	}
}

// Missed events are caught up, oldest first, at most the newest three; an older caught-up turn keeps its own end time.
// Mutation gate: drop the keep-3 cut, or stamp every turn with the event's time → red.
func TestCatchUp_RecordsTheEndedTurnsNewerThanTheNewestRecorded(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "a"))
	k.event("s1", 1000)
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"), endedTurn("t3", 300, "c"),
		endedTurn("t4", 400, "d"), endedTurn("t5", 500, "e"), endedTurn("t6", 600, "f"))
	k.event("s1", 7000)
	rows := k.entries("s1")
	if turnIDs(rows) != "t1,t4,t5,t6" {
		t.Fatalf("recorded %q, want t1,t4,t5,t6", turnIDs(rows))
	}
	if rows[1].TurnAt != 400 || rows[2].TurnAt != 500 || rows[3].TurnAt != 7000 {
		t.Fatalf("turn_at = %d %d %d, want 400 500 7000", rows[1].TurnAt, rows[2].TurnAt, rows[3].TurnAt)
	}
}

// The same event twice (a retried hook) records and queues nothing the second time — with a readable transcript the
// cursor sees to it, and when only the hook's text is known the insert's own idempotency does.
// Mutation gate: enqueue when InsertPending says it was not inserted → red.
func TestCatchUp_ARetriedEventIsIdempotent(t *testing.T) {
	for _, name := range []string{"transcript", "fallback"} {
		t.Run(name, func(t *testing.T) {
			k := newKit(t)
			k.capable["s1"] = true
			if name == "transcript" {
				k.turns.set("s1", endedTurn("t1", 100, "a"))
			} else {
				k.turns.err["s1"] = convfeed.ErrNotFound
			}
			k.event("s1", 1000)
			k.event("s1", 1500)
			retriedOnce(t, k)
		})
	}
}

func retriedOnce(t *testing.T, k *kit) {
	t.Helper()
	if n := len(k.entries("s1")); n != 1 {
		t.Fatalf("%d rows", n)
	}
	j := mustNext(t, k, "m1", "s1")
	k.finish("m1", j, Result{Reason: "refused"}) // frees the conversation: a duplicate job would be handed out now
	if _, ok := k.next("m2", "s1"); ok {
		t.Fatal("a second job for one turn")
	}
}

// Two catch-ups of one conversation (the subscriber and a busy re-queue) never interleave between insert and enqueue: the
// older turn is queued first (codex attack).
// Mutation gate: drop intakeMu → red.
func TestCatchUp_ConcurrentCatchUpsKeepTheOrder(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "a"))
	k.event("s1", 1000)
	blocked, release := make(chan struct{}), make(chan struct{})
	first := true
	k.e.d.Capable = func(string) bool { // asked after A's insert of t2 and before its enqueue
		if first {
			first = false
			close(blocked)
			<-release
		}
		return true
	}
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"))
	done := make(chan struct{}, 2)
	go func() { k.event("s1", 2000); done <- struct{}{} }()
	<-blocked
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"), endedTurn("t3", 300, "c"))
	go func() { k.event("s1", 3000); done <- struct{}{} }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-done
	<-done
	j1 := mustNext(t, k, "m", "s1")
	k.finish("m", j1, Result{Reason: "refused"})
	j2 := mustNext(t, k, "m", "s1")
	if !strings.Contains(j1.Complete.Prompt, "做 t1") || !strings.Contains(j2.Complete.Prompt, "做 t2") {
		t.Fatalf("order: %s | %s", j1.Complete.Prompt, j2.Complete.Prompt)
	}
}

// A newest recorded turn that is not in the window means the whole window is newer.
func TestCatchUp_ACursorOutsideTheWindowTakesTheNewestThree(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("old", 1, "a"))
	k.event("s1", 10)
	var ts []convmodel.Turn
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		ts = append(ts, endedTurn(id, 100, id))
	}
	k.turns.set("s1", ts...)
	k.event("s1", 20)
	if got := turnIDs(k.entries("s1")); got != "old,d,e,f" {
		t.Fatalf("recorded %q", got)
	}
}

// A turn with no assistant text and no tool step is not summarised: no entry.
func TestCatchUp_ATurnWithNothingToSummariseIsNotRecorded(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	empty := endedTurn("t1", 100, "")
	empty.Items = empty.Items[:1] // only the user's message
	k.turns.set("s1", empty)
	k.event("s1", 1000)
	if n := len(k.entries("s1")); n != 0 {
		t.Fatalf("%d rows", n)
	}
}

// An unreadable transcript falls back to the hook's text: the id is stable for a retry in the same two-minute bucket and
// differs in the next one; an empty text records nothing.
// Mutation gate: key the fallback on the text alone, or on At without the bucket → red.
func TestCatchUp_FallbackTurnID(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.err["s1"] = convfeed.ErrNotFound
	ev := func(text string, at int64) {
		k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: text, At: at, Seq: at})
	}
	ev("完成", 10_000)
	ev("完成", 70_000)  // a retry, same bucket
	ev("完成", 250_000) // another turn, same words, minutes later
	ev("  ", 260_000)
	rows := k.entries("s1")
	if len(rows) != 2 || rows[0].TurnID == rows[1].TurnID || !strings.HasPrefix(rows[0].TurnID, "t:") {
		t.Fatalf("rows = %v", turnIDs(rows))
	}
	j := mustNext(t, k, "m", "s1")
	if !strings.Contains(j.Complete.Prompt, "完成") {
		t.Fatalf("the job must carry the hook's text: %s", j.Complete.Prompt)
	}
}

// A busy transcript cache re-queues the event up to three times, 2 s apart, then falls back.
func TestCatchUp_BusyRequeuesThenFallsBack(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.err["s1"] = convfeed.ErrBusy
	k.event("s1", 5000)
	for i := 0; i < busyRetries; i++ {
		if len(k.afters) != 1 {
			t.Fatalf("round %d: %d timers", i, len(k.afters))
		}
		f := k.afters[0]
		k.afters = nil
		if n := len(k.entries("s1")); n != 0 {
			t.Fatalf("round %d: recorded while busy", i)
		}
		f()
	}
	if len(k.afters) != 0 || len(k.entries("s1")) != 1 || !strings.HasPrefix(k.entries("s1")[0].TurnID, "t:") {
		t.Fatalf("after the retries: timers=%d rows=%v", len(k.afters), turnIDs(k.entries("s1")))
	}
}

// A session whose mod did not announce workbook.v2 gets a skipped:no_mod entry and no job.
// Mutation gate: enqueue regardless of capability → red.
func TestCatchUp_NotCapableIsSkippedNoMod(t *testing.T) {
	k := newKit(t)
	k.turns.set("s1", endedTurn("t1", 100, "a"))
	k.event("s1", 1000)
	rows := k.entries("s1")
	if len(rows) != 1 || rows[0].State != StateSkipped || rows[0].Reason != ReasonNoMod {
		t.Fatalf("rows = %+v", rows)
	}
	k.capable["s1"] = true
	if _, ok := k.next("m", "s1"); ok {
		t.Fatal("a skipped turn must have no job")
	}
	if len(k.lines) != 1 || !strings.HasSuffix(k.lines[0], ":false") {
		t.Fatalf("push-line hook = %v", k.lines)
	}
}

// A relayed session records under its conversation's root key.
func TestCatchUp_ConversationKeyIsTheLineageRoot(t *testing.T) {
	k := newKit(t)
	k.capable["s2"] = true
	k.lineage["s2"] = "root"
	k.turns.set("s2", endedTurn("t1", 100, "a"))
	k.event("s2", 1000)
	if rows := k.entries("root"); len(rows) != 1 || rows[0].SessionID != "s2" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Nothing is recorded once the engine has stopped.
func TestCatchUp_AfterStopRecordsNothing(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "a"))
	k.e.Stop()
	k.event("s1", 1000)
	if n := len(k.entries("s1")); n != 0 {
		t.Fatalf("%d rows", n)
	}
}

// A real error (not busy, not a miss) from the reader is the fallback too, and an unrelated one does not panic.
func TestCatchUp_ReaderErrorFallsBack(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.err["s1"] = errors.New("boom")
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "x", At: 1, Seq: 1})
	if n := len(k.entries("s1")); n != 1 {
		t.Fatalf("%d rows", n)
	}
}
