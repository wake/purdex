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
	if err := e.w.AppendMarker(sidS, "<turn-summary 1 · 09:05 | 俳句>\n寫了兩首俳句。"); err != nil {
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
	if err := e.w.AppendMarker(sidS, "<turn-summary 1 · 09:05 | x>\nbody"); err != nil {
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

// A session with no file yet gets nothing, and no state is made up for it.
func TestAppendMarker_NoSegmentYetIsSkipped(t *testing.T) {
	e := newEnv(t, Hooks{})
	if err := e.w.AppendMarker(sidS, "x\ny"); err != nil {
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
	if err := e.w.AppendMarker("not-a-uuid", "x"); err == nil {
		t.Error("an invalid session id was accepted")
	}
}
