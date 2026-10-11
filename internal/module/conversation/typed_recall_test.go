package conversation

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// K-1b: POST /api/sessions/{code}/recall — the real feed (a transcript with queue-operation rows) and a real promptq.Queue
// with a scripted mod that answers read / clear_if from a one-line "composer" the fake Up key fills.

type composer struct {
	mu    sync.Mutex
	text  string
	clear []string
}

func (c *composer) set(s string) { c.mu.Lock(); c.text = s; c.mu.Unlock() }

func (c *composer) now() string { c.mu.Lock(); defer c.mu.Unlock(); return c.text }

func (c *composer) clears() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.clear...)
}

func (c *composer) job(j promptq.Job) promptq.Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch j.Kind {
	case promptq.KindRead:
		return promptq.Outcome{Status: promptq.Accepted, Text: c.text}
	case promptq.KindClearIf:
		c.clear = append(c.clear, j.Text)
		if c.text == j.Text {
			c.text = ""
			return promptq.Outcome{Status: promptq.Accepted}
		}
		return promptq.Outcome{Status: promptq.Dropped, Reason: "not_ours"}
	}
	return promptq.Outcome{Status: promptq.Dropped}
}

type recallEnv struct {
	*tenv
	comp   *composer
	path   string
	popped string // what the fake Up puts in the input
}

func newRecallEnv(t *testing.T, rows ...string) *recallEnv {
	t.Helper()
	te := newTEnv(t)
	p := te.transcript(idleTurns(1))
	te.env.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	if len(rows) > 0 {
		appendRows(t, p, rows...)
	}
	re := &recallEnv{tenv: te, comp: &composer{}, path: p}
	te.keys.onCall = func(c keyCall) {
		if len(c.keys) == 1 && c.keys[0] == tmux.KeyUp {
			re.comp.set(re.popped)
		}
	}
	te.fm.onJob = re.comp.job
	return re
}

func (re *recallEnv) recall(id string) *httptest.ResponseRecorder {
	m := map[string]string{"expected_tmux_instance": tInst}
	if id != "" {
		m["client_msg_id"] = id
	}
	b, _ := json.Marshal(m)
	return re.post("/api/sessions/"+tCode+"/recall", string(b))
}

func (re *recallEnv) ok(t *testing.T, w *httptest.ResponseRecorder, text string) {
	t.Helper()
	var a struct{ Text string }
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w.Code != 200 || a.Text != text {
		t.Fatalf("got %d %s, want 200 text=%q", w.Code, w.Body.String(), text)
	}
}

func (re *recallEnv) upCount() int {
	re.keys.mu.Lock()
	defer re.keys.mu.Unlock()
	n := 0
	for _, c := range re.keys.calls {
		if len(c.keys) == 1 && c.keys[0] == tmux.KeyUp {
			n++
		}
	}
	return n
}

func TestRecall_HappyPathJoinsItemsAndClearsTheInput(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "first  \t"), queueRow("enqueue", "second\n"))
	re.popped = "first\nsecond"
	re.ok(t, re.recall(""), "first\nsecond")
	if cl := re.comp.clears(); re.comp.now() != "" || len(cl) != 1 || cl[0] != "first\nsecond" {
		t.Fatalf("input %q, clears %q: our text must be cleared with clear_if", re.comp.now(), cl)
	}
	if k := re.keys.calls[0]; k.sid != "$1" || k.inst != tInst || k.pane != "%5" || len(k.keys) != 1 || k.keys[0] != tmux.KeyUp {
		t.Fatalf("keys = %+v", re.keys.calls)
	}
}

func TestRecall_NoOwnerIsRefusedWithoutKeys(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.owners.drop()
	errOf(t, re.recall(""), 409, "no_owner")
	if re.keys.n() != 0 {
		t.Fatal("recall has no keys fallback")
	}
}

func TestRecall_RefusalsThatPressNothing(t *testing.T) {
	cases := []struct {
		name, code string
		rows       []string
		input      string
	}{
		{"empty", "nothing_queued", nil, ""},
		{"peer", "recall_unsupported", []string{queueRow("enqueue", "a"), queueRow("enqueue", `<cross-session-message from="x">hi</cross-session-message>`)}, ""},
		{"hidden system", "recall_unsupported", []string{queueRow("enqueue", "a"), queueRow("enqueue", "<task-notification>x</task-notification>")}, ""},
		{"input not empty", "input_not_empty", []string{queueRow("enqueue", "a")}, "my draft"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re := newRecallEnv(t, c.rows...)
			re.comp.set(c.input)
			errOf(t, re.recall(""), 409, c.code)
			if re.keys.n() != 0 || re.comp.now() != c.input || len(re.comp.clears()) != 0 {
				t.Fatalf("must touch nothing: keys=%d input=%q clears=%q", re.keys.n(), re.comp.now(), re.comp.clears())
			}
		})
	}
}

func TestRecall_GuardRefusalsAreMapped(t *testing.T) {
	for err, code := range map[error]string{tmux.ErrPaneBusy: "pane_busy", tmux.ErrGenerationMismatch: "generation", tmux.ErrPaneMismatch: "pane_mismatch"} {
		re := newRecallEnv(t, queueRow("enqueue", "a"))
		re.keys.errs = []error{err}
		errOf(t, re.recall(""), 409, code)
		if len(re.comp.clears()) != 0 {
			t.Fatalf("%s: the guard declined, nothing to clear", code)
		}
	}
}

// full parses a 200 answer; matched is a pointer so absence is visible.
func (re *recallEnv) full(t *testing.T, w *httptest.ResponseRecorder, text string, matched bool, reason string) {
	t.Helper()
	var a struct {
		Text    string
		Matched *bool
		Reason  string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w.Code != 200 || a.Text != text || a.Matched == nil || *a.Matched != matched || a.Reason != reason {
		t.Fatalf("got %d %s, want 200 text=%q matched=%v reason=%q", w.Code, w.Body.String(), text, matched, reason)
	}
}

func TestRecall_HappyPathIsMatched(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.popped = "a"
	re.full(t, re.recall(""), "a", true, "")
}

func TestRecall_MismatchReturnsTheTextAndClears(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "queued one"))
	re.popped = "something else" // queue still non-empty: a join difference
	re.full(t, re.recall(""), "something else", false, "recall_mismatch")
	if cl := re.comp.clears(); re.comp.now() != "" || len(cl) != 1 || cl[0] != "something else" {
		t.Fatalf("input %q clears %q", re.comp.now(), cl)
	}
}

func TestRecall_QueueEmptiedMeansHistoryRecalled(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "queued one"))
	re.popped = "an old history line"
	re.keys.onCall = func(c keyCall) { // the turn takes the queue as Up lands
		if len(c.keys) == 1 && c.keys[0] == tmux.KeyUp {
			appendRows(t, re.path, queueRow("dequeue", ""))
			re.comp.set(re.popped)
		}
	}
	re.full(t, re.recall(""), "an old history line", false, "history_recalled")
	if cl := re.comp.clears(); re.comp.now() != "" || len(cl) != 1 {
		t.Fatalf("input %q clears %q", re.comp.now(), cl)
	}
}

func TestRecall_LongItemIsReturnedWholeNotDestroyed(t *testing.T) {
	long := strings.Repeat("長", 2500) // over the header's 2000-rune cap, so the join differs from what Up popped
	re := newRecallEnv(t, queueRow("enqueue", long))
	re.popped = long
	re.full(t, re.recall(""), long, false, "recall_mismatch")
	if re.comp.now() != "" {
		t.Fatalf("input %q", re.comp.now())
	}
}

func TestRecall_ClearRefusedKeepsTheText(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "q"))
	re.popped = "other"
	re.fm.onJob = func(j promptq.Job) promptq.Outcome {
		if j.Kind == promptq.KindClearIf {
			return promptq.Outcome{Status: promptq.Dropped, Reason: "not_ours"}
		}
		return re.comp.job(j)
	}
	re.full(t, re.recall(""), "other", false, "input_not_cleared")
}

func TestRecall_EmptyReadIsAlreadySent(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.popped = ""
	errOf(t, re.recall(""), 409, "already_sent")
	if len(re.comp.clears()) != 0 {
		t.Fatalf("nothing to clear: %q", re.comp.clears())
	}
}

func TestRecall_ReplayNeverPressesUpTwice(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.popped = "a"
	re.ok(t, re.recall("r1"), "a")
	re.ok(t, re.recall("r1"), "a")
	if re.upCount() != 1 {
		t.Fatalf("Up pressed %d times", re.upCount())
	}
	re2 := newRecallEnv(t, queueRow("enqueue", "q"))
	re2.popped = "history"
	re2.full(t, re2.recall("r2"), "history", false, "recall_mismatch")
	re2.popped = "q"
	re2.full(t, re2.recall("r2"), "history", false, "recall_mismatch") // the recorded answer, not a fresh try
	if re2.upCount() != 1 {
		t.Fatalf("replay pressed Up: %d", re2.upCount())
	}
}

func TestRecall_RefusalBeforeUpIsNotRecorded(t *testing.T) {
	re := newRecallEnv(t)
	errOf(t, re.recall("r3"), 409, "nothing_queued")
	appendRows(t, re.path, queueRow("enqueue", "late"))
	re.popped = "late"
	re.ok(t, re.recall("r3"), "late") // nothing was pressed the first time: a retry looks afresh
}

func TestRecall_ValidationAndGeneration(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"expected_tmux_instance":"1}"}`), 400, "bad_expected_tmux_instance")
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"client_msg_id":"bad id!","expected_tmux_instance":"`+tInst+`"}`), 400, "bad_client_msg_id")
	errOf(t, re.post("/api/sessions/nope/recall", `{"expected_tmux_instance":"`+tInst+`"}`), 404, "session_not_found")
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"expected_tmux_instance":"99:99"}`), 409, "generation")
	if re.keys.n() != 0 {
		t.Fatal("a refused request pressed keys")
	}
}
