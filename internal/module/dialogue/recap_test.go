package dialogue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
)

type recapRig struct {
	t      *testing.T
	e      *env
	st     *Store
	ch     *channel
	rc     *recapper
	mu     sync.Mutex
	capOK  bool
	casts  []string // "type value"
	nTurns int
}

func newRecapRig(t *testing.T) *recapRig {
	t.Helper()
	r := &recapRig{t: t, capOK: true}
	hooks := Hooks{
		Carried: func(sid string, no int) string { return carriedRecap(r.st, sid, no) },
		Placed:  func(sid, turnID string) []string { return placedBlocks(r.st, sid, turnID) },
	}
	r.e = newEnv(t, hooks)
	r.st = r.e.store
	r.rc = newRecapper(r.st, r.e.w, func(string, ...any) {})
	r.rc.broadcast = func(typ, val string) {
		r.mu.Lock()
		r.casts = append(r.casts, typ+" "+val)
		r.mu.Unlock()
	}
	r.ch = newChannel(chanDeps{
		capable:  func(string) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.capOK },
		handlers: map[string]kindHandler{kindRecap: &recapKind{r: r.rc}},
		logf:     func(string, ...any) {},
	})
	r.rc.enqueue = r.ch.enqueue
	return r
}

func (r *recapRig) setCapable(v bool) { r.mu.Lock(); r.capOK = v; r.mu.Unlock() }

func (r *recapRig) broadcasts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.casts...)
}

// seed writes n turns t1..tn into the dialogue file and gives each a done summary "題i".
func (r *recapRig) seed(n int) {
	r.t.Helper()
	var turns []convmodel.Turn
	for i := 1; i <= n; i++ {
		turns = append(turns, basicTurn(fmt.Sprintf("t%d", i)))
	}
	r.e.write(sidS, all(turns), "", false)
	for i := 1; i <= n; i++ {
		id, _, err := r.st.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: fmt.Sprintf("t%d", i), TurnNo: i, TurnAt: int64(1_800_000_000_000 + i*60_000)})
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := r.st.SetSummaryDone(id, fmt.Sprintf("題%d", i), "", fmt.Sprintf("內容%d。", i), false, usage{}); err != nil {
			r.t.Fatal(err)
		}
	}
	r.nTurns = n
}

// take hands the session's next job out as the mod would.
func (r *recapRig) take() (wireJob, bool) { return r.ch.Next(context.Background(), "streamA", sidS, 0) }

func (r *recapRig) answer(j wireJob, text string) {
	r.t.Helper()
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: text, Usage: usage{In: 7, Out: 3, LatencyMS: 40}}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *recapRig) file() string { return r.e.read(sidS, 1) }

// 2: the 30th summary queues a recap through the real trigger, the 29th does not.
func TestRecap_ThirtiethSummaryTriggers(t *testing.T) {
	r := newRecapRig(t)
	r.seed(1) // the session row; the count is then set by hand
	k := &summaryKind{st: r.st, w: r.e.w, recap: func(sid string) { r.rc.request(sid, "every30") }}
	run := func(turnID string, no int) {
		id, _, _ := r.st.InsertPendingSummary(SummaryRow{SessionID: sidS, ConvKey: sidS, TurnID: turnID, TurnNo: no, TurnAt: 1000})
		j := job{sid: sidS, kind: kindTurnSummary, payload: summaryJob{id: id}}
		if err := k.apply(j, haikuJSON, usage{}); err != nil {
			t.Fatal(err)
		}
		k.after(j)
	}
	r.st.db.Exec(`UPDATE dlg_sessions SET since_recap = 28 WHERE session_id = ?`, sidS)
	run("a", 2)
	if _, ok := r.take(); ok {
		t.Fatal("a recap was queued at 29")
	}
	run("b", 3)
	j, ok := r.take()
	if !ok || j.Kind != kindRecap {
		t.Fatalf("no recap at 30: %+v %v", j, ok)
	}
}

// 3: nothing newer than the last recap (or nothing at all) queues no job.
func TestRecap_RequestUpToDate(t *testing.T) {
	r := newRecapRig(t)
	if got := r.rc.request(sidS, "manual"); got != "up_to_date" {
		t.Fatalf("no summaries: %s", got)
	}
	r.seed(3)
	if _, _, err := r.st.InsertRecap(RecapRow{JobID: "j0", SessionID: sidS, FromNo: 1, ToNo: 3, Trigger: "manual", Headline: "h", BodyJSON: goodRecap}); err != nil {
		t.Fatal(err)
	}
	if got := r.rc.request(sidS, "open"); got != "up_to_date" {
		t.Fatalf("after a recap of everything: %s", got)
	}
	if _, ok := r.take(); ok {
		t.Fatal("a job was queued")
	}
}

// 4: a recap already queued is not queued again.
func TestRecap_NoSecondOneWhileOneIsInFlight(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	if got := r.rc.request(sidS, "manual"); got != "queued" {
		t.Fatal(got)
	}
	if got := r.rc.request(sidS, "every30"); got != "queued" {
		t.Fatal(got)
	}
	if _, ok := r.take(); !ok {
		t.Fatal("no job")
	}
	if _, ok := r.take(); ok {
		t.Fatal("a second recap job was queued")
	}
	// the session holds one lease at a time, so take alone cannot see a second job waiting behind the first
	r.ch.qmu.Lock()
	waiting := 0
	if q := r.ch.qs[sidS]; q != nil {
		waiting = len(q.waiting)
	}
	r.ch.qmu.Unlock()
	if waiting != 0 {
		t.Fatalf("%d recap job(s) waiting behind the first", waiting)
	}
}

// 5: the mod's answer lands in the file, resets the count and is announced once; the same answer again changes nothing.
func TestRecap_AnswerLandsInTheFile(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	if got := r.rc.request(sidS, "manual"); got != "queued" {
		t.Fatal(got)
	}
	j, _ := r.take()
	var in struct {
		Turns []struct {
			N       int
			At      string
			Title   string
			Summary string
		}
	}
	if !strings.Contains(j.Complete.System[0].Text, "recap 撰寫員") || json.Unmarshal([]byte(j.Complete.Prompt), &in) != nil || len(in.Turns) != 3 ||
		in.Turns[0].N != 1 || in.Turns[2].Title != "題3" || in.Turns[2].Summary != "內容3。" || in.Turns[0].At != localHM(1_800_000_060_000) {
		t.Fatalf("job = %+v", j.Complete)
	}
	r.answer(j, goodRecap)
	rows, err := r.st.ListRecaps(sidS, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("recaps %v %v", rows, err)
	}
	row := rows[0]
	if row.FromNo != 1 || row.ToNo != 3 || row.Trigger != "manual" || row.Headline != "h" || row.JobID != j.ID || row.UsageIn != 7 || row.ConvKey != sidS {
		t.Fatalf("row = %+v", row)
	}
	want := "\n\n<recap " + localHM(row.At) + " | turns 1–3>\nheadline: h\ndone:\n- a\n- b\nnow: n\ndecided:\n- d\nwaiting:\n- w\nnext: x\n"
	if !strings.HasSuffix(r.file(), want) {
		t.Fatalf("file = %q", r.file())
	}
	if n, _ := r.st.SinceRecap(sidS); n != 0 {
		t.Fatalf("since_recap = %d", n)
	}
	if got, wantB := r.broadcasts(), fmt.Sprintf(`dialogue.recap {"session_id":%q,"recap_id":%d}`, sidS, row.ID); len(got) != 1 || got[0] != wantB {
		t.Fatalf("broadcasts = %v, want %s", got, wantB)
	}
	// the same answer applied again (a retry after a store error) stores nothing more
	k := &recapKind{r: r.rc}
	if err := k.apply(job{id: j.ID, sid: sidS, kind: kindRecap, payload: recapJob{}}, goodRecap, usage{}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := r.st.ListRecaps(sidS, 10); len(rows) != 1 {
		t.Fatalf("a second row: %v", rows)
	}
	if strings.Count(r.file(), "<recap ") != 1 || len(r.broadcasts()) != 1 {
		t.Fatalf("file = %q", r.file())
	}
}

// 6: a new segment starts with the newest recap that ends at or before its first turn.
func TestRecap_CarriedPicksByToNo(t *testing.T) {
	r := newRecapRig(t)
	r.seed(9)
	for i, to := range []int{5, 9} {
		if _, _, err := r.st.InsertRecap(RecapRow{JobID: fmt.Sprintf("j%d", i), SessionID: sidS, FromNo: 1, ToNo: to, Trigger: "manual", Headline: fmt.Sprintf("上到%d", to), BodyJSON: `{"headline":"上到` + fmt.Sprint(to) + `","done":[],"now":"","decided":[],"waiting":[],"next":""}`}); err != nil {
			t.Fatal(err)
		}
	}
	if got := carriedRecap(r.st, sidS, 7); !strings.HasPrefix(got, "<carried recap ") || !strings.Contains(got, "turns 1–5>\nheadline: 上到5\n") {
		t.Fatalf("carried(7) = %q", got)
	}
	if got := carriedRecap(r.st, sidS, 9); !strings.Contains(got, "headline: 上到9\n") {
		t.Fatalf("carried(9) = %q", got)
	}
	if got := carriedRecap(r.st, sidS, 4); got != "" {
		t.Fatalf("carried(4) = %q", got)
	}
}

// 7: no answer, or a bad one twice: nothing is written, the count stays, and the next trigger can queue again.
func TestRecap_FailureKeepsTheCountAndFreesTheSlot(t *testing.T) {
	r := newRecapRig(t)
	r.seed(3)
	r.st.db.Exec(`UPDATE dlg_sessions SET since_recap = 30 WHERE session_id = ?`, sidS)
	r.rc.request(sidS, "every30")
	j, _ := r.take()
	r.answer(j, "garbage")
	j, ok := r.take() // attempt 2
	if !ok {
		t.Fatal("no second attempt")
	}
	r.answer(j, "still garbage")
	if rows, _ := r.st.ListRecaps(sidS, 10); len(rows) != 0 {
		t.Fatalf("rows = %v", rows)
	}
	if n, _ := r.st.SinceRecap(sidS); n != 30 {
		t.Fatalf("since_recap = %d", n)
	}
	if got := r.rc.request(sidS, "every30"); got != "queued" {
		t.Fatalf("after a failure: %s", got)
	}
	j, ok = r.take()
	if !ok {
		t.Fatal("not queued again")
	}
	if _, err := r.ch.Result("streamA", j.ID, result{Reason: "refused"}); err != nil {
		t.Fatal(err)
	}
	if got := r.rc.request(sidS, "every30"); got != "queued" {
		t.Fatalf("after a refusal: %s", got)
	}
}

// 8: a session that cannot take jobs gets "unavailable", and the slot is not kept.
func TestRecap_UnavailableFreesTheSlot(t *testing.T) {
	r := newRecapRig(t)
	r.seed(2)
	r.setCapable(false)
	if got := r.rc.request(sidS, "manual"); got != "unavailable" {
		t.Fatal(got)
	}
	r.setCapable(true)
	if got := r.rc.request(sidS, "manual"); got != "queued" {
		t.Fatalf("after becoming capable: %s", got)
	}
	if _, ok := r.take(); !ok { // "queued" alone would also be the answer of a slot never freed
		t.Fatal("no recap job was queued after the session became capable")
	}
}

// The input is the newest 30 done summaries, oldest first.
func TestRecap_InputIsTheLastThirty(t *testing.T) {
	r := newRecapRig(t)
	r.seed(35)
	r.rc.request(sidS, "manual")
	j, _ := r.take()
	var in struct{ Turns []struct{ N int } }
	if err := json.Unmarshal([]byte(j.Complete.Prompt), &in); err != nil || len(in.Turns) != 30 || in.Turns[0].N != 6 || in.Turns[29].N != 35 {
		t.Fatalf("turns = %+v err=%v", in, err)
	}
	r.answer(j, goodRecap)
	if rows, _ := r.st.ListRecaps(sidS, 1); len(rows) != 1 || rows[0].FromNo != 6 || rows[0].ToNo != 35 {
		t.Fatalf("rows = %+v", rows)
	}
}

// The store side: one recap per job, newest first, picked by to_no.
func TestStoreRecap_InsertOnceAndRead(t *testing.T) {
	r := newRecapRig(t)
	r.seed(2)
	r.st.db.Exec(`UPDATE dlg_sessions SET since_recap = 5 WHERE session_id = ?`, sidS)
	id, applied, err := r.st.InsertRecap(RecapRow{JobID: "j1", SessionID: sidS, FromNo: 1, ToNo: 2, Trigger: "open", Headline: "h", BodyJSON: "{}"})
	if err != nil || !applied || id == 0 {
		t.Fatalf("insert %d %v", id, err)
	}
	if n, _ := r.st.SinceRecap(sidS); n != 0 {
		t.Fatalf("since_recap = %d", n)
	}
	r.st.db.Exec(`UPDATE dlg_sessions SET since_recap = 4 WHERE session_id = ?`, sidS)
	if _, ap2, err := r.st.InsertRecap(RecapRow{JobID: "j1", SessionID: sidS, FromNo: 1, ToNo: 2, Trigger: "open", Headline: "other", BodyJSON: "{}"}); err != nil || ap2 {
		t.Fatalf("second insert of the job: applied=%v %v", ap2, err)
	}
	if n, _ := r.st.SinceRecap(sidS); n != 4 {
		t.Fatalf("the ignored insert reset the count: %d", n)
	}
	got, ok, _ := r.st.RecapByID(id)
	if !ok || got.Headline != "h" {
		t.Fatalf("by id: %+v %v", got, ok)
	}
	if l, ok, _ := r.st.LatestRecap(sidS); !ok || l.ID != id {
		t.Fatalf("latest: %+v %v", l, ok)
	}
	if _, ok, _ := r.st.LatestRecap(sidS1); ok {
		t.Fatal("a recap of another session")
	}
	if rows, _ := r.st.RecapsEndingAt(sidS, 2); len(rows) != 1 {
		t.Fatalf("ending at 2: %v", rows)
	}
	if rows, _ := r.st.RecapsEndingAt(sidS, 1); len(rows) != 0 {
		t.Fatalf("ending at 1: %v", rows)
	}
}

// 9, through the real module: a recap asked for and answered by a fake mod lands in the file, and a rebuild puts it back
// after the turn it ends at, behind that turn's summary.
func TestFlow_RecapThroughTheModuleAndRebuild(t *testing.T) {
	f := newFlow(t, true, true)
	f.setTurns(ended("t1"))
	f.stop(time.Now().UnixMilli() + 1000)
	f.answer(f.job(), haikuJSON)
	waitFor(t, "the summary block", func() bool { return strings.Contains(f.seg(), "<turn-summary 1") })
	if got := f.m.requestRecap(sidS, "manual"); got != "queued" {
		t.Fatalf("request: %s", got)
	}
	j := f.job()
	if !strings.Contains(j.Complete.System[0].Text, "recap 撰寫員") {
		t.Fatalf("job = %+v", j.Complete)
	}
	f.answer(j, goodRecap)
	waitFor(t, "the recap block", func() bool { return strings.Contains(f.seg(), "<recap ") })
	if got := f.m.requestRecap(sidS, "manual"); got != "up_to_date" {
		t.Fatalf("again: %s", got)
	}
	if err := os.Remove(filepath.Join(f.dir, "dialogue", sidS, "001.md")); err != nil {
		t.Fatal(err)
	}
	f.setTurns(ended("t1"), ended("t2"))
	f.stop(time.Now().UnixMilli() + 2000)
	got := f.seg()
	i, j2, k, l := strings.Index(got, "agent: answer t1"), strings.Index(got, "<turn-summary 1 · "), strings.Index(got, "<recap "), strings.Index(got, "user: question t2")
	if !(i >= 0 && i < j2 && j2 < k && k < l) || strings.Count(got, "<recap ") != 1 {
		t.Fatalf("file = %q", got)
	}
}
