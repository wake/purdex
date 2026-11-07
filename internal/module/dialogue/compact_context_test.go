package dialogue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
)

const injectedLine = "<injected by Purdex after compaction: not the user's words>"

// fakeTurns is the transcript reader: it hands out the turns it holds, from the cursor on when the cursor is among them.
type fakeTurns struct {
	turns    []convmodel.Turn
	whole    bool // answer with everything and Found=false, whatever the cursor
	err      error
	cursors  []string
	provider string
}

func (f *fakeTurns) TurnsFrom(_ context.Context, provider, _ string, from string) (convturns.TurnsFromResult, error) {
	f.provider = provider
	f.cursors = append(f.cursors, from)
	if f.err != nil {
		return convturns.TurnsFromResult{}, f.err
	}
	if f.whole {
		return convturns.TurnsFromResult{Turns: f.turns}, nil
	}
	return fromRes(f.turns, from), nil
}

func transcript(n int) []convmodel.Turn {
	var out []convmodel.Turn
	for i := 1; i <= n; i++ {
		out = append(out, basicTurn(fmt.Sprintf("t%d", i)))
	}
	return out
}

// ccRig is a recap rig whose module runs just enough for compactContext.
type ccRig struct {
	*recapRig
	m  *Module
	tr *fakeTurns
}

func newCCRig(t *testing.T, transcriptTurns int) *ccRig {
	t.Helper()
	r := newRecapRig(t)
	tr := &fakeTurns{turns: transcript(transcriptTurns)}
	return &ccRig{recapRig: r, tr: tr, m: &Module{store: r.st, rc: r.rc, turns: tr}}
}

func (c *ccRig) insertRecap(from, to int) {
	c.t.Helper()
	if _, _, err := c.st.InsertRecap(RecapRow{JobID: fmt.Sprintf("old-%d-%d", from, to), SessionID: sidS, ConvKey: sidS, FromNo: from, ToNo: to,
		Trigger: "manual", Headline: "h", BodyJSON: goodRecap}); err != nil {
		c.t.Fatal(err)
	}
}

func (c *ccRig) run(wait time.Duration) (string, bool) {
	return c.m.compactContext(context.Background(), sidS, wait)
}

func mustHave(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Fatalf("missing %q in:\n%s", w, text)
		}
	}
}

func mustLack(t *testing.T, text string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(text, b) {
			t.Fatalf("unexpected %q in:\n%s", b, text)
		}
	}
}

func inOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(text[at:], p)
		if i < 0 {
			t.Fatalf("%q not found after offset %d in:\n%s", p, at, text)
		}
		at += i + len(p)
	}
}

func (c *ccRig) dialogueLine() string {
	return "<dialogue: " + filepath.Join(c.e.root, sidS) + "/>"
}

func TestCompactContext_NewRecapThenTheTurnsAfterIt(t *testing.T) {
	c := newCCRig(t, 5)
	c.seed(3)
	type res struct {
		text string
		ok   bool
	}
	out := make(chan res, 1)
	go func() { s, ok := c.run(5 * time.Second); out <- res{s, ok} }()
	var j wireJob
	waitFor(t, "the recap job", func() bool { var ok bool; j, ok = c.take(); return ok })
	c.answer(j, `{"headline": "NEWHEAD", "done": ["a"], "now": "n", "decided": [], "waiting": [], "next": "x"}`)
	got := <-out
	if !got.ok {
		t.Fatal("no text")
	}
	inOrder(t, got.text, injectedLine, "<recap ", "| turns 1–3>", "headline: NEWHEAD", "<after recap | from turn 4>",
		"question t4", "answer t4", "question t5", "answer t5", c.dialogueLine(), "編號最大的檔是最新一段，每段開頭有前一段的路徑。要查原話或舊決定時讀這裡。")
	mustLack(t, got.text, "question t3", "answer t3", "<recent turns>")
	if c.tr.provider != "claude" {
		t.Fatalf("provider %q", c.tr.provider)
	}
}

func TestCompactContext_UpToDateQueuesNothing(t *testing.T) {
	c := newCCRig(t, 3)
	c.seed(3)
	c.insertRecap(1, 3)
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, injectedLine, "| turns 1–3>", c.dialogueLine())
	mustLack(t, text, "<after recap", "<recent turns>", "question t3")
	if _, queued := c.take(); queued {
		t.Fatal("a job was queued")
	}
}

func TestCompactContext_NotReadyInTimeUsesTheLastOne(t *testing.T) {
	c := newCCRig(t, 5)
	c.insertRecap(1, 2)
	c.seed(3) // a third summary: a new recap is wanted, and nobody answers it
	text, ok := c.run(30 * time.Millisecond)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "| turns 1–2>", "<after recap | from turn 3>", "question t3", "question t5")
	mustLack(t, text, "question t2")
}

func TestCompactContext_UnavailableUsesTheLastOne(t *testing.T) {
	c := newCCRig(t, 4)
	c.insertRecap(1, 2)
	c.seed(3)
	c.setCapable(false)
	start := time.Now()
	text, ok := c.run(10 * time.Second)
	if time.Since(start) > 3*time.Second {
		t.Fatal("it waited")
	}
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "| turns 1–2>", "<after recap | from turn 3>", "question t4")
}

func TestCompactContext_NoRecapAtAll(t *testing.T) {
	c := newCCRig(t, 2)
	c.seed(2)
	c.setCapable(false)
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, injectedLine, "<recent turns>", "question t1", "answer t2", c.dialogueLine())
	mustLack(t, text, "<recap ", "<after recap")
}

func TestCompactContext_FirstTimeThisSessionIsSeen(t *testing.T) {
	c := newCCRig(t, 0)
	var turns []convmodel.Turn
	for i := 1; i <= 400; i++ {
		turns = append(turns, done(fmt.Sprintf("t%d", i), userItem("u", fmt.Sprintf("question %d %s", i, strings.Repeat("填", 100))), agentItem("a", "ok")))
	}
	c.tr.turns = turns
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "<recent turns>", "<earlier turns omitted: see the dialogue file>", "question 400")
	mustLack(t, text, "question 1 ")
	if n := utf8.RuneCountInString(text); n > compactTailMax+1000 {
		t.Fatalf("text is %d runes", n)
	}
}

func TestCompactContext_RecapTurnGoneFromTheTranscript(t *testing.T) {
	c := newCCRig(t, 5)
	c.seed(3)
	c.insertRecap(1, 3)
	c.tr.whole = true
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "<after recap | from turn 4>", "question t4", "question t5")
	mustLack(t, text, "question t1", "question t2", "question t3")
}

func TestCompactContext_RecapTurnNotInTheStore(t *testing.T) {
	c := newCCRig(t, 5)
	c.seed(3)
	c.insertRecap(1, 9) // turn 9 has no number in the store, so the whole transcript is read
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	if len(c.tr.cursors) != 1 || c.tr.cursors[0] != "" {
		t.Fatalf("cursors %v", c.tr.cursors)
	}
	mustHave(t, text, "| turns 1–9>", "<after recap | from turn 10>", "question t4", "question t5")
	mustLack(t, text, "question t1", "question t3")
}

func TestCompactContext_NothingToSay(t *testing.T) {
	c := newCCRig(t, 0)
	if text, ok := c.run(time.Second); ok || text != "" {
		t.Fatalf("got %q %v", text, ok)
	}
}

func TestCompactContext_RunningTurnIsIncluded(t *testing.T) {
	c := newCCRig(t, 2)
	c.seed(2)
	c.insertRecap(1, 2)
	c.tr.turns = append(c.tr.turns, turn("t3", convmodel.OutcomeRunning, userItem("u3", "still going"), stepItem("s3", "Bash", "make build", convmodel.StepRunning)))
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "still going", "<tool Bash | make build>")
}

func TestCompactContext_SkipsTheBoundary(t *testing.T) {
	c := newCCRig(t, 1)
	c.seed(1)
	c.insertRecap(1, 1)
	c.tr.turns = append(c.tr.turns, done("t2", compactItem("c", "auto"), userItem("u2", "after the boundary")))
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "after the boundary")
	mustLack(t, text, "<compacted")
}

func TestCompactContext_ReaderFailsStillGivesTheRecap(t *testing.T) {
	c := newCCRig(t, 3)
	c.seed(3)
	c.insertRecap(1, 3)
	c.tr.err = errors.New("transcript unreadable")
	text, ok := c.run(time.Second)
	if !ok {
		t.Fatal("no text")
	}
	mustHave(t, text, "| turns 1–3>", c.dialogueLine())
	mustLack(t, text, "<after recap")
}

func TestCompactContext_RecapIsNotCountedInTheCap(t *testing.T) {
	c := newCCRig(t, 0)
	c.seed(1)
	long := strings.Repeat("字", compactTailMax*2)
	c.tr.turns = []convmodel.Turn{done("t2", userItem("u2", long))}
	if _, _, err := c.st.InsertRecap(RecapRow{JobID: "r", SessionID: sidS, ConvKey: sidS, FromNo: 1, ToNo: 1, Trigger: "manual", Headline: "h",
		BodyJSON: `{"headline": "h", "done": ["` + strings.Repeat("總", 500) + `"], "now": "", "decided": [], "waiting": [], "next": ""}`}); err != nil {
		t.Fatal(err)
	}
	text, _ := c.run(time.Second)
	mustHave(t, text, strings.Repeat("總", 500), "<earlier turns omitted")
}

func TestCompactContext_ModuleNotRunning(t *testing.T) {
	c := newCCRig(t, 2)
	c.seed(2)
	for name, m := range map[string]*Module{
		"no recapper": {store: c.st, turns: c.tr},
		"no store":    {rc: c.rc, turns: c.tr},
		"no reader":   {store: c.st, rc: c.rc},
	} {
		if text, ok := m.compactContext(context.Background(), sidS, time.Second); ok || text != "" {
			t.Fatalf("%s: got %q %v", name, text, ok)
		}
	}
}

func TestCapTail(t *testing.T) {
	const mark = "<earlier turns omitted: see the dialogue file>"
	if got := capTail("a\nb\n", 100); got != "a\nb\n" {
		t.Fatalf("short text changed: %q", got)
	}
	got := capTail("aaaa\nbbbb\ncccc\n", 8)
	if got != mark+"\ncccc\n" {
		t.Fatalf("got %q", got)
	}
	got = capTail("aaaa\nbbbb\ncccc\n", 12) // the cut falls inside "aaaa": it starts at the next line
	if got != mark+"\nbbbb\ncccc\n" {
		t.Fatalf("mid-line cut: %q", got)
	}
	got = capTail("aaaa\nbbbb\ncccc\n", 10) // the cut falls exactly at a line start
	if got != mark+"\nbbbb\ncccc\n" {
		t.Fatalf("line-start cut: %q", got)
	}
	long := strings.Repeat("中", 19999) + "尾"
	got = capTail(long, 8000)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "中尾") || utf8.RuneCountInString(got) > 8000+utf8.RuneCountInString(mark)+1 {
		t.Fatalf("hard cut: valid=%v runes=%d", utf8.ValidString(got), utf8.RuneCountInString(got))
	}
}
