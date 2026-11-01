package dialogue

import (
	"os"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func markerTurns() []convmodel.Turn {
	return []convmodel.Turn{
		done("t1", userItem("u1", "one"), agentItem("a1", "r1")),
		done("t2", userItem("u2", "two"), agentItem("a2", "r2")),
	}
}

// The marker goes after what is there, and the Write that follows does not take it for crash residue.
func TestAppendMarker_SurvivesTheNextWrite(t *testing.T) {
	e := newEnv(t, Hooks{})
	turns := markerTurns()
	e.write(sidS, fromRes(turns[:1], ""), "", false)
	if err := e.w.AppendMarker(sidS, 1, "<turn-summary 1 · 09:05 | 俳句>\n寫了兩首俳句。"); err != nil {
		t.Fatal(err)
	}
	st, _, _ := e.store.Session(sidS)
	if cursor := st.CursorTurn; cursor != "t1" {
		t.Fatalf("the cursor moved to %q", cursor)
	}
	e.write(sidS, fromRes(turns, e.cursorOf(sidS)), "", false)
	got := e.read(sidS, 1)
	i, j, k := strings.Index(got, "agent: r1"), strings.Index(got, "\n\n<turn-summary 1 · 09:05 | 俳句>\n寫了兩首俳句。\n"), strings.Index(got, "user: two")
	if !(i >= 0 && i < j && j < k) {
		t.Fatalf("file = %q", got)
	}
	st, _, _ = e.store.Session(sidS)
	if fi, err := os.Stat(e.seg(sidS, 1)); err != nil || fi.Size() != st.SegBytes {
		t.Fatalf("size %v %v, recorded %d", fi, err, st.SegBytes)
	}
}

// Bytes written but never committed are cut off before the marker is added.
func TestAppendMarker_CutsCrashResidueFirst(t *testing.T) {
	e := newEnv(t, Hooks{})
	e.write(sidS, fromRes(markerTurns()[:1], ""), "", false)
	f, err := os.OpenFile(e.seg(sidS, 1), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\nHALF A BLOCK")
	f.Close()
	if err := e.w.AppendMarker(sidS, 1, "<turn-summary 1 · 09:05 | x>\nbody"); err != nil {
		t.Fatal(err)
	}
	got := e.read(sidS, 1)
	if strings.Contains(got, "HALF A BLOCK") || !strings.HasSuffix(got, "\n\n<turn-summary 1 · 09:05 | x>\nbody\n") {
		t.Fatalf("file = %q", got)
	}
	st, _, _ := e.store.Session(sidS)
	if fi, _ := os.Stat(e.seg(sidS, 1)); fi.Size() != st.SegBytes {
		t.Fatalf("recorded %d, file %d", st.SegBytes, fi.Size())
	}
}

// During a rebuild that stopped at its target, a summary of a turn already rewritten goes to the end of the file; one of a
// turn not yet reached is left, and Placed puts it in when that turn is written (exactly once).
func TestAppendMarker_DuringRebuild(t *testing.T) {
	placed := map[string][]string{}
	e := newEnv(t, Hooks{Placed: func(sid, turnID string) []string { return placed[turnID] }})
	turns := []convmodel.Turn{done("t1", userItem("u1", "one")), done("t2", userItem("u2", "two")), done("t3", userItem("u3", "three"))}
	e.write(sidS, all(turns), "", false)
	cut := []convmodel.Turn{done("t1", userItem("u1", "one")), turn("t2", convmodel.OutcomeRunning, userItem("u2", "two")), turns[2]}
	if r := e.write(sidS, all(cut), "other", false); !r.Rebuilt {
		t.Fatalf("result = %+v", r)
	}
	if st, _, _ := e.store.Session(sidS); !st.Rebuilding || st.CursorTurn != "t1" {
		t.Fatalf("state after the cut = %+v", st)
	}
	if err := e.w.AppendMarker(sidS, 1, "<S1>"); err != nil {
		t.Fatal(err)
	}
	if err := e.w.AppendMarker(sidS, 2, "<S2>"); err != nil {
		t.Fatal(err)
	}
	got := e.read(sidS, 1)
	if strings.Count(got, "<S1>") != 1 || strings.Contains(got, "<S2>") {
		t.Fatalf("after the markers: %q", got)
	}
	st, _, _ := e.store.Session(sidS)
	if fi, _ := os.Stat(e.seg(sidS, 1)); fi.Size() != st.SegBytes {
		t.Fatalf("size %d, recorded %d", fi.Size(), st.SegBytes)
	}
	placed["t2"] = []string{"<S2>"} // its summary is done by the time t2 is written
	e.write(sidS, fromRes(turns, e.cursorOf(sidS)), "", false)
	got = e.read(sidS, 1)
	if strings.Count(got, "<S1>") != 1 || strings.Count(got, "<S2>") != 1 {
		t.Fatalf("after the rebuild: %q", got)
	}
}

// A session with no file yet gets nothing, and no state is made up for it.
func TestAppendMarker_NoSegmentYetIsSkipped(t *testing.T) {
	e := newEnv(t, Hooks{})
	if err := e.w.AppendMarker(sidS, 1, "x\ny"); err != nil {
		t.Fatal(err)
	}
	if _, has, _ := e.store.Session(sidS); has {
		t.Fatal("a session state was created")
	}
	if _, err := os.Stat(e.seg(sidS, 1)); err == nil {
		t.Fatal("a file was created")
	}
	if len(e.logs) == 0 {
		t.Error("the skip was not logged")
	}
	if err := e.w.AppendMarker("not-a-uuid", 1, "x"); err == nil {
		t.Error("an invalid session id was accepted")
	}
}
