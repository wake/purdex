package conversation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/module/session"
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

func (re *recallEnv) tune(f func(*typedTuning)) { f(&re.mod.tune) }

func (re *recallEnv) recall(id string) *httptest.ResponseRecorder {
	m := map[string]string{"expected_tmux_instance": tInst}
	if id == "" {
		id = "auto-id" // recall requires an id; tests that do not care share one per env
	}
	m["client_msg_id"] = id
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

// script answers the reads from seq in order (the last entry repeats; "!" is a failed read) and every clear_if with
// clearStatus; it records when each read happened and which texts were cleared.
type script struct {
	mu          sync.Mutex
	seq         []string
	n           int
	times       []time.Time
	clears      []string
	clearStatus string
	truncated   bool // reads after the first report Truncated
}

func (re *recallEnv) script(s *script) {
	re.fm.onJob = func(j promptq.Job) promptq.Outcome {
		s.mu.Lock()
		defer s.mu.Unlock()
		if j.Kind == promptq.KindClearIf {
			s.clears = append(s.clears, j.Text)
			return promptq.Outcome{Status: s.clearStatus, Reason: "not_ours"}
		}
		i := s.n
		if i >= len(s.seq) {
			i = len(s.seq) - 1
		}
		s.n++
		s.times = append(s.times, time.Now())
		if s.seq[i] == "!" {
			return promptq.Outcome{Status: promptq.Dropped}
		}
		return promptq.Outcome{Status: promptq.Accepted, Text: s.seq[i], Truncated: s.truncated && i > 0}
	}
}

func (s *script) cleared() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.clears...)
}

func (re *recallEnv) unknown(t *testing.T, w *httptest.ResponseRecorder, reason string) {
	t.Helper()
	var a struct {
		Outcome, Reason, Text string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w.Code != 200 || a.Outcome != "unknown" || a.Reason != reason || a.Text != "" {
		t.Fatalf("got %d %s, want 200 unknown %s with no text", w.Code, w.Body.String(), reason)
	}
}

func TestRecall_DraftTypedBetweenCheckAndUpIsNeverClearedNorReturned(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "queued one"))
	s := &script{seq: []string{"", "secret draft"}, clearStatus: promptq.Accepted}
	re.script(s)
	w := re.recall("")
	errOf(t, w, 409, "recall_mismatch")
	if strings.Contains(w.Body.String(), "secret draft") || len(s.cleared()) != 0 {
		t.Fatalf("body %s, clears %q: a mismatch must neither return nor clear", w.Body.String(), s.cleared())
	}
}

func TestRecall_QueuedTextPlusMoreTypedIsAMismatch(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "first"), queueRow("enqueue", "second"))
	re.popped = "first\nsecond and my own words"
	w := re.recall("")
	errOf(t, w, 409, "recall_mismatch")
	if len(re.comp.clears()) != 0 || re.comp.now() != re.popped || strings.Contains(w.Body.String(), "my own words") {
		t.Fatalf("clears %q, input %q, body %s", re.comp.clears(), re.comp.now(), w.Body.String())
	}
}

func TestRecall_ClearRefusedIsJudgedByAnotherRead(t *testing.T) {
	cases := []struct {
		name, reread string
		check        func(t *testing.T, re *recallEnv, w *httptest.ResponseRecorder)
	}{
		{"gone", "", func(t *testing.T, re *recallEnv, w *httptest.ResponseRecorder) { re.ok(t, w, "q") }},
		{"still ours", "q", func(t *testing.T, re *recallEnv, w *httptest.ResponseRecorder) { errOf(t, w, 409, "input_not_cleared") }},
		{"something else", "other", func(t *testing.T, re *recallEnv, w *httptest.ResponseRecorder) { re.unknown(t, w, "clear_unknown") }},
		{"read failed", "!", func(t *testing.T, re *recallEnv, w *httptest.ResponseRecorder) { re.unknown(t, w, "clear_unknown") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re := newRecallEnv(t, queueRow("enqueue", "q"))
			s := &script{seq: []string{"", "q", c.reread}, clearStatus: promptq.Dropped}
			re.script(s)
			w := re.recall("")
			c.check(t, re, w)
			if cl := s.cleared(); len(cl) != 1 || cl[0] != "q" {
				t.Fatalf("clears %q", cl)
			}
			if c.name != "gone" && strings.Contains(w.Body.String(), `"text"`) {
				t.Fatalf("no text may come back: %s", w.Body.String())
			}
		})
	}
}

func TestRecall_ClientDisconnectDoesNotStopTheReadsOrTheClear(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "q"))
	s := &script{seq: []string{"", "", "q"}, clearStatus: promptq.Accepted}
	re.script(s)
	re.tune(func(t *typedTuning) { t.recallSettle = 5 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	re.keys.onCall = func(c keyCall) {
		if len(c.keys) == 1 && c.keys[0] == tmux.KeyUp {
			cancel() // the client goes away right after Up
		}
	}
	b := `{"client_msg_id":"d1","expected_tmux_instance":"` + tInst + `"}`
	w := httptest.NewRecorder()
	re.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/"+tCode+"/recall", strings.NewReader(b)).WithContext(ctx))
	re.ok(t, w, "q")
	if cl := s.cleared(); len(cl) != 1 || cl[0] != "q" {
		t.Fatalf("clears %q", cl)
	}
}

func TestRecall_SlowDrawWithinTheBudgetIsStillOurs(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "q"))
	re.tune(func(t *typedTuning) { t.recallSettle, t.recallReadBudget = 10*time.Millisecond, 300*time.Millisecond })
	re.keys.onCall = func(c keyCall) { // the TUI draws the popped text ~60% into the budget
		if len(c.keys) == 1 && c.keys[0] == tmux.KeyUp {
			time.AfterFunc(180*time.Millisecond, func() { re.comp.set("q") })
		}
	}
	re.ok(t, re.recall(""), "q")
	if cl := re.comp.clears(); re.comp.now() != "" || len(cl) != 1 {
		t.Fatalf("input %q clears %q", re.comp.now(), cl)
	}
}

func TestRecall_NothingWithinTheBudgetIsUnknownAndTheLastReadIsAfterIt(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.tune(func(t *typedTuning) { t.recallSettle, t.recallReadBudget = 20*time.Millisecond, 100*time.Millisecond })
	s := &script{seq: []string{""}, clearStatus: promptq.Accepted}
	re.script(s)
	var upAt time.Time
	re.keys.onCall = func(c keyCall) {
		if len(c.keys) == 1 && c.keys[0] == tmux.KeyUp {
			upAt = time.Now()
		}
	}
	re.unknown(t, re.recall(""), "recall_empty")
	s.mu.Lock()
	defer s.mu.Unlock()
	if last := s.times[len(s.times)-1]; last.Sub(upAt) < 100*time.Millisecond || len(s.clears) != 0 {
		t.Fatalf("last read %v after Up (budget 100ms), clears %q", last.Sub(upAt), s.clears)
	}
}

func TestRecall_ReadFailureAfterUpIsUnknown(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.script(&script{seq: []string{"", "!"}, clearStatus: promptq.Accepted})
	re.unknown(t, re.recall(""), "read_failed")
}

func TestRecall_TooLongIsRefusedBeforeUp(t *testing.T) {
	var rows []string
	for i := 0; i < 9; i++ { // 9 x 1900 ASCII bytes + separators > 16 KiB, each item under the feed's cap
		rows = append(rows, queueRow("enqueue", strings.Repeat(string(rune('a'+i)), 1900)))
	}
	re := newRecallEnv(t, rows...)
	errOf(t, re.recall(""), 409, "recall_too_long")
	if re.keys.n() != 0 {
		t.Fatal("nothing may be pressed")
	}
}

func TestRecall_ItemAtTheFeedCapIsRefusedBeforeUp(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", strings.Repeat("長", ccnorm.MaxQueuedText+500))) // the feed cut it at the cap
	errOf(t, re.recall(""), 409, "recall_too_long")
	if re.keys.n() != 0 {
		t.Fatal("nothing may be pressed")
	}
}

func TestRecall_TruncatedReadIsAMismatchWithoutClear(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "q"))
	s := &script{seq: []string{"", "q"}, clearStatus: promptq.Accepted, truncated: true}
	re.script(s)
	errOf(t, re.recall(""), 409, "recall_mismatch")
	if len(s.cleared()) != 0 {
		t.Fatalf("clears %q", s.cleared())
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
	errOf(t, re2.recall("r2"), 409, "recall_mismatch")
	re2.popped = "q"
	errOf(t, re2.recall("r2"), 409, "recall_mismatch") // the recorded answer, not a fresh try
	if re2.upCount() != 1 {
		t.Fatalf("replay pressed Up: %d", re2.upCount())
	}
}

// two session codes over the same pane: the ledger must tell them apart.
type tTwoSessions struct{ inst string }

func (s tTwoSessions) GetSession(code string) (*session.SessionInfo, error) {
	if code != tCode && code != tCode+"b" {
		return nil, nil
	}
	return &session.SessionInfo{Code: code, TmuxID: "$1", TmuxInstance: s.inst}, nil
}

func TestRecall_LedgerIsBoundToTheSession(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a-private"))
	re.mod.td.sessions = tTwoSessions{tInst}
	re.popped = "a-private"
	re.ok(t, re.recall("r1"), "a-private")
	re.popped = "b-own"
	b := `{"client_msg_id":"r1","expected_tmux_instance":"` + tInst + `"}`
	w := re.post("/api/sessions/"+tCode+"b/recall", b)
	if strings.Contains(w.Body.String(), "a-private") || re.upCount() != 2 {
		t.Fatalf("B got %d %s, Ups %d: it must run its own recall", w.Code, w.Body.String(), re.upCount())
	}
	re.ok(t, re.recall("r1"), "a-private") // A's repeat: the same answer
	if re.upCount() != 2 {
		t.Fatalf("A's repeat pressed Up: %d", re.upCount())
	}
}

func TestRecall_RequiresClientMsgID(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.popped = "a"
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"expected_tmux_instance":"`+tInst+`"}`), 400, "bad_client_msg_id")
	if re.keys.n() != 0 || re.upCount() != 0 {
		t.Fatal("a recall without an id pressed keys")
	}
}

func TestRecall_ReplayWorksEvenWhenTheOwnerCannotBeResolved(t *testing.T) {
	re := newRecallEnv(t, queueRow("enqueue", "a"))
	re.popped = "a"
	re.ok(t, re.recall("r9"), "a")
	re.mod.td.owners = tResolver{} // the owner vanished (or its sid changed) before the retry
	re.ok(t, re.recall("r9"), "a")
	if re.upCount() != 1 {
		t.Fatalf("Up pressed %d times", re.upCount())
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
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"client_msg_id":"v1","expected_tmux_instance":"1}"}`), 400, "bad_expected_tmux_instance")
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"client_msg_id":"bad id!","expected_tmux_instance":"`+tInst+`"}`), 400, "bad_client_msg_id")
	errOf(t, re.post("/api/sessions/nope/recall", `{"client_msg_id":"v1","expected_tmux_instance":"`+tInst+`"}`), 404, "session_not_found")
	errOf(t, re.post("/api/sessions/"+tCode+"/recall", `{"client_msg_id":"v1","expected_tmux_instance":"99:99"}`), 409, "generation")
	if re.keys.n() != 0 {
		t.Fatal("a refused request pressed keys")
	}
}
