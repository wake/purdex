package dialogue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	agentmod "github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/workbooklines"
)

const flowStream = "Ab3_-xyz09"

const haikuJSON = `{"skip": false, "title": "俳句", "push": "寫了兩首俳句", "summary": "寫了雨和風兩首俳句。"}`

// flow is the real module over a fake transcript reader and a fake mod: the mod's side is jobsService, called the way the
// socket routes call it.
type flow struct {
	t       *testing.T
	m       *Module
	sub     *fakeSub
	dir     string
	svc     jobsService
	settled chan struct{}

	mu    sync.Mutex
	turns []convmodel.Turn
}

func newFlow(t *testing.T, capable, startModule bool) *flow {
	t.Helper()
	f := &flow{t: t, dir: t.TempDir(), settled: make(chan struct{}, 64)}
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: f.dir}})
	fr := &fakeReader{calls: map[string]int{}, froms: map[string][]string{},
		fn: func(sid, from string, n int) (convturns.TurnsFromResult, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return fromRes(append([]convmodel.Turn(nil), f.turns...), from), nil
		}}
	f.sub = &fakeSub{}
	c.Registry.Register(convturns.Key, fullReader{fr})
	c.Registry.Register(agentmod.TerminalSessionsKey, f.sub)
	reg := modevents.NewRegistry(time.Now)
	if capable {
		_, _ = reg.Apply(modevents.Batch{V: 1, Stream: flowStream, Agent: "cc", Caps: []string{modevents.CapWorkbookV2},
			Events: []modevents.Event{{Seq: 1, SID: sidS, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	}
	c.Registry.Register("modevents", reg)
	f.m = New()
	if err := f.m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.m.Stop(context.Background()); f.m.Close() })
	f.svc = jobsService{m: f.m}
	if startModule {
		f.start()
	}
	return f
}

func (f *flow) start() {
	f.t.Helper()
	if err := f.m.Start(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	prev := f.m.intake.d.onSettled
	f.m.intake.d.onSettled = func(sid string, at int64) { prev(sid, at); f.settled <- struct{}{} } // before the first event
}

func (f *flow) setTurns(ts ...convmodel.Turn) {
	f.mu.Lock()
	f.turns = ts
	f.mu.Unlock()
}

// stop ends an event of the session and waits until its intake is done.
func (f *flow) stop(at int64) {
	f.t.Helper()
	f.sub.emit(agentmod.TurnEndEvent{SessionID: sidS, At: at, Seq: at})
	select {
	case <-f.settled:
	case <-time.After(10 * time.Second):
		f.t.Fatal("the event did not settle")
	}
}

func (f *flow) seg() string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "dialogue", sidS, "001.md"))
	return string(b)
}

// job takes the next job as the mod would.
func (f *flow) job() wireJob {
	f.t.Helper()
	got, ok := f.svc.NextJob(context.Background(), flowStream, sidS, 3*time.Second)
	if !ok {
		f.t.Fatal("no job was handed out")
	}
	return got.(wireJob)
}

func (f *flow) answer(j wireJob, text string) {
	f.t.Helper()
	if _, err := f.svc.JobResult(flowStream, modevents.WorkbookResult{JobID: j.ID, Answered: true, Text: text,
		Usage: modevents.WorkbookUsage{Input: 10, Output: 5}, LatencyMS: 40}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *flow) summaries() []SummaryRow {
	f.t.Helper()
	rows, err := f.m.store.querySummaries(`SELECT ` + summaryCols + ` FROM dlg_summaries ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func ended(id string) convmodel.Turn { return basicTurn(id) }

func (f *flow) lines() workbooklines.Lines {
	svc, ok := f.m.core.Registry.Get(workbooklines.Key)
	if !ok {
		f.t.Fatal("workbook.push-lines is not registered")
	}
	return svc.(workbooklines.Lines)
}

// 5: a mod's answer puts the summary block into the dialogue file with the turn's number and time; 6: the push wait gets
// its title and push line.
func TestFlow_AnswerLandsInTheFileAndTheWait(t *testing.T) {
	f := newFlow(t, true, true)
	f.setTurns(ended("t1"))
	at := time.Now().Add(time.Hour).UnixMilli()
	got := make(chan workbooklines.Line, 1)
	go func() {
		l, ok := f.lines().Await(context.Background(), sidS, at, time.Now().Add(8*time.Second))
		if !ok {
			l = workbooklines.Line{}
		}
		got <- l
	}()
	f.stop(at)
	j := f.job()
	if !strings.Contains(j.Complete.Prompt, "question t1") || !strings.Contains(j.Complete.System[0].Text, "JSON") {
		t.Fatalf("job = %+v", j.Complete)
	}
	f.answer(j, haikuJSON)
	want := "<turn-summary 1 · " + time.UnixMilli(at).Format("15:04") + " | 俳句>\n寫了雨和風兩首俳句。\n"
	waitFor(t, "the block in the file", func() bool { return strings.Contains(f.seg(), want) })
	if !strings.HasSuffix(f.seg(), "\n\n"+want) {
		t.Fatalf("file = %q", f.seg())
	}
	select {
	case l := <-got:
		if l.Thing != "俳句" || l.Push != "寫了兩首俳句" {
			t.Fatalf("line = %+v", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the push wait did not return")
	}
	r := f.summaries()[0]
	if r.State != stateDone || r.TurnAt != at || r.TurnNo != 1 || r.Usage.In != 10 {
		t.Fatalf("row = %+v", r)
	}
}

// 2 + 3: the first event of a session summarises only its own turn; later five new turns make three summaries.
func TestFlow_FirstEventOneTurnThenFiveNewMakeThree(t *testing.T) {
	f := newFlow(t, true, true)
	f.setTurns(ended("t1"), ended("t2"), ended("t3"), ended("t4"), ended("t5"))
	f.stop(time.Now().UnixMilli() + 1000)
	if rows := f.summaries(); len(rows) != 1 || rows[0].TurnID != "t5" || rows[0].TurnNo != 5 {
		t.Fatalf("first event: %+v", rows)
	}
	f.setTurns(ended("t1"), ended("t2"), ended("t3"), ended("t4"), ended("t5"),
		ended("t6"), ended("t7"), ended("t8"), ended("t9"), ended("t10"))
	f.stop(time.Now().UnixMilli() + 2000)
	var ids []string
	for _, r := range f.summaries()[1:] {
		ids = append(ids, r.TurnID)
	}
	if strings.Join(ids, ",") != "t8,t9,t10" {
		t.Fatalf("second event: %v", ids)
	}
}

// 4 + 6: a session whose mod cannot run jobs gets a skipped row, and the push wait lets go at once.
func TestFlow_NotCapableIsSkippedAndTheWaitReleases(t *testing.T) {
	f := newFlow(t, false, true)
	f.setTurns(ended("t1"))
	at := time.Now().UnixMilli() + 1000
	f.stop(at)
	rows := f.summaries()
	if len(rows) != 1 || rows[0].State != stateSkipped || rows[0].Reason != reasonNoMod {
		t.Fatalf("rows = %+v", rows)
	}
	start := time.Now()
	if _, ok := f.lines().Await(context.Background(), sidS, at, time.Now().Add(5*time.Second)); ok || time.Since(start) > time.Second {
		t.Fatalf("Await ok=%v after %v", ok, time.Since(start))
	}
}

// 6: the model said skip, or the turn had nothing to summarise: no push line, and no waiting for one.
func TestFlow_SkipAndNoSummaryReleaseTheWait(t *testing.T) {
	f := newFlow(t, true, true)
	f.setTurns(ended("t1"))
	at := time.Now().UnixMilli() + 1000
	f.stop(at)
	f.answer(f.job(), `{"skip": true, "title": "", "push": "", "summary": ""}`)
	start := time.Now()
	if _, ok := f.lines().Await(context.Background(), sidS, at, time.Now().Add(5*time.Second)); ok || time.Since(start) > time.Second {
		t.Fatalf("skipped: ok=%v after %v", ok, time.Since(start))
	}
	if r := f.summaries()[0]; r.State != stateSkipped || r.Reason != reasonModel {
		t.Fatalf("row = %+v", r)
	}
	if strings.Contains(f.seg(), "turn-summary") {
		t.Fatal("a skipped summary reached the file")
	}
	// an event that makes no summary at all
	f.setTurns(ended("t1"), done("t2", userItem("u2", "hi")))
	at2 := at + 5000
	f.stop(at2)
	if n := len(f.summaries()); n != 1 {
		t.Fatalf("%d rows", n)
	}
	start = time.Now()
	if _, ok := f.lines().Await(context.Background(), sidS, at2, time.Now().Add(5*time.Second)); ok || time.Since(start) > time.Second {
		t.Fatalf("no summary: ok=%v after %v", ok, time.Since(start))
	}
}

// 5: an answer whose storing failed is applied again by the reaper; the block is put into the file once.
func TestFlow_RetriedAnswerPutsTheBlockOnce(t *testing.T) {
	f := newFlow(t, true, true)
	f.setTurns(ended("t1"))
	f.stop(time.Now().UnixMilli() + 1000)
	calls := 0
	f.m.store.failInSummaryDone = func() error {
		calls++
		if calls == 1 {
			return errors.New("disk full")
		}
		return nil
	}
	f.answer(f.job(), haikuJSON)
	if strings.Contains(f.seg(), "turn-summary") {
		t.Fatal("a block was put in before the answer was stored")
	}
	f.m.jobs.reap() // the reaper's tick
	if got := strings.Count(f.seg(), "<turn-summary 1 · "); got != 1 {
		t.Fatalf("%d blocks in %q", got, f.seg())
	}
	f.m.jobs.reap()
	if got := strings.Count(f.seg(), "<turn-summary"); got != 1 {
		t.Fatalf("%d blocks after another tick", got)
	}
}

// 7: what the last run left pending is failed at Start.
func TestFlow_PendingFromTheLastRunFailsAtStart(t *testing.T) {
	f := newFlow(t, true, false)
	id, _, err := f.m.store.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: "t1", TurnNo: 1, TurnAt: 5})
	if err != nil {
		t.Fatal(err)
	}
	f.start()
	r, _, _ := f.m.store.SummaryByID(id)
	if r.State != stateFailed || r.Reason != reasonStopped {
		t.Fatalf("row = %+v", r)
	}
}

// 8: a rebuild puts the summary right after its turn.
func TestFlow_RebuildPutsTheSummaryAfterItsTurn(t *testing.T) {
	f := newFlow(t, true, true)
	f.setTurns(ended("t1"))
	f.stop(time.Now().UnixMilli() + 1000)
	f.answer(f.job(), haikuJSON)
	waitFor(t, "the block", func() bool { return strings.Contains(f.seg(), "<turn-summary 1") })
	if err := os.Remove(filepath.Join(f.dir, "dialogue", sidS, "001.md")); err != nil {
		t.Fatal(err)
	}
	f.setTurns(ended("t1"), ended("t2"))
	f.stop(time.Now().UnixMilli() + 2000)
	got := f.seg()
	i, j, k := strings.Index(got, "agent: answer t1"), strings.Index(got, "<turn-summary 1 · "), strings.Index(got, "user: question t2")
	if !(i >= 0 && i < j && j < k) {
		t.Fatalf("file = %q", got)
	}
	if strings.Count(got, "<turn-summary") != 1 {
		t.Fatalf("file = %q", got)
	}
}

// The recap trigger is called when the count since the last recap reaches 30, and not before.
func TestSummaryKind_AfterTriggersTheRecapAtThirty(t *testing.T) {
	e := newEnv(t, Hooks{})
	e.write(sidS, all([]convmodel.Turn{basicTurn("t1")}), "", false)
	var recapped []string
	k := &summaryKind{st: e.store, w: e.w, recap: func(sid string) { recapped = append(recapped, sid) }}
	run := func(turnID string, no int) {
		id, _, err := e.store.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: turnID, TurnNo: no, TurnAt: 1000})
		if err != nil {
			t.Fatal(err)
		}
		j := job{sid: sidS, kind: kindTurnSummary, payload: summaryJob{id: id}}
		if err := k.apply(j, haikuJSON, usage{}); err != nil {
			t.Fatal(err)
		}
		k.after(j)
	}
	if _, err := e.store.db.Exec(`UPDATE dlg_sessions SET since_recap = 28 WHERE session_id = ?`, sidS); err != nil {
		t.Fatal(err)
	}
	run("a", 2) // 29
	if len(recapped) != 0 {
		t.Fatalf("recap at 29: %v", recapped)
	}
	run("b", 3) // 30
	if len(recapped) != 1 || recapped[0] != sidS {
		t.Fatalf("recap at 30: %v", recapped)
	}
	// a skipped answer counts for nothing and triggers nothing
	id, _, _ := e.store.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: "c", TurnNo: 4, TurnAt: 1000})
	j := job{sid: sidS, kind: kindTurnSummary, payload: summaryJob{id: id}}
	if err := k.apply(j, `{"skip": true, "title": "", "push": "", "summary": ""}`, usage{}); err != nil {
		t.Fatal(err)
	}
	k.after(j)
	if len(recapped) != 1 {
		t.Fatalf("recap after a skip: %v", recapped)
	}
}

func TestSummaryKind_ApplyContract(t *testing.T) {
	e := newEnv(t, Hooks{})
	k := &summaryKind{st: e.store, w: e.w}
	id, _, _ := e.store.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: "t1", TurnNo: 1, TurnAt: 5})
	j := job{sid: sidS, kind: kindTurnSummary, payload: summaryJob{id: id}}
	if err := k.apply(j, "not json", usage{}); !errors.Is(err, errFormat) {
		t.Fatalf("a bad answer: %v", err)
	}
	if err := k.apply(j, haikuJSON, usage{}); err != nil {
		t.Fatal(err)
	}
	if err := k.apply(j, `{"skip": true, "title": "", "push": "", "summary": ""}`, usage{}); err != nil {
		t.Fatalf("the same row again: %v", err)
	}
	if r, _, _ := e.store.SummaryByID(id); r.State != stateDone || r.Title != "俳句" {
		t.Fatalf("a second answer changed the row: %+v", r)
	}
	gone := job{sid: sidS, kind: kindTurnSummary, payload: summaryJob{id: id + 99}}
	if err := k.apply(gone, haikuJSON, usage{}); !errors.Is(err, errGone) {
		t.Fatalf("a missing row: %v", err)
	}
	// finish leaves a done row alone
	k.finish(j, stateFailed, reasonStopped)
	if r, _, _ := e.store.SummaryByID(id); r.State != stateDone {
		t.Fatalf("finish changed a done row: %+v", r)
	}
}

func TestSummaryBlock(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 5, 0, 0, time.Local).UnixMilli()
	got := summaryBlock(SummaryRow{TurnNo: 12, TurnAt: at, Title: "修\nbug", Summary: "做了甲。"})
	if got != "<turn-summary 12 · 09:05 | 修 bug>\n做了甲。\n" {
		t.Fatalf("block = %q", got)
	}
}
