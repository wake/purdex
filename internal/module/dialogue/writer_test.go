package dialogue

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/convmodel/ccnorm/scrub"
	"github.com/wake/purdex/internal/convturns"
)

const testSource = "/src/transcript.jsonl"

func fixtureTurns(t *testing.T, name string) []convmodel.Turn {
	t.Helper()
	input, err := os.ReadFile(filepath.Join(repoRoot, "testdata/conversation/v1/cc-transcript", name, "input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n := ccnorm.New(ccnorm.Options{SessionID: scrub.FixtureSessionID})
	var off int64
	for _, l := range bytes.Split(input, []byte("\n")) {
		if len(l) > 0 {
			if _, err := n.Feed(off, l); err != nil {
				t.Fatal(err)
			}
		}
		off += int64(len(l)) + 1
	}
	n.SetLive(false)
	return n.Conversation().Turns
}

// fromRes simulates TurnsFrom: the cursor turn and everything after it, or everything with Found=false.
func fromRes(turns []convmodel.Turn, cursor string) convturns.TurnsFromResult {
	for i, tr := range turns {
		if cursor != "" && tr.ID == cursor {
			return convturns.TurnsFromResult{Turns: turns[i:], Found: true, Path: testSource}
		}
	}
	return convturns.TurnsFromResult{Turns: turns, Path: testSource}
}

func all(turns []convmodel.Turn) convturns.TurnsFromResult { return fromRes(turns, "") }

type env struct {
	t     *testing.T
	root  string
	store *Store
	w     *Writer
	prevs map[string]string
}

func newEnv(t *testing.T, hooks Hooks) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "dialogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, root: filepath.Join(dir, "dialogue"), store: st, prevs: map[string]string{}}
	e.w = NewWriter(e.root, st, func(sid string) (string, bool) { p, ok := e.prevs[sid]; return p, ok }, hooks)
	return e
}

func (e *env) seg(sid string, n int) string { return filepath.Join(e.root, sid, segName(n)) }

func (e *env) read(sid string, n int) string {
	e.t.Helper()
	b, err := os.ReadFile(e.seg(sid, n))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) write(sid string, res convturns.TurnsFromResult, target string, force bool) WriteResult {
	e.t.Helper()
	r, err := e.w.Write(sid, res, target, force)
	if err != nil {
		e.t.Fatalf("Write: %v", err)
	}
	return r
}

// cursorOf is the cursor turn the store holds, as intake would pass to TurnsFrom.
func (e *env) cursorOf(sid string) string {
	st, _, err := e.store.Session(sid)
	if err != nil {
		e.t.Fatal(err)
	}
	return st.CursorTurn
}

func userItem(id, text string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemUser, User: &convmodel.UserMessage{ID: id, Text: text, Source: convmodel.SourceUser}}
}

func agentItem(id, text string) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemAgentText, AgentText: &convmodel.AgentText{ID: id, Markdown: text}}
}

func stepItem(id, tool, summary string, st convmodel.StepStatus) convmodel.Item {
	return convmodel.Item{Type: convmodel.ItemStep, Step: &convmodel.Step{ID: id, Tool: tool, Summary: summary, Status: st}}
}

func compactItem(id, trigger string) convmodel.Item {
	d, _ := json.Marshal(map[string]string{"trigger": trigger})
	return convmodel.Item{Type: convmodel.ItemSystem, System: &convmodel.System{ID: id, Kind: convmodel.SystemCompacted, Detail: d}}
}

func turn(id string, outcome convmodel.Outcome, items ...convmodel.Item) convmodel.Turn {
	end := int64(100)
	return convmodel.Turn{ID: id, StartedAt: 50, EndedAt: &end, Outcome: outcome, Items: items}
}

func done(id string, items ...convmodel.Item) convmodel.Turn {
	return turn(id, convmodel.OutcomeDone, items...)
}

// 1
func TestWriterFirstWriteCompactFixture(t *testing.T) {
	e := newEnv(t, Hooks{})
	turns := fixtureTurns(t, "compact")
	r := e.write("s1", all(turns), "", false)
	if !r.First || r.Rebuilt || len(r.Turns) != 6 {
		t.Fatalf("result = %+v", r)
	}
	golden, err := os.ReadFile(filepath.Join(repoRoot, "testdata/dialogue/compact.md"))
	if err != nil {
		t.Fatal(err)
	}
	before, after, ok := strings.Cut(string(golden), "\n<compacted | manual>\n\n")
	if !ok {
		t.Fatal("golden has no compaction boundary")
	}
	p1, p2 := e.seg("s1", 1), e.seg("s1", 2)
	want1 := "<prev: none>\n<source: " + testSource + ">\n\n" + before + "\n<compacted | manual>\n<next: " + p2 + ">\n"
	want2 := "<prev: " + p1 + ">\n<source: " + testSource + ">\n\n" + after
	if got := e.read("s1", 1); got != want1 {
		t.Errorf("001 = %q\nwant %q", got, want1)
	}
	if got := e.read("s1", 2); got != want2 {
		t.Errorf("002 = %q\nwant %q", got, want2)
	}
	nos, err := e.store.TurnNos("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nos) != 6 {
		t.Fatalf("nos = %v", nos)
	}
	for i, tr := range turns {
		if nos[tr.ID] != i+1 {
			t.Errorf("turn %s no = %d, want %d", tr.ID, nos[tr.ID], i+1)
		}
	}
	for i, wt := range r.Turns {
		if wt.ID != turns[i].ID || wt.No != i+1 {
			t.Errorf("written turn %d = %+v", i, wt)
		}
	}
}

// 2
func TestWriterRepeatedCallIsIdempotent(t *testing.T) {
	e := newEnv(t, Hooks{})
	turns := []convmodel.Turn{done("t1", userItem("u1", "hi"), agentItem("a1", "hello")), done("t2", userItem("u2", "more"), agentItem("a2", "ok"))}
	e.write("s", all(turns), "", false)
	before := e.read("s", 1)
	st1, _, _ := e.store.Session("s")
	r := e.write("s", fromRes(turns, e.cursorOf("s")), "", false)
	if r.First || r.Rebuilt || len(r.Turns) != 0 {
		t.Errorf("result = %+v", r)
	}
	if got := e.read("s", 1); got != before {
		t.Errorf("file changed: %q", got)
	}
	st2, _, _ := e.store.Session("s")
	if st1 != st2 {
		t.Errorf("state changed: %+v -> %+v", st1, st2)
	}
}

// 3
func TestWriterCursorTurnGrowsWithCompaction(t *testing.T) {
	e := newEnv(t, Hooks{})
	t1 := done("t1", userItem("u1", "hi"), agentItem("a1", "hello"))
	e.write("s", all([]convmodel.Turn{t1}), "", false)
	t1b := done("t1", userItem("u1", "hi"), agentItem("a1", "hello"), compactItem("c1", "auto"))
	t2 := done("t2", userItem("u2", "after"), agentItem("a2", "fine"))
	turns := []convmodel.Turn{t1b, t2}
	r := e.write("s", fromRes(turns, "t1"), "", false)
	if len(r.Turns) != 1 || r.Turns[0].ID != "t2" || r.Turns[0].No != 2 {
		t.Fatalf("result = %+v", r)
	}
	p2 := e.seg("s", 2)
	if got := e.read("s", 1); !strings.HasSuffix(got, "\n<compacted | auto>\n<next: "+p2+">\n") || strings.Contains(got, "after") {
		t.Errorf("001 = %q", got)
	}
	got2 := e.read("s", 2)
	if !strings.HasPrefix(got2, "<prev: "+e.seg("s", 1)+">\n<source: "+testSource+">\n\nuser: after\n") || !strings.Contains(got2, "agent: fine") {
		t.Errorf("002 = %q", got2)
	}
	st, _, _ := e.store.Session("s")
	if st.Segment != 2 || st.CursorTurn != "t2" || st.NextTurnNo != 3 {
		t.Errorf("state = %+v", st)
	}
}

// 4
func TestWriterCrashResidueIsTruncated(t *testing.T) {
	mk := func() []convmodel.Turn {
		return []convmodel.Turn{done("t1", userItem("u1", "hi"), agentItem("a1", "hello")), done("t2", userItem("u2", "two"), agentItem("a2", "ok"))}
	}
	control := newEnv(t, Hooks{})
	control.write("s", all(mk()[:1]), "", false)
	control.write("s", fromRes(mk(), control.cursorOf("s")), "", false)

	e := newEnv(t, Hooks{})
	e.write("s", all(mk()[:1]), "", false)
	f, err := os.OpenFile(e.seg("s", 1), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\nuser: half-written crash residue")
	f.Close()
	r := e.write("s", fromRes(mk(), e.cursorOf("s")), "", false)
	if r.Rebuilt {
		t.Error("a longer file is truncated, not rebuilt")
	}
	// the control file has different temp paths only in the header? source and prev are fixed, so equal.
	if got, want := e.read("s", 1), control.read("s", 1); got != want {
		t.Errorf("after crash repair %q, want %q", got, want)
	}
}

// 5
func TestWriterRebuild(t *testing.T) {
	setup := func(t *testing.T) (*env, []convmodel.Turn, map[string]int, string, string) {
		e := newEnv(t, Hooks{})
		turns := fixtureTurns(t, "compact")
		e.write("s", all(turns), "", false)
		nos, _ := e.store.TurnNos("s")
		return e, turns, nos, e.read("s", 1), e.read("s", 2)
	}
	check := func(t *testing.T, e *env, r WriteResult, nos map[string]int, c1, c2 string) {
		t.Helper()
		if !r.Rebuilt || r.First {
			t.Errorf("result = %+v", r)
		}
		if len(r.Turns) != 0 {
			t.Errorf("a rebuild reports no new turns: %+v", r.Turns)
		}
		if e.read("s", 1) != c1 || e.read("s", 2) != c2 {
			t.Error("rebuilt content differs")
		}
		got, _ := e.store.TurnNos("s")
		for k, v := range nos {
			if got[k] != v {
				t.Errorf("turn %s no %d -> %d", k, v, got[k])
			}
		}
		st, ok, _ := e.store.Session("s")
		if !ok || st.NextTurnNo != 7 {
			t.Errorf("state = %+v", st)
		}
	}
	t.Run("file deleted", func(t *testing.T) {
		e, turns, nos, c1, c2 := setup(t)
		os.Remove(e.seg("s", 2))
		cur := e.cursorOf("s")
		_, err := e.w.Write("s", fromRes(turns, cur), "", false)
		if !errors.Is(err, errNeedAll) {
			t.Fatalf("err = %v, want errNeedAll", err)
		}
		r := e.write("s", all(turns), "", false)
		check(t, e, r, nos, c1, c2)
	})
	t.Run("file shorter than recorded", func(t *testing.T) {
		e, turns, nos, c1, c2 := setup(t)
		if err := os.Truncate(e.seg("s", 2), 10); err != nil {
			t.Fatal(err)
		}
		if _, err := e.w.Write("s", fromRes(turns, e.cursorOf("s")), "", false); !errors.Is(err, errNeedAll) {
			t.Fatalf("err = %v, want errNeedAll", err)
		}
		r := e.write("s", all(turns), "", false)
		check(t, e, r, nos, c1, c2)
	})
	t.Run("not found", func(t *testing.T) {
		e, turns, nos, c1, c2 := setup(t)
		r := e.write("s", all(turns), "", false)
		check(t, e, r, nos, c1, c2)
	})
	t.Run("item id mismatch", func(t *testing.T) {
		e, turns, nos, c1, c2 := setup(t)
		cur := e.cursorOf("s")
		changed := append([]convmodel.Turn(nil), turns...)
		for i := range changed {
			if changed[i].ID == cur {
				items := append([]convmodel.Item(nil), changed[i].Items...)
				items[len(items)-1] = agentItem("different-id", "x")
				changed[i].Items = items
			}
		}
		if _, err := e.w.Write("s", fromRes(changed, cur), "", false); !errors.Is(err, errNeedAll) {
			t.Fatalf("err = %v, want errNeedAll", err)
		}
		r := e.write("s", all(turns), "", false)
		check(t, e, r, nos, c1, c2)
	})
	t.Run("fewer items", func(t *testing.T) {
		e, turns, _, _, _ := setup(t)
		cur := e.cursorOf("s")
		changed := append([]convmodel.Turn(nil), turns...)
		for i := range changed {
			if changed[i].ID == cur {
				changed[i].Items = changed[i].Items[:1]
			}
		}
		if _, err := e.w.Write("s", fromRes(changed, cur), "", false); !errors.Is(err, errNeedAll) {
			t.Fatalf("err = %v, want errNeedAll", err)
		}
	})
	t.Run("failed need-all leaves everything untouched", func(t *testing.T) {
		e, turns, _, c1, c2 := setup(t)
		os.Remove(e.seg("s", 2))
		e.w.Write("s", fromRes(turns, e.cursorOf("s")), "", false)
		if _, ok, _ := e.store.Session("s"); !ok {
			t.Error("state was reset by a refused call")
		}
		if e.read("s", 1) != c1 {
			t.Error("001 touched by a refused call")
		}
		_ = c2
	})
}

// 6
func TestWriterRunningTurn(t *testing.T) {
	t.Run("non-target stops", func(t *testing.T) {
		e := newEnv(t, Hooks{})
		turns := []convmodel.Turn{done("t1", userItem("u1", "one"), agentItem("a1", "r1")), turn("t2", convmodel.OutcomeRunning, userItem("u2", "two"))}
		r := e.write("s", all(turns), "other", false)
		if len(r.Turns) != 1 || r.WaitingTool {
			t.Fatalf("result = %+v", r)
		}
		got := e.read("s", 1)
		if !strings.Contains(got, "one") || strings.Contains(got, "two") {
			t.Errorf("file = %q", got)
		}
	})
	t.Run("target is written", func(t *testing.T) {
		e := newEnv(t, Hooks{})
		turns := []convmodel.Turn{done("t1", userItem("u1", "one")), turn("t2", convmodel.OutcomeRunning, userItem("u2", "two"), agentItem("a2", "partial"))}
		r := e.write("s", all(turns), "t2", false)
		if len(r.Turns) != 2 {
			t.Fatalf("result = %+v", r)
		}
		if got := e.read("s", 1); !strings.Contains(got, "agent: partial") {
			t.Errorf("file = %q", got)
		}
		// the turn grows: the second half is appended, nothing repeated
		turns[1].Items = append(turns[1].Items, agentItem("a3", "later"))
		turns[1].Outcome = convmodel.OutcomeDone
		e.write("s", fromRes(turns, e.cursorOf("s")), "", false)
		got := e.read("s", 1)
		if strings.Count(got, "agent: partial") != 1 || !strings.Contains(got, "agent: later") {
			t.Errorf("file = %q", got)
		}
	})
}

// 7
func TestWriterRunningTool(t *testing.T) {
	q := func(status convmodel.StepStatus, answers [][]string) convmodel.Item {
		it := stepItem("s1", "AskUserQuestion", "ignored", status)
		it.Step.Question = &convmodel.StepQuestion{Questions: []convmodel.QuestionItem{{Question: "recap into push?"}}, Answers: answers}
		return it
	}
	mk := func(it convmodel.Item) []convmodel.Turn {
		return []convmodel.Turn{done("t1", userItem("u1", "go"), it, agentItem("a1", "thanks"))}
	}
	t.Run("waits then writes the answer", func(t *testing.T) {
		e := newEnv(t, Hooks{})
		r := e.write("s", all(mk(q(convmodel.StepRunning, nil))), "", false)
		if !r.WaitingTool || len(r.Turns) != 0 {
			t.Fatalf("result = %+v", r)
		}
		if _, err := os.Stat(e.seg("s", 1)); !os.IsNotExist(err) {
			t.Error("nothing should be written while a tool runs")
		}
		if _, ok, _ := e.store.Session("s"); ok {
			t.Error("no state before anything is written")
		}
		r = e.write("s", all(mk(q(convmodel.StepDone, [][]string{{"yes"}}))), "", false)
		if r.WaitingTool || len(r.Turns) != 1 {
			t.Fatalf("result = %+v", r)
		}
		got := e.read("s", 1)
		if !strings.Contains(got, "<tool AskUserQuestion | recap into push?>\n<answer | yes>\n") {
			t.Errorf("file = %q", got)
		}
	})
	t.Run("force writes as it is", func(t *testing.T) {
		e := newEnv(t, Hooks{})
		r := e.write("s", all(mk(q(convmodel.StepRunning, nil))), "", true)
		if r.WaitingTool || len(r.Turns) != 1 {
			t.Fatalf("result = %+v", r)
		}
		if got := e.read("s", 1); !strings.Contains(got, "<tool AskUserQuestion | recap into push?>") || strings.Contains(got, "<answer") {
			t.Errorf("file = %q", got)
		}
	})
	t.Run("running tool in the target turn also waits", func(t *testing.T) {
		e := newEnv(t, Hooks{})
		turns := []convmodel.Turn{turn("t1", convmodel.OutcomeRunning, userItem("u1", "go"), stepItem("x", "Bash", "ls", convmodel.StepRunning))}
		r := e.write("s", all(turns), "t1", false)
		if !r.WaitingTool || len(r.Turns) != 0 {
			t.Fatalf("result = %+v", r)
		}
	})
}

// 8
func TestWriterHandoffLinksSessions(t *testing.T) {
	e := newEnv(t, Hooks{})
	a := []convmodel.Turn{done("a1", userItem("u1", "in A"), agentItem("x1", "A reply"))}
	e.write("A", all(a), "", false)
	if got := e.read("A", 1); !strings.HasPrefix(got, "<prev: none>\n") {
		t.Fatalf("A header = %q", got)
	}
	e.prevs["B"] = "A"
	b := []convmodel.Turn{done("b1", userItem("u2", "in B"), agentItem("x2", "B reply"))}
	r := e.write("B", all(b), "", false)
	if !r.First {
		t.Errorf("result = %+v", r)
	}
	aLast := e.seg("A", 1)
	if got := e.read("B", 1); !strings.HasPrefix(got, "<prev: "+aLast+">\n<source: "+testSource+">\n\n") {
		t.Errorf("B 001 = %q", got)
	}
	wantTail := "\n<next: " + e.seg("B", 1) + ">\n"
	gotA := e.read("A", 1)
	if !strings.HasSuffix(gotA, wantTail) {
		t.Fatalf("A tail = %q", gotA)
	}
	// A is written again: the next line is not mistaken for crash residue
	a = append(a, done("a2", userItem("u3", "A again"), agentItem("x3", "back")))
	e.write("A", fromRes(a, e.cursorOf("A")), "", false)
	gotA = e.read("A", 1)
	if strings.Count(gotA, "<next: "+e.seg("B", 1)+">") != 1 || !strings.Contains(gotA, "A again") {
		t.Errorf("A = %q", gotA)
	}
}

func TestWriterPermissions(t *testing.T) {
	e := newEnv(t, Hooks{})
	turns := fixtureTurns(t, "compact")
	e.write("s", all(turns), "", false)
	for _, p := range []string{e.root, filepath.Join(e.root, "s")} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v", p, fi.Mode().Perm())
		}
	}
	for n := 1; n <= 2; n++ {
		fi, err := os.Stat(e.seg("s", n))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("segment %d mode = %v", n, fi.Mode().Perm())
		}
	}
}

func TestWriterPlacedHookOnRebuild(t *testing.T) {
	e := newEnv(t, Hooks{Placed: func(sid, turnID string) []string {
		if turnID == "t1" {
			return []string{"<turn-summary 1 | x>\nbody"}
		}
		return nil
	}})
	turns := []convmodel.Turn{done("t1", userItem("u1", "one"), agentItem("a1", "r1")), done("t2", userItem("u2", "two"))}
	e.write("s", all(turns), "", false)
	if strings.Contains(e.read("s", 1), "turn-summary") {
		t.Error("Placed is only for rebuilds")
	}
	e.write("s", all(turns), "", false) // Found=false with a cursor: rebuild
	got := e.read("s", 1)
	i, j, k := strings.Index(got, "agent: r1"), strings.Index(got, "<turn-summary 1 | x>\nbody\n"), strings.Index(got, "user: two")
	if !(i >= 0 && i < j && j < k) {
		t.Errorf("file = %q", got)
	}
}

func TestWriterCarriedRecapOnNewSegment(t *testing.T) {
	e := newEnv(t, Hooks{Carried: func(sid string) string { return "<carried recap 05:44 | turns 1–1>\nheadline: x\n" }})
	turns := []convmodel.Turn{done("t1", userItem("u1", "a"), compactItem("c", "auto")), done("t2", userItem("u2", "b"))}
	e.write("s", all(turns), "", false)
	if got := e.read("s", 1); strings.Contains(got, "carried") {
		t.Errorf("001 = %q", got)
	}
	want := "<prev: " + e.seg("s", 1) + ">\n<source: " + testSource + ">\n<carried recap 05:44 | turns 1–1>\nheadline: x\n\nuser: b\n"
	if got := e.read("s", 2); got != want {
		t.Errorf("002 = %q\nwant %q", got, want)
	}
}
