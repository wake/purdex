package workbook

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// WB-1b′-b: what a result does to its entry (spec §5.1 table, §5.4, plan D3 / D9).

// The job's input is built when it is handed out, and the next job of a conversation is handed out only after the
// previous entry is final: its input then carries the previous status and entry.
// Mutation gate: clear the lease before the Finish, or build the input at insert time → red.
func TestQueue_OrderAndTheNextJobAfterFinish(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"))
	k.event("s1", 2000)
	j1 := mustNext(t, k, "m", "s1")
	if j1.Kind != JobTurn || j1.Complete.Model != "haiku" || j1.Complete.Effort != "low" || j1.Complete.MaxTokens != 4096 ||
		j1.Complete.TimeoutMS != 30000 || len(j1.Complete.System) != 1 || !j1.Complete.System[0].Cache || j1.Complete.System[0].Text != SystemPrompt {
		t.Fatalf("job = %+v", j1)
	}
	if !strings.Contains(j1.Complete.Prompt, "做 t1") {
		t.Fatalf("the oldest turn first: %s", j1.Complete.Prompt)
	}
	if _, ok := k.next("m", "s1"); ok {
		t.Fatal("the next job must wait for the previous result")
	}
	more := k.finish("m", j1, answerJSON("功能甲", "好了", "第一句。", "進行中", ""))
	if !more {
		t.Fatal("the second job is ready: more must be true")
	}
	rows := k.entries("s1")
	if rows[0].State != StateOK || rows[0].Thing != "功能甲" || rows[0].UsageIn != 100 || rows[0].LatencyMS != 900 {
		t.Fatalf("first entry = %+v", rows[0])
	}
	j2 := mustNext(t, k, "m", "s1")
	if !strings.Contains(j2.Complete.Prompt, `"previous_status":"進行中"`) || !strings.Contains(j2.Complete.Prompt, "第一句。") {
		t.Fatalf("the second input must read the first output: %s", j2.Complete.Prompt)
	}
}

// A result from another stream, an unknown job, or a second result is not leased.
func TestQueue_NotLeased(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	j := mustNext(t, k, "m1", "s1")
	if _, err := k.e.Result("m2", j.ID, answerJSON("x", "y", "z。", "s", "")); err != ErrNotLeased {
		t.Fatalf("other stream: %v", err)
	}
	if _, err := k.e.Result("m1", "nope", Result{}); err != ErrNotLeased {
		t.Fatalf("unknown: %v", err)
	}
	k.finish("m1", j, answerJSON("x", "y", "z。", "s", ""))
	if _, err := k.e.Result("m1", j.ID, answerJSON("x", "y", "z。", "s", "")); err != ErrNotLeased {
		t.Fatalf("twice: %v", err)
	}
}

// Each row of the spec §5.1 outcome table.
func TestQueue_OutcomeTable(t *testing.T) {
	cases := []struct {
		name   string
		r      Result
		state  string
		reason string
	}{
		{"auth", Result{Reason: "api-error", Error: "authentication_failed"}, StateFailed, ReasonAuth},
		{"api", Result{Reason: "api-error", Status: 529, Error: "overloaded_error"}, StateFailed, ReasonAPI},
		{"timeout", Result{Reason: "aborted", LatencyMS: 30000}, StateFailed, ReasonTimeout},
		{"aborted", Result{Reason: "aborted", LatencyMS: 1200}, StateFailed, ReasonStopped},
		{"refused", Result{Reason: "refused"}, StateFailed, ReasonRefused},
		{"unknown reason", Result{Reason: "weird"}, StateFailed, ReasonAPI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := kitWith(t, "s1", "t1")
			j := mustNext(t, k, "m", "s1")
			k.finish("m", j, c.r)
			e := k.entries("s1")[0]
			if e.State != c.state || e.Reason != c.reason {
				t.Fatalf("entry = %s/%s, want %s/%s", e.State, e.Reason, c.state, c.reason)
			}
			if k.lines[len(k.lines)-1] != "1:false" {
				t.Fatalf("hook = %v", k.lines)
			}
		})
	}
}

// A secret-looking error text from the mod never reaches the log.
func TestQueue_TheLogCarriesKindsNotText(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, Result{Reason: "api-error", Status: 500, Error: "Bearer abc123SECRET and more text"})
	if strings.Contains(k.logs.text(), "SECRET") {
		t.Fatalf("log = %s", k.logs.text())
	}
}

// A re-write's failure reason is text from the mod too: only a kind reaches the log (codex attack).
// Mutation gate: log r.Reason as it is → red.
func TestQueue_TheLogCarriesKindsForARewriteToo(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("事", "推", longEntry(), "狀", ""))
	rw := mustNext(t, k, "m", "s1")
	k.finish("m", rw, Result{Reason: "Bearer abc123SECRET"})
	if strings.Contains(k.logs.text(), "SECRET") {
		t.Fatalf("log = %s", k.logs.text())
	}
}

// A store that refuses an entry's final state does not stall the conversation or lose the entry: the queue moves on and
// the reaper fails the row (store) as soon as the store takes it (issue #2324).
// Mutation gate: drop the orphan record, or the retry in reap → red.
func TestQueue_AFinishTheStoreRefusesIsRetriedAndFailsTheRow(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"))
	k.event("s1", 2000)
	broken := true
	k.st.failFinish = func() error {
		if broken {
			return errTestStore
		}
		return nil
	}
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("事", "推", "短。", "狀", ""))
	if k.entries("s1")[0].State != StatePending {
		t.Fatal("the store refused: the row is still pending")
	}
	mustNext(t, k, "m", "s1") // the conversation moved on
	k.e.reap()                // still refused: stays remembered
	if k.entries("s1")[0].State != StatePending {
		t.Fatal("failed while the store still refuses")
	}
	broken = false
	k.e.reap()
	if e := k.entries("s1")[0]; e.State != StateFailed || e.Reason != ReasonStore {
		t.Fatalf("entry = %s/%s", e.State, e.Reason)
	}
	k.e.omu.Lock()
	n := len(k.e.orphans)
	k.e.omu.Unlock()
	if n != 0 {
		t.Fatalf("%d orphans left", n)
	}
}

var errTestStore = errors.New("test: store refuses")

// Not JSON (or an empty reply) gets one retry — the same kind, attempt 2, at the same place — then failed:format.
// Mutation gate: retry twice, or put the retry at the tail → red.
func TestQueue_FormatRetriesOnceThenFails(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"))
	k.event("s1", 2000)
	j := mustNext(t, k, "m", "s1")
	if !k.finish("m", j, Result{Answered: true, Text: "好的我來摘要"}) {
		t.Fatal("the retry is ready: more")
	}
	if k.entries("s1")[0].State != StatePending {
		t.Fatal("not final after one bad answer")
	}
	j2 := mustNext(t, k, "m", "s1")
	if j2.Kind != JobTurn || j2.ID == j.ID || !strings.Contains(j2.Complete.Prompt, "做 t1") {
		t.Fatalf("retry = %+v", j2)
	}
	k.finish("m", j2, Result{Reason: "empty-reply"})
	if e := k.entries("s1")[0]; e.State != StateFailed || e.Reason != ReasonFormat {
		t.Fatalf("entry = %s/%s", e.State, e.Reason)
	}
	j3 := mustNext(t, k, "m", "s1")
	if !strings.Contains(j3.Complete.Prompt, "做 t2") {
		t.Fatal("the next turn after the failure")
	}
}

func TestQueue_RetrySucceeds(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, Result{Reason: "empty-reply"})
	j2 := mustNext(t, k, "m", "s1")
	k.finish("m", j2, answerJSON("事", "推", "句。", "狀態", ""))
	if e := k.entries("s1")[0]; e.State != StateOK {
		t.Fatalf("state = %s", e.State)
	}
}

// The push line is final before the re-write: the hook fires with the entry still pending, the re-write keeps the
// conversation's place, and the entry is ok only after it.
// Mutation gate: notify after the re-write, or let the next turn pass the re-write → red.
func TestQueue_RewriteKeepsThePlaceAndTheHookFiresFirst(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"))
	k.event("s1", 2000)
	var stateAtHook string
	k.e.SetPushLineHook(func(id int64, ready bool) {
		e, _ := k.st.Entry(id)
		stateAtHook = e.State + "/" + e.Thing
	})
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("功能甲", "好了", longEntry(), "進行中", ""))
	if stateAtHook != "pending/功能甲" {
		t.Fatalf("at the hook: %s", stateAtHook)
	}
	rw := mustNext(t, k, "m", "s1")
	if rw.Kind != JobRewrite || rw.Complete.System[0].Text != RewritePrompt || rw.Complete.Prompt != longEntry() || rw.Complete.System[0].Cache {
		t.Fatalf("re-write job = %+v", rw)
	}
	if k.entries("s1")[0].State != StatePending {
		t.Fatal("ok before the re-write")
	}
	k.finish("m", rw, Result{Answered: true, Text: "改寫後的一句。", Usage: Usage{In: 10, Out: 5}, LatencyMS: 300})
	e := k.entries("s1")[0]
	if e.State != StateOK || e.Entry != "改寫後的一句。" || e.Thing != "功能甲" || e.Push != "好了" || e.UsageIn != 110 || e.LatencyMS != 1200 {
		t.Fatalf("entry = %+v", e)
	}
	j2 := mustNext(t, k, "m", "s1")
	if !strings.Contains(j2.Complete.Prompt, "改寫後的一句。") {
		t.Fatalf("the next turn must read the re-written entry: %s", j2.Complete.Prompt)
	}
}

// Even with no re-write the push hold hears the line while the entry is still pending: it never waits for the Finish.
// Mutation gate: notify after finishOK → red.
func TestQueue_TheHookFiresBeforeTheEntryIsFinal(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	var seen string
	k.e.SetPushLineHook(func(id int64, ready bool) {
		e, _ := k.st.Entry(id)
		seen = e.State + "/" + e.Push
	})
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("事", "推播句", "短。", "狀", ""))
	if seen != "pending/推播句" {
		t.Fatalf("at the hook: %q", seen)
	}
	if k.entries("s1")[0].State != StateOK {
		t.Fatal("not final afterwards")
	}
}

// A re-write that fails, or answers nothing, leaves the entry ok with the original cut at a sentence end ≤ 150.
func TestQueue_ARewriteThatFailsCutsTheEntry(t *testing.T) {
	for name, r := range map[string]Result{"api": {Reason: "api-error"}, "empty": {Answered: true, Text: "  "}} {
		t.Run(name, func(t *testing.T) {
			k := kitWith(t, "s1", "t1")
			j := mustNext(t, k, "m", "s1")
			k.finish("m", j, answerJSON("事", "推", longEntry(), "狀", ""))
			rw := mustNext(t, k, "m", "s1")
			k.finish("m", rw, r)
			e := k.entries("s1")[0]
			if e.State != StateOK || len([]rune(e.Entry)) > 150 || !strings.HasSuffix(e.Entry, "。") || e.Thing != "事" {
				t.Fatalf("entry = %s %q", e.State, e.Entry)
			}
		})
	}
}

// skip:true is skipped:model and its todo changes still apply; the todos are read through the job's number map.
func TestQueue_SkipKeepsItsTodos(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	// an open todo to close, from an earlier entry
	prev := mustInsert(t, k.st, pending("s1", "s1", "old", 1))
	if _, ok, err := k.st.SetPushLineV2(prev, PushLineV2{Thing: "x", Status: "s", Todos: TodoChanges{Adds: []TodoAdd{{Title: "寫文件"}}}, By: "model"}); err != nil || !ok {
		t.Fatal(err)
	}
	j := mustNext(t, k, "m", "s1")
	if !strings.Contains(j.Complete.Prompt, `"n":1,"title":"寫文件"`) {
		t.Fatalf("the prompt must number the open todo: %s", j.Complete.Prompt)
	}
	k.finish("m", j, Result{Answered: true, Text: `{"skip":true,"thing":"","push":"","entry":"","status":"","thing_done":false,"todos":{"done":[1,9],"dropped":[],"add":[{"title":"補測試"}]}}`})
	e := k.entries("s1")[0]
	if e.State != StateSkipped || e.Reason != ReasonModel {
		t.Fatalf("entry = %s/%s", e.State, e.Reason)
	}
	open, _ := k.st.OpenTodos("s1", 30)
	if len(open) != 1 || open[0].Title != "補測試" {
		t.Fatalf("open = %+v", open)
	}
}

// The hourly cap: a turn handed out past it is skipped:cap, one log line per hour; the next hour counts afresh.
// Mutation gate: count at insert instead of at hand-out, or never reset → red.
func TestQueue_HourlyCap(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.e.callCap = 2
	k.turns.set("s1", endedTurn("t1", 100, "a"), endedTurn("t2", 200, "b"), endedTurn("t3", 300, "c"))
	k.event("s1", 2000)
	for i := 0; i < 2; i++ {
		j := mustNext(t, k, "m", "s1")
		k.finish("m", j, answerJSON("事", "推", "句。", "狀", ""))
	}
	if _, ok := k.next("m", "s1"); ok {
		t.Fatal("a call past the cap")
	}
	var reasons []string
	for _, r := range k.entries("s1") {
		reasons = append(reasons, r.State+"/"+r.Reason)
	}
	if strings.Join(reasons, " ") != "ok/ ok/ skipped/cap" {
		t.Fatalf("entries = %v", reasons)
	}
	if strings.Count(k.logs.text(), "hourly cap") != 1 {
		t.Fatalf("log = %s", k.logs.text())
	}
	k.clock.Add(time.Hour)
	k.turns.set("s1", endedTurn("t5", 500, "e"))
	k.event("s1", 9000)
	mustNext(t, k, "m", "s1")
}

// At the cap a re-write is not asked for: the entry is cut and ok at once.
func TestQueue_NoRewriteAtTheCap(t *testing.T) {
	k := kitWith(t, "s1", "t1")
	k.e.callCap = 1
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, answerJSON("事", "推", longEntry(), "狀", ""))
	e := k.entries("s1")[0]
	if e.State != StateOK || len([]rune(e.Entry)) > 150 {
		t.Fatalf("entry = %s %q", e.State, e.Entry)
	}
}

// Stop (plan D9): a leased turn fails (stopped), a queued turn is skipped (stopped), a leased or queued re-write keeps its
// entry ok, cut; every one releases its push waiter false/true as it stands; a late result is not leased.
func TestQueue_Stop(t *testing.T) {
	k := newKit(t)
	k.capable["s1"], k.capable["s2"], k.capable["s3"] = true, true, true
	for sid, id := range map[string]string{"s1": "leased", "s2": "rw", "s3": "queued"} {
		k.turns.set(sid, endedTurn(id, 100, id))
		k.event(sid, 1000)
	}
	k.turns.set("s1", endedTurn("leased", 100, "x"), endedTurn("more", 200, "y"))
	k.event("s1", 2000)
	jl := mustNext(t, k, "m1", "s1")
	jr := mustNext(t, k, "m2", "s2")
	k.finish("m2", jr, answerJSON("事", "推", longEntry(), "狀", ""))
	mustNext(t, k, "m2", "s2") // the re-write is leased
	k.e.Stop()
	state := func(conv string) []string {
		var out []string
		for _, e := range k.entries(conv) {
			out = append(out, e.State+"/"+e.Reason)
		}
		return out
	}
	if got := strings.Join(state("s1"), " "); got != "failed/stopped skipped/stopped" {
		t.Fatalf("s1 = %s", got)
	}
	if e := k.entries("s2")[0]; e.State != StateOK || len([]rune(e.Entry)) > 150 || e.Thing != "事" {
		t.Fatalf("s2 = %+v", e)
	}
	if got := strings.Join(state("s3"), " "); got != "skipped/stopped" {
		t.Fatalf("s3 = %s", got)
	}
	if _, err := k.e.Result("m1", jl.ID, answerJSON("x", "y", "z。", "s", "")); err != ErrNotLeased {
		t.Fatalf("late result: %v", err)
	}
	if _, ok := k.next("m1", "s1"); ok {
		t.Fatal("a job after Stop")
	}
}
