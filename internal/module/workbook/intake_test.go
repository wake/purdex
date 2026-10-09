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

// The Stop hook fires before Claude Code writes the turn's duration row, so the turn that just ended still reads as
// running in the transcript (measured on a real session, WB-1c gate). The newest turn counts as the one that ended when
// its last words are the hook's last_assistant_message; a newer turn that has only just begun does not.
// Mutation gate: drop the running-last rule → the summary lags one turn behind → red.
func TestCatchUp_TheTurnThatJustEndedStillReadsRunning(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	justEnded := runningTurn("t2")
	justEnded.Items = []convmodel.Item{userItem("做 t2"), agentItem("全部完成了")}
	k.turns.set("s1", endedTurn("t1", 100, "a"), justEnded)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "全部完成了", At: 5000, Seq: 1})
	rows := k.entries("s1")
	if turnIDs(rows) != "t2" || rows[0].TurnAt != 5000 {
		t.Fatalf("recorded %q at %d, want t2 at 5000", turnIDs(rows), rows[0].TurnAt)
	}
	// the next prompt has begun: it is running with other words, and the ended turn is the one before it
	k2 := newKit(t)
	k2.capable["s1"] = true
	k2.turns.set("s1", endedTurn("t1", 100, "第一輪的結論"), runningTurn("t2")) // t2 says "working"
	k2.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "第一輪的結論", At: 6000, Seq: 1})
	if got := turnIDs(k2.entries("s1")); got != "t1" {
		t.Fatalf("recorded %q, want t1 (t2 only began)", got)
	}
	// a hook with no text (a failure, a tool-only turn): the running last turn that did something is taken
	k3 := newKit(t)
	k3.capable["s1"] = true
	k3.turns.set("s1", endedTurn("t1", 100, "a"), runningTurn("t2"))
	k3.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "", At: 7000, Seq: 1, Failed: true})
	if got := turnIDs(k3.entries("s1")); got != "t2" {
		t.Fatalf("recorded %q, want t2", got)
	}
}

// The Stop hook can fire before Claude Code has written the turn's last assistant row (measured on a real session: the
// row was stamped 52 ms before the hook arrived and still was not in the file). The event is looked at again every 100 ms
// for up to 1.5 s; when the words appear the real turn is used, and only if they never do are the hook's own words.

// runLater runs the timers the engine has set (the After seam), up to n rounds, and returns how many ran.
func runLater(k *kit, rounds int) int {
	ran := 0
	for i := 0; i < rounds && len(k.afters) > 0; i++ {
		fs := k.afters
		k.afters = nil
		for _, f := range fs {
			f()
			ran++
		}
	}
	return ran
}

func unflushedTurn(id, prompt string) convmodel.Turn {
	t := runningTurn(id)
	t.Items = []convmodel.Item{userItem(prompt)} // the answer is not in the file yet
	return t
}

// Mutation gate: record at once from the half-written turn, or never wait → red.
func TestCatchUp_WaitsForTheTranscriptToCatchUp(t *testing.T) {
	for name, polls := range map[string]int{"50ms": 1, "500ms": 5, "1.4s": 14} {
		t.Run(name, func(t *testing.T) {
			k := newKit(t)
			k.capable["s1"] = true
			k.turns.set("s1", endedTurn("t1", 100, "第一輪"))
			k.event("s1", 1000)
			k.turns.set("s1", endedTurn("t1", 100, "第一輪"), unflushedTurn("t2", "第二輪的問題"))
			k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "第二輪的回答", At: 5000, Seq: 2})
			if got := turnIDs(k.entries("s1")); got != "t1" || len(k.afters) != 1 {
				t.Fatalf("recorded %q with %d timers: the event must wait, not record a half turn", got, len(k.afters))
			}
			if n := runLater(k, polls-1); n != polls-1 {
				t.Fatalf("ran %d timers, want %d", n, polls-1)
			}
			// the file catches up: the answer is there now, in its own (longer) words
			flushed := runningTurn("t2")
			flushed.Items = []convmodel.Item{userItem("第二輪的問題"), agentItem("第二輪的回答，還有檔案裡才有的後半段")}
			k.turns.set("s1", endedTurn("t1", 100, "第一輪"), flushed)
			runLater(k, 1)
			rows := k.entries("s1")
			if turnIDs(rows) != "t1,t2" || rows[1].TurnAt != 5000 {
				t.Fatalf("recorded %q", turnIDs(rows))
			}
			if strings.Contains(k.logs.text(), "had not caught up") {
				t.Fatalf("the hook's words were used although the file caught up: %s", k.logs.text())
			}
			j1 := mustNext(t, k, "m", "s1")
			k.finish("m", j1, Result{Reason: "refused"})
			if j2 := mustNext(t, k, "m", "s1"); !strings.Contains(j2.Complete.Prompt, "後半段") {
				t.Fatalf("the job must carry the file's words: %s", j2.Complete.Prompt)
			}
		})
	}
}

// After 1.5 s the file still has nothing: the hook's own words stand in, and one log line says so.
// Mutation gate: wait forever, or drop the adoption → red.
func TestCatchUp_AfterTheLimitTheHookWordsStandIn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "第一輪"))
	k.event("s1", 1000)
	k.turns.set("s1", endedTurn("t1", 100, "第一輪"), unflushedTurn("t2", "第二輪的問題"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "第二輪的回答", At: 5000, Seq: 2})
	if n := runLater(k, 40); n != settleRetries {
		t.Fatalf("looked again %d times, want %d (1.5 s at 100 ms)", n, settleRetries)
	}
	rows := k.entries("s1")
	if turnIDs(rows) != "t1,t2" || rows[1].TurnAt != 5000 {
		t.Fatalf("recorded %q", turnIDs(rows))
	}
	if strings.Count(k.logs.text(), "had not caught up") != 1 {
		t.Fatalf("log = %s", k.logs.text())
	}
	j1 := mustNext(t, k, "m", "s1")
	k.finish("m", j1, Result{Reason: "refused"})
	if j2 := mustNext(t, k, "m", "s1"); !strings.Contains(j2.Complete.Prompt, "第二輪的問題") || !strings.Contains(j2.Complete.Prompt, "第二輪的回答") {
		t.Fatalf("the job must carry the prompt and the hook's words: %s", j2.Complete.Prompt)
	}
}

// Waiting is a timer, never a sleep: the call returns at once and other sessions go on.
func TestCatchUp_WaitingDoesNotBlockOtherSessions(t *testing.T) {
	k := newKit(t)
	k.capable["s1"], k.capable["s2"] = true, true
	k.turns.set("s1", endedTurn("t1", 100, "一"), unflushedTurn("t2", "問"))
	k.turns.set("s2", endedTurn("u1", 100, "甲"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "答", At: 5000, Seq: 1})
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s2", Text: "甲", At: 5001, Seq: 2})
	if got := turnIDs(k.entries("s2")); got != "u1" {
		t.Fatalf("s2 recorded %q while s1 waits", got)
	}
}

// Two answers in a row with the same words: the older turn matches the hook text too, but it is recorded already, so the
// newest (not flushed) turn is the one the event is about.
func TestCatchUp_RepeatedWordsStillFindTheNewTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "收到"))
	k.event("s1", 1000)
	k.turns.set("s1", endedTurn("t1", 100, "收到"), unflushedTurn("t2", "再說一次"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "收到", At: 6000, Seq: 2})
	runLater(k, 40)
	if got := turnIDs(k.entries("s1")); got != "t1,t2" {
		t.Fatalf("recorded %q, want t1,t2", got)
	}
}

// A late event with no words does not end a newer turn that began after its Stop, even one that has already done something
// (codex critic).
// Mutation gate: drop the started-after check from the no-words branch → red.
func TestCatchUp_ALateEventWithNoWordsDoesNotEndANewerTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "答1"))
	k.event("s1", 900) // t1 is recorded
	busy := runningTurn("t2")
	busy.StartedAt = 6000 // began after the old Stop at 5000, and has a reply row already
	k.turns.set("s1", endedTurn("t1", 100, "答1"), busy)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "", At: 5000, Seq: 3})
	if got := turnIDs(k.entries("s1")); got != "t1" {
		t.Fatalf("recorded %q: a newer turn was ended by an older event", got)
	}
}

// The first record of a session keeps only its newest ended turn (no history backfill) — but never loses the turn the event
// is about, nor drops it when a second event comes close behind a waiting one (codex R1).
// Mutation gate: trim to the newest turn regardless of the event's turn, or let the newer event overtake → red.
func TestCatchUp_TheFirstRecordKeepsTheEventsTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "答1"), endedTurn("t2", 200, "答2"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "答1", At: 5000, Seq: 1}) // an old event: its turn is t1
	if got := turnIDs(k.entries("s1")); got != "t1,t2" {
		t.Fatalf("recorded %q, want t1,t2", got)
	}
}

func TestCatchUp_ANewerEventQueuesBehindAWaitingOne(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", unflushedTurn("t1", "問1")) // the first turn of the session, not written yet
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "答1", At: 5000, Seq: 1})
	if len(k.afters) != 1 {
		t.Fatalf("%d timers", len(k.afters))
	}
	// the file catches up with both turns before the second event is handled
	k.turns.set("s1", endedTurn("t1", 100, "答1"), endedTurn("t2", 200, "答2"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "答2", At: 5200, Seq: 2})
	if got := turnIDs(k.entries("s1")); got != "" {
		t.Fatalf("the newer event overtook the waiting one: %q", got)
	}
	runLater(k, 40)
	if got := turnIDs(k.entries("s1")); got != "t1,t2" {
		t.Fatalf("recorded %q, want t1,t2 in order", got)
	}
	j1 := mustNext(t, k, "m", "s1")
	k.finish("m", j1, Result{Reason: "refused"})
	if j2 := mustNext(t, k, "m", "s1"); !strings.Contains(j2.Complete.Prompt, "做 t2") {
		t.Fatalf("order: %s", j2.Complete.Prompt)
	}
	if len(k.e.waiting) != 0 {
		t.Fatalf("the session is still marked waiting: %v", k.e.waiting)
	}
}

// A new prompt that began after this Stop is not this event's turn: it stays running and gets none of the old words
// (codex attack). The previous turn is recorded as the file shows it.
// Mutation gate: drop the started-after-the-Stop check → the old words land on the new turn → red.
func TestCatchUp_ANewerPromptDoesNotInheritTheOldWords(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t0", 50, "更早的"))
	k.event("s1", 900) // t0 is recorded: the next event has a cursor, and only the new prompt is newer than it
	fresh := unflushedTurn("t2", "下一個問題")
	fresh.StartedAt = 6000 // the user typed it after the Stop at 5000
	k.turns.set("s1", endedTurn("t0", 50, "更早的"), fresh)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "完全不同的文字", At: 5000, Seq: 1})
	runLater(k, 40)
	if got := turnIDs(k.entries("s1")); got != "t0" {
		t.Fatalf("recorded %q, want t0 only", got)
	}
}

// The file's own words differ from the hook's (no prefix relation) and the turn is the only one newer than the cursor:
// it is this event's turn, recorded as the file has it.
func TestCatchUp_DifferentWordsInTheFileStillAreTheEventsTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "舊"))
	k.event("s1", 1000)
	odd := runningTurn("t2")
	odd.Items = []convmodel.Item{userItem("問"), agentItem("檔案裡的版本")}
	k.turns.set("s1", endedTurn("t1", 100, "舊"), odd)
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "hook 的版本", At: 5000, Seq: 2})
	if got := turnIDs(k.entries("s1")); got != "t1,t2" {
		t.Fatalf("recorded %q", got)
	}
}

// A delayed event whose turn is older than later ended ones gives its own time and order to that turn, not to the newest.
// Mutation gate: always stamp the newest → red.
func TestCatchUp_TheEventsTimeGoesToItsOwnTurn(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "答1"), endedTurn("t2", 200, "答2"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "答1", At: 5000, Seq: 7})
	rows := k.entries("s1")
	if turnIDs(rows) != "t1,t2" || rows[0].TurnAt != 5000 || rows[0].TurnSeq != 7 || rows[1].TurnAt != 200 {
		t.Fatalf("rows = %+v", rows)
	}
}

// A hook that carries no last_assistant_message gives nothing to wait for or to adopt: a turn that has done something is
// the one that ended; a turn with only the user's prompt is not recorded (and nothing waits).
func TestCatchUp_AHookWithNoWords(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "a"), unflushedTurn("t2", "問"))
	k.e.OnTurnEnd(agent.TurnEndEvent{SessionID: "s1", Text: "", At: 5000, Seq: 1})
	if len(k.afters) != 0 || turnIDs(k.entries("s1")) != "t1" {
		t.Fatalf("recorded %q with %d timers", turnIDs(k.entries("s1")), len(k.afters))
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
