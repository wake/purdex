package workbook

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// WB-2b-i (b): the refresh request, its job, its result and the refresh_available sweep (spec §5.6, plan D10 / D11 / D14).

func (k *kit) canRefresh(sids ...string) {
	k.refresh = nil
	for _, s := range sids {
		k.capable[s] = true
		k.refresh = append(k.refresh, CapSession{SID: s, At: k.clock.Now()})
	}
}

func refreshAnswer(status, todos string) Result {
	return Result{Answered: true, Text: `{"status":` + quote(status) + `,"todos":` + todos + `}`, Usage: Usage{In: 900, Out: 40, CacheRead: 800}, LatencyMS: 5000}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func mustRefresh(t *testing.T, k *kit, sid, caller string) int64 {
	t.Helper()
	id, err := k.e.RequestRefresh(sid, caller)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return id
}

// No session able to run it → ErrNotLive and nothing is written; a mod with workbook.v2 alone (a WB-1c mod) is not able.
// Mutation gate: let a v2-only session count → red.
func TestRefresh_NotLiveWithoutARefreshCapableSession(t *testing.T) {
	k := newKit(t)
	k.capable["s1"] = true // v2 only
	if _, err := k.e.RequestRefresh("s1", ""); !errors.Is(err, ErrNotLive) {
		t.Fatalf("err = %v", err)
	}
	if rows := k.entries("s1"); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

// The refresh row: kind refresh, pending, in the running session, a unique r: turn id, the request time as turn_at, seq 0.
func TestRefresh_RowShapeAndPendingGate(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	id := mustRefresh(t, k, "s1", "")
	rows := k.entries("s1")
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if r.Kind != KindRefresh || r.State != StatePending || r.SessionID != "s1" || !strings.HasPrefix(r.TurnID, "r:") ||
		r.TurnAt != k.clock.Now().UnixMilli() || r.TurnSeq != 0 || r.TeamID != "team-9" || r.Role != "member" || r.PromptVer != PromptVersion {
		t.Fatalf("row = %+v", r)
	}
	if _, err := k.e.RequestRefresh("s1", ""); !errors.Is(err, ErrRefreshPending) {
		t.Fatalf("second request: %v", err)
	}
}

// Two refreshes of one session never collide on (session_id, turn_id), even in the same millisecond.
func TestRefresh_RowsOfOneSessionNeverCollide(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	a := mustRefresh(t, k, "s1", "")
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, refreshAnswer("狀況", `{"done":[],"dropped":[],"add":[]}`))
	b := mustRefresh(t, k, "s1", "") // same fake-clock millisecond
	rows := k.entries("s1")
	if len(rows) != 2 || a == b || rows[0].TurnID == rows[1].TurnID {
		t.Fatalf("rows = %+v", rows)
	}
}

// The caller's session is preferred; else the one whose stream announced workbook.refresh most recently.
func TestRefresh_ChoosesTheRunningSession(t *testing.T) {
	two := func() *kit {
		k := newKit(t)
		k.lineage["old"], k.lineage["new"] = "root", "root"
		k.canRefresh("old", "new")
		k.refresh = []CapSession{{SID: "old", At: k.clock.Now().Add(-5 * time.Second)}, {SID: "new", At: k.clock.Now()}}
		return k
	}
	k := two()
	id := mustRefresh(t, k, "old", "") // the Mac asks: no caller, the newest announcement runs it
	if e, _ := k.st.Entry(id); e.SessionID != "new" || e.ConvKey != "root" {
		t.Fatalf("mac: %+v", e)
	}
	k = two()
	id = mustRefresh(t, k, "new", "old") // the mod's command: the caller wins
	if e, _ := k.st.Entry(id); e.SessionID != "old" {
		t.Fatalf("caller: %+v", e)
	}
}

// The job on the wire is {id, kind: "refresh", fork: {prompt, timeout_ms}} and carries no complete; the prompt is filled with
// the status and the numbered open todos. A turn job keeps its complete and has no fork.
func TestRefresh_JobShape(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	e0 := mustInsert(t, k.st, pending("s1", "s1", "t0", 1))
	seedTodos(t, k.st, "s1", e0, "甲", "乙")
	k.st.SetStatus("s1", "在修登入", e0, "s1")
	mustRefresh(t, k, "s1", "")
	j := mustNext(t, k, "m", "s1")
	b, _ := json.Marshal(j)
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, has := wire["complete"]; has || j.Kind != JobRefresh || string(wire["kind"]) != `"refresh"` || wire["fork"] == nil {
		t.Fatalf("wire = %s", b)
	}
	var fork struct {
		Prompt    string `json:"prompt"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(wire["fork"], &fork); err != nil || fork.TimeoutMS != refreshTimeout ||
		!strings.Contains(fork.Prompt, "在修登入") || !strings.Contains(fork.Prompt, "1：甲") || !strings.Contains(fork.Prompt, "2：乙") ||
		strings.Contains(fork.Prompt, "{{") {
		t.Fatalf("fork = %+v (%v)", fork, err)
	}
}

// A refresh goes only to a refresh-capable session: a v2-only session of the conversation gets 204 for it, and so does the
// turn behind it; the capable one takes the refresh and then the turn follows.
func TestRefresh_OnlyACapableSessionTakesIt(t *testing.T) {
	k := newKit(t)
	k.capable["plain"] = true
	k.lineage["plain"] = "root"
	k.canRefresh("root")
	mustRefresh(t, k, "root", "")
	k.turns.set("plain", endedTurn("t1", 100, "做了"))
	k.event("plain", 1000) // a turn queued behind the refresh
	if _, ok := k.next("m1", "plain"); ok {
		t.Fatal("a v2-only session took the refresh (or the turn behind it)")
	}
	if k.e.JobWaiting("plain") {
		t.Fatal("a session that cannot run the head job was told one waits")
	}
	if !k.e.JobWaiting("root") {
		t.Fatal("the capable session was not told")
	}
	j := mustNext(t, k, "m2", "root")
	if j.Kind != JobRefresh {
		t.Fatalf("first job = %s", j.Kind)
	}
	k.finish("m2", j, refreshAnswer("狀況", `{"done":[],"dropped":[],"add":[]}`))
	if j2, ok := k.next("m1", "plain"); !ok || j2.Kind != JobTurn {
		t.Fatalf("after the refresh: %+v %v", j2, ok)
	}
}

// The asked session is gone: another capable session of the conversation leases it, and the row follows.
func TestRefresh_RepointedToTheSessionThatLeases(t *testing.T) {
	k := newKit(t)
	k.lineage["b"] = "a"
	k.canRefresh("a")
	id := mustRefresh(t, k, "a", "")
	k.canRefresh("b") // a's mod went away; b (a later session of the same conversation) can
	mustNext(t, k, "m", "b")
	if e, _ := k.st.Entry(id); e.SessionID != "b" || e.State != StatePending {
		t.Fatalf("row = %+v", e)
	}
}

// At the head with nobody able for 40 s → failed lost, and the turn behind it goes out. Before that, it waits.
// Mutation gate: never fail it → red.
func TestRefresh_HeadTimesOutWithoutACapableSession(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	id := mustRefresh(t, k, "s1", "")
	k.refresh = nil // the mod went away
	k.e.reap()      // starts the clock
	k.clock.Add(39 * time.Second)
	k.e.reap()
	if e, _ := k.st.Entry(id); e.State != StatePending {
		t.Fatalf("after 39 s: %+v", e)
	}
	k.clock.Add(2 * time.Second)
	k.e.reap()
	if e, _ := k.st.Entry(id); e.State != StateFailed || e.Reason != ReasonLost {
		t.Fatalf("after 41 s: %+v", e)
	}
	// the next turn job goes out
	k.capable["s1"] = true
	k.turns.set("s1", endedTurn("t1", 100, "做了"))
	k.event("s1", 1000)
	if j, ok := k.next("m", "s1"); !ok || j.Kind != JobTurn {
		t.Fatalf("turn after the lost refresh: %+v %v", j, ok)
	}
}

// A capable session coming back resets the head clock.
func TestRefresh_HeadClockResetsWhenASessionReturns(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	id := mustRefresh(t, k, "s1", "")
	k.refresh = nil
	k.e.reap()
	k.clock.Add(30 * time.Second)
	k.canRefresh("s1")
	k.e.reap() // capable again: the clock is cleared
	k.refresh = nil
	k.clock.Add(30 * time.Second)
	k.e.reap() // a new clock starts here
	k.clock.Add(30 * time.Second)
	k.e.reap()
	if e, _ := k.st.Entry(id); e.State != StatePending {
		t.Fatalf("failed too early: %+v", e)
	}
}

// The result is written in one step: status replaced, todos closed / added by "refresh", the entry ok with the current
// thing, the line and the usage; no push. Events: status, todos, entry. Mutation gate: apply the turn's 2-add cap → red.
func TestRefresh_ResultWrittenWhole(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	e0 := mustInsert(t, k.st, pending("s1", "s1", "t0", 1))
	k.st.SetPushLineV2(e0, PushLineV2{Thing: "登入修正", Push: "推", Status: "舊狀況"})
	k.st.Finish(e0, StateOK, "", Output{Thing: "登入修正", Push: "推", Entry: "e"})
	open := seedTodos(t, k.st, "s1", e0, "甲", "乙", "丙")
	var evs []Event
	k.st.SetObserver(func(e Event) { evs = append(evs, e) })
	id := mustRefresh(t, k, "s1", "")
	evs = nil
	j := mustNext(t, k, "m", "s1")
	adds := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		adds = append(adds, `{"title":"新`+string(rune('a'+i))+`","detail":"d"}`)
	}
	k.finish("m", j, refreshAnswer("新狀況", `{"done":[1],"dropped":[2],"add":[`+strings.Join(adds, ",")+`]}`))
	e, _ := k.st.Entry(id)
	if e.State != StateOK || e.Kind != KindRefresh || e.Thing != "登入修正" || e.Push != "" || e.Entry != "重整：完成 1、移除 1、新增 5" ||
		e.UsageIn != 900 || e.UsageOut != 40 || e.UsageCacheRead != 800 || e.LatencyMS != 5000 || e.PushReadyAt != 0 {
		t.Fatalf("entry = %+v", e)
	}
	if st, _, _ := k.st.Status("s1"); st.Status != "新狀況" || st.EntryID != id {
		t.Fatalf("status = %+v", st)
	}
	still, _ := k.st.OpenTodos("s1", 30)
	if titles(still) != "丙|新a|新b|新c|新d|新e" {
		t.Fatalf("open = %s", titles(still))
	}
	done, _ := k.st.Todos("s1", TodoDone, 5, 0)
	dropped, _ := k.st.Todos("s1", TodoDropped, 5, 0)
	if len(done) != 1 || done[0].ID != open[0].ID || done[0].ClosedBy != ClosedByRefresh || len(dropped) != 1 || dropped[0].ID != open[1].ID {
		t.Fatalf("done=%+v dropped=%+v", done, dropped)
	}
	var kinds []string
	for _, ev := range evs {
		kinds = append(kinds, ev.Kind)
	}
	if strings.Join(kinds, ",") != "status,todos,entry" {
		t.Fatalf("events = %v", kinds)
	}
}

// Failures: nothing-to-fork maps to its own reason; a bad answer is retried once as the same kind, then format; an API error is api.
func TestRefresh_Failures(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	id := mustRefresh(t, k, "s1", "")
	j := mustNext(t, k, "m", "s1")
	k.finish("m", j, Result{Reason: "nothing-to-fork"})
	if e, _ := k.st.Entry(id); e.State != StateFailed || e.Reason != ReasonNothingToFork {
		t.Fatalf("nothing-to-fork: %+v", e)
	}
	id = mustRefresh(t, k, "s1", "")
	j = mustNext(t, k, "m", "s1")
	k.finish("m", j, Result{Answered: true, Text: "not json"})
	j2 := mustNext(t, k, "m", "s1")
	if j2.Kind != JobRefresh || j2.Fork == nil {
		t.Fatalf("retry = %+v", j2)
	}
	k.finish("m", j2, Result{Answered: true, Text: "still not"})
	if e, _ := k.st.Entry(id); e.State != StateFailed || e.Reason != ReasonFormat {
		t.Fatalf("format: %+v", e)
	}
	id = mustRefresh(t, k, "s1", "")
	j = mustNext(t, k, "m", "s1")
	k.finish("m", j, Result{Reason: "api-error", Status: 500, Error: "api_error"})
	if e, _ := k.st.Entry(id); e.State != StateFailed || e.Reason != ReasonAPI {
		t.Fatalf("api: %+v", e)
	}
}

// A refresh in the queue when the module stops fails (stopped), never "skipped".
func TestRefresh_StopFailsAQueuedRefresh(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	id := mustRefresh(t, k, "s1", "")
	k.e.Stop()
	if e, _ := k.st.Entry(id); e.State != StateFailed || e.Reason != ReasonStopped {
		t.Fatalf("entry = %+v", e)
	}
}

// A refresh does not spend the hourly cap of summariser calls.
func TestRefresh_IgnoresTheHourlyCap(t *testing.T) {
	k := newKit(t)
	k.canRefresh("s1")
	k.e.callCap = 0
	mustRefresh(t, k, "s1", "")
	if j, ok := k.next("m", "s1"); !ok || j.Kind != JobRefresh {
		t.Fatalf("job = %+v %v", j, ok)
	}
}

// ---- refresh_available ----

func sweepConvs(k *kit) string {
	var out []string
	for _, c := range k.e.SweepAvailability() {
		out = append(out, c.ConvKey+"="+map[bool]string{true: "on", false: "off"}[c.Available])
	}
	return strings.Join(out, ",")
}

// One stream appears → one event; ten sweeps in a row → no duplicate; the last one lapses → one event.
func TestAvailability_OneEventPerChange(t *testing.T) {
	k := newKit(t)
	if got := sweepConvs(k); got != "" {
		t.Fatalf("nothing live: %q", got)
	}
	k.canRefresh("s1")
	if got := sweepConvs(k); got != "s1=on" {
		t.Fatalf("appears: %q", got)
	}
	for i := 0; i < 10; i++ {
		if got := sweepConvs(k); got != "" {
			t.Fatalf("sweep %d repeated: %q", i, got)
		}
	}
	k.refresh = nil
	if got := sweepConvs(k); got != "s1=off" {
		t.Fatalf("lapses: %q", got)
	}
	if got := sweepConvs(k); got != "" {
		t.Fatalf("repeated off: %q", got)
	}
}

// Two sessions of one conversation: one lapsing while the other is fresh sends nothing; both lapsing sends one event.
// Mutation gate: key availability by session, not conversation → red.
func TestAvailability_PerConversation(t *testing.T) {
	k := newKit(t)
	k.lineage["b"] = "a"
	k.canRefresh("a", "b")
	if got := sweepConvs(k); got != "a=on" {
		t.Fatalf("two streams: %q", got)
	}
	k.canRefresh("b")
	if got := sweepConvs(k); got != "" {
		t.Fatalf("one lapsed, one fresh: %q", got)
	}
	if !k.e.RefreshAvailable("a") || k.e.RefreshAvailable("other") {
		t.Fatal("RefreshAvailable is not per conversation")
	}
	k.refresh = nil
	if got := sweepConvs(k); got != "a=off" {
		t.Fatalf("both lapsed: %q", got)
	}
}
