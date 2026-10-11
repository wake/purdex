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

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// K-1a-2: POST /api/sessions/{code}/submit — a real promptq.Queue with a scripted mod, fakes for tmux, the light, the session
// lookup and the owner resolver.

const (
	tCode = "c1"
	tInst = "11:22"
)

func (o *tOwners) drop() {
	o.mu.Lock()
	o.m = map[string]string{}
	o.mu.Unlock()
}

type tOwners struct {
	mu sync.Mutex
	m  map[string]string
}

func (o *tOwners) OwnerOf(s string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.m[s]
	return v, ok
}

// tMod is the mod: it fetches jobs and answers them as scripted.
type tMod struct {
	q         *promptq.Queue
	mu        sync.Mutex
	jobs      []promptq.Job
	fillDelay time.Duration
	onFill    func(promptq.Job) (promptq.Outcome, bool) // false: never report
	onClear   func(n int) promptq.Outcome
}

func (m *tMod) run(ctx context.Context) {
	for ctx.Err() == nil {
		j, ok := m.q.Next(ctx, "mod1", sid, 20*time.Millisecond)
		if !ok {
			continue
		}
		m.mu.Lock()
		m.jobs = append(m.jobs, j)
		n := 0
		for _, k := range m.jobs {
			if k.Kind == promptq.KindClearIf {
				n++
			}
		}
		m.mu.Unlock()
		switch j.Kind {
		case promptq.KindFill:
			if m.fillDelay > 0 {
				time.Sleep(m.fillDelay)
			}
			out, report := promptq.Outcome{Status: promptq.Accepted, FilledAt: time.Now().UnixMilli()}, true
			if m.onFill != nil {
				out, report = m.onFill(j)
			}
			if report {
				_ = m.q.Result("mod1", j.ID, out)
			}
		case promptq.KindClearIf:
			out := promptq.Outcome{Status: promptq.Accepted}
			if m.onClear != nil {
				out = m.onClear(n)
			}
			_ = m.q.Result("mod1", j.ID, out)
		}
	}
}

func (m *tMod) count(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, j := range m.jobs {
		if j.Kind == kind {
			n++
		}
	}
	return n
}

type keyCall struct {
	sid, inst, pane string
	keys            []string
}

type tKeys struct {
	mu     sync.Mutex
	calls  []keyCall
	errs   []error
	onCall func(keyCall)
}

func (k *tKeys) SendKeysIfInstanceAndMode(s, inst, pane string, keys ...string) error {
	c := keyCall{s, inst, pane, keys}
	if k.onCall != nil {
		k.onCall(c)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, c)
	if len(k.errs) > 0 {
		e := k.errs[0]
		k.errs = k.errs[1:]
		return e
	}
	return nil
}

func (k *tKeys) n() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.calls)
}

// tLight answers the statuses in order, the last one for ever.
type tLight struct {
	mu  sync.Mutex
	seq []string
}

func (l *tLight) LightStatus(string, string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.seq[0]
	if len(l.seq) > 1 {
		l.seq = l.seq[1:]
	}
	return s, s != "!" // "!" is a failed read
}

type tSessions struct{ inst string }

func (s tSessions) GetSession(code string) (*session.SessionInfo, error) {
	if code != tCode {
		return nil, nil
	}
	return &session.SessionInfo{Code: code, TmuxID: "$1", TmuxInstance: s.inst}, nil
}

type tResolver struct {
	found bool
	owner agent.PaneOwner
}

func (r tResolver) ResolveSessionOwner(context.Context, string) (agent.PaneOwner, bool, error) {
	return r.owner, r.found, nil
}

// tFlip answers `first` once, then `then` for ever (the owner moved while the request waited for the locks).
type tFlip struct {
	mu          sync.Mutex
	first, then agent.PaneOwner
	n           int
}

func (r *tFlip) ResolveSessionOwner(context.Context, string) (agent.PaneOwner, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	if r.n == 1 {
		return r.first, true, nil
	}
	return r.then, true, nil
}

// tPanes is the confirmed live panes of the session (the identity check).
type tPanes struct {
	mu     sync.Mutex
	panes  []agent.PaneOwner
	err    error
	called int
}

func (p *tPanes) ConfirmedOwners(context.Context, string) ([]agent.PaneOwner, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.called++
	return p.panes, p.err
}

var tOwner = agent.PaneOwner{SessionID: sid, FrameID: "f1", TmuxPaneID: "%5", AgentType: "cc", Status: "idle"}

type tenv struct {
	*env
	q      *promptq.Queue
	owners *tOwners
	fm     *tMod
	keys   *tKeys
	light  *tLight
	panes  *tPanes
}

func newTEnv(t *testing.T) *tenv {
	t.Helper()
	e := newEnv(t)
	owners := &tOwners{m: map[string]string{sid: "mod1"}}
	q := promptq.New(owners)
	q.Wait, q.HandTimeout = 400*time.Millisecond, 150*time.Millisecond
	te := &tenv{env: e, q: q, owners: owners, keys: &tKeys{}, light: &tLight{seq: []string{"idle"}}, fm: &tMod{q: q}, panes: &tPanes{panes: []agent.PaneOwner{tOwner}}}
	e.mod.td = &typedDeps{q: q, sessions: tSessions{tInst}, keys: te.keys, panes: te.panes, owners: tResolver{found: true, owner: tOwner}}
	e.mod.light = te.light
	e.mod.tune = typedTuning{lockWait: 150 * time.Millisecond, poll: 5 * time.Millisecond, finaliseFor: 2 * time.Second, keysAfter: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { te.fm.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return te
}

func (te *tenv) submit(id, text string) *httptest.ResponseRecorder {
	return te.post("/api/sessions/"+tCode+"/submit", body(id, text, tInst))
}

func body(id, text, inst string) string {
	b, _ := json.Marshal(map[string]string{"text": text, "client_msg_id": id, "expected_tmux_instance": inst})
	return string(b)
}

type tAnswer struct {
	Outcome, Mode, Reason, Error string
}

func answer(t *testing.T, w *httptest.ResponseRecorder, status int, outcome, mode, reason string) {
	t.Helper()
	var a tAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if w.Code != status || a.Outcome != outcome || a.Mode != mode || (reason != "" && a.Reason != reason) {
		t.Fatalf("got %d %+v, want %d outcome=%q mode=%q reason=%q (%s)", w.Code, a, status, outcome, mode, reason, w.Body.String())
	}
}

func phaseOf(t *testing.T, q *promptq.Queue, id string) promptq.Result {
	t.Helper()
	r, ok := q.Lookup(id)
	if !ok {
		t.Fatalf("no ledger row %s", id)
	}
	return r
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func TestTypedSubmit_FillThenEnterIsHandedAndMarkedBeforeTheKeys(t *testing.T) {
	te := newTEnv(t)
	var phaseAtSend string
	te.keys.onCall = func(keyCall) { phaseAtSend = phaseOf(t, te.q, "m1").Phase }
	w := te.submit("m1", "hello  \n")
	answer(t, w, 200, "handed", "fill", "")
	if phaseAtSend != promptq.PhaseEntering {
		t.Fatalf("phase when Enter went out = %q: the at-most-once line must be written first", phaseAtSend)
	}
	if got := phaseOf(t, te.q, "m1").Phase; got != promptq.PhaseEntered {
		t.Fatalf("phase = %s", got)
	}
	want := keyCall{"$1", tInst, "%5", []string{tmux.KeyEnter}}
	if len(te.keys.calls) != 1 || te.keys.calls[0].sid != want.sid || te.keys.calls[0].inst != want.inst || te.keys.calls[0].pane != want.pane || te.keys.calls[0].keys[0] != tmux.KeyEnter {
		t.Fatalf("keys = %+v", te.keys.calls)
	}
	if te.fm.jobs[0].Text != "hello" {
		t.Fatalf("the mod got %q, want the sanitised text", te.fm.jobs[0].Text)
	}
}

func TestTypedSubmit_ValidationAndSanitiser(t *testing.T) {
	te := newTEnv(t)
	for _, c := range []struct{ text, code string }{{"/compact", "needs_terminal"}, {"!ls", "needs_terminal"}, {"  \n ", "empty_text"}, {strings.Repeat("a", 4001), "too_long"}} {
		errOf(t, te.submit("v1", c.text), 400, c.code)
	}
	errOf(t, te.post("/api/sessions/"+tCode+"/submit", body("bad id!", "x", tInst)), 400, "bad_client_msg_id")
	errOf(t, te.post("/api/sessions/"+tCode+"/submit", body("v2", "x", "1}")), 400, "bad_expected_tmux_instance")
	errOf(t, te.post("/api/sessions/nope/submit", body("v3", "x", tInst)), 404, "session_not_found")
	errOf(t, te.post("/api/sessions/"+tCode+"/submit", body("v4", "x", "99:99")), 409, "generation")
	if len(te.fm.jobs) != 0 || te.keys.n() != 0 {
		t.Fatal("a refused request must not reach the mod or tmux")
	}
	te.mod.td.owners = tResolver{}
	errOf(t, te.submit("v5", "x"), 409, "not_claude")
}

func TestTypedSubmit_WaitingRefusesBeforeAnythingIsTyped(t *testing.T) {
	te := newTEnv(t)
	te.light.seq = []string{"waiting"}
	answer(t, te.submit("w1", "x"), 409, "refused", "", "waiting")
	if len(te.fm.jobs) != 0 || te.keys.n() != 0 {
		t.Fatal("nothing may be typed while a question is open")
	}
}

func TestTypedSubmit_FillDroppedReasonsAreRefusedWithoutEnter(t *testing.T) {
	for _, reason := range []string{"input_not_empty", "dialog_open", "no_composer", "fill_mismatch"} {
		te := newTEnv(t)
		te.fm.onFill = func(promptq.Job) (promptq.Outcome, bool) {
			return promptq.Outcome{Status: promptq.Dropped, Reason: reason}, true
		}
		answer(t, te.submit("d-"+reason, "x"), 409, "refused", "", reason)
		if te.keys.n() != 0 {
			t.Fatalf("%s: Enter went out", reason)
		}
	}
}

func TestTypedSubmit_StaleFillIsClearedAndNoEnter(t *testing.T) {
	te := newTEnv(t)
	te.mod.tune.now = func() time.Time { return time.Now().Add(2 * time.Second) } // the result is 2 s old when we look
	answer(t, te.submit("s1", "x"), 409, "refused", "", "stale")
	if te.keys.n() != 0 || te.fm.count(promptq.KindClearIf) != 1 {
		t.Fatalf("keys=%d clear_if=%d", te.keys.n(), te.fm.count(promptq.KindClearIf))
	}
	if got := phaseOf(t, te.q, "s1").Phase; got != promptq.PhaseCleared {
		t.Fatalf("phase = %s", got)
	}
}

func TestTypedSubmit_LightTurnsWaitingBeforeEnter(t *testing.T) {
	te := newTEnv(t)
	te.light.seq = []string{"idle", "waiting"}
	answer(t, te.submit("l1", "x"), 409, "refused", "", "waiting")
	if te.keys.n() != 0 || te.fm.count(promptq.KindClearIf) != 1 || phaseOf(t, te.q, "l1").Phase != promptq.PhaseCleared {
		t.Fatalf("keys=%d clear_if=%d phase=%s", te.keys.n(), te.fm.count(promptq.KindClearIf), phaseOf(t, te.q, "l1").Phase)
	}
}

func TestTypedSubmit_GuardRefusalsClearTheComposer(t *testing.T) {
	for _, c := range []struct {
		err    error
		reason string
	}{{tmux.ErrPaneBusy, "pane_busy"}, {tmux.ErrGenerationMismatch, "generation"}, {tmux.ErrPaneMismatch, "pane_mismatch"}} {
		te := newTEnv(t)
		te.keys.errs = []error{c.err}
		answer(t, te.submit("g1", "x"), 409, "refused", "", c.reason)
		if te.fm.count(promptq.KindClearIf) != 1 || phaseOf(t, te.q, "g1").Phase != promptq.PhaseCleared {
			t.Fatalf("%s: clear_if=%d phase=%s", c.reason, te.fm.count(promptq.KindClearIf), phaseOf(t, te.q, "g1").Phase)
		}
	}
}

// A tmux invocation error that is not a guard refusal: the Enter may or may not have gone. The row is unknown (not cleared:
// the Enter was marked), our text is cleared best effort with clear_if, and the answer is 200 unknown, not a 502.
func TestTypedSubmit_TmuxErrorAfterEnteringIsUnknownNot502(t *testing.T) {
	te := newTEnv(t)
	te.keys.errs = []error{context.DeadlineExceeded}
	answer(t, te.submit("g2", "x"), 200, "unknown", "fill", "")
	if got := phaseOf(t, te.q, "g2").Phase; got != promptq.PhaseUnknown {
		t.Fatalf("phase = %s", got)
	}
	if n := te.fm.count(promptq.KindClearIf); n != 1 {
		t.Fatalf("clear_if = %d, want a best-effort clear", n)
	}
	answer(t, te.submit("g2", "x"), 200, "unknown", "fill", "") // a repeat never types again
	if te.fm.count(promptq.KindFill) != 1 || te.keys.n() != 1 {
		t.Fatal("a repeat typed")
	}
}

func TestTypedSubmit_LostFillResultIsUnknownAndCleaned(t *testing.T) {
	te := newTEnv(t)
	first := true
	te.fm.onFill = func(promptq.Job) (promptq.Outcome, bool) { first = false; return promptq.Outcome{}, false }
	answer(t, te.submit("u1", "x"), 200, "unknown", "fill", "")
	if first || te.keys.n() != 0 || te.fm.count(promptq.KindClearIf) != 1 || phaseOf(t, te.q, "u1").Phase != promptq.PhaseCleared {
		t.Fatalf("keys=%d clear_if=%d phase=%s", te.keys.n(), te.fm.count(promptq.KindClearIf), phaseOf(t, te.q, "u1").Phase)
	}
	te2 := newTEnv(t) // the mod itself says it cannot tell
	te2.fm.onFill = func(promptq.Job) (promptq.Outcome, bool) {
		return promptq.Outcome{Status: promptq.Unknown, Reason: "fill_error"}, true
	}
	answer(t, te2.submit("u2", "x"), 200, "unknown", "fill", "")
	if te2.keys.n() != 0 || te2.fm.count(promptq.KindClearIf) != 1 {
		t.Fatal("unknown must be cleaned, never entered")
	}
}

func TestTypedSubmit_LedgerReplayAnswersTheRecordedPhaseAndNeverTypes(t *testing.T) {
	for _, c := range []struct {
		phase  string
		status int
		out    string
	}{{promptq.PhaseEntered, 200, "handed"}, {promptq.PhaseEntering, 200, "unknown"}, {promptq.PhaseUnknown, 200, "unknown"}, {promptq.PhaseFilled, 200, "unknown"}, {promptq.PhaseCleared, 409, "refused"}} {
		te := newTEnv(t)
		if _, err := te.q.SubmitFill(context.Background(), sid, "r1", "x"); err != nil {
			t.Fatal(err)
		}
		switch c.phase {
		case promptq.PhaseEntered:
			te.q.MarkEntering("r1")
			te.q.MarkEntered("r1")
		case promptq.PhaseEntering:
			te.q.MarkEntering("r1")
		case promptq.PhaseUnknown:
			te.q.MarkEntering("r1")
			te.q.MarkUnknown("r1")
		case promptq.PhaseCleared:
			te.q.MarkCleared("r1")
		}
		before := len(te.fm.jobs)
		w := te.submit("r1", "x")
		mode := "fill"
		if c.out == "refused" {
			mode = ""
		}
		answer(t, w, c.status, c.out, mode, "")
		if te.keys.n() != 0 || te.fm.count(promptq.KindFill) != 1 || len(te.fm.jobs) != before {
			t.Fatalf("%s: a replay typed (keys=%d fills=%d)", c.phase, te.keys.n(), te.fm.count(promptq.KindFill))
		}
	}
}

func TestTypedSubmit_ClientGoneLeavesAFinaliserThatClearsALateFill(t *testing.T) {
	te := newTEnv(t)
	te.fm.fillDelay = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+tCode+"/submit", strings.NewReader(body("cg1", "x", tInst))).WithContext(ctx)
	done := make(chan struct{})
	go func() { te.mux.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	eventually(t, "the late fill is cleared", func() bool {
		r, ok := te.q.Lookup("cg1")
		return ok && r.Phase == promptq.PhaseCleared
	})
	if te.keys.n() != 0 || te.fm.count(promptq.KindClearIf) != 1 {
		t.Fatalf("keys=%d clear_if=%d", te.keys.n(), te.fm.count(promptq.KindClearIf))
	}
}

func TestTypedSubmit_CleanupThatFailsTwiceEndsAsGaveUp(t *testing.T) {
	te := newTEnv(t)
	te.fm.onClear = func(int) promptq.Outcome { return promptq.Outcome{Status: promptq.Dropped, Reason: "not_ours"} }
	te.keys.errs = []error{tmux.ErrPaneBusy}
	answer(t, te.submit("gu1", "x"), 409, "refused", "", "pane_busy")
	if n := te.fm.count(promptq.KindClearIf); n != 2 {
		t.Fatalf("clear_if jobs = %d, want one try and one retry", n)
	}
	if r := phaseOf(t, te.q, "gu1"); r.Phase != promptq.PhaseCleared || r.Reason != "cleanup_gave_up" {
		t.Fatalf("row = %+v", r)
	}
}

func TestTypedSubmit_SharesTheLockWithTheOldConversationRoute(t *testing.T) {
	te := newTEnv(t)
	f := &fakeSender{res: promptq.Result{Status: promptq.Accepted}}
	te.sender(f)
	release, ok := te.mod.locks.acquire(context.Background(), sid, time.Second)
	if !ok {
		t.Fatal("lock")
	}
	answerBusy := func(w *httptest.ResponseRecorder) {
		t.Helper()
		errOf(t, w, 429, "busy")
	}
	t0 := time.Now()
	answerBusy(te.submit("lk1", "x"))
	answerBusy(te.post("/api/conversations/claude/"+sid+"/submit", `{"text":"x","client_msg_id":"o1"}`))
	if time.Since(t0) > 2*time.Second || len(te.fm.jobs) != 0 || f.submits != 0 {
		t.Fatal("a busy lock must answer within the bound and run nothing")
	}
	release()
	if w := te.post("/api/conversations/claude/"+sid+"/submit", `{"text":"x","client_msg_id":"o2"}`); w.Code != 200 || f.submits != 1 {
		t.Fatalf("the old route after release: %d %s", w.Code, w.Body.String())
	}
	answer(t, te.submit("lk2", "x"), 200, "handed", "fill", "")
}
