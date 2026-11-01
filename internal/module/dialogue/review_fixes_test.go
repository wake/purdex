package dialogue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
	agentmod "github.com/wake/purdex/internal/module/agent"
)

// --- session id validation ---

func TestValidSessionID(t *testing.T) {
	cases := map[string]bool{
		sidS:                                     true,
		"123e4567-e89b-12d3-a456-426614174000":   true,
		"../../outside":                          false,
		"a/b":                                    false,
		"123E4567-E89B-12D3-A456-426614174000":   false,
		"":                                       false,
		"123e4567-e89b-12d3-a456-42661417400":    false,
		"123e4567-e89b-12d3-a456-4266141740000":  false,
		"123e4567-e89b-12d3-a456-426614174000/x": false,
	}
	for s, want := range cases {
		if got := validSessionID(s); got != want {
			t.Errorf("validSessionID(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestWriterRejectsBadSessionIDs(t *testing.T) {
	for _, bad := range []string{"../../outside", "a/b", "123E4567-E89B-12D3-A456-426614174000", ""} {
		e := newEnv(t, Hooks{})
		outside := filepath.Join(e.root, "..", "..", "outside")
		_, err := e.w.Write(bad, all([]convmodel.Turn{basicTurn("t1")}), "", false)
		if err == nil {
			t.Errorf("Write(%q) succeeded", bad)
		}
		if _, serr := os.Stat(outside); !os.IsNotExist(serr) {
			t.Errorf("Write(%q) created %s", bad, outside)
		}
		if _, serr := os.Stat(e.root); !os.IsNotExist(serr) {
			t.Errorf("Write(%q) created the root", bad)
		}
		if _, ok, _ := e.store.Session(bad); ok {
			t.Errorf("Write(%q) wrote the store", bad)
		}
	}
	e := newEnv(t, Hooks{})
	if _, err := e.w.Write(sidS, all([]convmodel.Turn{basicTurn("t1")}), "", false); err != nil {
		t.Errorf("a valid id was refused: %v", err)
	}
}

func TestWriterBadPreviousIDIsNoPrevious(t *testing.T) {
	e := newEnv(t, Hooks{})
	e.prevs[sidB] = "../../outside"
	e.write(sidB, all([]convmodel.Turn{basicTurn("t1")}), "", false)
	if got := e.read(sidB, 1); !strings.HasPrefix(got, "<prev: none>\n") {
		t.Errorf("header = %q", got)
	}
}

func TestIntakeDropsBadSessionID(t *testing.T) {
	h := newHarness(t, steady(basicTurn("t1")))
	for _, bad := range []string{"../../outside", "a/b", "123E4567-E89B-12D3-A456-426614174000"} {
		h.in.OnTurnEnd(agentmod.TurnEndEvent{SessionID: bad, At: 5, Seq: 5})
	}
	h.waitSettled(3)
	if len(h.settledCalls()) != 3 {
		t.Errorf("settled = %v", h.settledCalls())
	}
	if h.in.workers() != 0 {
		t.Error("a worker was started for a bad id")
	}
	if !h.logged("invalid session id") {
		t.Error("the drop was not logged")
	}
	if _, err := os.Stat(filepath.Join(h.root, "..", "..", "outside")); !os.IsNotExist(err) {
		t.Error("something was created outside the root")
	}
	if _, err := os.Stat(h.root); !os.IsNotExist(err) {
		t.Error("the root was created")
	}
}

// --- each Stop writes up to its own turn ---

func TestWriterStopsAtTarget(t *testing.T) {
	e := newEnv(t, Hooks{})
	t1, t2 := basicTurn("t1"), basicTurn("t2")
	turns := []convmodel.Turn{t1, t2}
	r := e.write(sidS, all(turns), "t1", false)
	if len(r.Turns) != 1 || r.Turns[0].ID != "t1" {
		t.Fatalf("first write = %+v", r.Turns)
	}
	if got := e.read(sidS, 1); strings.Contains(got, "answer t2") {
		t.Errorf("t2 was written early: %q", got)
	}
	r = e.write(sidS, fromRes(turns, e.cursorOf(sidS)), "t2", false)
	if len(r.Turns) != 1 || r.Turns[0].ID != "t2" {
		t.Fatalf("second write = %+v", r.Turns)
	}
	if got := e.read(sidS, 1); !strings.Contains(got, "answer t2") {
		t.Errorf("t2 missing: %q", got)
	}
}

func TestWriterTargetIsCursorTurnOnlyCompletesIt(t *testing.T) {
	e := newEnv(t, Hooks{})
	t1, t2 := basicTurn("t1"), basicTurn("t2")
	e.write(sidS, all([]convmodel.Turn{t1}), "t1", false)
	t1b := done("t1", userItem("t1-u", "question t1"), agentItem("t1-a", "answer t1"), agentItem("t1-late", "late words"))
	turns := []convmodel.Turn{t1b, t2}
	r := e.write(sidS, fromRes(turns, "t1"), "t1", false)
	if len(r.Turns) != 0 {
		t.Errorf("turns = %+v", r.Turns)
	}
	got := e.read(sidS, 1)
	if !strings.Contains(got, "late words") || strings.Contains(got, "answer t2") {
		t.Errorf("file = %q", got)
	}
}

func TestWriterLateItemsOfCursorTurnBeforeLaterTarget(t *testing.T) {
	e := newEnv(t, Hooks{})
	e.write(sidS, all([]convmodel.Turn{basicTurn("t1")}), "t1", false)
	t1b := done("t1", userItem("t1-u", "question t1"), agentItem("t1-a", "answer t1"), agentItem("t1-late", "late words"))
	turns := []convmodel.Turn{t1b, basicTurn("t2"), basicTurn("t3")}
	r := e.write(sidS, fromRes(turns, "t1"), "t2", false)
	if len(r.Turns) != 1 || r.Turns[0].ID != "t2" {
		t.Errorf("turns = %+v", r.Turns)
	}
	got := e.read(sidS, 1)
	if !strings.Contains(got, "late words") || !strings.Contains(got, "answer t2") || strings.Contains(got, "answer t3") {
		t.Errorf("file = %q", got)
	}
}

func TestWriterEmptyTargetWritesEverything(t *testing.T) {
	e := newEnv(t, Hooks{})
	r := e.write(sidS, all([]convmodel.Turn{basicTurn("t1"), basicTurn("t2")}), "", false)
	if len(r.Turns) != 2 {
		t.Errorf("turns = %+v", r.Turns)
	}
}

func TestIntakeEachStopGetsItsOwnTurn(t *testing.T) {
	h := newHarness(t, steady(basicTurn("t1"), basicTurn("t2")))
	e1 := agentmod.TurnEndEvent{SessionID: sidS, Text: "answer t1", At: 1000, Seq: 11}
	e2 := agentmod.TurnEndEvent{SessionID: sidS, Text: "answer t2", At: 2000, Seq: 22}
	h.in.OnTurnEnd(e1)
	waitFor(t, "first written", func() bool { return len(h.writtenCalls()) == 1 })
	if got := h.read(sidS, 1); strings.Contains(got, "answer t2") {
		t.Errorf("E1 wrote T2: %q", got)
	}
	h.in.OnTurnEnd(e2)
	waitFor(t, "second written", func() bool { return len(h.writtenCalls()) == 2 })
	w := h.writtenCalls()
	if len(w[0].wr.Turns) != 1 || w[0].wr.Turns[0].ID != "t1" || w[0].ev.At != 1000 || w[0].ev.Seq != 11 {
		t.Errorf("E1 call = %+v", w[0])
	}
	if len(w[1].wr.Turns) != 1 || w[1].wr.Turns[0].ID != "t2" || w[1].ev.At != 2000 || w[1].ev.Seq != 22 {
		t.Errorf("E2 call = %+v", w[1])
	}
}
